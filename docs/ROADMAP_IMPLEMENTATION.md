# Roadmap implementation and acceptance evidence

This ledger distinguishes local implementation from roadmap acceptance that needs
real users, labeled upstream merges, hosted CI or third-party publication.
No remote branch, issue, comment, marketplace or release is published by this work.

| Issue | Local implementation | Acceptance still requiring external evidence |
| --- | --- | --- |
| #0 | Local history mining, static replay and human-label report harness; replay uses the two parents' merge-base | Four-repository labeled recall and false-positive distribution |
| #1 | Literal named-reference and declaration conflicts, with inferred provenance and unknown syntax diagnostics | Exhaustive compiler-equivalent coverage is outside the design |
| #2 | Migration graph/version inspection plus explicit locations and additional supported tools | Real deployment behavior is not inspected |
| #3 | Workspace member source resolution shared by worktree and commit indexing | Dynamic build-script evaluation is unsupported |
| #4 | Trusted Actions PR-pair wrapper, ancestry collapse, incomplete omissions, opt-in idempotent comment publisher | Actual hosted PR workflow and comment publication |
| #5 | Explicit runner profiles, bounded dependency artifact cache and setup opt-in | Real package-manager installation/network portability |
| #6 | Direct dependency name/pinned Go version consistency against supported locks | Solver equivalence and arbitrary version ranges are not inferred |
| #7 | Bounded Protobuf/GraphQL compatibility, static generated warnings, isolated regeneration and generated contract specs | Unsupported schema constructs remain unknown |
| #8 | Supported literal application route scopes and declared regex/JSON key uniqueness | Dynamic registration/application composition remains unknown |
| #9 | Candidate-bound file observations, SQLite storage, fresh observed test-selection relationships | Platform-specific tracing beyond implemented tools |
| #10 | [Interview kit](ROADMAP_USER_RESEARCH.md) and empty participant table | Ten genuine observed first-run sessions |
| #11 | Checked/unavailable output groups and single-checkout gate fallback; candidate/index cost measured separately | Ten-second end-to-end target across required repositories; seven-user comprehension |
| #12 | README first screen and actual fixture GIF with measured-recall gap stated | Five-user comprehension test |
| #13 | Existing native plugin packages plus [orchestrator examples](../integrations/examples/README.md) | External marketplace listing and two third-party adoptions |
| #14 | [Launch draft/checklist](ROADMAP_LAUNCH.md) and actual 30-second static migration GIF | Labeled recall, successful install paths, public launch and launch-week metrics |
| #15 | Detector contribution instructions/template and [16 issue drafts](ROADMAP_DETECTOR_BACKLOG.md) | Published issues and three external contributor merges |

## Execution boundary

Default gate and MCP read committed data only. Runner setup, generators and
coverage observation require execution opt-in. Generator commands run on
separate private copies; their output diff cannot change the candidate used for
tests. Raw stdout/stderr and environment values are not persisted. Static and
observed relationships never imply compiler-resolved or complete runtime coverage.

JSON verdicts remain `pass`, `fail`, `blocked`, `error`. The legacy static CLI
exit-code contract remains unchanged: a policy-free static blocked result may
exit zero. CI wrappers must read `gate.verdict`, not infer pass from exit zero.
Human output groups checked facts before unavailable checks. Watch retains its
empty-selection waiting state; first-run HEAD fallback applies to `gate` only.

## Validation ledger

Final local validation passed on this checkout:

- `go test -count=1 ./...` and `go test -race -count=1 ./...`: 33 packages,
  no failures. Runs used `-p 3 -timeout 15m` and an isolated Go cache.
- `go vet ./...`, `go build -o /tmp/radar ./cmd/radar`, formatting and
  `git diff --check`.
- `scripts/smoke.sh` and `make demo-all`, including the new static conflict and
  migration demos. The Java/Maven demo explicitly skipped because Java was
  unavailable; this is not native Java execution evidence.
- Actions workflow (9), PR matrix (6), comment publisher (5), benchmark (14),
  external benchmark (31), history replay (7), workflow regression (7), plugin
  (4) and release metadata (2) tests. History replay used the final binary.
- Native agent hook regression checks, actionlint for CI and the matrix example,
  schema validation of the four single-repository gate JSON goldens, and exact
  embedded/distributed agent asset matching.

The GIF was captured from the final local binary. Live dependency downloads,
hosted CI, macOS/Windows execution, public downloads, participant sessions and
external publication are not locally verified. Setup/cache tests use controlled
local fixtures rather than a live registry.

## Performance sample

The final binary replayed NetBox merge
`6121418f5a4d6b0eb4c3a89644a7fd5f425f1aa9` twice in **11.159 seconds** and
**7.985 seconds**. Both returned the same static policy verdict (`pass`) and
candidate tree `28570610a0cb86e579c009e9fac3c01a43bd1549`. Optional unsupported
symbol resolution and dependency checks remained `unknown`; undeclared/discovered
contracts remained `incomplete`. Runtime verification was not run.

The base was the parents' merge-base,
`d4d931dd4f9292c2524f3b2b7138d31225b3e936`. The measured binary SHA-256 was
`7575b6f21d27ff3bd851ac8a0a0ba918b3ee3e7b01215fa0086e8293c8d1c30b`.
Candidate object copying, shared parsing, bounded parser concurrency and
independent discovery/indexing reduced repeated work. These two samples cover
one merge only: they do not replace the earlier 40-merge measurement, establish
a new median, or satisfy the roadmap's ten-second target across repositories.
