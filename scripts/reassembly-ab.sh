#!/usr/bin/env bash
set -euo pipefail
# Isolated, targeted A/B lab. No default routes; no host uplink changes.
ROOT=$(cd "$(dirname "$0")/.." && pwd)
OUT=${OUT:-$ROOT/reports/reassembly}
VARIANTS=${VARIANTS:-old}
REPEATS=${REPEATS:-3}
SECONDS_PER_RUN=${SECONDS_PER_RUN:-10}
BIN_DIR=${BIN_DIR:-/opt/dtun-poc/reassembly-variants}
mkdir -p "$OUT"
TMP=$(mktemp -d)
for ns in dtab-c dtab-r dtab-s; do
 if ip netns list | awk '{print $1}' | grep -qx "$ns"; then echo "$ns already exists";exit 1;fi
 done
cleanup(){
 for ns in dtab-c dtab-r dtab-s; do ip netns pids "$ns" 2>/dev/null | xargs -r kill 2>/dev/null || true;done
 sleep .2
 for ns in dtab-c dtab-r dtab-s; do ip netns del "$ns" 2>/dev/null || true;done
 rm -rf "$TMP"
}
trap cleanup EXIT
for ns in dtab-c dtab-r dtab-s; do ip netns add "$ns";ip -n "$ns" link set lo up;done
ip link add abc type veth peer name abrc
ip link set abc netns dtab-c;ip link set abrc netns dtab-r
ip link add abs type veth peer name abrs
ip link set abs netns dtab-s;ip link set abrs netns dtab-r
ip -n dtab-c addr add 192.0.2.1/30 dev abc
ip -n dtab-r addr add 192.0.2.2/30 dev abrc
ip -n dtab-r addr add 198.51.100.1/30 dev abrs
ip -n dtab-s addr add 198.51.100.2/30 dev abs
for spec in 'dtab-c abc' 'dtab-r abrc' 'dtab-r abrs' 'dtab-s abs';do read -r ns dev <<< "$spec";ip -n "$ns" link set "$dev" mtu 1500 up;done
ip -n dtab-c route add 198.51.100.0/30 via 192.0.2.2
ip -n dtab-s route add 192.0.2.0/30 via 198.51.100.1
ip netns exec dtab-r sysctl -qw net.ipv4.ip_forward=1
"$ROOT/scripts/certgen.sh" "$TMP" client
"$ROOT/scripts/certgen.sh" "$TMP" server
# Alternate variants per repetition to reduce order/time bias.
for rep in $(seq 1 "$REPEATS");do
 for variant in $VARIANTS;do
  run="$OUT/$variant-$rep";mkdir -p "$run"
  for dev in abrc abrs;do ip netns exec dtab-r tc qdisc del dev "$dev" root 2>/dev/null || true;done
  bin="$BIN_DIR/dtun-$variant"
  ip netns exec dtab-s "$bin" --max-rate-bps "${PACER_BPS:-80000000}" --server --endpoint 0.0.0.0:80 --allow-ip 192.0.2.1 --address 10.255.255.2/30 --cert "$TMP/server.crt" --key "$TMP/server.key" --peer-pin "$(cat "$TMP/client.pin")" > "$run/server.log" 2>&1 &
  sp=$!
  ip netns exec dtab-c "$bin" --max-rate-bps "${PACER_BPS:-80000000}" --endpoint 198.51.100.2:80 --address 10.255.255.1/30 --cert "$TMP/client.crt" --key "$TMP/client.key" --peer-pin "$(cat "$TMP/server.pin")" > "$run/client.log" 2>&1 &
  cp=$!
  for i in $(seq 1 100);do if grep -qs 'session established' "$run/client.log" && grep -qs 'session established' "$run/server.log";then break;fi;sleep .1;done
  grep -q 'session established' "$run/client.log"
  ip netns exec dtab-c ping -c 1 -W 2 10.255.255.2 > "$run/before-ping.txt"
  ip netns exec dtab-s iperf3 -s -B 10.255.255.2 -1 -J > "$run/server-iperf.json" &
  ipid=$!
  for dev in abrc abrs;do ip netns exec dtab-r tc qdisc replace dev "$dev" root netem rate "${LINK_RATE:-20mbit}" limit "${QLIMIT:-100}";done
  sleep .2
  python3 "$ROOT/scripts/sample-cpu.py" --pid "$sp" --seconds "$SECONDS_PER_RUN" --out "$run/server-cpu.json" &
  mp=$!
  ip netns exec dtab-c iperf3 -c 10.255.255.2 -u -b "${OFFERED:-100M}" -l "${PAYLOAD:-1472}" -t "$SECONDS_PER_RUN" -J > "$run/client-iperf.json"
  wait "$ipid" "$mp"
  ip netns exec dtab-r tc -s -j qdisc show dev abrs > "$run/forward-qdisc.json"
  ip netns exec dtab-r tc -s -j qdisc show dev abrc > "$run/reverse-qdisc.json"
  for dev in abrc abrs;do ip netns exec dtab-r tc qdisc del dev "$dev" root;done
  sleep 1.2
  ip netns exec dtab-c ping -c 1 -W 2 -M do -s 1472 10.255.255.2 > "$run/recovery-ping.txt"
  kill -TERM "$cp" "$sp";wait "$cp" "$sp" || true
  python3 - "$run" <<'PY'
import json,sys,pathlib
p=pathlib.Path(sys.argv[1]);s=json.load(open(p/'server-iperf.json'));e=s.get('end',{});assert e and not s.get('error'),s
u=e.get('sum',{});ints=s['intervals'];n=sum(x['sum']['bytes'] for x in ints);seconds=ints[-1]['sum']['end']-ints[0]['sum']['start'];print(p.name,'loss',u.get('lost_percent'),'receiver Mbps',n*8/seconds/1e6,flush=True)
PY
 done
done
