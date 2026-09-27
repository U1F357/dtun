#!/usr/bin/env bash
set -euo pipefail
OUT=${1:?}
for port in 5211 5212 5213; do ip netns exec dtun-s iperf3 -s -B 10.255.255.2 -p "$port" > "$OUT/diag-server-$port.log" 2>&1 & done
sleep .3
for round in 1 2 3; do
 ip netns exec dtun-c tcpdump -U -ni dtun0 -w "$OUT/diag-$round-client.pcap" icmp > "$OUT/diag-$round-capture-client.log" 2>&1 & a=$!
 ip netns exec dtun-s tcpdump -U -ni dtun0 -w "$OUT/diag-$round-server.pcap" icmp > "$OUT/diag-$round-capture-server.log" 2>&1 & b=$!
 sleep .2
 date -Is > "$OUT/diag-$round-start.txt"
 ip netns exec dtun-c ping -D -i .1 -c 200 -W 1 -M do -s 1472 10.255.255.2 > "$OUT/diag-$round-ping.txt" & pp=$!
 ip netns exec dtun-c iperf3 -c 10.255.255.2 -p 5211 -t 20 -J > "$OUT/diag-$round-forward.json" & p1=$!
 ip netns exec dtun-c iperf3 -c 10.255.255.2 -p 5212 -t 20 -R -J > "$OUT/diag-$round-reverse.json" & p2=$!
 ip netns exec dtun-c iperf3 -c 10.255.255.2 -p 5213 -t 20 -u -b 1M -l 1472 -J > "$OUT/diag-$round-udp.json" & p3=$!
 wait "$p1" "$p2" "$p3";wait "$pp" || true
 kill -INT "$a" "$b";wait "$a" "$b" || true
 for side in client server; do tcpdump -tt -nn -r "$OUT/diag-$round-$side.pcap" > "$OUT/diag-$round-$side-packets.txt" 2>/dev/null;done
 sleep 1
done
