# Codex integration

Run `radar setup --agent codex` inside the project to install its skill and project MCP configuration. Use `--dry-run` to inspect changes. Radar preserves unrelated settings and refuses conflicting Radar entries. Put the binary on PATH, trust the project configuration and restart Codex. No global configuration is written.

For manual skill installation:

```sh
mkdir -p .agents/skills/radar-architecture
cp /path/to/radar/integrations/codex/SKILL.md .agents/skills/radar-architecture/SKILL.md
```

Add this repository instruction when useful:

> For shared-contract or architectural changes, use the radar-architecture
> skill. Inspect Radar diagnostics and source evidence, create a structured
> plan, run preflight, and verify the implementation. Keep unresolved runtime
> and security claims explicit. Honor the user's existing task authorization.

The CLI requires no Codex account or API key. Other agents can consume the same
JSON outputs and plan artifacts. This is an installable instruction integration,
not an automatic agent process controller.

Use a committed baseline for design, then keep that baseline while selecting a
later implementation SHA for verification. The skill documents explicit approval
and test evidence commands; repository instructions alone do not approve a plan
or authorize arbitrary scripts. See [verification demo](../../docs/DEMO.md) for
an executable workflow and [security](../../docs/SECURITY.md) for execution limits.

For concurrent work, start with `radar check --base main --json`, inspect `radar discover --json`, and use `radar merge-check --base main --branches backend,frontend --json` before integration. MCP exposes these read-only tools; it cannot enable execution or record human approval. If the user authorizes tests, run the CLI with `--verify --allow-execution -- COMMAND ARGS`. Treat failed finding IDs as repair inputs and revalidate the combined source after repair; avoid repeating identical feedback indefinitely.


For change-aware proposals, add `--suggest-tests` to `radar check` and
`radar merge-check`. Review argv, CWD and inferred evidence before authorizing the
CLI with `--verify --suite recommended --allow-execution`. A versioned
`--policy PATH` selects required checks; inspect `gate.verdict` and `coverage`
separately. Recommendations neither execute code nor install dependencies, and
MCP remains read-only. See [intelligent verification](../../docs/INTELLIGENT_VERIFICATION.md).

MCP `radar_check` and `radar_merge_check` accept `policy`, `suggest_tests` and
`detail`. Default responses summarize at most ten findings and eight proposed
commands, omitting the test inventory. Request `detail: true` when those omissions
matter, or use CLI JSON for the complete local report; MCP output remains capped
at 40,000 bytes. Execution and review arguments are rejected rather than silently
ignored.
