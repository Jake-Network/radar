# Portable agent packages

This local marketplace bundles Radar's static Stop hook and reviewed integration
skill for Claude Code and Codex. It requires an independently installed `radar`
on PATH. Catalogs are ready for local native-client installation; their presence
is not marketplace submission, approval, or publication.

```sh
claude plugin marketplace add /absolute/path/to/radar/integrations/plugins
claude plugin install radar@radar-hooks
codex plugin marketplace add /absolute/path/to/radar/integrations/plugins
codex plugin add radar@radar-hooks
```

Review native-client trust settings and restart the client after installation.
The hook previews saved changes statically; repository tests require separate
explicit execution opt-in. Installation alone does not authorize execution.
Use either this package or `radar setup` for a project to avoid duplicate hooks.

```sh
python3 -m unittest discover -s integrations/plugins
python3 integrations/plugins/smoke_native.py --radar /tmp/radar
```

Native smoke installs both packages only into temporary configuration directories
and calls the installed Stop handlers with the continuation guard. It does not
start an authenticated model session or alter existing client registrations.
Canonical skills live in `integrations/codex/SKILL.md` and
`integrations/claude-code/skills/radar/SKILL.md`; bundled copies must match them.
