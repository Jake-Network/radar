# Mission validation (2026-10-09)

Starting HEAD: `e5ed75a`. Pre-existing CLI/indexer/docs/release edits were
preserved; this report describes the resulting working tree, not a clean tag.
No remote pushes, tags, hosted runs or releases were performed.

## 1. Verification correctness

Changed: `internal/testselection/{plan.go,plan_test.go,full_inventory_test.go}`,
`internal/cli/{check.go,check_source.go,check_source_test.go}`,
`internal/integration/{integration.go,mission_integrity_test.go}`,
`internal/evidence/{harness.go,candidate_plan.go,mission_integrity_test.go}`,
and `docs/TEST_SELECTION.md`.

Fixed: omitted Node suites, invisible unsupported full-mode inventory entries,
assumed runner availability, nested unittest discovery omissions, late worktree
mutations, configuration-only mutations, failed execution masked by later
errors, and missing JUnit masking harness-confirmed failure.

Added deterministic worktree writes during recommendation and immutable commit
checks; multilingual temporary repositories run Python unittest and Node,
then remove Node from PATH and introduce unsupported JavaScript tests. Existing
candidate identity, branch evidence, mutation, malformed/removal obligations,
combined failure, zero-test and budget tests remain in force.

Executed: `GOCACHE=/tmp/radar-go-cache go test ./internal/testselection
./internal/integration ./internal/evidence ./internal/gate ./internal/contracts
./internal/verification` — PASS. CLI snapshot regression selection — PASS.
Default cache was read-only; relocated build cache, without changing dependencies.

Limits: inventory is conventional/static, not compiler-certified test coverage.
Boundary fingerprints do not detect transient writes reverted before the final
boundary; committed revisions are the repeatable authority. No universal claim
about unsupported runners or repository configuration. Whole-crate/module runners
can still be influenced by repository configuration; observations identify actual
recognized counts, not comprehensive behavioral coverage.

## 2. GitHub Actions integration

Changed: `integrations/github-actions/{radar-pull-request.yml,README.md,run.py,
report.py,test_workflow.py}` and README adoption link.

The workflow builds an explicitly pinned, reviewed Radar commit and reads its
copied helpers from the adopter's trusted base. It analyzes base plus current
head as a combined candidate. Additional PRs require explicit selection; their
execution requires separate host-privilege consent. Static mode requires merge
and declared-contract checks, execution additionally requires full selection and
observed execution. No repository-configured policy can grant execution.
Credentials are not retained and the executable child receives no token/secrets.
Reports bound summaries and escape annotation metadata; full JSON remains available.

Executed: `RADAR_TEST_BIN=/tmp/radar-mission python3
integrations/github-actions/test_workflow.py` — eight scenarios PASS: successful
PR, breaking declared schema, independently passing branches with clean merge
but combined observed failure, missing Node, malformed manifest, rejected
untrusted execution, rejected shell metacharacters and annotation escaping.
Temporary repositories preserve source status and cleanup. Local YAML/security
structure assertions PASS. Hosted Actions and actionlint remain unverified;
actionlint is unavailable locally. The workflow requires a committed/pushed
reviewed tool revision from its maintainer; no unpublished release is assumed.

## 3. Contract discovery

Changed: discovery coordinator/types, new `python_resolve.go` and
`typescript_resolve.go` with dedicated tests, `quality_test.go`; removed obsolete
regex-assisted `syntax.go`; added `examples/fastapi-typescript/`; updated discovery
and capability docs and Python cache exclusions.

Implemented bounded Tree-sitter local alias/import/inheritance resolution,
registered router prefix composition, canonical TS type exports/barrels, typed
fetch/Axios and nested scope-aware property use. Discovery remains inferred;
Python source models are not silently accepted JSON Schema declarations. Review
and preserve a schema snapshot plus explicit bindings for authoritative gates.

Review caught and repaired false relationships from Axios baseURL configuration,
response property mutation and FastAPI inclusion before route registration.
Unsupported/dynamic configurations remain diagnostics. Private/ClassVar fields
are excluded and runtime model/decorator options are explicit coverage gaps.

