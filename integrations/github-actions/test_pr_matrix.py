import importlib.util
import json
import os
import pathlib
import subprocess
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location("matrix", pathlib.Path(__file__).with_name("pr_matrix.py"))
matrix = importlib.util.module_from_spec(spec)
spec.loader.exec_module(matrix)


class MatrixTests(unittest.TestCase):
    def test_api_validates_untrusted_inputs(self):
        calls = []
        def api(path, token):
            calls.append(path)
            return [{"number": 1, "head": {"sha": "a" * 40}}]
        self.assertEqual(matrix.open_prs("owner/repo", "main;$(bad)", "secret", api)[0]["number"], 1)
        self.assertIn("main%3B%24%28bad%29", calls[0])
        with self.assertRaises(ValueError):
            matrix.open_prs("owner/repo/../../", "main", "", api)
        with self.assertRaises(ValueError):
            matrix.open_prs("owner/repo", "main", "", lambda *args: [{"number": "1;bad", "head": {"sha": "a" * 40}}])

    def test_stacked_chain_and_event_selection(self):
        prs = [{"number": n, "head": str(n) * 40} for n in (1, 2, 3)]
        def git(root, *args, **kwargs):
            code = 0 if args[-2:] == ("2" * 40, "3" * 40) else 1
            return subprocess.CompletedProcess([], code, "", "")
        commands = []
        def run(argv, **kwargs):
            commands.append(argv)
            self.assertNotIn("GH_TOKEN", kwargs["env"])
            return subprocess.CompletedProcess(argv, 1, json.dumps({"gate": {"verdict": "fail"}, "checks": [{"id": "migration_order", "status": "failed"}]}), "")
        with patch.object(matrix, "git", side_effect=git), patch.object(matrix.subprocess, "run", side_effect=run):
            result = matrix.evaluate("/tmp", "/tmp/radar", "a" * 40, prs, trigger=2)
        self.assertEqual([p["pair"] for p in result["pairs"]], [[1, 3]])
        self.assertEqual(result["status"], "fail")
        self.assertNotIn("--run", commands[0])
        self.assertNotIn("--allow-execution", commands[0])
        self.assertEqual(result["pairs"][0]["members"], [[1], [2, 3]])

    def test_empty_is_incomplete(self):
        self.assertEqual(matrix.evaluate("/tmp", "radar", "a" * 40, []) ["status"], "incomplete")

    def test_real_ancestry_collapse(self):
        with tempfile.TemporaryDirectory() as root:
            def git(*args):
                return subprocess.check_output(["git", *args], cwd=root, text=True).strip()
            git("init", "-q")
            git("config", "user.name", "fixture")
            git("config", "user.email", "fixture@example.invalid")
            git("commit", "--allow-empty", "-qm", "base")
            base = git("rev-parse", "HEAD")
            git("commit", "--allow-empty", "-qm", "first")
            first = git("rev-parse", "HEAD")
            git("commit", "--allow-empty", "-qm", "stack")
            second = git("rev-parse", "HEAD")
            result = matrix.evaluate(root, "unused", base, [{"number": 1, "head": first}, {"number": 2, "head": second}])
            self.assertEqual(result["chains"][0]["members"], [1, 2])
            self.assertEqual(result["status"], "incomplete")

    @unittest.skipUnless(os.environ.get("RADAR_TEST_BIN"), "Set RADAR_TEST_BIN for real matrix gate regression")
    def test_real_combined_migration_failure(self):
        with tempfile.TemporaryDirectory() as root:
            def git(*args):
                return subprocess.check_output(["git", *args], cwd=root, text=True).strip()
            def put(name, value):
                target = pathlib.Path(root) / name
                target.parent.mkdir(parents=True, exist_ok=True)
                target.write_text(value)
            def commit(message):
                git("add", ".")
                git("commit", "-qm", message)
                return git("rev-parse", "HEAD")
            git("init", "-q")
            git("config", "user.name", "fixture")
            git("config", "user.email", "fixture@example.invalid")
            put("db/env.py", "# Alembic environment\n")
            put("db/versions/root.py", 'revision = "root"\ndown_revision = None\n')
            base = commit("base")
            put("db/versions/a.py", 'revision = "a"\ndown_revision = "root"\n')
            first = commit("a")
            git("checkout", "-q", base)
            put("db/versions/b.py", 'revision = "b"\ndown_revision = "root"\n')
            second = commit("b")
            put("notes.txt", "stacked follow-up\n")
            third = commit("stack")
            before = git("status", "--porcelain")
            result = matrix.evaluate(root, os.environ["RADAR_TEST_BIN"], base,
                [{"number": 1, "head": first}, {"number": 2, "head": second}, {"number": 3, "head": third}], trigger=2)
            self.assertEqual([p["pair"] for p in result["pairs"]], [[1, 3]])
            self.assertEqual(result["status"], "fail")
            self.assertTrue(any(c["id"] == "migration_order" for c in result["pairs"][0]["failed_checks"]))
            self.assertEqual(git("status", "--porcelain"), before)

    def test_bad_base_rejected_before_process(self):
        with self.assertRaises(ValueError):
            matrix.evaluate("/tmp", "radar", "--upload-pack=bad", [])


if __name__ == "__main__":
    unittest.main()
