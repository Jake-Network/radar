#!/usr/bin/env python3
"""Enforce Radar's checked-in workflow trust boundaries; actionlint checks YAML.

These targeted guardrails deliberately do not claim to validate arbitrary YAML
or replace protected GitHub environments and branch protection settings.
"""
import os
import pathlib
import subprocess
import tempfile
import textwrap
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
    if not re.search(r"^permissions:\s*\n(?:  [^\n]+\n)*?  contents: read\s*$", text, re.M):
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
            "examples/integration/demo.sh", "examples/watch/demo.sh", "examples/intelligent-verification/demo.sh",
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
        self.assertIn("needs.resolve.outputs.tag != ''", publish)
        self.assertIn("environment: release", publish)
        self.assertIn("needs: [resolve, collect]", publish)
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
        self.assertIn("needs: [resolve, build]", source)
        self.assertIn("if: runner.os != 'Windows'", source)

    def test_release_sources_are_pinned_to_resolved_commit(self):
        source = (ROOT / WORKFLOWS[1]).read_text()
        for job in ("build", "collect", "publish"):
            body = re.split(r"\n  [a-z]+:\n", source.split("\n  " + job + ":\n", 1)[1], maxsplit=1)[0]
            self.assertIn("ref: ${{ needs.resolve.outputs.sha }}", body)
        self.assertIn('test "$(git rev-parse --verify "refs/tags/$RELEASE_TAG^{commit}")" = "$SOURCE_SHA"', source)
        self.assertNotIn("Canonicalize macOS temporary directory", source)
        ci = (ROOT / WORKFLOWS[0]).read_text()
        self.assertIn("macos-default-tmpdir:", ci)
        self.assertIn("git config core.autocrlf true", ci)

    def test_source_resolution_uses_existing_tags_and_rejects_untrusted_input(self):
        source = (ROOT / WORKFLOWS[1]).read_text()
        resolver = source.split("      - name: Resolve an immutable source commit\n", 1)[1]
        resolver = resolver.split("        run: |\n", 1)[1].split("\n  build:", 1)[0]
        script = textwrap.dedent(resolver)
        with tempfile.TemporaryDirectory() as directory:
            repo = pathlib.Path(directory) / "repo"
            repo.mkdir()
            def git(*args):
                return subprocess.check_output(
                    ["git", "-c", "user.name=Radar", "-c", "user.email=radar@example.invalid", *args],
                    cwd=repo, text=True, stderr=subprocess.DEVNULL,
                ).strip()
            git("init", "-q")
            git("commit", "-q", "--allow-empty", "-m", "fixture")
            sha = git("rev-parse", "HEAD")
            git("tag", "-a", "v0.2.0", "-m", "immutable")
            git("remote", "add", "origin", str(repo))
            output = pathlib.Path(directory) / "outputs"
            def resolve(tag="", publish="false", ref="refs/heads/main"):
                output.write_text("")
                env = dict(os.environ, REQUESTED_TAG=tag, REQUESTED_PUBLISH=publish,
                           GITHUB_REF=ref, GITHUB_REF_NAME=ref.rsplit("/", 1)[-1],
                           GITHUB_SHA=sha, GITHUB_OUTPUT=str(output))
                result = subprocess.run(["bash", "-euo", "pipefail", "-c", script],
                                        cwd=repo, env=env, capture_output=True, text=True)
                return result.returncode, output.read_text()
            for tag, ref in (("v0.2.0", "refs/heads/main"), ("", "refs/tags/v0.2.0")):
                code, values = resolve(tag, "true", ref)
                self.assertEqual(code, 0)
                self.assertIn("sha=" + sha + "\n", values)
                self.assertIn("tag=v0.2.0\nversion=0.2.0\n", values)
            code, values = resolve()
            self.assertEqual(code, 0)
            self.assertIn("version=0.0.0-dryrun." + sha[:7], values)
            self.assertNotEqual(resolve(publish="true")[0], 0)
            marker = pathlib.Path(directory) / "injected"
            for tag in ("v9.9.9", "v00.2.0", "v0.2.0-01", "--upload-pack=bad", "v0.2.0\nsha=bad",
                        "$(touch " + str(marker) + ")"):
                with self.subTest(tag=tag):
                    code, values = resolve(tag)
                    self.assertNotEqual(code, 0)
                    self.assertEqual(values, "")
            self.assertFalse(marker.exists())


if __name__ == "__main__":
    unittest.main()
