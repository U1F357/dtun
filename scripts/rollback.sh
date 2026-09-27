#!/usr/bin/env bash
set -euo pipefail
role=${1:?client or server}
systemctl disable --now dtun-poc.service
if [ "$role" = client ]; then
 if nft list chain inet firewalld filter_FORWARD >/dev/null 2>&1; then
 nft -a list chain inet firewalld filter_FORWARD | awk '/comment "dtun-poc-exit"/ {print $NF}' | while read -r handle; do nft delete rule inet firewalld filter_FORWARD handle "$handle"; done
 fi
 ip rule del pref 11880 from 10.255.255.1/32 table 51880 2>/dev/null || true
 ip rule del pref 11881 from 10.254.253.2/32 table 51880 2>/dev/null || true
 ip route flush table 51880
 iptables -D FORWARD -i dtexit-host -o dtun0 -s 10.254.253.2/32 -j ACCEPT 2>/dev/null || true
 iptables -D FORWARD -i dtun0 -o dtexit-host -d 10.254.253.2/32 -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT 2>/dev/null || true
 ip netns del dtun-exit 2>/dev/null || true
 ip link del dtexit-host 2>/dev/null || true
 ip route del 47.242.247.74/32 via 172.25.63.253 dev eth0 2>/dev/null || true
else
 nft delete table inet dtun_poc 2>/dev/null || true
 if [ -f /var/lib/dtun/previous-ip-forward ]; then sysctl -qw "net.ipv4.ip_forward=$(cat /var/lib/dtun/previous-ip-forward)"; fi
fi
