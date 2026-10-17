# Labeled history benchmark

This workflow mines existing local clones and runs Radar statically. It never
fetches, installs dependencies, runs repository code, or labels failures from
commit messages. Follow-up fix words are sampling leads only. The latest 20
parallel merges per language are a bounded convenience sample, not a claim of
representative ecosystem recall. Squash merges without an already-fetched PR
head are excluded and counted by `replay.py mine`.

Create `sources.json` with three accessible local clones:

```json
[
  {"language":"go", "repo":"/local/go-repository", "base":"main"},
  {"language":"typescript", "repo":"/local/typescript-repository", "base":"main"},
  {"language":"python", "repo":"/local/python-repository", "base":"main"}
]
```

```sh
python3 benchmarks/history/dataset.py export sources.json --out corpus.json
python3 benchmarks/history/dataset.py review corpus.json --out reviews.csv
python3 benchmarks/history/replay.py mine /local/go-repository --base main --limit 20 --out go.json
python3 benchmarks/history/replay.py replay go.json --repo /local/go-repository --radar /tmp/radar --out go-results.json
# Repeat mine/replay for the other two sources with the same since/limit inputs.
python3 benchmarks/history/dataset.py radar-results corpus.json go-results.json ts-results.json py-results.json --out radar-observations.json
python3 benchmarks/history/dataset.py score corpus.json reviews.csv radar-observations.json --out score.json
```

Export records root-commit identity, pinned base/branch commits, their tree IDs,
merge provenance and a dataset digest. Keep the original source configuration
locally; portable corpus evidence excludes clone paths. If a clone lacks 20
eligible cases, output retains the actual count and the 60-case target. An empty
corpus or review template does not establish recall. Re-mine/replay from the
pinned references or preserve the original mined files when branches advance.
Replay records the private binary copy's SHA-256. Dataset ingestion rejects a
wrong repository, base, branch tuple, stale dataset or duplicate case.

Two distinct humans inspect each candidate and its follow-up history separately.
Fill `label_1`/`label_2` with `broken`, `clean` or `unknown`, `causes_1`/`causes_2`
with semicolon-separated causes for broken cases, and evidence columns with
commit/file references and the reason the failure is integration-specific.
Supported causes are `symbol`, `migration`, `lockfile`, `generated`, `route`,
`runtime`, `other`, `unique_key`, `contract`, and `textual`. Separate multiple
causes with semicolons. A fix-word heuristic alone is insufficient. Identify base-existing failures;
these are not new integration failures. Unknown requires an explicit reason and
no cause. Leave the row entirely blank after its ID until reviewed. Disagreement
requires a named adjudicator, final `label`, `causes` and `evidence`; the program
validates declarations but cannot authenticate reviewers or perform their review.

`score.json` includes per-cause available labeled denominators, recall (null when
unavailable), unavailable positives, missed case IDs, clean-case false positives
and excluded unknown/unlabeled counts. A tool must identify the labeled cause;
failing an unrelated check does not count as detection. Recall over available
cases must always be published beside unavailable counts. A tool failure,
blocked result, missing row or unexecuted tool is unavailable, never a negative.
No tool rows means no comparator result.

Comparators (`git`, `mergiraf`, `weave`, an explicitly versioned LLM resolver, or
another named detector) may supply the same JSON interchange:

```json
{
  "schema_version": 1,
  "dataset_digest": "<corpus digest>",
  "tool": "mergiraf@<version>",
  "observations": [{
    "id": "<case id>",
    "input_digest": "<exact case input digest>",
    "status": "available",
    "detected_causes": ["textual"],
    "evidence": {"version": "<version>", "argv": ["<actual argv>"], "output_digest": "<sha256>"}
  }]
}
```

Pass additional interchange files to `score`. Record the real tool version,
argv and output digest; LLM results also need model/prompt identifiers. Tool
execution must be separately authorized and performed in restricted disposable
state. A resolver successfully merging text does not prove that it detected a
migration, contract or runtime defect. Mark unsupported/unexecuted analysis
unavailable with a reason and an empty cause list. Repository code execution and
LLM calls are deliberately absent from this static benchmark workflow.

