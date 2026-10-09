#!/usr/bin/env bash
set -euo pipefail
# Install a published, explicit release. No Go toolchain or root access required.
usage() {
    printf 'Usage: bash install-release.sh --version vX.Y.Z [--dir PATH] [--force]\n'
}
version=""
install_dir="$HOME/.local/bin"
force=false
while (($#)); do
    case "$1" in
        --version|--dir)
            if (($# < 2)); then usage >&2; exit 2; fi
            if [[ "$1" == --version ]]; then version=$2; else install_dir=$2; fi
            shift 2 ;;
        --force) force=true; shift ;;
        --help|-h) usage; exit 0 ;;
        *) usage >&2; exit 2 ;;
    esac
done
if [[ ! "$version" =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-[A-Za-z0-9.-]+)?$ ]]; then
    printf 'Specify an explicit published version, for example --version v0.2.0.\n' >&2
    exit 2
fi
if [[ "$(uname -s)" != Linux || "$(uname -m)" != x86_64 ]]; then
    printf 'Published binary installation currently supports Linux x86_64 only.\n' >&2
    exit 2
fi
for utility in curl tar sha256sum install mktemp getconf; do
    command -v "$utility" >/dev/null || { printf 'Required tool missing: %s\n' "$utility" >&2; exit 2; }
done
# Release workflow builds on Ubuntu 22.04. Musl and older glibc are unsupported.
libc=$(getconf GNU_LIBC_VERSION 2>/dev/null || true)
if [[ ! "$libc" =~ ^glibc\ ([0-9]+)\.([0-9]+)$ ]] || (( BASH_REMATCH[1] < 2 || (BASH_REMATCH[1] == 2 && BASH_REMATCH[2] < 35) )); then
    printf 'Release binaries require glibc 2.35 or newer; detected %s. Build from source on other runtimes.\n' "${libc:-unknown}" >&2
    exit 2
fi
if [[ -z "$install_dir" ]]; then printf 'Installation directory cannot be empty.\n' >&2; exit 2; fi
if [[ -e "$install_dir/radar" || -L "$install_dir/radar" ]]; then
    if [[ "$force" != true ]]; then
        printf 'Existing installation preserved: %s/radar. Use --force to replace it.\n' "$install_dir" >&2
        exit 2
    fi
fi
temporary_dir=$(mktemp -d "${TMPDIR:-/tmp}/radar-install.XXXXXX")
staged_binary=""
trap 'rm -rf "$temporary_dir"; if [[ -n "$staged_binary" ]]; then rm -f "$staged_binary"; fi' EXIT
archive=radar-linux_amd64.tar.gz
base_url="https://github.com/Jake-Network/radar/releases/download/$version"
curl --fail --silent --show-error --location --proto '=https' --proto-redir '=https' "$base_url/$archive" -o "$temporary_dir/$archive"
curl --fail --silent --show-error --location --proto '=https' --proto-redir '=https' "$base_url/$archive.sha256" -o "$temporary_dir/checksum"
# Parse only the expected archive checksum; never execute downloaded text.
read -r digest filename extra < "$temporary_dir/checksum"
if [[ ! "$digest" =~ ^[a-fA-F0-9]{64}$ || "$filename" != "$archive" || -n "${extra:-}" ]]; then
    printf 'Invalid release checksum file.\n' >&2; exit 1
fi
printf '%s  %s\n' "$digest" "$archive" > "$temporary_dir/verified.sha256"
(cd "$temporary_dir" && sha256sum --check verified.sha256)
# Extract only a regular-file executable member. No archive path is trusted.
member=radar-linux_amd64/radar
listing=$(tar -tvzf "$temporary_dir/$archive" -- "$member")
if [[ "$listing" != -* || "$listing" == *$'\n'* ]]; then
    printf 'Invalid binary archive member.\n' >&2; exit 1
fi
tar -xOzf "$temporary_dir/$archive" -- "$member" > "$temporary_dir/radar"
chmod 755 "$temporary_dir/radar"
"$temporary_dir/radar" version
mkdir -p "$install_dir"
# Keep the original archive alongside the executable so all dependency notices survive.
install -m 644 "$temporary_dir/$archive" "$install_dir/radar-$version-notices.tar.gz"
staged_binary=$(mktemp "$install_dir/.radar-install.XXXXXX")
install -m 755 "$temporary_dir/radar" "$staged_binary"
mv -fT "$staged_binary" "$install_dir/radar"
staged_binary=""
printf 'Installed %s/radar. Add %s to PATH. Notices: radar-%s-notices.tar.gz\n' "$install_dir" "$install_dir" "$version"
