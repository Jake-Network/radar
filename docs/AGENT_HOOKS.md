# Agent Stop hooks

Radar can check a coding agent's saved work before its response finishes. Install
the Radar CLI first and ensure `radar` is on the agent process's `PATH`. The
plugin packages contain hook definitions, not a bundled binary or dependency
installer. Git is also required.

## Install into another project

From the project you want to check:

```sh
radar setup claude
radar setup codex
radar setup both
```

These commands install project-local integration. Restart the agent after setup.
In Codex, trust the project and use `/hooks` to inspect and trust the Radar hook.
Installing or enabling a Codex hook does not grant trust: changed definitions
require review again. Check the setup output for files written and activation
steps. Avoid installing both a native Radar plugin and the project-local hook
for the same agent, because the agent merges hook sources and could run both.

## Native plugin packages

Teams may distribute the two hook-only packages in
`integrations/plugins/claude` and `integrations/plugins/codex`. Each package can
be copied without this repository. To install from a Radar source checkout:

```sh
claude plugin marketplace add /absolute/path/to/radar/integrations/plugins
claude plugin install radar@radar-hooks

codex plugin marketplace add /absolute/path/to/radar/integrations/plugins
codex plugin add radar@radar-hooks
```

The directory contains separate Claude and Codex marketplace catalogs, both
named `radar-hooks`, pointing to the matching adapter. The local marketplace
directory must remain available for refreshes. Start a new agent session after
installation; inspect its hook list. Codex users must review the hook in
`/hooks` before it runs. Native plugin commands require an agent version that
supports plugins and Stop hooks; `radar setup` provides the project-local
installation path. Claude can also load the package for one session:

```sh
claude --plugin-dir /absolute/path/to/radar/integrations/plugins/claude
```

Hook-bearing packages are for local/team distribution. OpenAI's current public
plugin directory does not accept lifecycle-hook packages. Codex IDE and cloud
surfaces do not have the same plugin support as local CLI/desktop clients; this
integration targets local agent execution environments.

## What is checked

`Stop` means the end of an agent response, not completion of a feature or a
merge. The hook checks the current branch and its saved staged, unstaged, and
untracked changes in a private candidate. It does not automatically include
unrelated worktrees. To include parallel work explicitly, configure the
participating branches and base during setup. Selected branches with available
worktrees also contribute their saved dirty changes:

```sh
radar setup both --base main --branches api,client
```

The native plugin's default command is:

```sh
radar hook stop --agent claude --timeout 20s
radar hook stop --agent codex --timeout 20s
```

For a team-specific plugin, add `--base main --branches api,client` to the
matching hook command. Keep the agent's outer hook timeout longer than Radar's
analysis timeout (the packages use 25 seconds outside, 20 seconds inside).

The hook runs static analysis only. It does not run repository tests, fetch,
install dependencies, invoke repository hooks, or modify source branches. Test
execution still requires explicit `radar gate --run` authorization. Saved dirty
work is a preview, not evidence for a committed integration candidate.

Actionable failures return native Stop JSON feedback asking the agent to
investigate and repair them. A hook-induced continuation is bounded by
`stop_hook_active`; the hook must not create an unlimited repair loop. Unknown
or incomplete checks remain informational and never become a test PASS. A
successful static check is not proof of runtime compatibility. The final agent
report should distinguish these states and list remaining verification.

## Validate and remove

```sh
python3 -m unittest discover -s integrations/plugins -p 'test_*.py'
claude plugin validate integrations/plugins/claude
claude plugin validate integrations/plugins
python3 integrations/plugins/smoke_native.py --radar /absolute/path/to/radar

claude plugin uninstall radar@radar-hooks
codex plugin remove radar@radar-hooks
```

For project-local setup, remove only the Radar Stop handler from
`.claude/settings.json` or `.codex/hooks.json`; retain other hooks and settings.
Disable the native plugin in the agent's plugin browser if you want to keep it
installed. Package structure tests validate wiring, not a hosted agent session
or user trust. End-to-end hook behavior is exercised separately by
`scripts/agent-hooks-test.py` against a built Radar binary.
The opt-in native smoke test requires both agent CLIs and installs only into
temporary configuration homes. It also checks the installed Stop command's
continuation guard when passed a binary; it does not prove an authenticated
model session or automatic hook activation after trust review.

Packaging and trust rules are based on the current primary documentation:
[Claude plugin manifests](https://code.claude.com/docs/en/plugins-reference),
[Claude marketplaces](https://code.claude.com/docs/en/plugin-marketplaces),
[OpenAI plugin packaging](https://developers.openai.com/plugins/build/plugins),
and [Codex hooks](https://learn.chatgpt.com/docs/hooks).
