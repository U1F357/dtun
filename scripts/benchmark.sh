#!/usr/bin/env bash
set -euo pipefail
# On HK first: iperf3 -s -B 10.255.255.2 -p 5209
# On BJ: scripts/benchmark.sh [output-directory]
OUT=${1:-reports/public-repeat}
mkdir -p "$OUT"
NS=(ip netns exec dtun-exit)
"${NS[@]}" ping -c 10 -W 2 -M do -s 1472 10.255.255.2 > "$OUT/ping1500.txt"
"${NS[@]}" iperf3 -c 10.255.255.2 -p 5209 -t 10 -J > "$OUT/tcp.json"
sleep 2
"${NS[@]}" iperf3 -c 10.255.255.2 -p 5209 -R -t 10 -J > "$OUT/tcp-reverse.json"
sleep 2
"${NS[@]}" iperf3 -c 10.255.255.2 -p 5209 -u -b 5M -l 1472 -t 5 -J > "$OUT/udp.json"
