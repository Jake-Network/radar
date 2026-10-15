#!/usr/bin/env python3
"""Explicitly enabled, same-repository check runs for pinned Radar reports.

Use only reviewed base helpers/artifacts. Checks:write is required; no repository
code is executed. Unknown/incomplete results are neutral, never success.
"""
import argparse
import json
import os
from pathlib import Path, PurePosixPath
import re

from publish_matrix import api, REPO
from report import render

SHA = re.compile(r"[0-9a-f]{40,64}\Z")
CONCLUSIONS = {"pass": "success", "fail": "failure", "blocked": "neutral", "error": "action_required"}


def annotation(finding):
    locations = finding.get("locations") or []
    for location in locations:
        path, line = location.get("path", ""), location.get("line")
        if (not isinstance(path, str) or not path or "\\" in path or
                any(ord(c) < 32 for c in path) or PurePosixPath(path).is_absolute() or
                ".." in PurePosixPath(path).parts or type(line) is not int or line < 1):
            continue
        return {"path": path, "start_line": line, "end_line": line,
                "annotation_level": "failure" if finding.get("severity") == "error" else "warning",
                "title": str(finding.get("code", "Radar"))[:255],
                "message": str(finding.get("explanation", "Missing verification evidence."))[:2000]}
    return None


def object_list(value):
    return isinstance(value, list) and all(isinstance(item, dict) for item in value)


def valid_sha(value):
    return isinstance(value, str) and SHA.fullmatch(value) is not None


def complete_required_checks(report):
    if not isinstance(report, dict):
        return False
    gate = report.get("gate")
    if not isinstance(gate, dict) or gate.get("verdict") != "pass":
        return False
    policy, checks = gate.get("policy"), gate.get("required_checks")
    if not isinstance(policy, dict) or type(policy.get("version")) is not int or policy.get("version") != 1 or not object_list(checks) or not checks:
        return False
    required = policy.get("require")
    if (not isinstance(required, list) or not required or
            any(not isinstance(name, str) or not name for name in required) or
            len(set(required)) != len(required)):
        return False
    ids = [check.get("id") for check in checks]
    return (all(isinstance(name, str) and name for name in ids) and len(set(ids)) == len(ids) and
            set(required) == set(ids) and all(check.get("status") == "passed" and
            check.get("evidence") in ("verified_static", "verified_tool", "observed_test", "inferred") and
            isinstance(check.get("explanation"), str) for check in checks))


