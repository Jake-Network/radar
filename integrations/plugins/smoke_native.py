"""Opt-in native client installation test; all registrations stay under /tmp.

Run: python3 integrations/plugins/smoke_native.py [--radar /path/to/radar]
Requires installed Claude and Codex CLIs. Checks their local skill discovery
without starting a model turn or trusting hooks. With --radar, invokes each
installed Stop adapter directly.
"""

import argparse
import hashlib
import json
import os
from pathlib import Path
import queue
import shlex
import shutil
import subprocess
import tempfile
import threading
import time


def isolated_environment(home, config):
    """Do not inherit authentication or another agent session's configuration."""
    prefixes = ("OPENAI_", "ANTHROPIC_", "CLAUDE_", "CODEX_", "AWS_",
                "GOOGLE_", "AZURE_", "BEDROCK_", "VERTEX_")
    env = {key: value for key, value in os.environ.items()
           if not key.startswith(prefixes)
           and not any(part in key.upper()
                       for part in ("TOKEN", "API_KEY", "AUTH", "CREDENTIAL"))}
    env.update(HOME=str(home), CLAUDE_CONFIG_DIR=str(config),
               CODEX_HOME=str(config), CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC="1",
               DISABLE_AUTOUPDATER="1")
    return env


class MetadataClient:
    """Bounded JSON-lines exchange: initialization and metadata only."""

    def __init__(self, argv, env, cwd):
        self.argv, self.env, self.cwd = argv, env, cwd
        self.lines = queue.Queue()

    def __enter__(self):
        self.errors = tempfile.TemporaryFile(mode="w+")
        self.process = subprocess.Popen(
            self.argv, env=self.env, cwd=self.cwd, text=True,
            stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=self.errors)

        def receive():
            for line in self.process.stdout:
                self.lines.put(line)
            self.lines.put(None)

        self.reader = threading.Thread(target=receive, daemon=True)
        self.reader.start()
        return self

    def send(self, message):
        self.process.stdin.write(json.dumps(message) + "\n")
        self.process.stdin.flush()

    def response(self, matches):
        deadline = time.monotonic() + 30
        while True:
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                raise RuntimeError("native client metadata deadline exceeded")
            try:
                line = self.lines.get(timeout=remaining)
            except queue.Empty as exc:
                raise RuntimeError("native client metadata deadline exceeded") from exc
            if line is None:
                self.errors.seek(0)
                raise RuntimeError("native client closed metadata stream: "
                                   + self.errors.read(4096))
            data = json.loads(line)
            if matches(data):
                return data

    def request(self, request_id, method, params):
        self.send({"id": request_id, "method": method, "params": params})
        result = self.response(lambda data: data.get("id") == request_id)
        if "error" in result:
            raise RuntimeError(f"{method} failed: {result['error']}")
        return result["result"]

    def __exit__(self, *_):
        self.process.terminate()
        try:
            self.process.wait(timeout=5)
        except subprocess.TimeoutExpired:
            self.process.kill()
            self.process.wait(timeout=5)
        self.reader.join(timeout=1)
        self.process.stdin.close()
        self.process.stdout.close()
        self.errors.close()


def discover_codex(env, cwd, skill_path):
    with MetadataClient(["codex", "app-server"], env, cwd) as client:
        client.request(1, "initialize", {
            "clientInfo": {"name": "radar_native_smoke", "version": "1"},
            "capabilities": {"experimentalApi": True}})
        client.send({"method": "initialized", "params": {}})
        result = client.request(2, "skills/list", {
            "cwds": [str(cwd)], "forceReload": True})
        row, = result["data"]
        assert not row["errors"], row["errors"]
        skill, = [item for item in row["skills"]
                  if item.get("pluginId") == "radar@radar-hooks"]
        assert skill["name"] == "radar:radar" and skill["enabled"], skill
        assert Path(skill["path"]).resolve() == skill_path.resolve(), skill
        hooks = client.request(3, "hooks/list", {"cwds": [str(cwd)]})
        hook_row, = hooks["data"]
        assert not hook_row["errors"], hook_row["errors"]
        print("codex: skills/list discovered enabled radar:radar from installed plugin")
        plugin_hooks = [hook for hook in hook_row["hooks"]
                        if hook.get("pluginId") == "radar@radar-hooks"]
        hook, = plugin_hooks
        assert hook["eventName"] == "stop" and hook["enabled"], hook
        assert hook["trustStatus"] == "untrusted", hook
        assert hook["command"] == "radar hook stop --agent codex --timeout 20s", hook
        assert Path(hook["sourcePath"]).resolve() == (skill_path.parents[2] / "hooks/hooks.json").resolve(), hook
        print("codex: hooks/list discovered enabled plugin Stop hook; "
              "trustStatus=untrusted; trust activation was not requested")


