# Releasing

Radar embeds Tree-sitter through CGO, so every target is built and tested on a
native runner. A cross-compiled binary is not treated as qualified.

## Targets

| Target | Runner | Archive |
| --- | --- | --- |
| Linux amd64 | Ubuntu 22.04 | `radar-linux_amd64.tar.gz` |
| Linux arm64 | Ubuntu 22.04 ARM | `radar-linux_arm64.tar.gz` |
| macOS arm64 | macOS 15, Apple Silicon | `radar-darwin_arm64.tar.gz` |
| macOS amd64 | macOS 15, Intel | `radar-darwin_amd64.tar.gz` |

All four passed the hosted matrix for the pre-release `v0.4.0` tag. 0.1.0 is
qualified by its own tag run. Windows amd64 can be packaged locally but is not
in the matrix: tests fail on the runner's Git defaults (`core.autocrlf`, long
paths), so no Windows binary is published.

Each target must pass vet, the full test suite, the race suite and a smoke test
of the installed archive. If any target fails, nothing is collected or
published.

## First public release (0.1.0)

Development used milestone numbers 0.1 to 0.4 and pushed a `v0.4.0` tag. It was
never published as a GitHub release, but the Go module proxy cached it, and
proxy versions are permanent. Without a retraction, `go install ...@latest`
would keep resolving to v0.4.0. So `go.mod` retracts `[v0.4.0, v0.4.1]`, and
because `go` reads retractions from the highest version, the retraction itself
has to be published as v0.4.1. Never move or delete the `v0.4.0` tag; the
checksum database already has it.

Once the release commit with the `retract` directive is on `main` and CI has
passed:

```sh
git tag -a v0.4.1 -m "Retract pre-release v0.4.0" <release-commit>
git push origin v0.4.1
GOPROXY=https://proxy.golang.org go list -m github.com/Jake-Network/radar@v0.4.1
git tag -a v0.1.0 -m "Radar 0.1.0" <release-commit>
git push origin v0.1.0
```

The `v0.4.1` tag also starts an asset run. Cancel it if you like, and never
dispatch it with `publish=true`. When the `v0.1.0` run has passed on every
host, dispatch the release workflow on `v0.1.0` with `publish=true`. Then check
that `GOPROXY=https://proxy.golang.org go list -m github.com/Jake-Network/radar@latest`
reports v0.1.0 (the proxy can take a few minutes) and that the README install
command works.

When the version line reaches 0.4, skip v0.4.0 and v0.4.1 and use v0.4.2.

## Building assets locally

On the matching host, with Go, a C compiler, Git and Python:

```sh
CGO_ENABLED=1 bash scripts/release.sh /tmp/radar-assets 0.1.0-rc.1
python3 scripts/release-test.py
python3 scripts/workflow-test.py
python3 scripts/validate-release.py /tmp/radar-assets 0.1.0-rc.1
bash scripts/install-release-test.sh
```

On Windows, with Go and UCRT64 GCC on `PATH`, use `scripts/release.py`,
`scripts/validate-release.py` and `scripts/install-release-test.ps1`. The
installed-binary smoke test removes MinGW from `PATH` first, so an accidental
GCC DLL dependency fails qualification.

`release.py` checks the host, target and CGO setting, builds with `-trimpath`,
verifies the reported version and parses real Python, TypeScript, Go and Rust
symbols before archiving. Archives contain the binary, `LICENSE`,
`VERSION.txt`, dependency notices, docs, schemas, examples and install
scripts, with normalized metadata and a SHA-256 file for each archive.

## Workflows

`ci.yml` runs on every push and pull request: gofmt, vet, unit and race tests,
smoke and all demos, PR workflow regressions, benchmark harness tests, archive
and installer tests, actionlint and a Windows installer job.

`release.yml` builds each archive on its native runner and collects them only
after every host passes. A tag only prepares assets. Publishing needs a manual
dispatch with `publish=true` on an existing tag, through the protected
`release` environment. Configure required reviewers on that environment before
the first publication. Checksums are verified again before
`gh release create --verify-tag`.

The Homebrew formula is generated as a release asset for review. Nothing pushes
to a tap. Signing, notarization and provenance attestations are not set up.

Branch protection and environment reviewers are GitHub settings; the files in
this repository do not configure them.

## Installing and runtime limits

```sh
bash scripts/install-release.sh --version vX.Y.Z --dir "$HOME/.local/bin"
```

`--force` replaces an existing binary, and only after the new one validates.
Installers check the OS and CPU, the checksum and the archive layout, extract
only the binary and never edit `PATH`.

- Linux binaries need glibc 2.35 or newer. On musl or older glibc, build from
  source.
- The macOS deployment target is 11.0.
- If a CGO build fails, check `go env CGO_ENABLED` and that a native C compiler
  is installed. Setting `GOOS`/`GOARCH` does not install one.
- Source build: `go build -trimpath -o radar ./cmd/radar`.
