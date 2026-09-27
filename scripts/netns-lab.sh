#!/usr/bin/env bash
set -euo pipefail
ROOT=$(cd "$(dirname "$0")/.." && pwd)
BIN=${DTUN_BIN:-$ROOT/dtun}
OUT=${OUT:-$ROOT/reports/lab}
mkdir -p "$OUT"
read -r -a RESOURCE_ARGS <<< "${DTUN_RESOURCE_ARGS:-}"
TMP=$(mktemp -d)
cleanup() {
 for ns in dtun-c dtun-r dtun-s; do
  ip netns pids "$ns" 2>/dev/null | xargs -r kill 2>/dev/null || true
 done
 sleep .3
 for ns in dtun-c dtun-r dtun-s; do ip netns del "$ns" 2>/dev/null || true; done
 rm -rf "$TMP"
}
for ns in dtun-c dtun-r dtun-s; do
 if ip netns list | awk '{print $1}' | grep -qx "$ns"; then echo "namespace $ns already exists; abort"; exit 1; fi
 done
trap cleanup EXIT
for ns in dtun-c dtun-r dtun-s; do ip netns add "$ns"; ip -n "$ns" link set lo up; done
ip link add dc type veth peer name drc
ip link set dc netns dtun-c; ip link set drc netns dtun-r
ip link add ds type veth peer name drs
ip link set ds netns dtun-s; ip link set drs netns dtun-r
ip -n dtun-c addr add 192.0.2.1/30 dev dc
ip -n dtun-r addr add 192.0.2.2/30 dev drc
ip -n dtun-r addr add 198.51.100.1/30 dev drs
ip -n dtun-s addr add 198.51.100.2/30 dev ds
for spec in 'dtun-c dc' 'dtun-r drc' 'dtun-r drs' 'dtun-s ds'; do read -r ns dev <<< "$spec"; ip -n "$ns" link set "$dev" mtu 1500 up; done
ip -n dtun-c route add 198.51.100.0/30 via 192.0.2.2
ip -n dtun-s route add 192.0.2.0/30 via 198.51.100.1
ip netns exec dtun-r sysctl -qw net.ipv4.ip_forward=1
"$ROOT/scripts/certgen.sh" "$TMP" client
"$ROOT/scripts/certgen.sh" "$TMP" server
ip netns exec dtun-c tcpdump -U -ni dc -w "$OUT/outer.pcap" udp >"$OUT/capture.log" 2>&1 &
CAP=$!
ip netns exec dtun-s "$BIN" "${RESOURCE_ARGS[@]}" --trace-icmp-drops="${TRACE_ICMP_DROPS:-false}" --max-rate-bps "${MAX_RATE_BPS:-0}" --server --ports "${TEST_PORTS:-80}" --endpoint 0.0.0.0:80 --allow-ip 192.0.2.1 --address 10.255.255.2/30 --cert "$TMP/server.crt" --key "$TMP/server.key" --peer-pin "$(cat "$TMP/client.pin")" >"$OUT/server.log" 2>&1 &
SP=$!
ip netns exec dtun-c "$BIN" "${RESOURCE_ARGS[@]}" --trace-icmp-drops="${TRACE_ICMP_DROPS:-false}" --max-rate-bps "${MAX_RATE_BPS:-0}" --ports "${TEST_PORTS:-80}" --switch-interval "${TEST_SWITCH_INTERVAL:-0}" --endpoint 198.51.100.2:80 --address 10.255.255.1/30 --cert "$TMP/client.crt" --key "$TMP/client.key" --peer-pin "$(cat "$TMP/server.pin")" >"$OUT/client.log" 2>&1 &
CP=$!
for i in $(seq 1 50); do if grep -q 'session established' "$OUT/client.log" && grep -q 'session established' "$OUT/server.log"; then break; fi; sleep .2; done
grep -q 'session established' "$OUT/client.log"
ip netns exec dtun-c tcpdump -U -ni dtun0 -w "$OUT/client-tun.pcap" >"$OUT/client-capture.log" 2>&1 &
CC=$!
ip netns exec dtun-s tcpdump -U -ni dtun0 -w "$OUT/server-tun.pcap" >"$OUT/server-capture.log" 2>&1 &
SC=$!
sleep .3
LAB_MTU=$(ip -j -n dtun-c link show dtun0 | python3 -c 'import json,sys;print(json.load(sys.stdin)[0]["mtu"])')
PING_PAYLOAD=$(( LAB_MTU < 1500 ? LAB_MTU-28 : 1472 ))
ip netns exec dtun-c ping -c 5 -W 2 -M do -s "$PING_PAYLOAD" 10.255.255.2 | tee "$OUT/ping1500.txt"
ip netns exec dtun-s iperf3 -s -B 10.255.255.2 >"$OUT/iperf-server.log" 2>&1 &
sleep .3
ip netns exec dtun-c iperf3 -c 10.255.255.2 -t 5 -J > "$OUT/tcp.json"
ip netns exec dtun-c iperf3 -c 10.255.255.2 -t 5 -R -J > "$OUT/tcp-reverse.json"
ip netns exec dtun-c iperf3 -c 10.255.255.2 -u -b 5M -l "$PING_PAYLOAD" -t 3 -J > "$OUT/udp.json"
# IPv6 L3 forwarding on the TUN only (not IPv6 Internet exit).
if [ "$LAB_MTU" -ge 1280 ]; then
ip -n dtun-c -6 addr add fd42:6474:756e::1/64 dev dtun0
ip -n dtun-s -6 addr add fd42:6474:756e::2/64 dev dtun0
sleep 1
V6_PAYLOAD=$(( LAB_MTU < 1500 ? LAB_MTU-48 : 1452 ))
ip netns exec dtun-c ping -6 -c 3 -W 2 -M do -s "$V6_PAYLOAD" fd42:6474:756e::2 > "$OUT/ping6.txt"
fi
kill -INT "$CAP" "$CC" "$SC"; wait "$CAP" "$CC" "$SC" || true
python3 "$ROOT/scripts/check-pcap.py" "$OUT/outer.pcap" "$OUT/client-tun.pcap" "$OUT/server-tun.pcap" | tee "$OUT/pcap-check.json"
# Fault injection remains entirely within isolated router namespace.
if [ "${MATRIX:-0}" = 1 ]; then
 for rtt in 50 100 200; do for loss in 0 0.1 1 3; do
  "$ROOT/scripts/netem.sh" "$rtt" "$loss"
  ip netns exec dtun-c iperf3 -c 10.255.255.2 -t 10 -J > "$OUT/netem-rtt${rtt}-loss${loss}.json"
 done; done
fi
if [ -n "${LAB_HOOK:-}" ]; then "$LAB_HOOK" "$OUT"; fi
kill -TERM "$CP" "$SP"; wait "$CP" "$SP" || true
printf 'Lab passed; artifacts: %s\n' "$OUT"
