# Local launch materials (#14)

Status: draft. Nothing has been published or sent. Recall, user interviews,
first-run latency on the acceptance repositories, hosted installs and upstream
integration adoption remain unverified. Do not invent numbers to fill the gaps.

## Capture script

The checked [30-second GIF](assets/migration-demo.gif) and
[unabridged transcript](assets/migration-demo.txt) contain actual migration demo
output. Two separate static PASS scenes precede the combined FAIL and corrected
PASS. No tests run; this fixture is not a historical recall measurement. Regenerate
with `python3 examples/launch/capture.py --radar /tmp/radar --output docs/assets/migration-demo.gif`.
The dependency-free renderer uses uppercase bitmap text; the transcript preserves
exact output.

For a higher-resolution terminal recording, build `go build -o /tmp/radar ./cmd/radar`, then record the existing
`bash examples/migrations/demo.sh /tmp/radar` in a terminal. This demo checks
separate branches before showing the combined migration conflict. For the final
PASS scene use its corrected-chain scenario. Read the script before recording;
it uses temporary repositories and runs no migration tool. Trim to 30 seconds:
5 seconds setup, 5 seconds separate PASS, 10 seconds combined FAIL and source
locations, 10 seconds corrected PASS. Export GIF only after checking every frame
against the current CLI; the generated GIF above is a local preview, not a published campaign.

## Draft community post

Title: Radar checks combined agent branches before you merge.

Two passing branches can still break together. Radar combines committed branches
in private Git state, performs deterministic static checks, and explains missing
evidence. Tests require explicit execution authorization. It has no LLM in its
analysis path, no telemetry, and does not change your refs.

Try the README's local demo. We are collecting human-labeled historical merge
cases and first-run feedback; a recall percentage is not available yet. Which
post-merge failure would you want covered next?

Adapt this draft to each community's rules before any posting. Separate explicit
authorization is needed for Show HN, Reddit, X, upstream issues or messages.

## Measurement article outline

State the checked Radar revision, repositories, exact merge sample and replay
base (merge-base of both parents). Separate all replayed merges from human-labeled
true breaks. Define recall and false positives with their denominators, publish
labels and misses, report repeatability and elapsed timings, and disclose squash
history and heuristic limitations. Forty static PASS results alone are not a
zero-percent or hundred-percent recall estimate.

## Release checklist

- [ ] Human-labeled benchmark report and independently checked claims
- [ ] First-run timings on the acceptance repositories
- [x] Local 30-second fixture GIF and evidence-focused README (current tool output)
- [ ] Verified binary, source and package-manager install paths
- [ ] Issue templates and named 48-hour response coverage
- [ ] Explicit publication authorization
- [ ] Baseline and day-seven stars, installations (known measurement source), issues

Leave unavailable installation counts unavailable; Radar has no telemetry.
