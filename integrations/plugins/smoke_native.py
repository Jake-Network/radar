"""Opt-in native client installation test; all registrations stay under /tmp.

Run: python3 integrations/plugins/smoke_native.py [--radar /path/to/radar]
Requires installed Claude and Codex CLIs. Does not start an authenticated model
session or trust hooks. With --radar, invokes each installed Stop adapter directly.
"""

import argparse
import json
import os
from pathlib import Path
import shlex
import shutil
import subprocess
import tempfile


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--radar", type=Path)
    args = parser.parse_args()
    binary = args.radar.resolve() if args.radar else None
    if binary and not binary.is_file():
        parser.error("--radar must name a built Radar binary")
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
            env = dict(os.environ)
            env["HOME"] = str(home)
            env["CLAUDE_CONFIG_DIR"] = str(config)
            env["CODEX_HOME"] = str(config)

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