def workspace_success_errors(report):
    """Validate evidence supporting a workspace success, not just its headline.

    A workspace with only per-repo checks can legitimately have gate verdict
    pass, but this publisher withholds a cross-repository integration success
    when no declared links were checked. Original verdict and scope stay visible.
    """
    errors = []
    repos = report.get("repos")
    if (report.get("version") != 1 or not object_list(repos) or not repos or
            type(report.get("repo_count")) is not int or report["repo_count"] != len(repos)):
        return ["Missing or malformed nonempty workspace repository scope."]
    if (not isinstance(report.get("excluded_repos", []), list) or not isinstance(report.get("only", []), list) or
            report.get("excluded_repos") or report.get("only")):
        errors.append("Excluded or restricted repositories leave workspace coverage incomplete.")
    ids = [repo.get("id") for repo in repos]
    if any(not isinstance(name, str) or not name for name in ids) or len(set(ids)) != len(ids):
        errors.append("Workspace repository identities are missing or duplicated.")
    count = 0
    for repo in repos:
        nested = repo.get("report")
        branches = repo.get("branches")
        if (repo.get("verdict") != "pass" or repo.get("error") or repo.get("missing") or
                repo.get("worktree_inspection_error") or not object_list(branches) or
                not valid_sha(repo.get("base")) or not isinstance(nested, dict)):
            errors.append("A repository has missing, malformed or unavailable pinned candidate evidence.")
            continue
        count += len(branches)
        commits = [branch.get("commit") for branch in branches]
        if (any(not valid_sha(commit) for commit in commits) or
                nested.get("base") != repo["base"] or nested.get("inputs") != commits or
                not valid_sha(nested.get("candidate_commit")) or not valid_sha(nested.get("candidate_tree"))):
            errors.append("A repository candidate does not match its pinned base and selected commits.")
        if not complete_required_checks(nested):
            errors.append("A repository is missing passed required checks bound to its policy.")
        dirty_rows = repo.get("dirty", [])
        if not object_list(dirty_rows) or any(dirty.get("inspection_error") for dirty in dirty_rows):
            errors.append("A worktree could not be inspected.")
    if type(report.get("branch_count")) is not int or report["branch_count"] != count:
        errors.append("Workspace branch coverage does not match the pinned repository inputs.")
    cross = report.get("cross_repo")
    links = report.get("links")
    if not isinstance(cross, dict) or cross.get("status") != "passed" or not object_list(links) or not links:
        errors.append("Declared cross-repository link coverage is missing or incomplete.")
    else:
        for link in links:
            cells = link.get("cells")
            if (link.get("status") != "passed" or not object_list(cells) or len(cells) != 4 or
                    any(not isinstance(link.get(key), str) or not link.get(key) for key in ("id", "producer", "consumer")) or
                    link.get("producer") not in ids or link.get("consumer") not in ids or
                    cells[-1].get("producer") != "candidate" or cells[-1].get("consumer") != "candidate" or
                    cells[-1].get("status") != "passed"):
                errors.append("A declared link has unavailable or nonpassing candidate cells.")
    pairs = report.get("cross_repo_pairs")
    if pairs is not None:
        if (not isinstance(pairs, dict) or pairs.get("status") != "passed" or
                pairs.get("complete") is not True or pairs.get("omitted") or
                not object_list(pairs.get("pairs")) or not pairs["pairs"] or
                any(pair.get("status") != "passed" for pair in pairs["pairs"])):
            errors.append("Cross-repository pair coverage is incomplete or nonpassing.")
        else:
            by_id = {repo["id"]: repo for repo in repos if isinstance(repo.get("id"), str)}
            for pair in pairs["pairs"]:
                inputs = pair.get("inputs")
                if not object_list(inputs) or len(inputs) != 2:
                    errors.append("A cross-repository pair is missing its evaluated input pins.")
                    continue
                for pin in inputs:
                    source = by_id.get(pin.get("repo")) if isinstance(pin.get("repo"), str) else None
                    if (source is None or pin.get("base") != source.get("base") or
                            not valid_sha(pin.get("commit")) or not valid_sha(pin.get("candidate_tree"))):
                        errors.append("A cross-repository pair has stale or malformed candidate pins.")
                if not object_list(pair.get("links")) or not pair["links"] or any(link.get("status") != "passed" for link in pair["links"]):
                    errors.append("A cross-repository pair has missing or nonpassing link coverage.")
    return errors


def payload(report, head, name="Radar integration gate"):
    gate = report.get("gate")
    if not isinstance(gate, dict):
        gate = {}
    verdict = gate.get("verdict", report.get("verdict", "error"))
    conclusion = CONCLUSIONS.get(verdict, "action_required")
    validation_errors = workspace_success_errors(report) if verdict == "pass" and "repos" in report else []
    if validation_errors:
        conclusion = "action_required"
    try:
        _, summary = render(report)
    except (AttributeError, TypeError, KeyError, ValueError):
        summary = "Malformed report details; inspect the JSON artifact and rerun verification."
        conclusion = "action_required"
    checks = gate.get("required_checks", [])
    if not object_list(checks):
        checks = []
        conclusion = "action_required"
    if verdict == "pass" and "repos" not in report and (not checks or any(c.get("status") != "passed" for c in checks)):
        conclusion = "action_required"
    checked = [c.get("id", "unknown") for c in checks if c.get("status") == "passed"]
    missing = [c.get("id", "unknown") for c in checks if c.get("status") != "passed"]
    summary = ("Checked: " + (", ".join(map(str, checked)) or "none recorded") +
               "\n\nNot verified: " + (", ".join(map(str, missing)) or "see scope and limitations below") +
               "\n\n" + summary)
    if validation_errors:
        summary = "Publication validation: success withheld. " + " ".join(validation_errors) + "\n\n" + summary
    findings = report.get("findings", [])
    annotations = [a for f in findings if isinstance(f, dict) and (a := annotation(f)) is not None][:50] if isinstance(findings, list) else []
    return {"name": name, "head_sha": head, "status": "completed", "conclusion": conclusion,
            "output": {"title": name + ": " + str(verdict)[:40], "summary": summary[:60000],
                       "annotations": annotations}}


