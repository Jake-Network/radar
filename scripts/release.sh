#!/usr/bin/env bash
set -euo pipefail
# Backward-compatible native release entry point: release.sh [OUTPUT_DIR] [VERSION].
# Python is a maintainer packaging dependency; installed binaries need neither it
# nor Go/C compilers for source analysis. Windows uses release.py directly.
project_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
exec python3 "$project_dir/scripts/release.py" "${1:-$project_dir/dist}" "${2:-${RADAR_VERSION:-}}"
