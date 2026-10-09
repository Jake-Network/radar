#!/usr/bin/env bash
set -euo pipefail
# Build this checkout; no remote installer or privileged write is used.
project_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
install_dir=${1:-"$HOME/.local/bin"}
cd "$project_dir"
mkdir -p "$install_dir"
go build -trimpath -o "$install_dir/radar" ./cmd/radar
printf 'Installed Radar at %s/radar. Add this directory to PATH.\n' "$install_dir"