Executed: `GOCACHE=/tmp/radar-go-cache go test ./internal/discovery
./internal/languages ./internal/indexer ./internal/contracts ./internal/testselection
./internal/cli` — PASS; discovery race tests and vet PASS. Reproducible quality test
found 2/2 relationships, zero false consumer candidates, one unresolved dynamic
URL diagnostic, 20–23 ms observed local discovery. This measures only this fixture.
Reviewed committed bindings fail for removed nested consumed fields and recommend
Python/Node tests. Both standard-library snapshot tests PASS when invoked directly.
Broad unittest directory discovery attempted to import FastAPI packages and
reported missing FastAPI; no dependency installation or application execution was
claimed. The documented test command uses the explicit snapshot test module.

Limits: no comprehensive Python/TS typechecker or framework semantics; namespace
imports, TS path-map discovery, .then chains/destructured response aliases,
composite/quoted nested Python annotations and dynamic registrations remain outside
this slice. Configured Axios baseURL is intentionally unresolved. The representative GET response was additionally qualified with FastAPI 0.115.0,
Pydantic 2.9.2 and httpx 0.27.2; broader serializers and live Axios integration
remain unverified.

Final Milestone 1 hardening additionally preserves unsupported **required**
candidates and recommendation-limit omissions in targeted/balanced mode, and
keeps confirmed harness failures when later execution times out. An exploratory
Python-unknown filename fallback was rejected because it broke the existing
mock-only regression; conventional filenames alone remain insufficient evidence.
The regression was preserved and restored to passing.

## 4. Cross-platform release qualification

Changed: `scripts/release.sh`, new native `release.py`, `release-test.py`,
`smoke-release.py`, `validate-release.py`, Unix installer and its tests,
Windows `install-release.ps1`/`install-release-test.ps1`, release workflow,
README, release/security/capability/status/roadmap docs.

Native packaging checks host/target/CGO and reported version, normalizes tar/ZIP
members, includes dependency inventory and actual licenses/notices, and runs
embedded-parser smoke before archiving. Installed-archive validation verifies
checksum, notice inclusion, reported version and read-only source parsing with
compiler paths removed. Windows static external linking is configured. Unsupported
hosts never get placeholder archives. Windows and Unix installers preserve old
binaries, reject malicious members and verify exact release version. Release
workflow requires vet, tests, race and smoke on every native host; no
continue-on-error qualification. Tags prepare assets; explicit manual dispatch on
an existing tag requests protected-environment publication. Homebrew formula is
reviewable output; no automatic tap push.

Executed locally:

- `GOCACHE=/tmp/radar-go-cache bash scripts/release.sh /tmp/radar-mission-release 0.3.0-mission` — Linux amd64 archive built, native smoke PASS.
- `python3 scripts/validate-release.py /tmp/radar-mission-release 0.3.0-mission` — checksum, extracted notices, version, doctor, both-agent dry-run and multilingual immutable read-only check PASS.
- `python3 scripts/release-test.py` — deterministic Unix/Windows archive metadata and symlink refusal tests PASS. Synthetic ZIP validation is not a native Windows binary claim.
- `bash scripts/install-release-test.sh` — existing success/replace/bad-version/checksum/libc/symlink checks plus traversal, duplicate, wrong executable version and preservation-on-failure PASS.
- Native Windows PowerShell: `pwsh.exe -NoProfile -File C:\Users\jwsong\PycharmProjects\radar\scripts\install-release-test.ps1` — 27 assertions PASS, exit 0. WSL interop required approved execution outside sandbox. Native tests caught and fixed an atomic File.Replace null-string issue.
- Bash syntax and local YAML/security assertions — PASS; every action pinned to full SHA, tests required, contents permissions read-only outside publish.

Limits/unverified: macOS runtime, actual Windows Radar executable, all hosted
matrix results, public release/downloads/Homebrew, signing and attestations.
Windows host tool inspection found Git and a Python WindowsApps launcher but no
native Go/GCC on PATH, so no Windows binary build was fabricated. The locally
built Linux archive inherits this host's libc; glibc 2.35 qualification requires
the configured Ubuntu 22.04 native run. Windows direct-process timeout does not
qualify child-process-tree termination. Signing/notarization remains unavailable.

## Final regression and review boundaries

Existing unrelated dirty CLI, indexer, agent integrations and docs were preserved.
No commits were created because several changed files overlap pre-existing work;
review the focused paths above together with the preserved local diff. No tags,
pushes, hosted jobs, releases or external branch/tap mutations were performed.

