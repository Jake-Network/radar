# GitHub Actions

- `radar-pull-request.yml` is an example workflow for repositories that use
  Radar. On each pull request it lints `.radar/contracts.json`, lists files and
  contracts affected through import dependencies, checks the pull request's
  contract changes against declared consumers, and scans for contract conflicts
  with other open pull requests (where concurrent agents usually collide).
  Findings become workflow annotations. Edit `RADAR_SOURCE` or replace the
  build step with your own binary distribution.
- Radar's own CI lives in `.github/workflows/ci.yml`.

Conflicts are evaluated against the merge base of this pull request. Other pull
requests forked from older commits may show base drift as changes; treat those
findings as prompts to rebase or re-run, not as proof of a runtime break.


For a selected combined-source gate, keep `--policy` separate from test execution
consent. `radar merge-check --base BASE --branches A,B --suggest-tests --json`
proposes commands without execution. After reviewing repository code and runner
privileges, an explicitly authorized CI job may run
`--verify --suite recommended --allow-execution --policy POLICY`. Read
`gate.verdict`, per-command observations and `coverage`; unknown required evidence
blocks the gate, while unrelated analyzer gaps stay visible. Private source
copies are not an OS sandbox, particularly for untrusted pull requests.
See [intelligent verification](../../docs/INTELLIGENT_VERIFICATION.md).
