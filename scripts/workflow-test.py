#!/usr/bin/env python3
"""Enforce Radar's checked-in workflow trust boundaries; actionlint checks YAML.

These targeted guardrails deliberately do not claim to validate arbitrary YAML
or replace protected GitHub environments and branch protection settings.
"""
import pathlib
import re
import unittest

ROOT = pathlib.Path(__file__).resolve().parent.parent
WORKFLOWS = (
    ".github/workflows/ci.yml",
    ".github/workflows/release.yml",
    "integrations/github-actions/radar-pull-request.yml",
)


def violations(text):
    errors = []
    if re.search(r"\bpull_request_target\s*:", text):
        errors.append("privileged pull_request_target trigger")
    for action in re.findall(r"^\s*(?:-\s*)?uses:\s*(\S+)", text, re.M):
        if not re.fullmatch(r"[\w.-]+/[\w./-]+@[0-9a-f]{40}", action):
            errors.append("action must use an immutable SHA: " + action)
    if not re.search(r"^permissions:\s*\n  contents: read\s*$", text, re.M):
        errors.append("workflow default token must be read only")
    # Expressions must enter scripts through env, where quotes preserve data.
    in_run = False
    indent = 0
    for line in text.splitlines():
        stripped = line.lstrip()
        current = len(line) - len(stripped)
        if stripped and not stripped.startswith("#") and in_run and current <= indent:
            in_run = False
        if re.match(r"(?:-\s*)?run:\s*", stripped):
            in_run, indent = True, current
        if in_run and "${{" in line:
            errors.append("workflow expression interpolated directly into script")
    for checkout in re.split(r"uses: actions/checkout@", text)[1:]:
        step = re.split(r"\n      - ", checkout, maxsplit=1)[0]
        if "persist-credentials: false" not in step:
            errors.append("checkout must not persist write credentials")
    return errors


class WorkflowPolicyTests(unittest.TestCase):
    def test_all_workflows_follow_trust_boundaries(self):
        for path in WORKFLOWS:
            with self.subTest(path=path):
                self.assertEqual(violations((ROOT / path).read_text()), [])

    def test_mutations_are_rejected(self):
        source = (ROOT / WORKFLOWS[0]).read_text()
        for mutation in (
            re.sub(r"actions/checkout@[0-9a-f]{40}", "actions/checkout@v4", source),
            source.replace("contents: read", "contents: write", 1),
            source.replace("persist-credentials: false", "persist-credentials: true", 1),
            source + "\non:\n  pull_request_target:\n",
            source + '\n      - run: echo "${{ github.event.pull_request.title }}"\n',
        ):
            self.assertTrue(violations(mutation))

    def test_ci_enforces_existing_validation(self):
        source = (ROOT / WORKFLOWS[0]).read_text()
        for required in (
            "gofmt -l cmd internal", "go vet ./...", "go test ./...",
            "go test -race ./...", "go build", "scripts/smoke.sh",
            "examples/organization/demo.sh", "examples/verification/demo.sh",
            "examples/integration/demo.sh", "examples/intelligent-verification/demo.sh",
            "examples/selection-eval/demo.sh", "github-actions/test_workflow.py",
            "scripts/release-test.py", "scripts/install-release-test.sh",
            "scripts/install-release-test.ps1", "scripts/validate-release.py",
            "actionlint@v1.7.7", "scripts/workflow-test.py",
        ):
            with self.subTest(required=required):
                self.assertIn(required, source)

    def test_publication_requires_explicit_manual_request(self):
        source = (ROOT / WORKFLOWS[1]).read_text()
        publish = source.split("\n  publish:\n", 1)[1]
        self.assertIn("github.event_name == 'workflow_dispatch' && inputs.publish", publish)
        self.assertIn("startsWith(github.ref, 'refs/tags/v')", publish)
        self.assertIn("environment: release", publish)
        self.assertIn("needs: collect", publish)
        self.assertIn("--verify-tag", publish)
        self.assertEqual(source.count("contents: write"), 1)
        self.assertNotRegex(source, r"git (?:push|tag)\s")

    def test_every_native_platform_must_qualify(self):
        source = (ROOT / WORKFLOWS[1]).read_text()
        for target in ("linux_amd64", "linux_arm64", "darwin_amd64", "darwin_arm64", "windows_amd64"):
            self.assertIn("target: " + target, source)
        for required in ("CGO_ENABLED: \"1\"", "go vet ./...", "go test ./...", "go test -race ./...", "scripts/release-test.py", "scripts/validate-release.py", "scripts/install-release-test.ps1", "sha256sum --check"):
            self.assertIn(required, source)
        self.assertNotIn("continue-on-error", source)
        self.assertIn("needs: build", source)
        self.assertIn("if: runner.os != 'Windows'", source)


if __name__ == "__main__":
    unittest.main()
