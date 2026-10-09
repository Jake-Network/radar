#!/usr/bin/env python3
"""Labeled mutation evaluation of Radar test selection.

For every mutation: establish a passing baseline, introduce one defect, run
the complete relevant test suite to observe which test files actually fail
(ground truth), ask Radar for a read-only selection, and compare.

Usage:
  evaluate.py fixture --radar PATH [--out DIR]
  evaluate.py click --radar PATH --repo CLICK_CHECKOUT --python VENV_PYTHON
                    [--mutations click-mutations.json] [--out DIR]

The fixture mode needs python3, node and go. Click mode needs a prepared
checkout and a Python environment with pytest and Click importable; this
script never installs anything.
"""
import argparse
import json
import pathlib
import shutil
import subprocess
import sys
import tempfile
import time

HERE = pathlib.Path(__file__).resolve().parent
MODES = ("targeted", "balanced")
# A mutation can hang the suite (for example an endless prompt loop). Its
# ground truth is then unavailable and the row is reported, not dropped.
SUITE_TIMEOUT = 120


def git(repo, *args):
    env = {"GIT_CONFIG_NOSYSTEM": "1", "GIT_CONFIG_GLOBAL": "/dev/null", "PATH": "/usr/bin:/bin",
           "GIT_AUTHOR_NAME": "eval", "GIT_AUTHOR_EMAIL": "eval@example.invalid",
           "GIT_COMMITTER_NAME": "eval", "GIT_COMMITTER_EMAIL": "eval@example.invalid"}
    return subprocess.run(["git", "-C", str(repo), *args], check=True, capture_output=True, text=True, env=env).stdout.strip()


def apply(repo, mutation):
    for edit in mutation["edits"]:
        path = repo / edit["path"]
        text = path.read_text()
        if edit["find"] not in text:
            raise SystemExit(f"{mutation['id']}: pattern not found in {edit['path']}")
        path.write_text(text.replace(edit["find"], edit["replace"], -1 if edit.get("all") else 1))


def radar_select(radar, repo, base, head, mode):
    started = time.monotonic()
    proc = subprocess.run([radar, "--root", str(repo), "--json", "check", "--base", base, "--head", head,
                           "--suite", mode, "--max-commands", "64"], capture_output=True, text=True)
    elapsed = time.monotonic() - started
    if proc.returncode == 2:
        raise SystemExit(f"radar error: {proc.stdout}\n{proc.stderr}")
    report = json.loads(proc.stdout)
    selection = report.get("selection") or {}
    files = set()
    for command in selection.get("commands", []):
        files.update(command.get("test_files", []))
    return {"files": files, "seconds": elapsed, "commands": len(selection.get("commands", [])),
            "blocking": selection.get("blocking", []), "uncovered": selection.get("uncovered_changes", []),
            "omitted": [(o["reason"], o["test_files"]) for o in selection.get("omitted", [])],
            "inventory": selection.get("inventory_test_files", 0),
            "contract_findings": [f["code"] for f in report.get("findings", []) if f.get("contract")]}


# --- fixture ground truth -------------------------------------------------

def fixture_tests(repo):
    tests = []
    for p in sorted(repo.glob("services/api/tests/test_*.py")):
        tests.append((p.relative_to(repo).as_posix(), "services/api", ["python3", "-m", "unittest", "discover", "-s", "tests", "-p", p.name]))
    for p in sorted(repo.glob("integration/test_*.py")):
        tests.append((p.relative_to(repo).as_posix(), ".", ["python3", "-m", "unittest", "discover", "-s", "integration", "-p", p.name]))
    for p in sorted(repo.glob("web/test/*.test.js")):
        tests.append((p.relative_to(repo).as_posix(), "web", ["node", "--test", "test/" + p.name]))
    for p in sorted(repo.glob("gosvc/**/*_test.go")):
        pkg = "./" + p.parent.relative_to(repo / "gosvc").as_posix()
        tests.append((p.relative_to(repo).as_posix(), "gosvc", ["go", "test", "-count=1", pkg]))
    return tests


