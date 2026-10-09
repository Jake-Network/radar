#!/usr/bin/env python3
"""Trusted workflow driver. Never constructs a shell command from PR inputs."""
import json
import os
import pathlib
import re
import subprocess
import tempfile


def blocked(message):
    return {"status": "blocked", "gate": {"verdict": "blocked", "required_checks": [
        {"id": "execution_authorization", "status": "blocked", "explanation": message}]},
        "findings": [], "checks": [], "limitations": [message]}


def run(env):
    output = pathlib.Path(env.get("RADAR_REPORT", "report.json"))
    root = env.get("RADAR_ROOT", "")
    execute = env.get("RADAR_EXECUTE", "false")
    trusted = env.get("RADAR_TRUSTED", "false")
    if not all(env.get(k) for k in ("RADAR_ROOT", "RADAR_BIN", "RADAR_BASE", "RADAR_HEAD")):
        report, code = blocked("Missing required workflow input or tool path."), 2
        report["gate"]["verdict"] = "error"
    elif execute not in ("true", "false") or trusted not in ("true", "false"):
        report, code = blocked("Invalid execution authorization configuration."), 1
    elif execute == "true" and trusted != "true":
        static_env = dict(env, RADAR_EXECUTE="false")
        code = run(static_env)
        report = json.loads(output.read_text(encoding="utf-8"))
        report["gate"].setdefault("required_checks", []).append({"id":"execution_authorization", "status":"blocked", "explanation":"Execution not authorized for this untrusted PR; static findings retained."})
        if report["gate"]["verdict"] == "pass":
            report["gate"]["verdict"], code = "blocked", 1
    else:
        try:
            base, head = env["RADAR_BASE"], env["RADAR_HEAD"]
            if not all(re.fullmatch(r"[0-9a-f]{40,64}", v) for v in (base, head)):
                raise ValueError("Base and head must be pinned commit object IDs.")
            numbers = [x.strip() for x in env.get("RADAR_ADDITIONAL_PRS", "").split(",") if x.strip()]
            if numbers and execute == "true" and env.get("RADAR_TRUST_ADDITIONAL", "false") != "true":
                output.write_text(json.dumps(blocked("Additional PR execution requires explicit authorization of every selected head via RADAR_TRUST_ADDITIONAL."), indent=2) + "\n", encoding="utf-8")
                return 1
            if len(numbers) > 8 or any(not re.fullmatch(r"[1-9][0-9]{0,8}", n) for n in numbers):
                raise ValueError("Additional PRs must be at most eight explicit positive PR numbers.")
            branches = [base, head]
            for n in numbers:
                ref = "refs/radar/selected-pr-" + n
                subprocess.run(["git", "fetch", "--no-tags", "origin", "pull/" + n + "/head:" + ref], cwd=root, check=True, stdout=subprocess.DEVNULL)
                sha = subprocess.check_output(["git", "rev-parse", "--verify", ref + "^{commit}"], cwd=root, text=True).strip()
                if not re.fullmatch(r"[0-9a-f]{40,64}", sha):
                    raise ValueError("Invalid selected PR commit.")
                branches.append(sha)
            with tempfile.TemporaryDirectory(prefix="radar-policy-") as tmp:
                required = ["textual_merge", "no_breaking_contracts"]
                if execute == "true":
                    required += ["test_selection", "integration_execution"]
                policy = pathlib.Path(tmp) / "policy.json"
                policy.write_text(json.dumps({"version": 1, "require": required}), encoding="utf-8")
                argv = [env["RADAR_BIN"], "merge-check", "--base", base, "--branches", ",".join(branches), "--suggest-tests", "--policy", str(policy), "--json"]
                if execute == "true":
                    argv += ["--verify", "--allow-execution", "--suite", "full", "--timeout", "10m"]
                # No workflow credentials, secrets or attacker-supplied shell.
                clean_env = {k: v for k, v in os.environ.items() if k in ("PATH", "HOME", "TMPDIR", "SYSTEMROOT", "GOCACHE", "GOMODCACHE")}
                proc = subprocess.run(argv, cwd=root, env=clean_env, capture_output=True, text=True, timeout=720)
                try:
                    report = json.loads(proc.stdout)
                except json.JSONDecodeError:
                    report = blocked("Radar did not produce a structured report; inspect installation and input configuration.")
                    report["gate"]["verdict"] = "error"
                code = {"pass": 0, "fail": 1, "blocked": 1, "error": 2}.get(report.get("gate", {}).get("verdict"), 2)
                if proc.returncode != 0 and code == 0:
                    code = 2
                    report["gate"]["verdict"] = "error"
                report["workflow"] = {"execution_authorized": execute == "true", "selected_additional_prs": numbers, "base": base, "head": head}
                if execute != "true":
                    report.setdefault("limitations", []).append("Repository tests were not executed. Enable reviewed execution on an ephemeral runner to establish runtime evidence.")
        except (ValueError, OSError, subprocess.SubprocessError) as err:
            report, code = blocked("Workflow input or environment error: " + str(err)), 2
            report["gate"]["verdict"] = "error"
    output.write_text(json.dumps(report, indent=2) + "\n", encoding="utf-8")
    return code


if __name__ == "__main__":
    raise SystemExit(run(os.environ))
