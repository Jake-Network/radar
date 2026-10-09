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