Run regressions with `python3 -m unittest discover -s benchmarks/history`.
Set `RADAR_BENCHMARK_BIN=/tmp/radar` to include the executable replay test.

## Checked-in 60-case candidate corpus

`corpus/candidates.json` contains 20 actual parallel merge candidates each from
Cobra (Go), Click (Python), and Hono (TypeScript), pinned by the public URLs and
SHAs in `corpus/sources.json`. It is a candidate corpus, not 60 labeled failures.
All 60 rows in `corpus/reviews.csv` are intentionally unlabeled. Human recall,
false-positive rates and a comparative leaderboard remain unavailable until
independent review is complete. Hono's history is shallow; identities reflect
the recorded shallow boundaries. The source manifest records the fetched public
PR head refs used to reconstruct squash merges. These temporary clone refs are
unrelated to the user's branches.

To reproduce with network access, clone each source URL into its relative
`repo` directory using `git clone --depth 1000 URL DIRECTORY`, then fetch the
pinned `base` SHA with `git -C DIRECTORY fetch --depth 1000 origin SHA` and each
recorded `pull_heads` SHA into its corresponding temporary-clone ref. Check
that every fetched ref matches the manifest before export. Do not advance refs
to current upstream tips. Run `dataset.py export` with the resulting local source
configuration; replay can consume the checked-in exact tuples directly:

```sh
python3 benchmarks/history/dataset.py replay benchmarks/history/corpus/candidates.json --language go --repo /tmp/radar-history-cobra --radar /tmp/radar-roadmap --limit 3 --timeout 60 --out go-diagnostic.json
```

Repeat with `python`/Click and `typescript`/Hono. Raise `--limit` to 20 for all
cases after checking cost; `--offset 3 --limit 17` resumes after three completed
cases without repeating them. Static replay uses no repository code. This corpus
may omit runtime-only defects and is not a random or balanced recall dataset.

The checked-in `corpus/radar-observations.json` records a static development
snapshot of all 60 cases, bound to binary SHA-256
`84c676bcb1a085515e8af7c662e5bb622d477839eefb835d1f6c124296ed80a7`.
It was built from an in-progress development tree, not a published release or a
claim that later source edits were validated by this snapshot.

| Source | Cases | Static pass | Fail | Blocked | Total gate seconds |
| --- | ---: | ---: | ---: | ---: | ---: |
| Cobra / Go | 20 | 17 | 0 | 3 | 10.491 |
| Click / Python | 20 | 8 | 4 | 8 | 35.773 |
| Hono / TypeScript | 20 | 5 | 13 | 2 | 381.106 |

No command timed out or failed to return gate JSON. These are observed detector
verdicts, not human-validated positives or negatives. Click's four FAILs are
textual merge conflicts in the reconstructed pairs; the corpus is not limited
to clean textual merges. `corpus/score.json` deliberately reports null recall
and false-positive rates: all 60 human labels are absent. There are no git,
mergiraf, weave or LLM comparator observations in this snapshot.

## Reproducible 400-request sample

`sampling.py` extends this harness without changing the 60-case export. The
[source lock](sampling-sources.json) pins AgenticFlict's immutable Zenodo record
`20118379` (the `19396916` concept currently resolves there) and AIDev v4 commit
`a1a0e853682c2396474538c2026930802505c1d5`. Both datasets require CC-BY-4.0
attribution. Source size and publisher checksum are checked before conversion.
Downloads are data only; archives are never extracted onto the filesystem.

