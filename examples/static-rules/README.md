# Static combined-branch conflicts

Run `bash examples/static-rules/demo.sh /tmp/radar-roadmap` with a built Radar
binary. Requires Git and Python 3; the applications and their tests never run.
The script creates temporary repositories and worktrees, asserts JSON verdicts,
required checks and exact finding codes, and removes its temporary workspace.

Two reproducible cases each pass on the individual branches, fail when combined,
and pass after repair:

- Python: one branch renames an unused declaration, another adds a named import
  of its old name. The combined check finds `symbol_dangling_reference` using
  inferred symbol resolution.
- Explicit uniqueness rule: branches add the same literal error code in separate
  files. `unique_keys` finds `duplicate_unique_key`; giving one a distinct code
  repairs the combination. Regex rules can match comments; keep their scope narrow.

These are synthetic supported-syntax fixtures. They establish neither historical
recall nor runtime safety. `make demo-all` also includes this example.
