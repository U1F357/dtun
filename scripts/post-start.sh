#!/usr/bin/env bash
set -euo pipefail
for i in $(seq 1 50); do ip link show dtun0 >/dev/null 2>&1 && break; sleep .1; done
if [ "$1" = server ]; then
 exec /opt/dtun-poc/scripts/setup-server.sh eth0
else
 exec /opt/dtun-poc/scripts/setup-client.sh 47.242.247.74 172.25.63.253 eth0
fi
