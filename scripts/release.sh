#!/usr/bin/env bash
set -euo pipefail
# Native-host packaging. GNU tar, gzip, Go and a C compiler are required.
project_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
output_dir=${1:-"$project_dir/dist"}
mkdir -p "$output_dir"
output_dir=$(cd "$output_dir" && pwd)
cd "$project_dir"
platform="$(go env GOOS)_$(go env GOARCH)"
if [[ "$platform" != linux_amd64 || "$(go env CGO_ENABLED)" != 1 || "$(uname -s)" != Linux || "$(uname -m)" != x86_64 ]]; then
    printf 'Supported release target is native Linux amd64 with CGO_ENABLED=1; got %s.\n' "$platform" >&2
    exit 1
fi
package_dir=$(mktemp -d "${TMPDIR:-/tmp}/radar-release.XXXXXX")
trap 'rm -rf "$package_dir"' EXIT
stage="$package_dir/radar-$platform"
mkdir -p "$stage/licenses"
go build -trimpath -buildvcs=false -ldflags=-buildid= -o "$stage/radar" ./cmd/radar
cp LICENSE README.md CONTRIBUTING.md "$stage/"
cp -R third_party docs integrations schemas "$stage/"
# Include clean runnable examples, never local Radar databases/configuration.
python3 - "$project_dir/examples" "$stage/examples" <<'PY_EXAMPLES'
import pathlib, shutil, sys
source, target = map(pathlib.Path, sys.argv[1:])
shutil.copytree(source, target, ignore=shutil.ignore_patterns('.radar', '__pycache__', 'node_modules', 'target'))
for binding in source.rglob('.radar/contracts.json'):
    destination = target / binding.relative_to(source)
    destination.parent.mkdir(parents=True, exist_ok=True)
    shutil.copyfile(binding, destination)
PY_EXAMPLES
# Collect modules contributing packages to the executable, including indirect libraries.
go list -deps -json ./cmd/radar > "$stage/modules.json"
python3 - "$stage/modules.json" "$stage/licenses" <<'PY_LICENSES'
import json, pathlib, shutil, sys
text = pathlib.Path(sys.argv[1]).read_text()
decoder = json.JSONDecoder()
modules = {}
while text.strip():
    item, end = decoder.raw_decode(text.lstrip())
    text = text.lstrip()[end:]
    item = item.get('Module', {})
    if item.get('Main') or not item.get('Dir'):
        continue
    modules[item['Path']] = item
for item in modules.values():
    source = pathlib.Path(item['Dir'])
    dest = pathlib.Path(sys.argv[2]) / (item['Path'].replace('/', '_') + '@' + item.get('Version', ''))
    for candidate in source.iterdir():
        if candidate.is_file() and candidate.name.upper().startswith(('LICENSE', 'COPYING', 'COPYRIGHT', 'NOTICE')):
            dest.mkdir(parents=True, exist_ok=True)
            shutil.copyfile(candidate, dest / candidate.name)
# Inventory uses module identities, never host cache paths.
inventory = pathlib.Path(sys.argv[2]).parent / 'DEPENDENCIES.txt'
inventory.write_text(''.join(item['Path'] + ' ' + item.get('Version','') + '\n' for _, item in sorted(modules.items())))
PY_LICENSES
python3 - "$stage/modules.json" <<'PY_INVENTORY'
import pathlib, sys
pathlib.Path(sys.argv[1]).unlink()
PY_INVENTORY
archive="$output_dir/radar-$platform.tar.gz"
tar --sort=name --mtime="@${SOURCE_DATE_EPOCH:-0}" --owner=0 --group=0 --numeric-owner -C "$package_dir" -cf - "radar-$platform" | gzip -n > "$archive"
(cd "$output_dir" && sha256sum "radar-$platform.tar.gz" > "radar-$platform.tar.gz.sha256")
printf 'Release archive: %s\n' "$archive"
