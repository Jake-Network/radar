#!/usr/bin/env python3
"""Bounded, escaped GitHub annotations and summary for existing Radar JSON."""
import html
import json
import os
import pathlib
import sys


def escape(value, prop=False):
    value = str(value).replace("%", "%25").replace("\r", "%0D").replace("\n", "%0A")
    return value.replace(":", "%3A").replace(",", "%2C") if prop else value


def cell(value):
    return html.escape(str(value)[:2000]).replace("|", "&#124;").replace("\n", " ").replace("\r", " ")


def render(report):
    if "repos" in report and "cross_repo" in report:
        return render_workspace(report)
    gate = report.get("gate", {})
    verdict = gate.get("verdict", "error")
    annotations = []
    rows = ["## Radar integration gate: " + cell(verdict), "", "A selected gate pass covers its named requirements only.", "", "| Required check | Status | Explanation |", "|---|---|---|"]
    for check in gate.get("required_checks", [])[:32]:
        rows.append("| " + " | ".join(cell(check.get(k, "")) for k in ("id", "status", "explanation")) + " |")
    for finding in report.get("findings", [])[:30]:
        level = "error" if finding.get("severity") == "error" else "warning"
        props = "title=" + escape(finding.get("code", "radar"), True)
        locations = finding.get("locations") or []
        if locations:
            loc = locations[0]
            # Report path stays annotation metadata; never interpreted as code.
            props += ",file=" + escape(loc.get("path", ""), True)
            if isinstance(loc.get("line"), int) and loc["line"] > 0:
                props += ",line=" + str(loc["line"])
        annotations.append("::" + level + " " + props + "::" + escape(str(finding.get("explanation", ""))[:2000]))
        rows += ["", "**" + cell(finding.get("code", "finding")) + "**: " + cell(finding.get("explanation", ""))]
        for k in ("remediation", "verification"):
            if finding.get(k):
                rows.append(cell(finding[k]))
    rows += ["", "Changed/affected components: " + cell(", ".join(str(x) for x in (report.get("changed", []) + report.get("affected", []))[:40])), "", "### Selected commands and reasons"]
    selection = report.get("selection") or {}
    commands = selection.get("commands", (report.get("verification_proposal") or {}).get("commands", []))
    for c in commands[:32]:
        rows.append("- " + cell(c.get("cwd", ".")) + ": " + cell(" ".join(c.get("command", []))) + " — " + cell("; ".join(r.get("explanation", "") for r in c.get("evidence_reasons", []))))
    rows += ["", "### Executed / failed / not executed"]
    for ev in report.get("executions", [])[:32]:
        obs = ev.get("observation") or {}
        rows.append("- " + cell(ev.get("status")) + ": " + cell(" ".join(ev.get("command", []))) + "; recognized tests: " + cell(obs.get("tests_run", 0)) + "; failed: " + cell(", ".join(obs.get("failed_cases", []))))
    if not report.get("executions"):
        rows.append("- No repository tests executed; runtime compatibility remains unverified.")
    for omission in selection.get("omitted", (report.get("verification_proposal") or {}).get("omitted", []))[:32]:
        rows.append("- " + cell(omission.get("reason")) + ": " + cell(", ".join(omission.get("test_files", []))) + " — " + cell(omission.get("explanation")))
    for file in selection.get("uncovered_changes", [])[:32]:
        rows.append("- Uncovered change: " + cell(file))
    for reason in selection.get("blocking", [])[:32]:
        rows.append("- Blocking: " + cell(reason))
    for limitation in report.get("limitations", [])[:12]:
        rows.append("- Coverage: " + cell(limitation))
    rows += ["", "Next: repair failures or prepare missing runners, then rerun the same selected candidate and policy. Review discovered bindings before accepting them.", "", "Full JSON is available as the workflow artifact."]
    if verdict != "pass" and not annotations:
        annotations.append("::error title=Radar gate::Required gate " + escape(verdict) + "; see job summary and JSON for missing evidence.")
    return "\n".join(annotations), "\n".join(rows) + "\n"


def render_workspace(report):
    """Preserve static link scope separately from per-repository execution."""
    verdict = report.get("verdict", "error")
    rows = ["## Radar workspace gate: " + cell(verdict), "", cell(report.get("scope_summary", "Scope unavailable.")),
            "", "Cross-repository evidence covers declared supported static links only; runtime interoperability remains unverified."]
    annotations = []
    for repo in report.get("repos", [])[:32]:
        rows += ["", "### Repository: " + cell(repo.get("id", repo.get("name", "unknown")))]
        nested = repo.get("report")
        if isinstance(nested, dict):
            _, summary = render(nested)
            rows.append(summary)
        else:
            rows.append(cell(repo.get("verdict", repo.get("status", "unknown"))))
    cross = report.get("cross_repo") or {}
    rows += ["", "### Declared cross-repository links", "", "Status: " + cell(cross.get("status", "unknown"))]
    rows.append(cell(cross.get("reason", "")))
    for link in report.get("links", [])[:32]:
        rows.append("- " + cell(link.get("id", "link")) + ": " + cell(link.get("status", "unknown")))
        for link_cell in link.get("cells", [])[:4]:
            rows.append("  - " + cell(link_cell.get("producer", "")) + " / " + cell(link_cell.get("consumer", "")) + ": " + cell(link_cell.get("status", "unknown")))
            for finding in link_cell.get("findings", [])[:8]:
                rows.append("    - " + cell(finding.get("code", "finding")) + ": " + cell(finding.get("explanation", "")))
    pair_report = report.get("cross_repo_pairs") or {}
    if pair_report:
        rows += ["", "### Cross-repository branch pairs", "", "Status: " + cell(pair_report.get("status", "unknown"))]
        for pair in pair_report.get("pairs", [])[:32]:
            labels = [str(value.get("repo", "unknown")) + ":" + str(value.get("ref", "")) for value in pair.get("inputs", [])]
            rows.append("- " + cell(" + ".join(labels)) + ": " + cell(pair.get("status", "unknown")) + " — " + cell(pair.get("reason", "")))
        if pair_report.get("omitted"):
            rows.append("- Not verified: " + cell(len(pair_report["omitted"])) + " omitted pairs.")
    if verdict != "pass":
        annotations.append("::error title=Radar workspace gate::" + escape(verdict) + "; inspect static link and per-repository scope in the JSON artifact.")
    return "\n".join(annotations), "\n".join(rows) + "\n"


if __name__ == "__main__":
    report = json.loads(pathlib.Path(sys.argv[1]).read_text(encoding="utf-8"))
    annotations, summary = render(report)
    print(annotations)
    if os.environ.get("GITHUB_STEP_SUMMARY"):
        with open(os.environ["GITHUB_STEP_SUMMARY"], "a", encoding="utf-8") as out:
            out.write(summary)
    else:
        print(summary)
