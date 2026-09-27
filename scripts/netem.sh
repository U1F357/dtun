#!/usr/bin/env bash
set -euo pipefail
# ONLY touches the isolated lab router, never the real host uplink.
rtt=${1:?RTT milliseconds}; loss=${2:?loss percent per direction}
for dev in drc drs; do
 ip netns exec dtun-r tc qdisc replace dev "$dev" root netem delay "$((rtt/2))ms" loss "$loss%" rate 100mbit limit 1000
 done
