#!/usr/bin/env python3
"""Opt-in publisher for trusted matrix artifacts; never executes repository code."""
import argparse
import json
import os
from pathlib import Path
import re
import urllib.request
from urllib.parse import urlencode

REPO = re.compile(r"[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+\Z")


def api(path, token, method="GET", body=None):
    request = urllib.request.Request("https://api.github.com/" + path,
        data=None if body is None else json.dumps(body).encode(), method=method,
        headers={"Accept": "application/vnd.github+json", "Authorization": "Bearer " + token,
                 "Content-Type": "application/json", "X-GitHub-Api-Version": "2022-11-28"})
    with urllib.request.urlopen(request, timeout=30) as response:
        return json.load(response)


def drafts(report):
    messages = []
    for pair in report.get("pairs", []):
        numbers = pair.get("pair", [])
        if len(numbers) != 2 or any(type(n) is not int or not 0 < n < 1000000000 for n in numbers):
            raise ValueError("Invalid pair identity.")
        verdict = pair.get("verdict")
        if verdict not in ("pass", "fail", "blocked", "error"):
            raise ValueError("Invalid pair verdict.")
        marker = "<!-- radar-pr-matrix:" + "-".join(map(str, sorted(numbers))) + " -->"
        checks = [c.get("id", "") for c in pair.get("failed_checks", [])]
        if any(not re.fullmatch(r"[a-zA-Z0-9_-]{1,100}", name) for name in checks):
            raise ValueError("Invalid failed check ID.")
        body = marker + "\nRadar static pair #" + str(numbers[0]) + " + #" + str(numbers[1]) + ": **" + verdict + "**.\n"
        body += "Failed checks: " + (", ".join(checks) or "none") + ".\n"
        body += "Static evidence only; repository tests were not run. See the radar-pr-matrix workflow artifact for pinned heads, gaps and reproduction argv."
        for number in sorted(set(n for chain in pair.get("members", [numbers]) for n in chain)):
            if type(number) is not int or not 0 < number < 1000000000:
                raise ValueError("Invalid chain member.")
            messages.append({"number": number, "marker": marker, "body": body, "verdict": verdict})
    return messages


def publish(repo, report, token, enabled=False, trusted=False, request=api):
    if not enabled or not trusted:
        raise ValueError("Comment publication needs explicit enablement and a same-repository trusted PR.")
    if not REPO.fullmatch(repo) or not token:
        raise ValueError("Valid repository and token required.")
    results = []
    for draft in drafts(report):
        existing = None
        for page in range(1, 101):
            rows = request("repos/" + repo + "/issues/" + str(draft["number"]) + "/comments?" + urlencode({"per_page": 100, "page": page}), token)
            if not isinstance(rows, list):
                raise ValueError("Invalid comment response.")
            for row in rows:
                if row.get("user", {}).get("login") == "github-actions[bot]" and row.get("body", "").startswith(draft["marker"] + "\n"):
                    existing = row
            if len(rows) < 100:
                break
        else:
            raise ValueError("Comment pagination exceeded limit.")
        if existing and existing.get("body") == draft["body"]:
            results.append("unchanged")
        elif existing:
            comment_id = existing.get("id")
            if type(comment_id) is not int or comment_id <= 0:
                raise ValueError("Invalid comment ID.")
            request("repos/" + repo + "/issues/comments/" + str(comment_id), token, "PATCH", {"body": draft["body"]})
            results.append("updated")
        else:
            # Only failing or unavailable pairs create comments. Passing pairs
            # update earlier comments so a repaired pair does not look broken.
            if draft["verdict"] == "pass":
                results.append("skipped")
                continue
            request("repos/" + repo + "/issues/" + str(draft["number"]) + "/comments", token, "POST", {"body": draft["body"]})
            results.append("created")
    return results


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--report", required=True)
    parser.add_argument("--repo", required=True)
    args = parser.parse_args()
    results = publish(args.repo, json.loads(Path(args.report).read_text()), os.environ.get("GH_TOKEN", ""),
                      enabled=os.environ.get("RADAR_PUBLISH_COMMENTS") == "true",
                      trusted=os.environ.get("RADAR_TRUSTED") == "true")
    print(json.dumps({"comments": results}))


if __name__ == "__main__":
    main()
