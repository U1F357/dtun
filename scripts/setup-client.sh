#!/usr/bin/env bash
set -euo pipefail
REMOTE=${1:?HK IPv4}; GW=${2:?underlay gateway}; WAN=${3:-eth0}
TUN_MTU=$(cat /sys/class/net/dtun0/mtu)
# Explicit endpoint underlay exception, even though policy routing is scoped.
ip route replace "$REMOTE/32" via "$GW" dev "$WAN"
# Test-only routing: remove this project's former default policies.
ip rule del pref 11880 from 10.255.255.1/32 table 51880 2>/dev/null || true
ip rule del pref 11881 from 10.254.253.2/32 table 51880 2>/dev/null || true
ip route flush table 51880 2>/dev/null || true
ip netns list | awk '{print $1}' | grep -qx dtun-exit || ip netns add dtun-exit
if ! ip link show dtexit-host >/dev/null 2>&1; then
 ip link add dtexit-host type veth peer name dtexit-ns
 ip link set dtexit-ns netns dtun-exit
fi
ip addr replace 10.254.253.1/30 dev dtexit-host
ip link set dtexit-host mtu "$TUN_MTU" up
ip -n dtun-exit link set lo up
ip -n dtun-exit addr replace 10.254.253.2/30 dev dtexit-ns
ip -n dtun-exit link set dtexit-ns mtu "$TUN_MTU" up
ip -n dtun-exit route del default 2>/dev/null || true
ip -n dtun-exit route replace 10.255.255.0/30 via 10.254.253.1
mkdir -p /etc/netns/dtun-exit
printf 'nameserver 1.1.1.1\nnameserver 8.8.8.8\n' > /etc/netns/dtun-exit/resolv.conf
# Loose RPF only on our TUN, needed for asymmetric policy routing.
sysctl -qw net.ipv4.conf.dtun0.rp_filter=2
sysctl -qw net.ipv4.ip_forward=1
iptables -C FORWARD -i dtexit-host -o dtun0 -s 10.254.253.2/32 -j ACCEPT 2>/dev/null || iptables -I FORWARD 1 -i dtexit-host -o dtun0 -s 10.254.253.2/32 -j ACCEPT
iptables -C FORWARD -i dtun0 -o dtexit-host -d 10.254.253.2/32 -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT 2>/dev/null || iptables -I FORWARD 1 -i dtun0 -o dtexit-host -d 10.254.253.2/32 -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT
# firewalld's nft backend is a second forward filter AFTER iptables. Add only
# this namespace-to-TUN exception, without reloading unrelated runtime policies.
if nft list chain inet firewalld filter_FORWARD >/dev/null 2>&1; then
 if ! nft list chain inet firewalld filter_FORWARD | grep -q 'dtun-poc-exit'; then
  nft insert rule inet firewalld filter_FORWARD iifname '"dtexit-host"' oifname '"dtun0"' ip saddr 10.254.253.2 accept comment '"dtun-poc-exit"'
 fi
fi
