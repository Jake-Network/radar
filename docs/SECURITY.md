# Security and privacy

Radar's deterministic core runs locally. It does not upload source, call an LLM,
or enable telemetry. Indexing parses files; it does not execute application
code, package-install hooks, repository scripts, or agent instructions.

Git analysis reads revisions using subprocess arguments rather than shell
interpolation. Radar does not merge, rebase, revert, push, or rewrite commits.
Analysis inputs and paths are untrusted. Supported paths must remain within
the repository. Large files and generated directories are bounded or skipped;
diagnostics explain available analysis.

Verification itself does not execute commands. `radar gate` without `--run`
never executes repository code; `radar gate --run` is equivalent to
`merge-check --verify --allow-execution --suite balanced` and carries the same
host-permission caveats as the execution modes below. `radar test --allow-execution`
explicitly runs an exact command declared by a plan's `test_run` rule, in a private
copy of committed files, under a timeout and output limit. Review the command and
repository before opting in. This copy protects your checkout from normal test
writes, but is not an OS sandbox: malicious code can access credentials, the
network or files available to your user. Tests receive PATH plus private HOME and temporary directories; inherited secrets
and language startup settings are removed. Package-manager offline settings are
set and dependencies are not installed automatically. Existing local dependency
caches (Go module and build caches, Cargo and rustup homes, npm cache, Python
user base, an active virtualenv) are made available so offline builds work;
tests can read and may write them. A rule may additionally pass named
environment variables (`env`), link untracked checkout directories into the
snapshot (`link`) and run `setup` commands; all are part of the reviewed plan,
so review them like the test command. This is an environment restriction, not
a network or filesystem sandbox. Evidence stores output digests, not raw
stdout/stderr; the last 4 KiB of output is displayed to the operator (and
returned in `--json` output) but never persisted. Avoid sensitive command
arguments because arguments and passed-through variable names are stored.
Optional cloud inference remains unimplemented and must disclose data sent before
any future opt-in.

`radar mcp` runs locally over stdio for one fixed root. It does not expose
`test` or `approve`, so an agent cannot execute repository code or declare a
review through it; it can write Radar state and new plan files under the root.
The Claude Code Stop hook only runs `radar verify`.

Agent skills are advisory instructions, not a security sandbox. Review plans
and consequential decisions. A local plan review declaration is not an
authenticated identity or permission to mutate Git history.

Avoid including source or secrets in public issues. Report a security issue
privately to the maintainers before publishing exploit details. A dedicated
private reporting channel is not configured yet.

Read-only Git subprocesses disable repository-configured filesystem monitor hooks; Radar never enables Git pagers and runs repository test commands only through the explicit opt-in workflow. Checks use bounded local reads and do not resolve remote schema references.

## PR execution and native installation

The PR example uses `pull_request`, read-only contents permissions, pinned tool
source, trusted-base helpers and no persisted checkout credentials. Untrusted
metadata becomes subprocess arguments, never shell source. Static inspection is
available without executable verification. Execution requires repository-owner
configuration; selected additional PRs require separate explicit authorization.
Numbers are mutable: review their latest fetched heads. Candidate directories
are not OS sandboxes, and local/Radar execution consent is not a claim that code
is safe. Use ephemeral runners without repository secrets or privileged services.

Release installers validate checksums, version and archive paths/types, stream
only the expected executable, and preserve installed binaries on validation
failure. Windows rejects destination reparse points and supports native amd64
only. Checksums do not authenticate a compromised publisher. Release signing,
notarization and provenance attestations are not configured. Native release
qualification blocks on tests; publication is an explicit manual protected
workflow operation. No automatic branch or Homebrew-tap updates occur.
