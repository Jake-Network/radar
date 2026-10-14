#!/usr/bin/env python3
"""Trusted, static-only PR matrix wrapper. Network belongs here, never in Radar.

Run from reviewed base helpers with a pinned Radar binary. Never run PR helpers.
JSON includes skipped pairs and gaps; an empty matrix is not verification.
"""
import argparse
import itertools
import json
import os
from pathlib import Path
import re
import subprocess
import urllib.request

SHA = re.compile(r"[0-9a-f]{40,64}\Z")
REPO = re.compile(r"[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+\Z")


def github(path, token):
    request = urllib.request.Request("https://api.github.com/" + path,
        headers={"Accept": "application/vnd.github+json", "Authorization": "Bearer " + token,
                 "X-GitHub-Api-Version": "2022-11-28"})
    with urllib.request.urlopen(request, timeout=30) as response:
        return json.load(response)


def open_prs(repo, base_ref, token, api=github):
    if not REPO.fullmatch(repo):
        raise ValueError("Repository must be owner/name.")
    from urllib.parse import urlencode
    result = []
    for page in range(1, 101):
        rows = api("repos/" + repo + "/pulls?" + urlencode({"state": "open", "base": base_ref,
                   "per_page": 100, "page": page}), token)
        if not isinstance(rows, list):
            raise ValueError("Invalid PR API response.")
        for row in rows:
            number, head = row.get("number"), row.get("head", {}).get("sha", "")
            if type(number) is not int or not 0 < number < 1000000000 or not SHA.fullmatch(head):
                raise ValueError("Invalid PR number or pinned head SHA.")
            result.append({"number": number, "head": head})
        if len(rows) < 100:
            return sorted(result, key=lambda pr: pr["number"])
    raise ValueError("PR listing exceeds the bounded pagination limit.")


def git(root, *args, codes=(0,)):
    proc = subprocess.run(["git", "-c", "core.hooksPath=/dev/null", "-c", "core.fsmonitor=false", *args],
                          cwd=root, text=True, capture_output=True, timeout=120)
    if proc.returncode not in codes:
        raise ValueError("Git inspection failed: " + args[0])
    return proc


def collapse(prs, ancestor):
    """Collapse chains into tips, retaining member PR numbers for event selection."""
    groups = []
    for pr in prs:
        descendants = [other for other in prs if other["head"] != pr["head"] and ancestor(pr["head"], other["head"])]
        if descendants or any(g["head"] == pr["head"] for g in groups):
            continue
        members = sorted(p["number"] for p in prs if p["head"] == pr["head"] or ancestor(p["head"], pr["head"]))
        groups.append(dict(pr, members=members))
    return groups


def evaluate(root, radar, base, prs, trigger=None, max_pairs=1000):
    if not SHA.fullmatch(base):
        raise ValueError("Base must be a pinned commit SHA.")
    def ancestor(a, b):
        return git(root, "merge-base", "--is-ancestor", a, b, codes=(0, 1)).returncode == 0
    tips = collapse(prs, ancestor)
    results, skipped = [], []
    clean_env = {k: v for k, v in os.environ.items() if k in ("PATH", "HOME", "TMPDIR", "SYSTEMROOT")}
    changed = {}
    if len(prs) > 50:
        for tip in tips:
            common = git(root, "merge-base", base, tip["head"]).stdout.strip()
            changed[tip["head"]] = set(git(root, "diff", "--name-only", "-z", common, tip["head"]).stdout.split("\0")) - {""}
    for a, b in itertools.combinations(tips, 2):
        if trigger is not None and trigger not in a["members"] + b["members"]:
            continue
        pair = [a["number"], b["number"]]
        if len(results) >= max_pairs:
            skipped.append({"pair": pair, "reason": "pair budget exhausted"})
            continue
        if changed and not changed[a["head"]].intersection(changed[b["head"]]):
            skipped.append({"pair": pair, "reason": "large-matrix changed-file prefilter; cross-file conflicts not checked"})
            continue
        argv = [radar, "merge-check", "--base", base, "--branches", a["head"] + "," + b["head"], "--suggest-tests", "--json"]
        proc = subprocess.run(argv, cwd=root, env=clean_env, text=True, capture_output=True, timeout=720)
        try:
            report = json.loads(proc.stdout)
        except json.JSONDecodeError:
            report = {"gate": {"verdict": "error"}, "limitations": ["Radar emitted no structured report."]}
        verdict = report.get("gate", {}).get("verdict", "error")
        if proc.returncode not in (0, 1) or verdict not in ("pass", "fail", "blocked", "error"):
            verdict = "error"
        results.append({"pair": pair, "members": [a["members"], b["members"]], "heads": [a["head"], b["head"]],
                        "verdict": verdict, "failed_checks": [c for c in report.get("checks", []) if c.get("status") == "failed"],
                        "reproduce_argv": argv, "report": report})
    status = "fail" if any(r["verdict"] == "fail" for r in results) else "error" if any(r["verdict"] == "error" for r in results) else "incomplete" if skipped or not results or any(r["verdict"] == "blocked" for r in results) else "pass"
    return {"version": 1, "status": status, "base": base, "chains": tips, "pairs": results, "skipped": skipped,
            "limitations": ["Static evidence only; repository code was not executed.", "Comment publication is a separate explicitly enabled workflow step."]}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--root", required=True)
    parser.add_argument("--radar", required=True)
    parser.add_argument("--repo", required=True)
    parser.add_argument("--base", required=True)
    parser.add_argument("--base-ref", required=True)
    parser.add_argument("--trigger", type=int)
    parser.add_argument("--output", required=True)
    args = parser.parse_args()
    try:
        prs = open_prs(args.repo, args.base_ref, os.environ.get("GH_TOKEN", ""))
        for pr in prs:
            # Fixed origin, validated number and isolated local ref. Fetch PR ref,
            # then verify the pinned API SHA to detect updates during evaluation.
            ref = "refs/radar/matrix/" + str(pr["number"])
            git(args.root, "fetch", "--no-tags", "origin", "+refs/pull/" + str(pr["number"]) + "/head:" + ref)
            if git(args.root, "rev-parse", "--verify", ref + "^{commit}").stdout.strip() != pr["head"]:
                raise ValueError("PR head changed during fetch; rerun the matrix.")
        report = evaluate(args.root, args.radar, args.base, prs, args.trigger)
    except (ValueError, OSError, subprocess.SubprocessError) as error:
        report = {"version": 1, "status": "error", "pairs": [], "limitations": [str(error)]}
    Path(args.output).write_text(json.dumps(report, indent=2) + "\n", encoding="utf-8")
    return {"pass": 0, "fail": 1, "incomplete": 1, "error": 2}[report["status"]]


if __name__ == "__main__":
    raise SystemExit(main())
