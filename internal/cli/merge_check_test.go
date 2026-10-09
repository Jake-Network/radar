package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestMergeCheckDefaultDoesNotWriteRepositoryState(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip(err)
	}
	root := gitRepo(t)
	put(t, root, "test_real.py", "import unittest\nclass Case(unittest.TestCase):\n def test_real(self): self.assertEqual(1+1,2)\n")
	gitTest(t, root, "add", ".")
	gitTest(t, root, "commit", "-qm", "baseline")
	before := gitTest(t, root, "status", "--porcelain")
	args := []string{"merge-check", "--base", "HEAD", "--branches", "HEAD", "--verify", "--allow-execution", "--", "python3", "-m", "unittest", "test_real"}
	r := invoke(t, root, 0, args...)
	if r["execution"].(map[string]any)["status"] != "passed" {
		t.Fatal(r)
	}
	if _, err := os.Stat(filepath.Join(root, ".radar")); !os.IsNotExist(err) {
		t.Fatal("preview wrote state", err)
	}
	if gitTest(t, root, "status", "--porcelain") != before {
		t.Fatal("source worktree changed")
	}
	args = append([]string{"merge-check", "--evidence-output", ".radar/evidence/candidate.json"}, args[1:]...)
	invoke(t, root, 0, args...)
	if _, err := os.Stat(filepath.Join(root, ".radar/evidence/candidate.json")); err != nil {
		t.Fatal(err)
	}
	invoke(t, root, 2, args...)
	invoke(t, root, 2, "merge-check", "--base", "HEAD", "--branches", "HEAD", "--evidence-output", ".git/new-object")
}
