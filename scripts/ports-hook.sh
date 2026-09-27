#!/usr/bin/env bash
set -euo pipefail
OUT=${1:?report directory}
# Baseline lab supplies 5201; use independent ports for scenario transitions.
for port in 5211 5212 5213; do
 ip netns exec dtun-s iperf3 -s -B 10.255.255.2 -p "$port" > "$OUT/server-$port.log" 2>&1 &
done
sleep .3
ip netns exec dtun-c ping -i .1 -c 400 -W 1 -M do -s 1472 10.255.255.2 > "$OUT/switch-ping.txt" &
pingpid=$!
ip netns exec dtun-c iperf3 -c 10.255.255.2 -p 5211 -t 40 -J > "$OUT/switch-tcp.json" &
a=$!
ip netns exec dtun-c iperf3 -c 10.255.255.2 -p 5212 -t 40 -R -J > "$OUT/switch-reverse.json" &
b=$!
ip netns exec dtun-c iperf3 -c 10.255.255.2 -p 5213 -t 40 -u -b 1M -l 1472 -J > "$OUT/switch-udp.json" &
c=$!
wait "$a" "$b" "$c";wait "$pingpid" || true
# Block an inactive candidate port, keeping the current path healthy.
for i in $(seq 1 200); do
 if tail -40 "$OUT/client.log" | grep "session established endpoint=" | tail -1 | grep -q ":80 "; then break; fi
 sleep .1
done
ip netns exec dtun-r nft -f - <<'NFT'
table inet port_test {
 chain forward { type filter hook forward priority 0; policy accept;
 udp dport 443 drop
 udp sport 443 drop
 }
}
NFT
ip netns exec dtun-c ping -i .2 -c 175 -W 1 -M do -s 1472 10.255.255.2 > "$OUT/blocked-port-ping.txt" || true
ip netns exec dtun-r nft delete table inet port_test
sleep 12
ip netns exec dtun-c ping -c 5 -W 2 -M do -s 1472 10.255.255.2 > "$OUT/ports-restored-ping.txt"
python3 - "$OUT" <<'PY'
import json,pathlib,sys,re
p=pathlib.Path(sys.argv[1])
for name in ['switch-tcp','switch-reverse','switch-udp']:
 j=json.loads((p/(name+'.json')).read_text());assert not j.get('error'),j
 assert j.get('end'),j
logs=(p/'client.log').read_text()
ports=re.findall(r'session established endpoint=198.51.100.2:(\d+)',logs)
assert set(ports)=={'80','443','23333'},ports
assert len(ports)>=10,ports
assert 'session setup' in logs,'blocked port was not exercised'
print('MULTIPORT ROTATION AND BLOCKED PORT RECOVERY PASSED',ports)
PY
