#!/usr/bin/env bash
set -euo pipefail
exec "$(dirname "$0")/stability-lab.sh" --hook "$1"
