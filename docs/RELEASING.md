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
| Windows amd64 | Windows Server 2022 + UCRT64 GCC | `radar-windows_amd64.zip` |

These are configured qualification targets, not a claim that the current branch
passed hosted runs. The existing public v0.2.0 distribution has no Windows binary.
Private committed candidates disable Git line-ending conversion; Windows CI
exercises `core.autocrlf=true` and packaging, and macOS CI keeps the default
`TMPDIR`. Local Linux regression checks cannot qualify either native platform.

## Release procedure

After the release commit is on main and its CI passes:

```sh
git tag -a vX.Y.Z -m "Radar X.Y.Z" <release-commit>
git push origin vX.Y.Z
```

The tag prepares assets without publishing. After all five targets qualify:

```sh
gh workflow run release.yml --ref vX.Y.Z -f publish=true
```

For a workflow-only repair, preserve the tag and source commit and run the
repaired workflow from main with an explicit tag:

```sh
gh workflow run release.yml --ref main -f tag=vX.Y.Z
# After all five targets qualify:
gh workflow run release.yml --ref main -f tag=vX.Y.Z -f publish=true
```

The resolver rejects missing/malformed tags and branch publication requests.
Build and collection scripts check out the resolved tag SHA; publication
rechecks that the tag still identifies it. Candidate construction canonicalizes
its private temporary root; CI does not rewrite macOS `TMPDIR` to hide aliases.

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
actionlint, the Windows installer job, Windows native tests/archive validation,
and macOS default-TMPDIR smoke. `release.yml` repeats native
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

## 0.3.0 stabilization and subsequent releases

0.3.0 qualifies the existing workflow on real repositories: declared setup and
large-candidate integrity, saved-change hooks within their deadline, landing
input exclusions, targeted suggestions and useful doctor diagnostics. Release
only after all five CI jobs pass for the exact release candidate and the required
benchmark evidence is recorded. A local Linux result does not qualify macOS or
Windows; an acquired benchmark request is not a reviewed integration defect.
Publish textual-oracle and human-reviewed clean-merge results with their actual
denominators, unavailable cases, false positives and missed cases.

Use `integrations/skills/radar/SKILL.md` as the agent skill source and run
`python3 scripts/sync-agent-skills.py --check` before packaging. Test project
setup and plugin discovery separately in the actual clients. Exact Radar-owned
legacy skill files may migrate; edited files must survive setup.

Before publication, validate the native archive and its checksum, install its
binary into a clean temporary directory and execute it. After publication,
repeat the binary installer, generated Homebrew formula and
`go install github.com/Jake-Network/radar/cmd/radar@v0.3.0` checks from clean
cache/configuration directories. Local archive validation cannot prove that a
future public URL or module-proxy version installs. Tag, release, tap and
marketplace publication require a separate explicit execution request.

For a Homebrew candidate, install and run `brew test` in a disposable native
Homebrew environment, with auto-update/analytics disabled. Preserve the generated
formula's `install` and `test` bodies. Its test pins Homebrew's resolved Git
executable because the shim's fallback locator loop can replace stdin; otherwise
Radar's `git cat-file --batch` may see EOF. A local Linux-only qualification
formula may use the genuine Linux archive/checksum via `file://`; label it as
local transport, and do not invent the other platform checksums required by
`scripts/homebrew-formula.sh`. This does not validate the published formula,
other architectures or macOS. Follow the
[Homebrew test contract](https://docs.brew.sh/Formula-Cookbook#add-a-test-to-the-formula).

Source-install qualification can use an immutable candidate in a local file
module proxy before publication. Record its module-zip/source-manifest digests,
use separate temporary `GOBIN`, `GOMODCACHE`, `GOCACHE` and `GOPATH`, and run
`go install ...@v0.3.0` outside a checkout. Follow
[Go's module archive rules](https://go.dev/ref/mod#zip-files), including exclusion
of nested modules; exempt only the local candidate module from the public
checksum database. Check `radar version`, `go version -m`, then run
`python3 scripts/smoke-release.py /path/to/installed/radar`. Record this as a
local proxy result; public module availability still needs a post-publication
check. Neither method needs a global HOME change or a remote tag.

The Java demo needs a JDK, Maven and Python 3. In a disposable environment with
network access explicitly allowed for cache priming, run
`RADAR_DEMO_REQUIRE=1 bash examples/java-maven/demo.sh /path/to/radar --prime`.
The demo primes its baseline dependencies before Radar runs Maven offline;
verify both standalone JUnit successes and the combined source compilation
failure. Pin the container image digest, record the binary digest and discard
the environment afterwards. A skipped demo is not qualification.

Land follows stabilization as **0.4.2**, then Work (0.5.0), Prove (0.6.0) and
Queue (0.7.0). The dependency and acceptance boundaries are in
[capabilities](CAPABILITIES.md). Retracted 0.4.0 and 0.4.1 remain unavailable for
reuse. Split remains conditional on user research and observed drift.

Announcement draft, to use after qualification: "Radar 0.3.0 stabilizes local
integration checks for parallel agent branches. Large candidates retain source
integrity checks through declared setup and test execution; Stop hooks inspect
saved changes first and report peer gaps; landing identifies branches that need
restack before evaluating the remaining combination. Static and executed
results retain their exact scope. See the benchmark report for actual measured
denominators and unavailable cases." Link the qualified release, installation
instructions and an observed quickstart recording when publishing; do not add
unreviewed accuracy or performance claims.