def discover_claude(env, cwd):
    # SDK initialization loads command metadata without submitting any user
    # message. No model turn, API key or account login is needed.
    argv = ["claude", "--print", "--input-format", "stream-json",
            "--output-format", "stream-json", "--verbose",
            "--strict-mcp-config", "--mcp-config", '{"mcpServers":{}}']
    with MetadataClient(argv, env, cwd) as client:
        client.send({"type": "control_request", "request_id": "radar-native-init",
                     "request": {"subtype": "initialize"}})
        result = client.response(lambda data: data.get("type") == "control_response")
        response = result["response"]
        assert response["subtype"] == "success", response
        assert response["request_id"] == "radar-native-init", response
        metadata = response["response"]
        skill, = [item for item in metadata["commands"]
                  if item["name"] == "radar:radar"]
        assert "radar" in skill.get("aliases", []), skill
        assert not skill.get("builtin", False), skill
        assert skill["description"].startswith("(radar) "), skill
        print("claude: SDK initialization discovered plugin command radar:radar")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--radar", type=Path)
    args = parser.parse_args()
    binary = args.radar.resolve() if args.radar else None
    if binary and not binary.is_file():
        parser.error("--radar must name a built Radar binary")
    canonical = (Path(__file__).resolve().parents[1]
                 / "skills/radar/SKILL.md").read_bytes()
    # A space in the package path catches accidental shell interpolation.
    with tempfile.TemporaryDirectory(prefix="radar native plugins ") as tmp:
        root = Path(tmp)
        package = root / "portable package"
        shutil.copytree(Path(__file__).resolve().parent, package,
                        ignore=shutil.ignore_patterns("__pycache__"))
        for agent in ("claude", "codex"):
            home = root / agent
            home.mkdir()
            config = home / "config"
            config.mkdir()
            env = isolated_environment(home, config)

            def run(argv, **kwargs):
                result = subprocess.run(argv, env=env, cwd=root, text=True,
                                        capture_output=True, timeout=45, **kwargs)
                if result.returncode:
                    raise RuntimeError(f"{agent} command failed: {argv!r}\n"
                                       f"{result.stdout}\n{result.stderr}")
                return result.stdout

            run([agent, "plugin", "marketplace", "add", str(package)])
            install = "install" if agent == "claude" else "add"
            run([agent, "plugin", install, "radar@radar-hooks"])
            listed = json.loads(run([agent, "plugin", "list", "--json"]))
            entries = listed if agent == "claude" else listed["installed"]
            entry, = entries
            assert entry["enabled"], entry
            installed = config / "plugins/cache/radar-hooks/radar" / entry["version"]
            skill_path = installed / "skills/radar/SKILL.md"
            assert skill_path.read_bytes() == canonical, "installed skill differs from canonical"
            print(f"{agent}: installed canonical skill SHA256 "
                  f"{hashlib.sha256(canonical).hexdigest()}")
            if agent == "codex":
                discover_codex(env, root, skill_path)
            else:
                discover_claude(env, root)
            config_json = json.loads((installed / "hooks/hooks.json").read_text())
            handler = config_json["hooks"]["Stop"][0]["hooks"][0]
            print(f"{agent}: native installation enabled; Stop definition present")
            if binary:
                bindir = home / "bin"
                bindir.mkdir()
                (bindir / "radar").symlink_to(binary)
                env["PATH"] = str(bindir) + os.pathsep + env.get("PATH", "")
                # Native continuation guard: no repository analysis needed.
                event = {"hook_event_name": "Stop", "cwd": str(root),
                         "session_id": "native-package-smoke", "stop_hook_active": True}
                output = run(shlex.split(handler["command"]), input=json.dumps(event))
                result = json.loads(output)
                assert result.get("decision") != "block", result
                print(f"{agent}: installed Stop command returned valid bounded JSON")


if __name__ == "__main__":
    main()