```sh
python3 benchmarks/history/sampling.py acquire /tmp/history-inputs
# Parquet conversion needs pyarrow in your chosen Python environment.
# No project dependency or upstream code is installed by these tools.
python3 benchmarks/history/sampling.py sample /tmp/history-inputs --seed 20261016 --out sample.json
python3 benchmarks/history/sampling.py bind sample.json local-inputs.json --out corpus-400.json
python3 benchmarks/history/dataset.py review corpus-400.json --out reviews-400.csv
python3 benchmarks/history/sampling.py replay corpus-400.json --case CASE_DIGEST --repo /local/clone --radar /tmp/radar --out case-run.json
# Repeat for available cases; existing converter accepts all resulting run files.
python3 benchmarks/history/dataset.py radar-results corpus-400.json case-run.json --out observations-400.json
python3 benchmarks/history/sampling.py score corpus-400.json reviews-400.csv observations-400.json --out tables-400.json
```

Selection ranks canonical PR IDs by SHA-256 of `[seed, source, PR ID]`, then takes
200 per source. It records all conversion rejection reasons and the actual
counts. AgenticFlict rows must carry a textual-conflict label and immutable
`base_oid`/`head_oid`. AIDev rows are **clean-merge candidates**, not verified
clean merges: its pinned PR parquet has no base/head SHA fields. Historical
anchors must be supplied with evidence; substituting a current PR tip cannot
reproduce an earlier merge. This distinction remains in each sampled request.

The local input map uses `source:owner/repo#number` keys:

```json
{
  "agenticflict:owner/repo#123": {"repo": "/local/clone"},
  "aidev:owner/repo#456": {
    "repo": "/local/clone",
    "base_oid": "<full historical target SHA>",
    "head_oid": "<full historical PR SHA>",
    "anchor_evidence": "<reference to preserved historical metadata>"
  }
}
```

Binding requires complete local history and runs Git's textual merge probe in a
private bare repository that reads only the clone's object store. Local merge
drivers, hooks and replacement refs are not imported. The original refs, index
and worktree are untouched. Each accepted pair records repository root identity,
merge-base and both input commits/trees, plus the Git probe's output digest.
Missing history, absent anchors, probe errors, duplicate tuples and a mismatched
textual stratum remain unavailable, with reasons. Rejected sampled requests are
not silently replaced to improve the measured denominator.

`tables-400.json` separates AgenticFlict and AIDev, retaining each target,
actual input count, unavailable inputs, human label counts, tool denominators,
false positives and missed case IDs. Review and scoring reuse the two-human
review/adjudication rules above. Git-confirmed textual conflict is recorded as
a textual oracle, never as a semantic human label. A PR versus its target branch
is not evidence about two independent agents. Resolver success is not semantic
defect detection. External runtime oracles still require separate execution
consent and restricted disposable containers.

The checked-in `corpus/sample-400.json.gz` was generated from those verified
source artifacts with seed `20261016`: 200 of 29,609 eligible AgenticFlict
conflict rows (142,652 total rows; 113,043 nonconflict rows excluded), and 200 of
71,677 AIDev PR rows. It includes the rejection manifest and source lock.
`bind` reads both JSON and gzip JSON. The raw sample preserves its initial
unverified request states. The separate acquisition/binding snapshot below
records the subsequently verified subset without changing or replacing those
requests. Semantic recall and false-positive rates remain unavailable until
human review. The existing 60-case results above remain separate. After Git
binding, `textual_oracle_metrics` reports textual positives/negatives, missed
cases and false positives independently of the human-label metrics; no textual
observation becomes semantic ground truth.

### Acquire immutable Git objects and replay the bound subset

Git acquisition is an explicit, separate network command. It never runs tests,
setup, repository hooks, custom filters or merge drivers:

```sh
python3 benchmarks/history/acquire_git.py benchmarks/history/corpus/sample-400.json.gz /tmp/history-400 \
  --jobs 6 --timeout 120 --repo-mib 384 --total-mib 12288
python3 benchmarks/history/sampling.py bind benchmarks/history/corpus/sample-400.json.gz \
  /tmp/history-400/local-inputs.json --out /tmp/history-400/corpus.json
python3 benchmarks/history/dataset.py review /tmp/history-400/corpus.json --out /tmp/history-400/reviews.csv
python3 benchmarks/history/replay_bound.py /tmp/history-400/corpus.json /tmp/history-400/local-inputs.json \
  --radar /tmp/radar --out /tmp/history-400/replay-final --jobs 2 --timeout 90
python3 benchmarks/history/sampling.py score /tmp/history-400/corpus.json /tmp/history-400/reviews.csv \
  /tmp/history-400/replay-final/radar-observations.json --out /tmp/history-400/tables.json
```

