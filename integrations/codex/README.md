# Codex integration

Install the bundled skill in a project or personal Codex skills directory:

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
