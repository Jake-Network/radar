"""Verify portable plugin wiring without modifying agent registrations.

Run: python3 -m unittest discover -s integrations/plugins -p 'test_*.py'
Native agent manifest validation and session execution are separate checks.
"""

import json
from pathlib import Path
import shlex
import unittest


ROOT = Path(__file__).resolve().parent


def read(path):
    return json.loads((ROOT / path).read_text())


class PluginPackages(unittest.TestCase):
    def test_claude_catalog_resolves_plugin(self):
        catalog = read(".claude-plugin/marketplace.json")
        entry, = catalog["plugins"]
        plugin = ROOT / entry["source"]
        manifest = json.loads((plugin / ".claude-plugin/plugin.json").read_text())
        self.assertEqual(entry["name"], manifest["name"])
        # Claude auto-loads this file: listing it in the manifest could run it twice.
        self.assertNotIn("hooks", manifest)
        self.assertTrue((plugin / "hooks/hooks.json").is_file())

    def test_codex_catalog_and_extension_resolve_plugin(self):
        catalog = read(".agents/plugins/marketplace.json")
        entry, = catalog["plugins"]
        self.assertEqual(entry["source"]["source"], "local")
        plugin = ROOT / entry["source"]["path"]
        manifest = json.loads((plugin / "plugin.json").read_text())
        self.assertEqual(entry["name"], manifest["name"])
        hook_path = manifest["extensions"]["com.openai"]["hooks"]
        self.assertTrue((plugin / hook_path).is_file())

    def test_stop_commands_preserve_execution_boundary(self):
        for agent in ("claude", "codex"):
            with self.subTest(agent=agent):
                config = read(f"{agent}/hooks/hooks.json")
                self.assertEqual(set(config["hooks"]), {"Stop"})
                group, = config["hooks"]["Stop"]
                handler, = group["hooks"]
                self.assertEqual(handler["type"], "command")
                self.assertEqual(shlex.split(handler["command"]), [
                    "radar", "hook", "stop", "--agent", agent, "--timeout", "20s"
                ])
                self.assertGreater(handler["timeout"], 20)
                self.assertNotIn("async", handler)

    def test_package_versions_match(self):
        claude = read("claude/.claude-plugin/plugin.json")
        codex = read("codex/plugin.json")
        self.assertEqual(claude["version"], codex["version"])


if __name__ == "__main__":
    unittest.main()