Read-only GitHub API metadata uses the caller's `gh` authentication. Anonymous
HTTPS Git fetches acquire only public repositories, complete commit/tree history
and explicitly requested blobs from the exact input and merge-base trees.
No implicit object fetching is permitted during binding or replay. Per-request
deadlines, repository/total disk budgets and the 10,000 tracked-file limit keep
acquisition bounded. Budget, inaccessible object, API and textual-stratum
failures remain unavailable; a failed sampled request is never replaced.

AIDev historical inputs are accepted only from a merged PR's immutable two-parent
merge commit, with the PR head matching its second parent. Its first parent is
the historical target; the API's current target SHA is not used. Squash/rebase,
unmerged, inaccessible and moved-head cases remain unavailable. Recorded API
response digests, retrieval times, merge commits and trees make this provenance
explicit. The sampler's AgenticFlict inputs remain exactly its pinned CSV
`base_oid` and `head_oid`: the upstream README defines these as the simulation
inputs and reproduces a merge by checking out the base and merging the head.
Safe Git uses private configuration and ignores custom merge drivers. A source
textual label that does not reproduce with these exact Git inputs is recorded
as a stratum mismatch, rather than silently treated as a verified positive.

Acquisition resumes validated successes and retries unavailable requests; prior
manifests are retained under `attempt-history/`. Cached repositories must be
plain, locally owned directories with the expected public origin and a small
configuration allowlist. Includes, configured filters and symlinks anywhere in
Git metadata are rejected before a Git command can write through them. Git's
monitor checks limits while running, and uncached final checks cover operations
that finish between polls. Replay pins one verified binary for the batch,
requires every actual replay digest to match it, and rejects stale cached input
or binary tuples. A different binary requires a different output directory.

`local-inputs.json` contains machine-local clone paths and must not be published.
Portable corpus, acquisition and observation artifacts contain immutable public
identifiers and digests. Review CSVs begin with both independent reviewers blank;
the user coordinates those reviews and any required adjudication. Until that
happens, semantic recall and false-positive rates remain unavailable even when
Git and Radar have completed every executable static check.

### Initial 105-case input snapshot (preserved)

The initial `corpus/phase0-400/acquisition.json.gz` records an actual attempt for every one
of the original 400 requests, with no substitutions. Its successful cached
repositories were revalidated using the recorded acquisition helper digest,
strict configuration checks and uncached disk checks. Git is version 2.43.0;
the local cache occupied approximately 5.2 GB. The source manifests/checksums
above remain unchanged.

| Stratum | Sampled | Objects acquired | Git-bound cases | Input unavailable |
| --- | ---: | ---: | ---: | ---: |
| AgenticFlict textual conflicts | 200 | 165 | 77 | 123 |
| AIDev clean-merge candidates | 200 | 28 | 28 | 172 |
| Total | 400 | 193 | 105 | 295 |

The 105 cases in `corpus/phase0-400/candidates.json` include exact input commits,
merge-base and tree IDs, complete root-commit identities, and verified textual
Git oracle digests. Of 193 acquired tuples, 88 AgenticFlict textual labels did
not reproduce as conflicts for the exact pinned input pair. They remain
`textual_stratum_mismatch`, never substituted or counted as tool detections.
One such mismatch was also checked using an actual private-worktree
`git merge --no-ff --no-commit --no-verify`, confirming a clean merge with no
unmerged paths; the source README's base/head interpretation was preserved.
These are Git-probe observations, not a claim about the dataset's pipeline or
about semantic compatibility.

