#!/usr/bin/env bash
set -euo pipefail
# Run on BJ only. HK must have iperf3 listeners on 10.255.255.2:5209 and :5210.
OUT=${1:-/opt/dtun-poc/reports/pacer}
mkdir -p "$OUT"
NS=(ip netns exec dtun-exit)
"${NS[@]}" iperf3 -c 10.255.255.2 -p 5209 -t 15 -J > "$OUT/80-forward.json"
sleep 2
"${NS[@]}" iperf3 -c 10.255.255.2 -p 5209 -t 15 -R -J > "$OUT/80-reverse.json"
sleep 2
"${NS[@]}" iperf3 -c 10.255.255.2 -p 5209 -u -b 100M -l 1472 -t 10 -J > "$OUT/80-overload-udp.json"
sleep 2
"${NS[@]}" iperf3 -c 10.255.255.2 -p 5209 -P 2 -t 20 -J > "$OUT/80-duplex-forward.json" &
pid=$!
"${NS[@]}" iperf3 -c 10.255.255.2 -p 5210 -P 2 -R -t 20 -J > "$OUT/80-duplex-reverse.json"
wait "$pid"
"${NS[@]}" ping -c 5 -W 2 -M do -s 1472 10.255.255.2 > "$OUT/80-final-ping.txt"
