# Radar

**Two agents. Two green branches. One broken merge. Radar catches it before you merge.**

You run coding agents in parallel, each in its own worktree (cmux, Orca, Claude
Code, Codex, or plain `git worktree`). Each branch passes its own tests. Merged
together, they break. Radar combines the branches in private Git state, checks
the combination, runs the tests that cover it, and points at the branch to fix.

It is a local CLI. It needs no API key, sends no telemetry, and never touches
your branches.

```console
$ radar gate --run
Radar gate: FAIL — 2 branch(es) onto main @ cecea977bdde

  agent-backend   172789253204  1 file(s) changed
  agent-frontend  480351edc8ad  1 file(s) changed

  ✓  branches merge without conflicts
  ✓  no breaking contract change found
  ✗  tests on the combined tree did not pass

  ✗ integration_execution_failed: … Failed cases: test_checkout.T.test_budget
    look at (import-based lead): agent-backend (backend.py)

Next: repair on the branches above, commit, then rerun: radar gate --run
```

## Install

Use an explicitly selected **published** version; the commands below do not
assume that a release or Homebrew tap already exists. Download the installer
from that reviewed tag, inspect it, then install a checksum-verified binary:

```sh
# Linux amd64 / macOS arm64 (after the corresponding native release qualifies)
version=vX.Y.Z
curl -fsSL "https://raw.githubusercontent.com/Jake-Network/radar/$version/scripts/install-release.sh" -o install-radar.sh
bash install-radar.sh --version "$version" --dir "$HOME/.local/bin"
radar version
radar doctor
radar setup --agent both --dry-run
```

Windows amd64, from a reviewed local installer downloaded from the selected tag:

```powershell
./install-release.ps1 -Version vX.Y.Z
# Add $env:LOCALAPPDATA\Radar\bin to your user PATH, then run radar doctor.
```

Binaries need no Go or C compiler. Git is required for checkpoint analysis;
verification needs the selected repository's test runners and dependencies.
Installers preserve existing binaries unless explicitly replaced, check SHA-256,
reject unsafe archive members, and retain license notices.

| Platform | Native packaging | Validation in this workspace |
|---|---|---|
| Linux amd64 | `.tar.gz`; CI baseline glibc 2.35+ | Native archive, installed binary smoke and installer regressions passed |
| macOS arm64 | `.tar.gz`; native Apple Silicon runner | Workflow configured; native runtime/hosted result unverified |
| Windows amd64 | `.zip`; native UCRT64 CGO build | Native PowerShell installer: 27 assertions passed; actual binary/hosted build unverified |
| Linux arm64 / macOS amd64 | Optional native matrix entries retained | Unverified here |

Build from source on other systems (Go 1.23+ and a native C compiler for
Tree-sitter):

```sh
go build -trimpath -o radar ./cmd/radar
# Or install an explicitly reviewed published module version:
go install github.com/Jake-Network/radar/cmd/radar@vX.Y.Z
```

Homebrew formula generation remains available for maintainers after all native
assets qualify; no tap update is automatic. See [release requirements and runtime
limits](docs/RELEASING.md) and [validation evidence](docs/MISSION_VALIDATION.md).

## Use

From the main checkout, after your agents have committed on their branches:

```sh
radar gate          # combine every worktree branch; conflicts, contract breaks, tests to run
radar gate --run    # also run those tests on the combined tree
```

That is the whole everyday workflow. You don't need setup or configuration
files. `radar gate` picks the base (`origin/HEAD`'s branch, `main`, `master` or
`trunk`) and every worktree branch that has commits beyond it. To choose
branches yourself, name them: `radar gate feature/api feature/web --base
develop`.

| Exit | Meaning |
| --- | --- |
| `0` | Every required check passed, or (without `--run`) nothing supported failed |
| `1` | A check failed (conflict, breaking contract, failing tests) or required evidence is missing |
| `2` | Radar could not run (bad ref, unreadable configuration, environment error) |

## What it checks

- **Merge:** Radar merges all branches in a private object database. Your
  repository, refs and working tree are never written.
- **Contracts:** it compares OpenAPI and JSON Schema producers with their
  consumers. Without configuration, it reports candidates it discovers as
  warnings. Bindings you declare in `.radar/contracts.json` turn
  incompatibilities into failures.
- **Dependency impact:** it builds an import graph for TypeScript/JavaScript
  (relative imports, tsconfig `paths`/`baseUrl`, workspace package names),
  Python, Go and Rust, then follows it from the changed files to their
  dependents.
- **Tests:** it selects the tests related to the combined change. With `--run`,
  it runs them on the combined tree with a time and command budget. Tests it
  skips are listed, never silently dropped.
- **Attribution:** for each finding, it names the branches whose changed files
  the finding points at. For failing tests, it follows the tests' static
  imports. Treat this as a lead for repair, not proof of blame.

## Agents and CI

**Agents.** `radar setup --agent claude` (or `codex`, or `both`) installs a
project skill and the MCP server. Agents then call `radar_gate` before they
propose a merge. Agents can't run the tests themselves, because `--run`
executes repository code; a person runs it.

**CI.** Name the branches and choose which evidence is required:

```sh
mkdir -p .radar && echo '{"version":1,"require":["textual_merge","no_breaking_contracts","integration_execution"]}' \
  > .radar/integration-policy.json
radar gate --run --policy .radar/integration-policy.json feature/api feature/web
```

With a policy, required evidence that is missing makes the gate fail with
`NOT VERIFIED` (blocked), rather than letting it pass.

For a deployable PR workflow, copy the [GitHub Actions integration](integrations/github-actions/README.md).
It builds a reviewed immutable Radar revision, keeps static inspection available
by default, and requires explicit consent for repository-code execution. Its job
summary and JSON artifact show the candidate, findings, selected tests and missing
evidence. Additional PR heads participate only when explicitly selected.

## What a PASS means

Radar reports evidence. It doesn't certify correctness.

- **PASS:** every required check passed. Without `--run`, the gate says
  `PASS (static)`, because no tests ran.
- **Import-based analysis:** dependency analysis follows imports. It does not
  resolve compiler types, and it does not see runtime reads such as files,
  environment variables or network calls.
- **Not a sandbox:** `--run` executes your tests with your user's permissions.
  The tests run in a private copy of the combined source, but that copy is not
  an operating-system sandbox. Untracked dependency directories such as
  `node_modules` are not copied, so a runner that is missing is reported as
  not run rather than as passed.

The full guarantees and limits are in [CAPABILITIES](docs/CAPABILITIES.md),
[VERIFICATION_POLICY](docs/VERIFICATION_POLICY.md) and
[SECURITY](docs/SECURITY.md).

## Beyond the gate

Radar also has a plan-and-evidence toolkit, listed under `radar help --all`.
You can write plans grounded in the indexed code, approve them with a review
bound to their digest, record test evidence bound to a commit, query the import
graph, and lint explicit contracts. See the [usage reference](docs/USAGE.md).
You can also run the [deterministic integration demo](docs/INTEGRATION_DEMO.md).

## Development

```sh
go test ./...
go vet ./...
bash scripts/smoke.sh "$(go build -o /tmp/radar ./cmd/radar && echo /tmp/radar)"
make demo-all
```

Releases are built natively on Linux and macOS (amd64 and arm64) and published
as reviewed release assets; publication is a separate manual step. See [RELEASING](docs/RELEASING.md).
Also see [contributing](CONTRIBUTING.md), [architecture](docs/ARCHITECTURE.md)
and the [roadmap](docs/ROADMAP.md). MIT licensed.
