# Radar 0.4.0 changelog

* Default executed gates require selection completeness alongside recognized
  combined execution; uncovered changes and incomplete inventory block CI.
* Gate options work before/after branch arguments; literal comma-containing
  refs and safely quoted repair commands preserve branch identity.
* Dirty/detached worktree observations explicitly identify excluded uncommitted
  changes in CLI JSON, terminal warnings and bounded MCP summaries.
* Captured plan/policy digests and final boundary checks prevent changed
  configuration artifacts from silently satisfying verification.
* CI enforces PR-driver, workflow syntax/security, archive metadata, installer,
  parser/artifact smoke and executable benchmark regressions. Fixed a PR example
  workflow expression-scope error; Actions use pinned revisions.
* 0.4.0 binaries are published for Linux and macOS (amd64, arm64) after the
  native hosted matrix passed. Fixed Git for Windows rejecting `NUL` as
  `GIT_CONFIG_GLOBAL` and macOS `/private/var` path comparisons. Windows
  binaries are withheld until Windows Git defaults (`core.autocrlf`) qualify.
* Added pinned, offline-default evaluation with separate integration and mutation
  detection, observed execution versus selection recall, full-CI comparison,
  bounded command output, timing/RSS and retained unavailable samples.
* Improved onboarding/verdict documentation and corrected Windows, publication
  and Homebrew drift. Java-first and separate C/C++ designs are proposals only.

See [validation](RADAR_0_4_VALIDATION.md) for executed evidence, compatibility
changes and remaining limits.
