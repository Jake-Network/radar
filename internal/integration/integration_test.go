package integration

import (
	"context"
	"github.com/Jake-Network/radar/internal/model"
	"github.com/Jake-Network/radar/internal/planning"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func gitTest(t *testing.T, root string, args ...string) string {
	t.Helper()
	c := exec.Command("git", append([]string{"-C", root, "-c", "user.name=test", "-c", "user.email=test@example.invalid", "-c", "core.hooksPath=" + os.DevNull}, args...)...)
	out, e := c.CombinedOutput()
	if e != nil {
		t.Fatalf("git %v: %v %s", args, e, out)
	}
	return strings.TrimSpace(string(out))
}
func put(t *testing.T, root, path, content string) {
	t.Helper()
	if e := os.MkdirAll(filepath.Dir(filepath.Join(root, path)), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(root, path), []byte(content), 0600); e != nil {
		t.Fatal(e)
	}
}
func fixture(t *testing.T) string {
	t.Helper()
	if _, e := exec.LookPath("python3"); e != nil {
		t.Skip("python3 unavailable")
	}
	root := t.TempDir()
	gitTest(t, root, "init", "-q")
	put(t, root, "backend.py", "UNIT_PRICE = 1\n")
	put(t, root, "frontend.ts", "export const QUANTITY = 1;\n")
	put(t, root, "integration_test.py", "import re, unittest\nfrom pathlib import Path\nfrom backend import UNIT_PRICE\nclass CheckoutIntegration(unittest.TestCase):\n def test_combined_budget(self):\n  quantity = int(re.search(r'QUANTITY = (\\d+)', Path('frontend.ts').read_text())[1])\n  self.assertLessEqual(UNIT_PRICE * quantity, 2, 'combined checkout exceeds approved budget')\n")
	gitTest(t, root, "add", ".")
	gitTest(t, root, "commit", "-qm", "baseline")
	gitTest(t, root, "branch", "baseline")
	gitTest(t, root, "checkout", "-qb", "backend")
	put(t, root, "backend.py", "UNIT_PRICE = 2\n")
	gitTest(t, root, "commit", "-qam", "backend price")
	gitTest(t, root, "checkout", "-qb", "frontend", "baseline")
	put(t, root, "frontend.ts", "export const QUANTITY = 2;\n")
	gitTest(t, root, "commit", "-qam", "frontend quantity")
	gitTest(t, root, "checkout", "-qb", "repaired", "frontend")
	put(t, root, "frontend.ts", "export const QUANTITY = 1;\n")
	gitTest(t, root, "commit", "-qam", "respect integrated budget")
	gitTest(t, root, "checkout", "baseline")
	return root
}
func opts(branches ...string) Options {
	return Options{Base: "baseline", Branches: branches, Verify: true, AllowExecution: true, Command: []string{"python3", "-m", "unittest", "integration_test"}, Timeout: 10 * time.Second}
}
func checkStatus(t *testing.T, r Report, id string, want model.Status) {
	t.Helper()
	for _, c := range r.Checks {
		if c.ID == id {
			if c.Status != want {
				t.Fatalf("%s=%s want %s report=%+v", id, c.Status, want, r)
			}
			return
		}
	}
	t.Fatalf("missing check %s", id)
}
func TestIndependentPassingBranchesFailCombinedAndRepair(t *testing.T) {
	root := fixture(t)
	ctx := context.Background()
	put(t, root, "backend.py", "UNIT_PRICE = 999 # unrelated unstaged work\n")
	put(t, root, "user_notes.txt", "unrelated staged work\n")
	gitTest(t, root, "add", "user_notes.txt")
	before := gitTest(t, root, "status", "--porcelain")
	indexTree := gitTest(t, root, "write-tree")
	head := gitTest(t, root, "rev-parse", "HEAD")
	for _, branch := range []string{"backend", "frontend"} {
		r, e := Preview(ctx, root, opts(branch))
		if e != nil {
			t.Fatal(e)
		}
		checkStatus(t, r, "integration_execution", model.StatusPassed)
	}
	r, e := Preview(ctx, root, opts("backend", "frontend"))
	if e != nil {
		t.Fatal(e)
	}
	checkStatus(t, r, "textual_merge", model.StatusPassed)
	checkStatus(t, r, "integration_execution", model.StatusFailed)
	if r.Status != model.StatusFailed || r.Execution.CandidateTree == "" || len(r.Execution.Inputs) != 2 || r.Execution.SourceAfterExecution != "unchanged" {
		t.Fatalf("unbound evidence: %+v", r)
	}
	fixed, e := Preview(ctx, root, opts("backend", "repaired"))
	if e != nil {
		t.Fatal(e)
	}
	checkStatus(t, fixed, "integration_execution", model.StatusPassed)
	if fixed.Execution.CandidateTree == r.Execution.CandidateTree {
		t.Fatal("repaired state reused failing tree")
	}
	if gitTest(t, root, "status", "--porcelain") != before || gitTest(t, root, "rev-parse", "HEAD") != head {
		t.Fatal("source state mutated")
	}
	if gitTest(t, root, "write-tree") != indexTree {
		t.Fatal("source index changed")
	}
}
func TestConflictCleanupAndMissingTool(t *testing.T) {
	root := fixture(t)
	gitTest(t, root, "checkout", "-qb", "conflicting", "baseline")
	put(t, root, "backend.py", "UNIT_PRICE = 3\n")
	gitTest(t, root, "commit", "-qam", "conflict")
	gitTest(t, root, "checkout", "baseline")
	temp := t.TempDir()
	t.Setenv("TMPDIR", temp)
	r, e := Preview(context.Background(), root, Options{Base: "baseline", Branches: []string{"backend", "conflicting"}})
	if e != nil {
		t.Fatal(e)
	}
	if r.Status != model.StatusFailed || len(r.Conflicts) != 1 {
		t.Fatalf("%+v", r)
	}
	entries, e := os.ReadDir(temp)
	if e != nil || len(entries) != 0 {
		t.Fatalf("preview leaked: %v %v", entries, e)
	}
	o := opts("backend")
	o.Command = []string{"radar-nonexistent-verification-tool"}
	r, e = Preview(context.Background(), root, o)
	if e != nil {
		t.Fatal(e)
	}
	checkStatus(t, r, "integration_execution", model.StatusError)
	entries, _ = os.ReadDir(temp)
	if len(entries) != 0 {
		t.Fatal("failed execution leaked candidate")
	}
}
func TestAuthorizationAndUnknownEvidence(t *testing.T) {
	root := fixture(t)
	o := opts("backend")
	o.AllowExecution = false
	if _, e := Preview(context.Background(), root, o); e == nil {
		t.Fatal("execution accepted without opt-in")
	}
	r, e := Preview(context.Background(), root, Options{Base: "baseline", Branches: []string{"backend", "repaired"}})
	if e != nil {
		t.Fatal(e)
	}
	checkStatus(t, r, "integration_execution", model.StatusUnknown)
	if r.Status == model.StatusPassed {
		t.Fatal("unknown presented as comprehensive pass")
	}
}
func TestSymlinkRejected(t *testing.T) {
	root := fixture(t)
	if e := os.Symlink("/tmp", filepath.Join(root, "escape")); e != nil {
		t.Skip(e)
	}
	gitTest(t, root, "add", "escape")
	gitTest(t, root, "commit", "-qm", "unsafe tree")
	if _, e := Preview(context.Background(), root, Options{Base: "baseline", Branches: []string{"HEAD"}}); e == nil {
		t.Fatal("symlink accepted")
	}
}
func TestMutatingCommandCannotProveCandidate(t *testing.T) {
	root := fixture(t)
	o := opts("backend")
	o.Command = []string{"python3", "-c", "from pathlib import Path; Path('backend.py').write_text('UNIT_PRICE = 0\\n')"}
	r, e := Preview(context.Background(), root, o)
	if e != nil {
		t.Fatal(e)
	}
	checkStatus(t, r, "integration_execution", model.StatusUnknown)
	if r.Execution.SourceAfterExecution != "modified" {
		t.Fatal("candidate mutation hidden")
	}
}

func TestZeroTestsAndMissingDependencyAreNotPassing(t *testing.T) {
	root := fixture(t)
	put(t, root, "empty_test.py", "# No tests\n")
	put(t, root, "dependency_test.py", "import radar_nonexistent_library\n")
	gitTest(t, root, "add", ".")
	gitTest(t, root, "commit", "-qm", "missing verification inputs")
	o := opts("HEAD")
	o.Command = []string{"python3", "-m", "unittest", "empty_test"}
	r, e := Preview(context.Background(), root, o)
	if e != nil {
		t.Fatal(e)
	}
	checkStatus(t, r, "integration_execution", model.StatusUnknown)
	o.Command = []string{"python3", "-m", "unittest", "dependency_test"}
	r, e = Preview(context.Background(), root, o)
	if e != nil {
		t.Fatal(e)
	}
	checkStatus(t, r, "integration_execution", model.StatusError)
}

func TestVerificationTimeout(t *testing.T) {
	root := fixture(t)
	o := opts("backend")
	o.Timeout = 100 * time.Millisecond
	o.Command = []string{"python3", "-c", "import time; time.sleep(10)"}
	r, e := Preview(context.Background(), root, o)
	if e != nil {
		t.Fatal(e)
	}
	checkStatus(t, r, "integration_execution", model.StatusTimeout)
}

func TestIntegratedContractRemovalAndUnrelatedSchema(t *testing.T) {
	root := t.TempDir()
	gitTest(t, root, "init", "-q")
	put(t, root, "api.json", `{"type":"object","properties":{"total":{"type":"integer"},"note":{"type":"string"}}}`)
	put(t, root, "backend.py", "def response(): return {'total': 1}\n")
	put(t, root, "client.ts", "export const total = (r: {total: number}) => r.total;\n")
	put(t, root, ".radar/contracts.json", `{"version":1,"bindings":[{"id":"checkout","schema":"api.json","pointer":"","producer":"backend.py","consumer":"client.ts","fields":["total"],"direction":"response"}]}`)
	gitTest(t, root, "add", ".")
	gitTest(t, root, "commit", "-qm", "base")
	gitTest(t, root, "branch", "baseline")
	gitTest(t, root, "checkout", "-qb", "removed")
	put(t, root, "api.json", `{"type":"object","properties":{"note":{"type":"string"}}}`)
	gitTest(t, root, "commit", "-qam", "remove response total")
	gitTest(t, root, "checkout", "-qb", "unrelated", "baseline")
	put(t, root, "api.json", `{"type":"object","properties":{"total":{"type":"integer"},"note":{"type":"integer"}}}`)
	gitTest(t, root, "commit", "-qam", "change unused note")
	gitTest(t, root, "checkout", "-qb", "consumer", "baseline")
	put(t, root, "client.ts", "export const total = (r: {total: number}) => r.total * 2;\n")
	gitTest(t, root, "commit", "-qam", "consumer still uses total")
	r, e := Preview(context.Background(), root, Options{Base: "baseline", Branches: []string{"removed", "consumer"}})
	if e != nil {
		t.Fatal(e)
	}
	checkStatus(t, r, "textual_merge", model.StatusPassed)
	checkStatus(t, r, "declared_contracts", model.StatusFailed)
	found := false
	for _, f := range r.Findings {
		if f.Contract == "checkout" && f.Consumer == "client.ts" {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing concrete consumer: %+v", r.Findings)
	}
	r, e = Preview(context.Background(), root, Options{Base: "baseline", Branches: []string{"unrelated", "consumer"}})
	if e != nil {
		t.Fatal(e)
	}
	checkStatus(t, r, "declared_contracts", model.StatusPassed)
	if r.Status == model.StatusFailed {
		t.Fatalf("invented break: %+v", r.Findings)
	}
}

func TestCandidateTreeAndFindingStableAcrossIdenticalPreviews(t *testing.T) {
	root := fixture(t)
	o := opts("backend", "frontend")
	a, e := Preview(context.Background(), root, o)
	if e != nil {
		t.Fatal(e)
	}
	b, e := Preview(context.Background(), root, o)
	if e != nil {
		t.Fatal(e)
	}
	if a.CandidateCommit != b.CandidateCommit || a.CandidateTree != b.CandidateTree {
		t.Fatal("synthetic candidate identity changed")
	}
	if len(a.Findings) != len(b.Findings) {
		t.Fatal("findings changed")
	}
	for i := range a.Findings {
		if a.Findings[i].ID != b.Findings[i].ID {
			t.Fatal("identical feedback identity changed")
		}
	}
}

func TestPlanRulesEvaluatedWithoutFabricatingCriterionEvidence(t *testing.T) {
	root := fixture(t)
	o := opts("backend")
	p := planning.Plan{SchemaVersion: "1", FeatureID: "checkout", Intent: "integrated budget", BaseRevision: gitTest(t, root, "rev-parse", "baseline"), Requirements: []planning.Requirement{{ID: "budget", Intent: "stay within budget"}}, Acceptance: []planning.Criterion{{ID: "test-budget", Requirement: "budget", Intent: "verify budget", Rule: &planning.Rule{Kind: "test_run", Command: o.Command}}, {ID: "required-file", Requirement: "budget", Intent: "ensure integration artifact", Rule: &planning.Rule{Kind: "file_exists", Path: "missing_artifact.py"}}}, Tasks: []planning.Task{{ID: "backend-task", Intent: "backend", Requirements: []string{"budget"}, Components: []string{"file:backend.py"}, Acceptance: []string{"test-budget", "required-file"}}}}
	o.Plan = &p
	r, e := Preview(context.Background(), root, o)
	if e != nil {
		t.Fatal(e)
	}
	checkStatus(t, r, "integration_execution", model.StatusPassed)
	checkStatus(t, r, "plan_verification", model.StatusFailed)
	if r.Plan == nil || r.Execution.PlanDigest != planning.Digest(p) {
		t.Fatal("plan not digest-bound")
	}
	for _, c := range r.Plan.Checks {
		if c.ID == "test-budget" {
			if c.Status == model.StatusPassed {
				t.Fatal("generic execution fabricated plan evidence")
			}
			return
		}
	}
	t.Fatal("missing plan test criterion check")
}

func TestCommittedAndUntrackedMutationCannotProveOriginalCandidate(t *testing.T) {
	root := fixture(t)
	put(t, root, "mutation_test.py", "import unittest, subprocess\nfrom pathlib import Path\nclass Mutation(unittest.TestCase):\n def test_mutate(self):\n  Path('backend.py').write_text('UNIT_PRICE = 0\\n')\n  subprocess.run(['git','add','backend.py'],check=True)\n  subprocess.run(['git','-c','user.name=test','-c','user.email=test@example.invalid','-c','core.hooksPath=/dev/null','commit','-qm','mutation'],check=True)\n")
	put(t, root, "untracked_test.py", "import unittest\nfrom pathlib import Path\nclass Mutation(unittest.TestCase):\n def test_mutate(self):\n  Path('untracked_input.py').write_text('VALUE = 42\\n')\n")
	gitTest(t, root, "add", ".")
	gitTest(t, root, "commit", "-qm", "mutation fixtures")
	for _, module := range []string{"mutation_test", "untracked_test"} {
		o := opts("HEAD")
		o.Command = []string{"python3", "-m", "unittest", module}
		r, e := Preview(context.Background(), root, o)
		if e != nil {
			t.Fatal(e)
		}
		checkStatus(t, r, "integration_execution", model.StatusIncomplete)
		if r.Execution.SourceAfterExecution != "modified" {
			t.Fatal("clean git status hid candidate mutation")
		}
	}
}
