# Native release qualification

Radar embeds Tree-sitter via CGO. A working GOOS/GOARCH cross-build is not native
qualification. `scripts/release.py` checks the actual host, target and CGO setting,
builds with trimpath, collects contributing dependency licenses, verifies the
reported version and checks actual parsed symbols in Python, TypeScript, Go and
Rust before creating an archive. Installed artifact smoke also checks index
counts, agent setup dry-run and gate options following branch arguments.
The existing Bash `release.sh [OUT] [VERSION]` entry point remains compatible.
Python is a maintainer packaging dependency; installed binaries need no Go or C
compiler. Source analysis needs Git, and optional test execution needs its runners.

## Platform matrix

| Target | Native build runner | Archive | Current qualification |
|---|---|---|---|
| Linux amd64 | Ubuntu 22.04, system C compiler | `radar-linux_amd64.tar.gz` | Local Linux native package/installed smoke/installer pass; hosted unverified |
| macOS arm64 | macOS 15 Apple Silicon, Clang | `radar-darwin_arm64.tar.gz` | Configured, native runtime unverified in this workspace |
| Windows amd64 | Windows 2022, UCRT64 GCC | `radar-windows_amd64.zip` | Configured; native executable and installer execution unverified in the 0.4 Linux workspace |
| Linux arm64 | Ubuntu 22.04 ARM | `radar-linux_arm64.tar.gz` | Required hosted matrix target, native execution unverified here |
| macOS amd64 | macOS 15 Intel | `radar-darwin_amd64.tar.gz` | Required hosted matrix target, native execution unverified here |

No unvalidated placeholder binaries are created. Every native build's vet, full
suite, race suite and installed artifact smoke are required; macOS failures are
no longer informational. A platform failing those checks prevents collection and
publication. Tool presence or a configured matrix is not an observed CI success.

## Prepare assets locally

On the matching native host with Go, a C compiler, Git and Python:

```sh
CGO_ENABLED=1 bash scripts/release.sh /tmp/radar-assets 0.3.0-rc.1
python3 scripts/release-test.py
python3 scripts/workflow-test.py
python3 scripts/validate-release.py /tmp/radar-assets 0.3.0-rc.1
bash scripts/install-release-test.sh
```

On Windows, with native Go and UCRT64 GCC on PATH:

```powershell
$env:CGO_ENABLED = '1'
$env:CC = 'gcc'
python scripts/release.py "$env:TEMP/radar-assets" 0.3.0-rc.1
python scripts/validate-release.py "$env:TEMP/radar-assets" 0.3.0-rc.1
./scripts/install-release-test.ps1
```

Windows external linking requests static compiler runtime libraries. Installed
artifact smoke removes MSYS/MinGW compiler directories from PATH before launching
Radar, so an accidentally required GCC DLL prevents qualification. Windows's
system UCRT remains an OS dependency. Tests run against the archive executable,
not merely an unarchived build.

Archives contain the correct executable name, LICENSE, VERSION.txt, dependency
inventory, actual module notices, third_party notices, docs, schemas, examples
and installation scripts. Sorted metadata avoids timestamps, owners, cache paths
and build-directory leaks. SHA-256 files cover the exact archive bytes.
`VERSION.txt` and `radar version` match the selected version without leading `v`.

## Controlled hosted workflow

Every push and pull request runs Go formatting, vet, unit and race checks, all
five demos and integration smoke, PR driver regressions, benchmark fixture
regressions, archive metadata tests, offline Unix installer regressions, Linux
native packaging and installed artifact validation. A separate Windows job
executes native PowerShell installer regressions. Workflow syntax and expressions
are checked by pinned actionlint v1.7.7; targeted regression checks enforce SHA
action pins, read-only default tokens, no persisted checkout credentials and
manual publication guards. GitHub branch protection must require these CI jobs;
the repository files alone do not configure branch protection or environment
reviewers. Native Windows packaging still runs in the release qualification
matrix, while the ordinary CI Windows job tests the installer using local fixtures.

`.github/workflows/release.yml` builds each artifact on its native runner, then
collects only after every host's required tests and smoke pass. Tags **prepare**
assets only. A manual workflow dispatch on an already existing version tag with
`publish=true` requests publication through the `release` environment.
Configure that environment with required maintainers and protected tag rules
before enabling publication. No script here creates a tag or pushes a branch.
The job verifies checksums again before `gh release create --verify-tag`.

The Homebrew formula is generated as a reviewable asset; the workflow does not
push to a tap. Maintainers may review and update their tap separately. Hosted
Actions, release creation, authentic downloads and Homebrew installation remain
unverified until actual runs and published assets exist. Artifact attestations
and signing/notarization are not currently enabled; checksums provide integrity,
not an independent publisher authentication mechanism.

## Installation and runtime limits

From a reviewed checkout, use an explicit published version:

```sh
bash scripts/install-release.sh --version vX.Y.Z --dir "$HOME/.local/bin"
# --force replaces an existing binary only after successful validation.
```

```powershell
./scripts/install-release.ps1 -Version vX.Y.Z
# Default: $env:LOCALAPPDATA/Radar/bin; -Directory overrides; -Force replaces.
```

Installers validate OS/CPU and exact checksum name, reject traversal and special
archive members, extract only the named executable as a stream, check version,
retain the original notice archive, and preserve existing executables on failure.
Windows rejects directory junctions/symlinks in installation paths and replaces
files atomically. No administrator privileges are required for default paths.
Installers do not silently edit PATH.

Linux hosted release baseline is glibc 2.35+; musl/Alpine and older glibc need a
source build. A local archive inherits its local host's libc and must not be
advertised as the Ubuntu baseline. macOS deployment target is 11.0; Developer ID
signing/notarization are not configured. Windows arm64 is unsupported for binary
installation. Windows process timeout currently kills the direct process; child
process-tree termination is not qualified, so executable verification on Windows
has that explicit limitation. Read-only embedded parsing is the platform smoke.

The 0.4 local qualification on 2026-10-09 used Linux x86_64, Go 1.26.8,
GCC 13.3.0 and glibc 2.39. Native `0.4.0-dev` packaging and unpacked checksum,
version metadata, notice preservation, actual parser symbols and CLI smoke
passed. Archive metadata regressions cover all five artifact formats; this is
format validation, not macOS/Windows/arm64 executable evidence. Unix installer
regressions are now host-aware and required on Linux and macOS release runners;
only Linux execution was available locally. PowerShell was unavailable, so the
Windows installer and executable remain unverified for this milestone. No release
was published and no tag was created.

If CGO build fails, verify `go env CGO_ENABLED` and native compiler availability;
setting target variables does not install a compiler. If verification blocks,
prepare the declared test environment rather than treating runner absence as a
passing suite. `radar doctor` reports available tools without enabling semantic
indexing. Source fallback: `go build -trimpath -o radar ./cmd/radar` (`radar.exe` on
Windows), using a compiler for the actual host.