def run_fixture_suite(repo):
    """Runs every test file; returns {file: (passed, seconds)}."""
    results = {}
    for path, cwd, argv in fixture_tests(repo):
        started = time.monotonic()
        proc = subprocess.run(argv, cwd=repo / cwd, capture_output=True, text=True, timeout=SUITE_TIMEOUT)
        results[path] = (proc.returncode == 0, time.monotonic() - started)
    return results


# --- click ground truth ---------------------------------------------------

def run_pytest_suite(repo, python):
    started = time.monotonic()
    proc = subprocess.run([python, "-m", "pytest", "-q", "-rfE", "-p", "no:cacheprovider", "tests"],
                          cwd=repo, capture_output=True, text=True, timeout=SUITE_TIMEOUT)
    elapsed = time.monotonic() - started
    failing = set()
    for line in proc.stdout.splitlines():
        if line.startswith(("FAILED ", "ERROR ")):
            failing.add(line.split()[1].split("::")[0])
    if proc.returncode not in (0, 1):
        raise SystemExit("pytest could not run the suite:\n" + proc.stdout[-2000:])
    files = {p.relative_to(repo).as_posix() for p in (repo / "tests").rglob("test_*.py")}
    # Per-file durations are not observed in one pytest process; attribute evenly.
    return {f: (f not in failing, elapsed / max(len(files), 1)) for f in files}, elapsed


# --- evaluation -----------------------------------------------------------

def evaluate(radar, repo, mutations, suite):
    base = git(repo, "rev-parse", "HEAD")
    baseline = suite(repo)
    broken = [f for f, (ok, _) in baseline.items() if not ok]
    if broken:
        raise SystemExit(f"baseline is not passing: {broken}")
    rows = []
    for mutation in mutations:
        git(repo, "checkout", "-q", "--detach", base)
        apply(repo, mutation)
        git(repo, "commit", "-qam", mutation["id"])
        head = git(repo, "rev-parse", "HEAD")
        started = time.monotonic()
        try:
            results = suite(repo)
        except subprocess.TimeoutExpired:
            rows.append({"id": mutation["id"], "category": mutation["category"], "ground_truth": "unavailable: suite timed out", "failing": [], "inventory": 0})
            git(repo, "checkout", "-q", "--detach", base)
            continue
        full_seconds = time.monotonic() - started
        failing = {f for f, (ok, _) in results.items() if not ok}
        row = {"id": mutation["id"], "category": mutation["category"], "ground_truth": "observed", "failing": sorted(failing),
               "inventory": len(results), "full_suite_seconds": round(full_seconds, 3)}
        for mode in MODES:
            s = radar_select(radar, repo, base, head, mode)
            selected = s["files"]
            hit = failing & selected
            row[mode] = {
                "selected": sorted(selected),
                "selected_count": len(selected),
                "missed": sorted(failing - selected),
                "recall": (len(hit) / len(failing)) if failing else None,
                "precision": (len(hit) / len(selected)) if selected else None,
                "reduction": 1 - len(selected) / len(results) if results else None,
                "selection_seconds": round(s["seconds"], 3),
                "selected_test_seconds": round(sum(results[f][1] for f in selected if f in results), 3),
                "commands": s["commands"],
                "blocking": s["blocking"],
                "uncovered": s["uncovered"],
                "fallback_needed": bool(s["blocking"] or s["uncovered"]),
                "contract_findings": s["contract_findings"],
            }
        rows.append(row)
        git(repo, "checkout", "-q", "--detach", base)
    return rows


