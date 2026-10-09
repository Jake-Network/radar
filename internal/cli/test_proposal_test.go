package cli

import (
	"os/exec"
	"testing"
)

func TestCheckRecommendsWithoutExecution(t *testing.T) {
	root := gitRepo(t)
	put(t, root, "model.py", "VALUE=1\n")
	put(t, root, "test_model.py", "import unittest\nfrom model import VALUE\nclass Case(unittest.TestCase):\n def test_value(self): self.fail('recommendation must not run')\n")
	gitTest(t, root, "add", ".")
	gitTest(t, root, "commit", "-qm", "base")
	put(t, root, "model.py", "VALUE=2\n")
	r := invoke(t, root, 0, "check", "--base", "HEAD", "--suggest-tests")
	proposal := r["verification_proposal"].(map[string]any)
	if len(proposal["commands"].([]any)) != 1 {
		t.Fatal(r)
	}
	for _, check := range r["checks"].([]any) {
		c := check.(map[string]any)
		if c["id"] == "integration_tests" && c["status"] != "unknown" {
			t.Fatal("recommendations became evidence", r)
		}
	}
}

func TestExplicitCandidateWorkingDirectory(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip(err)
	}
	root := gitRepo(t)
	put(t, root, "service/test_real.py", "import unittest\nclass Case(unittest.TestCase):\n def test_real(self): self.assertEqual(2,2)\n")
	gitTest(t, root, "add", ".")
	gitTest(t, root, "commit", "-qm", "nested test")
	r := invoke(t, root, 0, "merge-check", "--base", "HEAD", "--branches", "HEAD", "--verify", "--allow-execution", "--cwd", "service", "--", "python3", "-m", "unittest", "test_real")
	if r["execution"].(map[string]any)["cwd"] != "service" || r["gate"].(map[string]any)["verdict"] != "pass" {
		t.Fatal(r)
	}
	invoke(t, root, 2, "merge-check", "--base", "HEAD", "--branches", "HEAD", "--verify", "--allow-execution", "--cwd", "../outside", "--", "python3", "-m", "unittest", "test_real")
}
