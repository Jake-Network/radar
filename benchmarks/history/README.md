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