def summarize(rows):
    out = {"ground_truth_unavailable": [r["id"] for r in rows if r["ground_truth"] != "observed"]}
    rows = [r for r in rows if r["ground_truth"] == "observed"]
    for mode in MODES:
        detecting = [r for r in rows if r["failing"]]
        caught = sum(1 for r in detecting if not r[mode]["missed"])
        failing = sum(len(r["failing"]) for r in detecting)
        hits = sum(len(r["failing"]) - len(r[mode]["missed"]) for r in detecting)
        selected = sum(r[mode]["selected_count"] for r in rows)
        inventory = sum(r["inventory"] for r in rows)
        out[mode] = {
            "mutations": len(rows),
            "mutations_with_failures": len(detecting),
            "mutations_fully_detected": caught,
            "failing_file_recall": round(hits / failing, 3) if failing else None,
            "selected_files": selected,
            "selected_failing_files": hits,
            "precision": round(hits / selected, 3) if selected else None,
            "execution_reduction": round(1 - selected / inventory, 3) if inventory else None,
            "false_negative_mutations": [r["id"] for r in detecting if r[mode]["missed"]],
            "selections_without_observed_failure": [r["id"] for r in rows if not r["failing"] and r[mode]["selected_count"]],
            "fallback_flagged": [r["id"] for r in rows if r[mode]["fallback_needed"]],
            "median_selection_seconds": sorted(r[mode]["selection_seconds"] for r in rows)[len(rows) // 2],
        }
    return out


def markdown(rows, summary, title):
    lines = [f"# {title}", "", "| Mutation | Category | Failing files | " + " | ".join(f"{m} selected / missed" for m in MODES) + " |",
             "| --- | --- | --- | " + " | ".join("---" for _ in MODES) + " |"]
    for r in rows:
        if r["ground_truth"] != "observed":
            lines.append(f"| {r['id']} | {r['category']} | {r['ground_truth']} | " + " | ".join("-" for _ in MODES) + " |")
            continue
        cells = [f"{r[m]['selected_count']}/{r['inventory']} / {', '.join(r[m]['missed']) or '-'}" for m in MODES]
        lines.append(f"| {r['id']} | {r['category']} | {', '.join(r['failing']) or '(none)'} | " + " | ".join(cells) + " |")
    lines += ["", "```json", json.dumps(summary, indent=2), "```"]
    return "\n".join(lines) + "\n"


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("target", choices=["fixture", "click"])
    ap.add_argument("--radar", required=True)
    ap.add_argument("--repo")
    ap.add_argument("--python")
    ap.add_argument("--mutations")
    ap.add_argument("--out", default=".")
    a = ap.parse_args()
    radar = str(pathlib.Path(a.radar).resolve())
    out = pathlib.Path(a.out)
    out.mkdir(parents=True, exist_ok=True)
    if a.target == "fixture":
        work = pathlib.Path(tempfile.mkdtemp(prefix="radar-selection-eval-"))
        repo = work / "repo"
        shutil.copytree(HERE / "fixture", repo)
        git(repo, "init", "-q")
        git(repo, "add", ".")
        git(repo, "commit", "-qm", "baseline")
        mutations = json.loads((HERE / (a.mutations or "mutations.json")).read_text())
        rows = evaluate(radar, repo, mutations, run_fixture_suite)
        title = "Fixture mutation evaluation"
    else:
        if not (a.repo and a.python):
            ap.error("click mode requires --repo and --python")
        repo = pathlib.Path(a.repo).resolve()
        mutations = json.loads((HERE / (a.mutations or "click-mutations.json")).read_text())
        rows = evaluate(radar, repo, mutations, lambda r: run_pytest_suite(r, a.python)[0])
        title = "Pallets Click mutation evaluation"
    summary = summarize(rows)
    (out / f"{a.target}-results.json").write_text(json.dumps({"rows": rows, "summary": summary}, indent=2) + "\n")
    (out / f"{a.target}-results.md").write_text(markdown(rows, summary, title))
    print(markdown(rows, summary, title))


if __name__ == "__main__":
    sys.exit(main())
