#!/usr/bin/env bash
set -euo pipefail
# Run after dtun0 exists. Owns only inet dtun_poc and its scoped routes/sysctl.
WAN=${1:-eth0}
ip route replace 10.254.253.0/30 dev dtun0
mkdir -p /var/lib/dtun
if [ ! -e /var/lib/dtun/previous-ip-forward ]; then sysctl -n net.ipv4.ip_forward > /var/lib/dtun/previous-ip-forward; fi
sysctl -qw net.ipv4.ip_forward=1
nft list table inet dtun_poc >/dev/null 2>&1 && nft delete table inet dtun_poc
nft -f - <<NFT
table inet dtun_poc {
 chain forward {
  type filter hook forward priority -10; policy accept;
  iifname "dtun0" ip saddr != { 10.255.255.1, 10.254.253.2 } drop
  iifname "dtun0" oifname "$WAN" accept
  iifname "$WAN" oifname "dtun0" ct state established,related accept
 }
 chain postrouting {
  type nat hook postrouting priority srcnat; policy accept;
  oifname "$WAN" ip saddr { 10.255.255.1, 10.254.253.2 } masquerade
 }
}
NFT
