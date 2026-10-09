#!/usr/bin/env python3
"""Portable native artifact smoke: embedded parsers work without build tools."""
import json
import pathlib
import subprocess
import sys
import tempfile

binary = str(pathlib.Path(sys.argv[1]).resolve())
with tempfile.TemporaryDirectory(prefix="radar-native-smoke-") as tmp:
    root = pathlib.Path(tmp)
    def git(*args):
        subprocess.run(["git", *args], cwd=root, check=True, stdout=subprocess.DEVNULL)
    def radar(*args):
        return subprocess.check_output([binary, *args, "--root", str(root), "--json"], cwd=root, text=True)
    git("init", "-q")
    git("config", "user.name", "Radar release smoke")
    git("config", "user.email", "smoke@example.invalid")
    for name, content in {"api.py":"def answer():\n    return 42\n", "client.ts":"export interface Response { answer: number }\n", "main.go":"package main\nfunc main() {}\n", "lib.rs":"pub fn answer() -> i32 { 42 }\n"}.items():
        (root/name).write_text(content, encoding="utf-8")
    git("add", ".")
    git("commit", "-qm", "baseline")
    version = json.loads(radar("version"))
    assert version["version"]
    doctor = json.loads(radar("doctor"))
    assert doctor["telemetry"] is False
    before = sorted(p.relative_to(root).as_posix() for p in root.rglob("*") if ".git" not in p.parts)
    json.loads(radar("setup", "--agent", "both", "--dry-run"))
    policy = root / "policy.json"
    policy.write_text('{"version":1,"require":["source_stability","dependency_impact","no_breaking_contracts"]}', encoding="utf-8")
    report = json.loads(radar("check", "--base", "HEAD", "--head", "HEAD", "--policy", str(policy)))
    assert report["gate"]["verdict"] == "pass", report
    after = sorted(p.relative_to(root).as_posix() for p in root.rglob("*") if ".git" not in p.parts and p.name != "policy.json")
    assert before == after, "Read-only smoke modified source state"
print("Native release smoke passed: version, doctor, agent dry-run, multilingual read-only check")
