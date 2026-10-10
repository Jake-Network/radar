#!/usr/bin/env bash
set -euo pipefail
# Install a published, explicit release on Linux or macOS (amd64/arm64).
# No Go toolchain or root access required. On macOS, `brew install` is simpler.
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
    printf 'Specify an explicit published version, for example --version v0.1.0.\n' >&2
    exit 2
fi
case "$(uname -s)" in
    Linux) os=linux ;;
    Darwin) os=darwin ;;
    *) printf 'Published binaries support Linux and macOS; build from source elsewhere.\n' >&2; exit 2 ;;
esac
case "$(uname -m)" in
    x86_64|amd64) arch=amd64 ;;
    aarch64|arm64) arch=arm64 ;;
    *) printf 'Published binaries support amd64 and arm64; build from source elsewhere.\n' >&2; exit 2 ;;
esac
platform="${os}_${arch}"
for utility in curl tar install mktemp; do
    command -v "$utility" >/dev/null || { printf 'Required tool missing: %s\n' "$utility" >&2; exit 2; }
done
if command -v sha256sum >/dev/null; then
    sha256() { sha256sum "$1" | cut -d' ' -f1; }
elif command -v shasum >/dev/null; then
    sha256() { shasum -a 256 "$1" | cut -d' ' -f1; }
else
    printf 'Required tool missing: sha256sum or shasum\n' >&2; exit 2
fi
if [[ "$os" == linux ]]; then
    # Release workflow builds on Ubuntu 22.04. Musl and older glibc are unsupported.
    command -v getconf >/dev/null || { printf 'Required tool missing: getconf\n' >&2; exit 2; }
    libc=$(getconf GNU_LIBC_VERSION 2>/dev/null || true)
    if [[ ! "$libc" =~ ^glibc\ ([0-9]+)\.([0-9]+)$ ]] || (( BASH_REMATCH[1] < 2 || (BASH_REMATCH[1] == 2 && BASH_REMATCH[2] < 35) )); then
        printf 'Release binaries require glibc 2.35 or newer; detected %s. Build from source on other runtimes.\n' "${libc:-unknown}" >&2
        exit 2
    fi
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
archive="radar-$platform.tar.gz"
base_url="https://github.com/Jake-Network/radar/releases/download/$version"
curl --fail --silent --show-error --location --proto '=https' --proto-redir '=https' "$base_url/$archive" -o "$temporary_dir/$archive"
curl --fail --silent --show-error --location --proto '=https' --proto-redir '=https' "$base_url/$archive.sha256" -o "$temporary_dir/checksum"
# Parse only the expected archive checksum; never execute downloaded text.
read -r digest filename extra < "$temporary_dir/checksum"
if [[ ! "$digest" =~ ^[a-fA-F0-9]{64}$ || "$filename" != "$archive" || -n "${extra:-}" ]]; then
    printf 'Invalid release checksum file.\n' >&2; exit 1
fi
actual=$(sha256 "$temporary_dir/$archive")
if [[ "$(printf '%s' "$digest" | tr '[:upper:]' '[:lower:]')" != "$actual" ]]; then
    printf 'Checksum mismatch for %s.\n' "$archive" >&2; exit 1
fi
printf '%s: OK\n' "$archive"
# Validate all member names and kinds before retaining the notice archive.
# Extract only the executable as a stream: no archive-chosen destination is used.
member_names=$(tar -tzf "$temporary_dir/$archive")
while IFS= read -r member_path; do
    case "$member_path" in
        "radar-$platform"|"radar-$platform/"|"radar-$platform/"*) ;;
        *) printf 'Unsafe archive member path.\n' >&2; exit 1 ;;
    esac
    case "/$member_path/" in
        *'/../'*|*'/./'*|*'\\'*|*':'*) printf 'Unsafe archive member path.\n' >&2; exit 1 ;;
    esac
done <<< "$member_names"
member_details=$(tar -tvzf "$temporary_dir/$archive")
while IFS= read -r member_detail; do
    case "${member_detail:0:1}" in
        -|d) ;;
        *) printf 'Non-regular archive member refused.\n' >&2; exit 1 ;;
    esac
done <<< "$member_details"
# Extract only a regular-file executable member. No archive path is trusted.
member="radar-$platform/radar"
listing=$(tar -tvzf "$temporary_dir/$archive" -- "$member")
if [[ "$listing" != -* || "$listing" == *$'\n'* ]]; then
    printf 'Invalid binary archive member.\n' >&2; exit 1
fi
tar -xOzf "$temporary_dir/$archive" -- "$member" > "$temporary_dir/radar"
chmod 755 "$temporary_dir/radar"
reported=$("$temporary_dir/radar" version)
if [[ "$reported" != "radar ${version#v}" ]]; then
    printf 'Release executable version mismatch: expected %s, got %s.\n' "${version#v}" "$reported" >&2; exit 1
fi
mkdir -p "$install_dir"
# Keep the original archive alongside the executable so all dependency notices survive.
install -m 644 "$temporary_dir/$archive" "$install_dir/radar-$version-notices.tar.gz"
staged_binary=$(mktemp "$install_dir/.radar-install.XXXXXX")
install -m 755 "$temporary_dir/radar" "$staged_binary"
if [[ -d "$install_dir/radar" ]]; then
    printf 'Refusing to replace directory %s/radar.\n' "$install_dir" >&2; exit 2
fi
mv -f "$staged_binary" "$install_dir/radar"
staged_binary=""
printf 'Installed %s/radar. Add %s to PATH. Notices: radar-%s-notices.tar.gz\n' "$install_dir" "$install_dir" "$version"