def pinned_pr(repo, number, head, base, token, request):
    if type(number) is not int or not 0 < number < 1000000000 or not SHA.fullmatch(head or "") or not SHA.fullmatch(base or ""):
        raise ValueError("A PR number and pinned base/head SHAs are required.")
    row = request("repos/" + repo + "/pulls/" + str(number), token)
    if (row.get("state") != "open" or row.get("head", {}).get("sha") != head or
            row.get("base", {}).get("sha") != base or
            row.get("head", {}).get("repo", {}).get("full_name") != repo or
            row.get("base", {}).get("repo", {}).get("full_name") != repo):
        raise ValueError("PR is stale, closed, or not a same-repository trusted head; rerun evaluation.")


def publish(repo, report, token, number=None, head=None, base=None, enabled=False, trusted=False, request=api):
    if not enabled or not trusted or not REPO.fullmatch(repo) or not token:
        raise ValueError("Check publication requires explicit enablement, same-repository trust, repository and token.")
    jobs = []
    if "pairs" in report:
        base = report.get("base")
        for pair in report.get("pairs", []):
            numbers, heads = pair.get("pair", []), pair.get("heads", [])
            if len(numbers) != 2 or len(heads) != 2:
                raise ValueError("Pair identity and pinned heads required.")
            inner = pair.get("report", {})
            # Pair wrapper errors must never become successful checks.
            inner = dict(inner, gate=dict(inner.get("gate", {}), verdict=pair.get("verdict", "error")))
            name = "Radar pair #" + str(numbers[0]) + " + #" + str(numbers[1])
            # Collapsed ancestor members were never evaluated at their own
            # heads. A descendant may have repaired their breakage; only the
            # two evaluated tips receive candidate-bound check evidence.
            for number, sha in zip(numbers, heads):
                jobs.append((number, sha, inner, name))
    else:
        workflow = report.get("workflow", {})
        if workflow.get("base") != base or workflow.get("head") != head:
            raise ValueError("Report workflow identity does not match the requested pinned candidate.")
        jobs.append((number, head, report, "Radar integration gate"))
    # Validate every target before publishing anything. Never attach stale pair
    # evidence to a new head, or transfer a fork report onto a trusted head.
    for n, sha, _, _ in jobs:
        pinned_pr(repo, n, sha, base, token, request)
    return [request("repos/" + repo + "/check-runs", token, "POST", payload(r, sha, name))
            for _, sha, r, name in jobs]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--report", required=True)
    parser.add_argument("--repo", required=True)
    parser.add_argument("--number", type=int)
    parser.add_argument("--head")
    parser.add_argument("--base")
    args = parser.parse_args()
    rows = publish(args.repo, json.loads(Path(args.report).read_text()), os.environ.get("GH_TOKEN", ""),
                   args.number, args.head, args.base,
                   enabled=os.environ.get("RADAR_PUBLISH_CHECKS") == "true",
                   trusted=os.environ.get("RADAR_TRUSTED") == "true")
    print(json.dumps({"check_runs": len(rows)}))


if __name__ == "__main__":
    main()
