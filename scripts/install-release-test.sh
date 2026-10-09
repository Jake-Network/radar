#!/usr/bin/env bash
set -euo pipefail
project_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
fixture=$(mktemp -d "${TMPDIR:-/tmp}/radar-installer-test.XXXXXX")
trap 'rm -rf "$fixture"' EXIT
case "$(uname -s)" in
    Linux) installer_os=linux ;;
    Darwin) installer_os=darwin ;;
    *) printf 'Unix installer regressions require Linux or macOS.\n' >&2; exit 2 ;;
esac
case "$(uname -m)" in
    x86_64|amd64) installer_arch=amd64 ;;
    aarch64|arm64) installer_arch=arm64 ;;
    *) printf 'Unsupported test host architecture.\n' >&2; exit 2 ;;
esac
platform="${installer_os}_${installer_arch}"
checksum_file() {
    python3 - "$fixture/assets/radar-$platform.tar.gz" <<'PY_CHECKSUM'
import hashlib, pathlib, sys
p = pathlib.Path(sys.argv[1])
p.with_name(p.name + '.sha256').write_text(hashlib.sha256(p.read_bytes()).hexdigest() + '  ' + p.name + '\n')
PY_CHECKSUM
}
binary_digest() {
    python3 - "$fixture/installed/radar" <<'PY_DIGEST'
import hashlib, pathlib, sys
print(hashlib.sha256(pathlib.Path(sys.argv[1]).read_bytes()).hexdigest())
PY_DIGEST
}
mkdir -p "$fixture/tools" "$fixture/package/radar-$platform" "$fixture/assets"
printf '#!/usr/bin/env bash\nprintf "radar 0.2.0\\n"\n' > "$fixture/package/radar-$platform/radar"
chmod +x "$fixture/package/radar-$platform/radar"
tar -czf "$fixture/assets/radar-$platform.tar.gz" -C "$fixture/package" "radar-$platform"
checksum_file
cat > "$fixture/tools/curl" <<'MOCK'
#!/usr/bin/env bash
set -euo pipefail
url=""; output=""
while (($#)); do
  case "$1" in
    -o) output=$2; shift 2 ;;
    --proto|--proto-redir) shift 2 ;;
    https://*) url=$1; shift ;;
    *) shift ;;
  esac
done
[[ "$url" == https://github.com/Jake-Network/radar/releases/download/v0.2.0/* ]]
cp "$RADAR_INSTALL_TEST_ASSETS/${url##*/}" "$output"
MOCK
cat > "$fixture/tools/getconf" <<'MOCK'
#!/usr/bin/env bash
printf 'glibc 2.35\n'
MOCK
chmod +x "$fixture/tools/"*
export PATH="$fixture/tools:$PATH"
export RADAR_INSTALL_TEST_ASSETS="$fixture/assets"
installer="$project_dir/scripts/install-release.sh"
bash "$installer" --version v0.2.0 --dir "$fixture/installed"
test "$("$fixture/installed/radar" version)" = 'radar 0.2.0'
test -f "$fixture/installed/radar-v0.2.0-notices.tar.gz"
if bash "$installer" --version v0.2.0 --dir "$fixture/installed"; then exit 1; fi
bash "$installer" --version v0.2.0 --dir "$fixture/installed" --force
if bash "$installer" --version invalid --dir "$fixture/invalid"; then exit 1; fi
printf '%064d  radar-%s.tar.gz\n' 0 "$platform" > "$fixture/assets/radar-$platform.tar.gz.sha256"
if bash "$installer" --version v0.2.0 --dir "$fixture/corrupt"; then exit 1; fi
test ! -e "$fixture/corrupt/radar"
checksum_file
if [[ "$installer_os" == linux ]]; then
    printf '#!/usr/bin/env bash\nprintf "glibc 2.34\\n"\n' > "$fixture/tools/getconf"
    if bash "$installer" --version v0.2.0 --dir "$fixture/old-libc"; then exit 1; fi
    test ! -e "$fixture/old-libc/radar"
fi
printf '#!/usr/bin/env bash\nprintf "glibc 2.35\\n"\n' > "$fixture/tools/getconf"
mv "$fixture/package/radar-$platform/radar" "$fixture/package/real-radar"
ln -s ../real-radar "$fixture/package/radar-$platform/radar"
tar -czf "$fixture/assets/radar-$platform.tar.gz" -C "$fixture/package" "radar-$platform"
checksum_file
if bash "$installer" --version v0.2.0 --dir "$fixture/symlink"; then exit 1; fi
test ! -e "$fixture/symlink/radar"
printf 'Release installer tests passed.\n'
# Malicious archives cannot overwrite an already installed executable.
original=$(binary_digest)
python3 - "$fixture/assets/radar-$platform.tar.gz" "$platform" <<'PY_ARCHIVE'
import io, sys, tarfile
with tarfile.open(sys.argv[1], 'w:gz') as t:
    binary = b'#!/bin/sh\necho radar 0.2.0\n'
    entry = tarfile.TarInfo('radar-' + sys.argv[2] + '/radar'); entry.size = len(binary); entry.mode = 0o755
    t.addfile(entry, io.BytesIO(binary))
    bad = tarfile.TarInfo('radar-' + sys.argv[2] + '/../../outside'); bad.size = 1
    t.addfile(bad, io.BytesIO(b'x'))
PY_ARCHIVE
checksum_file
if bash "$installer" --version v0.2.0 --dir "$fixture/installed" --force; then exit 1; fi
test "$(binary_digest)" = "$original"
test ! -e "$fixture/outside"
python3 - "$fixture/assets/radar-$platform.tar.gz" "$platform" <<'PY_ARCHIVE'
import io, sys, tarfile
with tarfile.open(sys.argv[1], 'w:gz') as t:
    for _ in range(2):
        binary = b'#!/bin/sh\necho radar 0.2.0\n'
        entry = tarfile.TarInfo('radar-' + sys.argv[2] + '/radar'); entry.size = len(binary); entry.mode = 0o755
        t.addfile(entry, io.BytesIO(binary))
PY_ARCHIVE
checksum_file
if bash "$installer" --version v0.2.0 --dir "$fixture/duplicate"; then exit 1; fi
test ! -e "$fixture/duplicate/radar"
printf 'Traversal, duplicate and non-destructive failure tests passed.\n'

# A valid checksum for the wrong executable version still cannot replace Radar.
python3 - "$fixture/assets/radar-$platform.tar.gz" "$platform" <<'PY_ARCHIVE'
import io,sys,tarfile
with tarfile.open(sys.argv[1], 'w:gz') as t:
    binary=b'#!/bin/sh\necho radar 9.9.9\n'
    entry=tarfile.TarInfo('radar-' + sys.argv[2] + '/radar');entry.size=len(binary);entry.mode=0o755
    t.addfile(entry,io.BytesIO(binary))
PY_ARCHIVE
checksum_file
if bash "$installer" --version v0.2.0 --dir "$fixture/installed" --force; then exit 1; fi
test "$(binary_digest)" = "$original"
printf 'Executable version mismatch preserves existing installation.\n'
