# Security and privacy

Radar's deterministic core runs locally. It does not upload source, call an LLM,
or enable telemetry. Indexing parses files; it does not execute application
code, package-install hooks, repository scripts, or agent instructions.

Git analysis reads revisions using subprocess arguments rather than shell
interpolation. Radar does not merge, rebase, revert, push, or rewrite commits.
Analysis inputs and paths are untrusted. Supported paths must remain within
the repository. Large files and generated directories are bounded or skipped;
diagnostics explain available analysis.

Verification itself does not execute commands. `radar test --allow-execution`
explicitly runs an exact command declared by a plan's `test_run` rule, in a private
copy of committed files, under a timeout and output limit. Review the command and
repository before opting in. This copy protects your checkout from normal test
writes, but is not an OS sandbox: malicious code can access credentials, the
network or files available to your user. Tests receive PATH plus private HOME and temporary directories; inherited secrets
and language startup settings are removed. Package-manager offline settings are
set and dependencies are not installed automatically. This is an environment
restriction, not a network or filesystem sandbox. Evidence stores output digests, not
raw stdout/stderr; avoid sensitive command arguments because arguments are stored.
Optional cloud inference remains unimplemented and must disclose data sent before
any future opt-in.

Agent skills are advisory instructions, not a security sandbox. Review plans
and consequential decisions. A local plan review declaration is not an
authenticated identity or permission to mutate Git history.

Avoid including source or secrets in public issues. Report a security issue
privately to the maintainers before publishing exploit details. A dedicated
private reporting channel is not configured yet.

Read-only Git subprocesses disable repository-configured filesystem monitor hooks; Radar never enables Git pagers and runs repository test commands only through the explicit opt-in workflow. Checks use bounded local reads and do not resolve remote schema references.