The current product advantage is candidate-bound observed integration failure:
independently passing inputs cannot substitute for the combined test observation.
Missing runners, unsupported required selection, stale candidates and unavailable
evidence remain blocked/unknown/error. Discovery supplies reviewable contract
leads while accepted declarations and runtime observations retain stronger roles.

Final observed regression commands:

```sh
GOCACHE=/tmp/radar-go-cache go vet ./...
GOCACHE=/tmp/radar-go-cache go test ./...
GOCACHE=/tmp/radar-go-cache go test -race ./...
GOCACHE=/tmp/radar-go-cache go build -trimpath -o /tmp/radar-mission ./cmd/radar
RADAR_TEST_BIN=/tmp/radar-mission python3 integrations/github-actions/test_workflow.py
python3 scripts/release-test.py
bash scripts/install-release-test.sh
```

All PASS. Full package suites completed with exit 0; the final blocked-status
aggregation guard was additionally covered by integration race tests. `gofmt -l`
on changed Go paths returned no files; `git diff --check` PASS. Earlier tests
correctly rejected the exploratory mock-only Python classification change; that
change was reverted, preserving the original regression. Go module stat-cache
write warnings on source builds were nonfatal; output binaries and smoke passed.

Existing demos, using `/tmp/radar-mission`, all returned exit 0:
`examples/integration/demo.sh`, `examples/intelligent-verification/demo.sh`,
`examples/verification/demo.sh`, `examples/organization/demo.sh`,
`examples/selection-eval/demo.sh`, `scripts/smoke.sh`. The intelligent HTTP demo
first failed in sandbox with `PermissionError: [Errno 1] Operation not permitted`
on socket creation; approved rerun outside sandbox passed real stdlib HTTP/Node
fetch combined failure and repaired verification. The FastAPI fixture's broad
unittest discovery failed on unavailable FastAPI, while explicit `test_contract`
and Node snapshot tests passed. These environment failures were not recast as
passing observations.

The final local Linux artifact is `dist/mission-validation/radar-linux_amd64.tar.gz`
with adjacent SHA-256. It is a custom local build version `0.3.0-mission`, not a
published release or a glibc-2.35-qualified hosted artifact. The PR workflow's
additional-PR fetch currently uses credential-free origin access; private
additional refs require a separately reviewed trusted fetch configuration and
otherwise correctly error. Current PR checkout remains available via the normal
read-only Actions checkout token.

| Milestone | Implemented | Tests passed | CI/platform validated | Remaining gaps |
|---|---|---|---|---|
| 1. Verification correctness | Full inventory accounting, required omissions, failure precedence, snapshot invalidation | Focused and full Go/race suites | Local Linux | Boundary fingerprints cannot detect transient changes reverted between reads; working trees remain informational |
| 2. GitHub Actions integration | Pinned install, explicit candidates, policies, escaped annotations, bounded summary/JSON | Eight temporary-repository scenarios | Local driver/YAML/security checks | Hosted Actions, private additional-ref credential provisioning |
| 3. Contract discovery | Bounded Python/TS syntax and import resolution, inspectable candidates, reviewed binding example | Existing/new regressions, race, 2/2 ground truth with zero false candidates | Local static discovery and Python/Node snapshot tests | Dynamic/runtime schemas, origin, complex type semantics, broader serialization/deployed TS/Axios transport |
| 4. Cross-platform release | Native packaging, safe Unix/Windows install, required native workflow, controlled publication | Linux archive/installer plus 27 native Windows installer assertions | Linux native artifact; Windows installer only | macOS/native Windows executable/hosted qualification, authentic downloads, signing/attestations |


Additional real-framework qualification: installed fixed FastAPI 0.115.0,
Pydantic 2.9.2 and httpx 0.27.2 in uv's temporary environment/cache after approved
network access. `python -m unittest test_contract test_fastapi_runtime` passed
2 tests. `uv run --offline --no-project --with fastapi==0.115.0 --with
pydantic==2.9.2 --with httpx==0.27.2 python
examples/fastapi-typescript/verify-example.py /tmp/radar-mission` passed the actual
Radar full candidate gate with three commands, one recognized test each (real
FastAPI response, Python snapshot, Node snapshot), all source unchanged.
The isolated dependencies do not change Radar's Go dependencies or read-only
behavior. Final discovery regressions and discovery race reruns passed after
adding the opt-in runtime fixture. Deployed TypeScript/Axios transport and broad
framework-version compatibility remain unverified.
