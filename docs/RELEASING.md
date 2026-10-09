# Native release packaging

## Install a published binary

The repository includes a no-Go installer for **Linux x86_64 with glibc 2.35 or
newer**. A matching GitHub Release must first exist; the commands below do not
claim that any version is currently published. Replace `vX.Y.Z` with a published
version from [GitHub Releases](https://github.com/Jake-Network/radar/releases).

```sh
curl -fsSL https://raw.githubusercontent.com/Jake-Network/radar/main/scripts/install-release.sh -o /tmp/install-radar.sh
# Inspect the script before running it.
bash /tmp/install-radar.sh --version vX.Y.Z
export PATH="$HOME/.local/bin:$PATH"
radar version
radar setup --agent codex
```

The installer checks the archive checksum before extracting a single executable
member, preserves existing installations unless `--force` is explicit, and keeps
the complete release archive alongside the binary for project and dependency
notices. `--dir PATH` selects another user-writable installation directory.
Downloads and checksums both come from the same GitHub Release; integrity checking
does not provide independent publisher authentication. It requires Bash, curl,
GNU tar, GNU coreutils and a compatible C runtime. No privileged write, Go, Python,
LLM credential, or telemetry is involved. It executes the downloaded binary's
`version` command after checksum verification.

Windows, macOS, Linux arm64, musl/Alpine and older glibc do not have a validated
binary release target in this milestone. The existing source installer remains
available for native development environments; successful compilation on another
platform is not a support claim.

## Prepare artifacts without publishing

`.github/workflows/release.yml` is a manual `workflow_dispatch` workflow with
read-only repository permissions. It runs regression, race and vet checks,
packages on native Ubuntu 22.04, inspects runtime libraries, executes bundled
demos and uploads an Actions artifact. It neither creates tags nor publishes a
release. Download and review that artifact before any separately authorized
publication. Keep the archive and its adjacent `.sha256` file together when
uploading assets to a reviewed, explicitly versioned release.

The workflow has not been executed remotely as part of local validation. Linux
amd64 is the only prepared binary target. Its CGO runtime minimum is glibc 2.35
when built by this workflow; locally packaged binaries may require a different
C runtime depending on the build host.

## Build locally

From a reviewed checkout with Go 1.23+, a C compiler, Python 3, GNU tar and gzip:

```sh
go test ./...
go vet ./...
go test -race ./...
go build -o /tmp/radar ./cmd/radar
bash scripts/install-release-test.sh
bash examples/organization/demo.sh /tmp/radar
bash examples/verification/demo.sh /tmp/radar
bash scripts/release.sh /tmp/radar-release
```

The helper builds for the current Go host target and packages `radar`, project
license, README, documentation, integration skill, clean examples, schemas, dependency inventory and license notices copied from exact
module directories contributing packages to the executable. GNU tar ordering and fixed archive timestamps,
`gzip -n`, `-trimpath`, disabled VCS stamping and disabled Go build ID reduce
incidental build differences. Reproducibility requires the same source,
toolchain, C compiler, target and dependencies; the helper is not a claim of
cross-toolchain reproducibility. Set `SOURCE_DATE_EPOCH` to a fixed release time
if desired. The checksum file permits integrity checking after distribution.

Inspect dependency notices before publishing. This is packaging only: it does
not upload, tag, push, sign or create a GitHub release. The initial tested target
is Linux amd64, and the helper accepts only that native target with CGO enabled.
Other platforms require native build and execution validation;
CGO parsers preclude pretending that a pure-Go cross-build validates support.

To inspect a packaged build, extract it into a new directory and run its bundled
examples with the absolute binary path:

```sh
tar -xzf /tmp/radar-release/radar-linux_amd64.tar.gz -C /tmp
/tmp/radar-linux_amd64/radar doctor --root /tmp/radar-linux_amd64/examples/organization --json
bash /tmp/radar-linux_amd64/examples/verification/demo.sh /tmp/radar-linux_amd64/radar
```

Do not overwrite an existing installation until you have checked the checksum
and validated the target platform. Executable scripts in examples run only when
you invoke them explicitly.

The Linux binary uses CGO and the native system C runtime. Distribute it only to compatible architecture and libc targets; verify runtime dependencies when producing an artifact for another host.
