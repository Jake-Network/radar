# Codex integration

Run `radar setup codex` (or `radar setup --agent codex`) inside the project to install a native Stop hook, its skill and project MCP configuration. The hook in `.codex/hooks.json` invokes `radar hook stop --agent codex`, previews saved changes on the current branch and returns supported static failures for one repair continuation. Select parallel branches with `--base main --branches agent-api,agent-client`. It runs no tests and requires no active plan. Use `--dry-run` to inspect changes or `--hook=false` to install only the skill and MCP. Radar preserves unrelated settings and refuses conflicting Radar entries. Put the binary on PATH, trust the project configuration, restart Codex, then use `/hooks` to review and trust the exact installed hook definition. No global configuration is written. See [native hooks and plugin installation](../../docs/AGENT_HOOKS.md).

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
JSON outputs and plan artifacts. The native hook runs at response boundaries;
the skill and MCP tools support additional investigation and verification.

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
MCP remains read-only. See [test selection](../../docs/TEST_SELECTION.md) and
[verdicts and policies](../../docs/VERIFICATION_POLICY.md).

MCP `radar_check` and `radar_merge_check` accept `policy`, `suggest_tests` and
`detail`. Default responses summarize at most ten findings and eight proposed
commands, omitting the test inventory. Request `detail: true` when those omissions
matter, or use CLI JSON for the complete local report; MCP output remains capped
at 40,000 bytes. Execution and review arguments are rejected rather than silently
ignored.
