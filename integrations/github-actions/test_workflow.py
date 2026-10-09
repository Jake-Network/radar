#!/usr/bin/env python3
import importlib.util
import json
import os
import pathlib
import subprocess
import tempfile
import unittest

HERE = pathlib.Path(__file__).resolve().parent

def module(name):
    spec = importlib.util.spec_from_file_location(name, HERE / (name + ".py"))
    m = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(m)
    return m

runner, presentation = module("run"), module("report")

class WorkflowTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory(prefix="radar-workflow-test-")
        self.addCleanup(self.tmp.cleanup)
        self.root = pathlib.Path(self.tmp.name) / "repo"
        self.root.mkdir()
        self.git("init", "-q")
        self.git("config", "user.email", "test@example.invalid")
        self.git("config", "user.name", "fixture")
        self.put("values.py", "from left import A\nfrom right import B\n")
        self.put("left.py", "A = False\n")
        self.put("right.py", "B = False\n")
        self.put("test_values.py", "import unittest\nfrom values import A, B\nclass Invariant(unittest.TestCase):\n def test_combined(self): self.assertFalse(A and B)\n")
        self.put("consumer.ts", "export {};\n")
        self.put("schema.json", json.dumps({"type":"object", "properties":{"email":{"type":"string"}}}))
        self.put(".radar/contracts.json", json.dumps({"version":1,"bindings":[{"id":"user","schema":"schema.json","producer":"values.py","consumer":"consumer.ts","fields":["email"],"direction":"response"}]}))
        self.git("add", ".")
        self.git("commit", "-qm", "base")
        self.base = self.git("rev-parse", "HEAD").strip()
        self.env = {"RADAR_ROOT":str(self.root),"RADAR_BIN":os.environ["RADAR_TEST_BIN"],"RADAR_BASE":self.base,"RADAR_HEAD":self.base,"RADAR_EXECUTE":"true","RADAR_TRUSTED":"true","RADAR_REPORT":str(pathlib.Path(self.tmp.name)/"report.json")}

    def git(self, *args):
        return subprocess.check_output(["git", *args], cwd=self.root, text=True)

    def put(self, path, content):
        p = self.root/path
        p.parent.mkdir(parents=True, exist_ok=True)
        p.write_text(content)

    def commit(self):
        self.git("add", ".")
        self.git("commit", "-qm", "change")
        self.env["RADAR_HEAD"] = self.git("rev-parse", "HEAD").strip()

    def observe(self, expected, verdict):
        before = self.git("status", "--porcelain")
        self.assertEqual(runner.run(self.env), expected)
        report = json.loads(pathlib.Path(self.env["RADAR_REPORT"]).read_text())
        self.assertEqual(report["gate"]["verdict"], verdict)
        annotations, summary = presentation.render(report)
        self.assertIn("Radar integration gate: " + verdict, summary)
        if expected:
            self.assertIn("::error", annotations)
        self.assertEqual(self.git("status", "--porcelain"), before)
        return report

    def test_success(self):
        self.observe(0, "pass")

    def test_declared_break(self):
        self.put("schema.json", '{"type":"object","properties":{}}')
        self.commit()
        r = self.observe(1, "fail")
        self.assertTrue(any(x.get("contract") for x in r["findings"]))

    def test_separate_pass_combined_fail(self):
        self.put("left.py", "A = True\n")
        self.commit()
        a = self.env["RADAR_HEAD"]
        self.observe(0, "pass")
        self.git("checkout", "-q", self.base)
        self.put("right.py", "B = True\n")
        self.commit()
        b = self.env["RADAR_HEAD"]
        self.observe(0, "pass")
        # Explicitly selected additional PR produces a clean textual merge
        # with a real observed failure; separately passing heads are insufficient.
        self.git("update-ref", "refs/pull/2/head", b)
        self.git("remote", "add", "origin", str(self.root))
        self.env["RADAR_BASE"] = self.base
        self.env["RADAR_HEAD"] = a
        self.env["RADAR_ADDITIONAL_PRS"] = "2"
        self.env["RADAR_TRUST_ADDITIONAL"] = "true"
        r = self.observe(1, "fail")
        self.assertEqual(r["workflow"]["selected_additional_prs"], ["2"])
        self.assertTrue(any(c["id"] == "integration_execution" and c["status"] == "failed" for c in r["checks"]))
        self.assertTrue(any(c["id"] == "textual_merge" and c["status"] == "passed" for c in r["checks"]))

    def test_missing_runner(self):
        self.put("node/package.json", '{}')
        self.put("node/a.test.js", "const test = require('node:test'); test('ok', () => {});\n")
        self.commit()
        # Keep Git/Python but omit Node from execution PATH.
        tools = pathlib.Path(self.tmp.name)/"tools"
        tools.mkdir()
        import shutil
        for name in ("git", "python3"):
            (tools/name).symlink_to(shutil.which(name))
        old = os.environ.get("PATH", "")
        os.environ["PATH"] = str(tools)
        try:
            r = self.observe(1, "blocked")
            self.assertTrue(r["selection"]["omitted"])
        finally:
            os.environ["PATH"] = old

    def test_malformed_manifest(self):
        self.put(".radar/contracts.json", '{bad')
        self.commit()
        self.observe(2, "error")

    def test_untrusted_execution_rejected(self):
        self.env["RADAR_TRUSTED"] = "false"
        r = self.observe(1, "blocked")
        self.assertNotIn("executions", r)

    def test_shell_metacharacters_rejected(self):
        self.env["RADAR_ADDITIONAL_PRS"] = "1; touch /tmp/radar-unsafe"
        self.env["RADAR_TRUST_ADDITIONAL"] = "true"
        self.observe(2, "error")

    def test_annotation_escaping(self):
        a, s = presentation.render({"gate":{"verdict":"fail"},"findings":[{"severity":"error","code":"x\n::notice::bad","explanation":"a\n::notice::bad%","locations":[{"path":"a,b\n::error::bad","line":2}]}]})
        self.assertEqual(len(a.splitlines()), 1)
        self.assertIn("%0A", a)
        self.assertIn("%2C", a)
        self.assertNotIn("\n::notice", s)

if __name__ == "__main__":
    unittest.main()
