#!/usr/bin/env bash
set -euo pipefail
OUT=${1:?}
# This hook expects both TUN interfaces at MTU 9000; outer veths remain 1500.
ip netns exec dtun-c ping -c 3 -W 2 -M do -s 8972 10.255.255.2 > "$OUT/jumbo-ipv4.txt"
ip netns exec dtun-c ping -6 -c 3 -W 2 -M do -s 8952 fd42:6474:756e::2 > "$OUT/jumbo-ipv6.txt"
ip netns add dtmtu-e
trap 'ip netns pids dtmtu-e 2>/dev/null | xargs -r kill 2>/dev/null || true; ip netns del dtmtu-e' EXIT
ip -n dtmtu-e link set lo up
ip link add dme-s type veth peer name dme-e
ip link set dme-s netns dtun-s;ip link set dme-e netns dtmtu-e
ip -n dtun-s addr add 203.0.113.1/24 dev dme-s
ip -n dtmtu-e addr add 203.0.113.2/24 dev dme-e
ip -n dtmtu-e addr add 203.0.113.3/32 dev dme-e
ip -n dtun-s -6 addr add fd42:eeee::1/64 dev dme-s nodad
ip -n dtmtu-e -6 addr add fd42:eeee::2/64 dev dme-e nodad
ip -n dtun-s link set dme-s mtu 1500 up;ip -n dtmtu-e link set dme-e mtu 1500 up
ip -n dtun-c route add 203.0.113.0/24 via 10.255.255.2
ip -n dtmtu-e route add 10.255.255.0/30 via 203.0.113.1
ip -n dtun-c -6 route add fd42:eeee::/64 via fd42:6474:756e::2
ip -n dtmtu-e -6 route add fd42:6474:756e::/64 via fd42:eeee::1
ip netns exec dtun-s sysctl -qw net.ipv4.ip_forward=1
ip netns exec dtun-s sysctl -qw net.ipv6.conf.all.forwarding=1
if ip netns exec dtun-c ping -c 1 -W 2 -M do -s 1972 203.0.113.2 > "$OUT/exit-ipv4-df.txt" 2>&1;then echo 'DF oversized packet incorrectly succeeded';exit 1;fi
grep -qi 'mtu.*1500' "$OUT/exit-ipv4-df.txt"
ip netns exec dtun-c ping -c 3 -W 2 -M do -s 1472 203.0.113.2 > "$OUT/exit-ipv4-fit.txt"
ip netns exec dtmtu-e tcpdump -U -ni dme-e -w "$OUT/exit-fragments.pcap" icmp > "$OUT/exit-capture.log" 2>&1 & cap=$!
sleep .2
ip netns exec dtun-c ping -c 3 -W 2 -M dont -s 1972 203.0.113.3 > "$OUT/exit-ipv4-fragment.txt"
kill -INT "$cap";wait "$cap" || true
tcpdump -nn -r "$OUT/exit-fragments.pcap" 'ip[6:2] & 0x3fff != 0' > "$OUT/exit-fragments.txt" 2>/dev/null
test -s "$OUT/exit-fragments.txt"
if ip netns exec dtun-c ping -6 -c 1 -W 2 -M do -s 1952 fd42:eeee::2 > "$OUT/exit-ipv6-ptb.txt" 2>&1;then echo 'IPv6 oversized packet incorrectly succeeded';exit 1;fi
grep -qi 'mtu.*1500' "$OUT/exit-ipv6-ptb.txt"
ip netns exec dtun-c ping -6 -c 3 -W 2 -M do -s 1452 fd42:eeee::2 > "$OUT/exit-ipv6-fit.txt"
ip netns exec dtmtu-e iperf3 -s -B 203.0.113.2 -1 > "$OUT/exit-tcp-server.txt" 2>&1 & server=$!
sleep .2
ip netns exec dtun-c iperf3 -c 203.0.113.2 -t 5 -J > "$OUT/exit-tcp.json"
wait "$server"
echo 'JUMBO TUN AND SMALLER EGRESS MTU TESTS PASSED'
