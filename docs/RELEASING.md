# Releasing

Each CGO binary must pass vet, full tests, race tests and an installed-archive
smoke test on its native runner. A cross-build is not qualification; one failed
target prevents collection and publication.

## Targets

| Target | Native runner | Archive |
| --- | --- | --- |
| Linux amd64 | Ubuntu 22.04 | `radar-linux_amd64.tar.gz` |
| Linux arm64 | Ubuntu 22.04 ARM | `radar-linux_arm64.tar.gz` |
| macOS arm64 | macOS 15 Apple Silicon | `radar-darwin_arm64.tar.gz` |
| macOS amd64 | macOS 15 Intel | `radar-darwin_amd64.tar.gz` |

Windows packaging exists, but Git defaults (`core.autocrlf`, long paths) still
prevent native qualification. No Windows binary is published.

## Release procedure

After the release commit is on main and its CI passes:

```sh
git tag -a vX.Y.Z -m "Radar X.Y.Z" <release-commit>
git push origin vX.Y.Z
```

The tag prepares assets without publishing. After all four targets qualify:

```sh
gh workflow run release.yml --ref vX.Y.Z -f publish=true
```

For a workflow-only repair, preserve the tag and source commit and run the
repaired workflow from main with an explicit tag:

```sh
gh workflow run release.yml --ref main -f tag=vX.Y.Z
# After all four targets qualify:
gh workflow run release.yml --ref main -f tag=vX.Y.Z -f publish=true
```

The resolver rejects missing/malformed tags and branch publication requests.
Build and collection scripts check out the resolved tag SHA; publication
rechecks that the tag still identifies it. macOS resolves `TMPDIR` to its
physical path to match Git's canonical paths.

Publication uses the protected `release` environment. Configure reviewers in
GitHub; repository files do not configure environment or branch protection.
After publication, verify the public assets and checksums, then install with
the release's installer and exercise the installed binary.

## Local packaging

On the matching native host, with Go, a C compiler, Git and Python:

```sh
CGO_ENABLED=1 bash scripts/release.sh /tmp/radar-assets X.Y.Z
python3 scripts/release-test.py
python3 scripts/workflow-test.py
python3 scripts/validate-release.py /tmp/radar-assets X.Y.Z
bash scripts/install-release-test.sh
```

`release.py` validates the host/target/CGO setting, builds with `-trimpath`,
checks the version, indexes real Python/TypeScript/Go/Rust symbols and exercises
installed native agent hooks. Archives include the binary, license, version,
dependency notices, docs, plugin packages, schemas, examples and install
scripts, with normalized metadata and SHA-256 checksums.

Windows uses `scripts/release.py`, `validate-release.py` and
`install-release-test.ps1` with Go and UCRT64 GCC. Installed-binary smoke removes
MinGW from PATH to catch accidental compiler DLL dependencies.

## CI and runtime limits

`ci.yml` requires formatting, vet, full/race tests, hook and plugin regressions,
all demos, PR-driver and benchmark regressions, archive/installer checks,
actionlint and the Windows installer job. `release.yml` repeats native
qualification, verifies every checksum and generates a Homebrew formula for
review; it never pushes to a tap. Signing, notarization and attestations are
not implemented.

```sh
bash scripts/install-release.sh --version vX.Y.Z --dir "$HOME/.local/bin"
```

Installers validate the checksum, archive layout and executable version.
`--force` replaces an existing binary only after validation; PATH is unchanged.
Linux requires glibc 2.35+, macOS targets 11.0+. Build from source on other
runtimes with Go 1.23+ and a native C compiler; setting GOOS/GOARCH alone does
not provide one.

## Version history constraints

- 0.1.1 was the first published release; 0.1.0 failed macOS qualification.
- Development tags v0.4.0/v0.4.1 are permanently cached. Keep both tags and
  `go.mod`'s retraction. When public versions reach 0.4, start at v0.4.2.
- The unpublished v0.2.0 tag was moved for the hook release with explicit owner
  authorization. Its original source commit `127305d` was already cached by
  the Go module proxy. Install 0.2.0 from GitHub binaries or a Git tag checkout;
  `go install ...@v0.2.0` can resolve the earlier source.
- Future release tags remain immutable.
