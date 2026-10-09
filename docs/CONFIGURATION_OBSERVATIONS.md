# Configuration observations

`gate` and `merge-check` analyze source files, declared contracts and runner
configuration from pinned Git commits in a private combined candidate. Edits to
an agent's working tree are excluded from that candidate.

An explicitly selected `--plan` or `--policy` is an invocation input. Radar reads
its bounded bytes once, decodes those exact bytes, and records their SHA-256 in
`configuration_inputs`. These artifacts may be outside the candidate revision;
a captured policy never grants execution permission. Plans are limited to
4 MiB and policies to 1 MiB.

After analysis and execution, Radar reads selected regular-file paths again.
Changed, removed or unreadable inputs produce an incomplete
`configuration_stability` check and invalidate passing dependent checks. A
custom policy cannot bypass this instability. Known failures remain failures.
Rerun with stable selected artifacts; Radar never rewrites them.
Reusable plan records are omitted from rejected configuration observations, and
`merge-check --evidence-output` does not persist such an invocation. The report
retains actual test outcomes and explains why evidence output was omitted.

Pipes, process substitutions and `/dev/stdin` are accepted as bounded, one-shot
inputs. Their consumed bytes have a digest, but Radar does not reopen a stream
or claim that its backing configuration stayed stable. Its stability observation
is unknown; an explicit policy requiring `configuration_stability` blocks.
With no selected plan or policy, no configuration check is added. Missing required
checks remain gate requirements, not fabricated observations in `checks`.

`check --head WORKTREE` also observes repository input contents before and after
analysis, including runner configuration and `.radar/contracts.json`. Selected
plan and policy observations supplement that source observation. Pinned commit
analysis is unaffected by unrelated working-tree source edits.

These are bounded observations at invocation boundaries. They do not provide
operating-system snapshot isolation, detect every intermediate write, or notice
transient edits reverted before the final observation. The captured artifact
digest describes the bytes actually decoded; the final digest describes the
later observation. Static relationships do not establish behavioral coverage.
