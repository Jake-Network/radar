#!/usr/bin/env python3
"""Exercise installed native Stop hooks against real private Git candidates."""
import json
import os
from pathlib import Path
import shlex
import subprocess
import sys
import tempfile


def main():
    binary = Path(sys.argv[1]).resolve()
    with tempfile.TemporaryDirectory(prefix="radar-agent-hooks-") as tmp:
        root = Path(tmp) / "project with spaces"
        root.mkdir()
        env = dict(os.environ, RADAR_CONFIG_DIR=str(Path(tmp) / "registry"),
                   RADAR_STATE_DIR=str(Path(tmp) / "records"))
        env["PATH"] = str(binary.parent) + os.pathsep + env.get("PATH", "")

        def run(argv, **kwargs):
            return subprocess.run(argv, cwd=root, env=env, text=True,
                                  capture_output=True, timeout=60, **kwargs)

        def git(*args):
            result = run(["git", *args])
            assert result.returncode == 0, result.stderr
            return result.stdout.strip()

        def setup(*args):
            result = run([str(binary), "setup", "both", "--json", *args])
            assert result.returncode == 0, (result.stdout, result.stderr)
            return json.loads(result.stdout)

        git("init", "-q", "-b", "main")
        git("config", "user.name", "Radar hook smoke")
        git("config", "user.email", "smoke@example.invalid")
        (root / "api.py").write_text("def value():\n    return 1\n", encoding="utf-8")
        git("add", "api.py")
        git("commit", "-qm", "baseline")
        git("checkout", "-qb", "agent-a")
        (root / "api.py").write_text("def value():\n    return 2\n", encoding="utf-8")
        git("commit", "-qam", "first edit")
        git("checkout", "-qb", "agent-b", "main")
        (root / "api.py").write_text("def value():\n    return 3\n", encoding="utf-8")
        git("commit", "-qam", "conflicting edit")

        dry = setup("--dry-run", "--base", "main", "--branches", "agent-a,agent-b")
        assert dry["dry_run"] and not (root / ".codex").exists()
        setup("--base", "main", "--branches", "agent-a,agent-b")
        again = setup("--base", "main", "--branches", "agent-a,agent-b")
        assert all(c["action"] == "unchanged" for c in again["changes"]), again
        before = (git("show-ref"), git("status", "--porcelain"),
                  (root / ".git" / "index").read_bytes())

        for agent, config in (("claude", ".claude/settings.json"),
                              ("codex", ".codex/hooks.json")):
            hooks = json.loads((root / config).read_text())["hooks"]["Stop"]
            entries = [h for group in hooks for h in group["hooks"]
                       if "radar hook stop" in h.get("command", "")]
            assert len(entries) == 1, hooks
            argv = shlex.split(entries[0]["command"])
            assert argv[:3] == ["radar", "hook", "stop"], argv
            argv[0] = str(binary)
            result = run(argv, input=json.dumps({"cwd": str(root),
                         "hook_event_name": "Stop", "stop_hook_active": False}))
            assert result.returncode == 0, (agent, result.stdout, result.stderr)
            feedback = json.loads(result.stdout)
            assert "decision" not in feedback, (agent, feedback)
            assert "restack" in feedback["systemMessage"].lower(), feedback
            cached = run(argv, input=json.dumps({"cwd": str(root)}))
            assert cached.returncode == 0, cached.stderr
            assert "reused" in json.loads(cached.stdout)["systemMessage"], cached.stdout
            repeat = run(argv, input=json.dumps({"cwd": str(root), "stop_hook_active": True}))
            assert repeat.returncode == 0 and "decision" not in json.loads(repeat.stdout)

        after = (git("show-ref"), git("status", "--porcelain"),
                 (root / ".git" / "index").read_bytes())
        assert before == after, "Hook changed original refs, index, or source state"
    print("Agent hooks passed: native installs, idempotence, conflict feedback, bounded continuation, original Git state preserved")


if __name__ == "__main__":
    main()
