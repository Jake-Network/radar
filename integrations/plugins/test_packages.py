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

    def test_codex_native_manifest_resolves_hooks_and_skills(self):
        catalog = read(".agents/plugins/marketplace.json")
        entry, = catalog["plugins"]
        self.assertEqual(entry["source"]["source"], "local")
        plugin = ROOT / entry["source"]["path"]
        manifest = json.loads((plugin / ".codex-plugin/plugin.json").read_text())
        self.assertEqual(entry["name"], manifest["name"])
        # The universal manifest takes precedence in Codex 0.162.1 but drops
        # bundled hooks; native discovery must use only this manifest.
        self.assertFalse((plugin / "plugin.json").exists())
        hook_path = manifest["hooks"]
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

    def test_packages_bundle_the_reviewed_integration_skills(self):
        sources = {"claude": ROOT.parent / "claude-code/skills/radar/SKILL.md",
                   "codex": ROOT.parent / "codex/SKILL.md"}
        for agent, source in sources.items():
            self.assertEqual((ROOT / agent / "skills/radar/SKILL.md").read_text(), source.read_text())
        manifest = read("codex/.codex-plugin/plugin.json")
        self.assertEqual(manifest["skills"], "./skills")

    def test_all_skill_copies_match_canonical_source(self):
        repo = ROOT.parent.parent
        canonical = (repo / "integrations/skills/radar/SKILL.md").read_bytes()
        for relative in (
            "internal/onboarding/assets/radar.md", "integrations/codex/SKILL.md",
            "integrations/claude-code/skills/radar/SKILL.md",
            "integrations/plugins/claude/skills/radar/SKILL.md",
            "integrations/plugins/codex/skills/radar/SKILL.md",
        ):
            with self.subTest(path=relative):
                self.assertEqual((repo / relative).read_bytes(), canonical)

    def test_package_versions_match(self):
        claude = read("claude/.claude-plugin/plugin.json")
        codex = read("codex/.codex-plugin/plugin.json")
        self.assertEqual(claude["version"], codex["version"])
        self.assertEqual(codex["version"], "0.3.0")


if __name__ == "__main__":
    unittest.main()
