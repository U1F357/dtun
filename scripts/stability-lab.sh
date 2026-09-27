#!/usr/bin/env bash
set -euo pipefail
ROOT=$(cd "$(dirname "$0")/.." && pwd)
if [ "${1:-}" != --hook ]; then
 export MAX_RATE_BPS=80000000
 export OUT=${OUT:-$ROOT/reports/stability}
 export LAB_HOOK="$ROOT/scripts/stability-hook.sh"
 exec "$ROOT/scripts/netns-lab.sh"
fi
OUT=${2:?output directory}
# Base lab already runs an iperf server on TCP/UDP 5201 in dtun-s.
log() { printf '%s %s\n' "$(date -Is)" "$*" | tee -a "$OUT/scenarios.log"; }
resources() {
 for ns in dtun-c dtun-s; do
  for pid in $(ip netns pids "$ns"); do
   if [ "$(cat /proc/"$pid"/comm 2>/dev/null || true)" = dtun ]; then
    printf '%s %s pid=%s fd=%s ' "$(date -Is)" "$ns" "$pid" "$(ls /proc/"$pid"/fd | wc -l)"
    awk '/VmRSS|Threads/ {printf "%s=%s%s ",$1,$2,$3} END {print ""}' /proc/"$pid"/status
   fi
  done
 done >> "$OUT/resources.txt"
}
clear_netem() { for dev in drc drs; do ip netns exec dtun-r tc qdisc del dev "$dev" root 2>/dev/null || true; done; }
check_json() { python3 - "$1" <<'PY'
import json,sys
j=json.load(open(sys.argv[1]));assert not j.get('error'),j.get('error');assert j.get('end'),j
PY
}
if [ "${SKIP_SOAK:-0}" != 1 ]; then
log 'soak: 120s simultaneous TCP forward/reverse plus UDP and 1500-byte ICMP'
resources
ip netns exec dtun-s iperf3 -s -B 10.255.255.2 -p 5202 > "$OUT/soak-server2.log" 2>&1 &
ip netns exec dtun-s iperf3 -s -B 10.255.255.2 -p 5203 > "$OUT/soak-server3.log" 2>&1 &
sleep .3
ip netns exec dtun-c iperf3 -c 10.255.255.2 -p 5201 -t 120 -P 2 -J > "$OUT/soak-forward.json" &
p1=$!
ip netns exec dtun-c iperf3 -c 10.255.255.2 -p 5202 -t 120 -P 2 -R -J > "$OUT/soak-reverse.json" &
p2=$!
ip netns exec dtun-c iperf3 -c 10.255.255.2 -p 5203 -t 120 -u -b 5M -l 1472 -J > "$OUT/soak-udp.json" &
p3=$!
ip netns exec dtun-c ping -i .2 -c 600 -W 1 -M do -s 1472 10.255.255.2 > "$OUT/soak-ping.txt" &
p4=$!
for i in $(seq 1 12); do sleep 10; resources; done
wait "$p1" "$p2" "$p3"; wait "$p4" || true
for f in soak-forward soak-reverse soak-udp; do check_json "$OUT/$f.json"; done
fi
log 'mixed sizes: ICMP 28..1500-byte IPv4; reject >1500 locally'
for size in 0 1 64 1072 1073 1471 1472; do ip netns exec dtun-c ping -c 3 -W 2 -M do -s "$size" 10.255.255.2 > "$OUT/size-$size.txt"; done
if ip netns exec dtun-c ping -c 1 -W 1 -M do -s 1473 10.255.255.2 > "$OUT/size-too-large.txt" 2>&1; then echo 'oversize unexpectedly accepted';exit 1; fi
log 'netem: 100ms RTT + jitter/reorder/duplicate/loss, 25s TCP'
for dev in drc drs; do ip netns exec dtun-r tc qdisc replace dev "$dev" root netem delay 50ms 15ms loss 0.5% duplicate 2% reorder 15% 50% limit 1000; done
ip netns exec dtun-c iperf3 -c 10.255.255.2 -t 25 -J > "$OUT/reorder-jitter-loss.json"
check_json "$OUT/reorder-jitter-loss.json"
clear_netem
# Give the next scenario its own server: impaired TCP teardown may linger.
ip netns exec dtun-s iperf3 -s -B 10.255.255.2 -p 5204 > "$OUT/bottleneck-server.log" 2>&1 &
sleep .3
log 'bottleneck: underlay 20Mbps with paced 80Mbps offered UDP load, whole-packet drops allowed'
for dev in drc drs; do ip netns exec dtun-r tc qdisc replace dev "$dev" root netem rate 20mbit limit 100; done
ip netns exec dtun-c iperf3 -c 10.255.255.2 -p 5204 -u -b 100M -l 1472 -t 10 -J > "$OUT/bottleneck-udp.json"
check_json "$OUT/bottleneck-udp.json"
clear_netem
log 'short outage: 10s loss=100%; then ping recovery'
for dev in drc drs; do ip netns exec dtun-r tc qdisc replace dev "$dev" root netem loss 100%; done
if ip netns exec dtun-c ping -c 2 -W 1 10.255.255.2 > "$OUT/blackhole-ping.txt"; then echo 'blackhole unexpectedly passed';exit 1; fi
sleep 8
clear_netem
ip netns exec dtun-c ping -c 3 -W 2 10.255.255.2 > "$OUT/short-recovery.txt"
log 'long outage: 90s loss=100%; verify auto reconnect after restore'
for dev in drc drs; do ip netns exec dtun-r tc qdisc replace dev "$dev" root netem loss 100%; done
for i in $(seq 1 9); do sleep 10; resources; done
clear_netem
ok=0
for i in $(seq 1 20); do if ip netns exec dtun-c ping -c 1 -W 1 10.255.255.2 >> "$OUT/long-recovery.txt"; then ok=1;break;fi;sleep 1;done
[ "$ok" = 1 ]
log 'malformed unauthenticated UDP flood: 10000 datagrams while live'
ip netns exec dtun-c python3 - <<'PY'
import socket,os
s=socket.socket(socket.AF_INET,socket.SOCK_DGRAM)
for i in range(10000):s.sendto(os.urandom(64 if i%2 else 1300),('198.51.100.2',80))
PY
ip netns exec dtun-c ping -c 5 -W 2 -M do -s 1472 10.255.255.2 > "$OUT/flood-recovery.txt"
log 'idle: 35s to observe keepalives and resource recovery'
sleep 35
resources
log 'ALL STABILITY SCENARIOS PASSED'