The other 207 requests remain unavailable: 90 lack a qualifying historical
two-parent merge anchor; 61 are not merged; 26 encountered a Git command error;
12 exceed the tracked-file limit; 11 cannot be accessed anonymously; three API
requests were unavailable; two exceed the repository disk budget; and two
pinned objects are unavailable. The acquisition manifest retains every request,
retrieval time, available API digest, Git error operation/output digest and
reason. A current branch tip, squash-tree equivalence or a favorable replacement
case was not used to fill these gaps.

`corpus/phase0-400/reviews.csv` has 105 entirely blank independent-review rows.
Each reviewer must inspect the exact case separately and supply their own
identity and evidence; disagreement requires adjudication as described above.
Until those reviews are supplied, semantic denominators are zero and semantic
recall/false-positive rates remain null. Git-bound input counts alone do not
qualify the 400-case release benchmark.

### Expanded v2: 127 immutable cases

`corpus/phase0-400-v2/` preserves the original 400 sampled requests and adds 22
verified cases to the initial 105. No source pair or historical-anchor criterion
changed. Investigation found that negotiated blob fetches could require an
absent promisor delta base even though implicit fetching was disabled. Explicit
blob hydration now uses [`git fetch --refetch`](https://git-scm.com/docs/git-fetch)
to avoid that negotiation, with automatic maintenance, commit-graph writing and
submodule recursion disabled. This recovered 24 acquired tuples; two remained
textual-stratum mismatches, and 22 became valid additional cases.

| Stratum | Sampled | Objects acquired | Git-bound cases | Input unavailable |
| --- | ---: | ---: | ---: | ---: |
| AgenticFlict textual conflicts | 200 | 173 | 83 | 117 |
| AIDev clean-merge candidates | 200 | 44 | 44 | 156 |
| Total | 400 | 217 | 127 | 273 |

V2 retains 90 textual-stratum mismatches and 183 acquisition-unavailable
requests. The latter comprise 90 without a qualifying historical two-parent
anchor, 61 unmerged PRs, 12 tracked-file-limit cases, 11 inaccessible repositories,
three disk-budget cases, three unavailable API requests, two unavailable pinned
objects and one failed Git merge-base probe. No such request is counted as a
verified tool negative or replaced by another PR.

`version.json` links the previous dataset digest and lists all 105 retained and
22 added case IDs. The original 105-case corpus, review CSV and delivered packet
remain untouched. V2 `reviews.csv` copies existing human cells at packet-creation
time; `additional-reviews.csv` contains only the 22 new rows. Later edits to the
original review CSV must be retained and merged by case ID, never overwritten.
The versioned review packet excludes local clone paths and Radar findings; both
independent reviewer fields start blank unless the user actually supplied them.
Semantic results remain unavailable until those reviews and any required
adjudication are completed.

The checked-in `radar-observations.json` and `tables.json` replay all 127 cases
with development binary SHA-256
`8966ecb7231130293a5680b151a652c04ec7feea0f8d7f64e960ca4e950311f0`
(`radar 0.2.0-dev`), using two workers and a 90-second per-case timeout on Linux.
No upstream tests, setup commands or repository hooks were executed. The sum of
per-case elapsed times was 595.547 seconds; it is not wall time or a controlled
performance comparison.

| Stratum | Original requests | Git-bound | Tool available | Total unavailable | Textual missed / false positives |
| --- | ---: | ---: | ---: | ---: | --- |
| AgenticFlict textual conflicts | 200 | 83 | 77 | 123 | 0 / not applicable |
| AIDev clean textual merges | 200 | 44 | 32 | 168 | not applicable / 0 |

The 77 available conflict cases were detected as textual conflicts. The 32
available clean merges had no textual conflict finding. These denominators
exclude unavailable inputs and unavailable tool observations; they do not claim
semantic defect detection or accuracy on all 400 requests. Across bound cases,
Radar reported 77 fail, 32 pass, five blocked and 13 error results. The errors
comprise 12 unsupported repository layouts and one 90-second timeout; all error
and blocked results remain
unavailable. Response digests and closed error kinds are recorded without raw
repository output. All 127 independent human-review rows remain unlabeled in
this snapshot, so semantic recall and false-positive rates are null.
