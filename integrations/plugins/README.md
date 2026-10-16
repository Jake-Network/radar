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

The prepared packages are version 0.3.0. To replace an installed 0.2.0 Codex
package, update the local marketplace directory, then repeat
`codex plugin add radar@radar-hooks`; Codex selects the new versioned cache.
`codex plugin marketplace upgrade` refreshes Git marketplaces only. The local
0.2.0-to-0.3.0 upgrade was checked with the same temporary configuration home;
the discovered Stop hook remained untrusted.

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
The canonical skill is `integrations/skills/radar/SKILL.md`. Claude, Codex,
plugins and the embedded setup copy carry identical bytes. Run
`python3 scripts/sync-agent-skills.py --check` after changes. The installer
migrates only exact Radar-owned legacy skills and preserves user edits.
Native smoke checks client skill discovery and Codex Stop-hook metadata without
trusting the hook or sending a model turn. Codex uses `.codex-plugin/plugin.json`;
do not add a root `plugin.json`, which takes precedence and hides bundled hooks
in Codex 0.162.1. Discovering an untrusted hook does not prove host activation.
