package integration

import (
	"context"
	"github.com/Jake-Network/radar/internal/gate"
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

func reviewedIntegrationPlan(t *testing.T, root string, argv []string) planning.Plan {
	t.Helper()
	p := planning.Plan{SchemaVersion: "1", FeatureID: "checkout", Intent: "verify combined checkout", BaseRevision: gitTest(t, root, "rev-parse", "baseline"), Requirements: []planning.Requirement{{ID: "budget", Intent: "stay within budget"}}, Acceptance: []planning.Criterion{{ID: "budget-test", Requirement: "budget", Intent: "observe real checkout test", Rule: &planning.Rule{Kind: "test_run", Command: argv}}}, Tasks: []planning.Task{{ID: "checkout-task", Intent: "adjust checkout", Requirements: []string{"budget"}, Components: []string{"file:backend.py", "file:frontend.ts"}, Acceptance: []string{"budget-test"}}}}
	p.Approval = &planning.Approval{Reviewer: "human-reviewer", ReviewedAt: "2026-10-09T00:00:00Z", Checkpoint: p.BaseRevision, PlanDigest: planning.Digest(p)}
	return p
}

func TestReviewedPlanCandidateCriteriaPassWithoutBranchEvidence(t *testing.T) {
	root := fixture(t)
	put(t, root, ".radar/contracts.json", `{"version":1,"bindings":[]}`)
	gitTest(t, root, "add", ".radar/contracts.json")
	gitTest(t, root, "commit", "-qm", "explicit empty contract scope")
	o := opts("backend", "repaired")
	p := reviewedIntegrationPlan(t, root, o.Command)
	o.Plan = &p
	o.Policy = &gate.Policy{Version: 1, Require: []string{"textual_merge", "integration_execution", "plan_verification", "criterion:budget-test"}}
	r, e := Preview(context.Background(), root, o)
	if e != nil {
		t.Fatal(e)
	}
	checkStatus(t, r, "criterion:budget-test", model.StatusPassed)
	checkStatus(t, r, "plan_verification", model.StatusPassed)
	if r.Gate.Verdict != gate.Pass || r.Execution.PlanRecord == nil || r.Execution.PlanRecord.Candidate == nil || !r.Plan.Authoritative {
		t.Fatalf("candidate not verified: %+v", r)
	}
	originalTree := r.CandidateTree
	o.Branches = []string{"backend", "frontend"}
	broken, brokenErr := Preview(context.Background(), root, o)
	if brokenErr != nil {
		t.Fatal(brokenErr)
	}
	checkStatus(t, broken, "criterion:budget-test", model.StatusFailed)
	if broken.Gate.Verdict != gate.Fail || broken.Execution.PlanRecord == nil || len(broken.Execution.Observation.FailedCases) == 0 || len(broken.Execution.Observation.Locations) == 0 || broken.CandidateTree == originalTree {
		t.Fatal("approved failure lacked candidate-bound repair evidence")
	}
	o.Branches = []string{"backend", "repaired"}
	o.Command = []string{"python3", "-m", "unittest", "-v", "integration_test"}
	r, e = Preview(context.Background(), root, o)
	if e != nil {
		t.Fatal(e)
	}
	checkStatus(t, r, "integration_execution", model.StatusPassed)
	checkStatus(t, r, "criterion:budget-test", model.StatusUnknown)
	if r.Execution.PlanRecord != nil || r.Gate.Verdict == gate.Pass {
		t.Fatal("mismatched command fabricated criterion pass")
	}
}

func TestReviewedPlanMissingEnvironmentAndConflictingDeclarationDoNotExecuteGeneric(t *testing.T) {
	root := fixture(t)
	o := opts("backend")
	p := reviewedIntegrationPlan(t, root, o.Command)
	p.Acceptance[0].Rule.Env = []string{"RADAR_CANDIDATE_REQUIRED_MISSING_ENV"}
	p.Approval.PlanDigest = planning.Digest(p)
	o.Plan = &p
	_ = os.Unsetenv("RADAR_CANDIDATE_REQUIRED_MISSING_ENV")
	r, e := Preview(context.Background(), root, o)
	if e != nil {
		t.Fatal(e)
	}
	checkStatus(t, r, "integration_execution", model.StatusError)
	checkStatus(t, r, "criterion:budget-test", model.StatusUnknown)
	p.Acceptance = append(p.Acceptance, planning.Criterion{ID: "conflict-test", Requirement: "budget", Intent: "conflicting execution declaration", Rule: &planning.Rule{Kind: "test_run", Command: o.Command}})
	p.Approval.PlanDigest = planning.Digest(p)
	r, e = Preview(context.Background(), root, o)
	if e != nil {
		t.Fatal(e)
	}
	checkStatus(t, r, "integration_execution", model.StatusError)
	if r.Execution.Observation.TestsRun > 0 {
		t.Fatal("conflict fell back to generic execution")
	}
}

func TestMissingAndMalformedContractsCannotPassDefaultIntegrationGate(t *testing.T) {
	for _, test := range []struct {
		name, manifest string
		want           gate.Verdict
	}{{"missing-schema", `{"version":1,"bindings":[{"id":"missing","schema":"missing.json","fields":["total"],"direction":"response"}]}`, gate.Blocked}, {"malformed", `{"version":`, gate.Error}} {
		t.Run(test.name, func(t *testing.T) {
			root := fixture(t)
			gitTest(t, root, "checkout", "baseline")
			put(t, root, ".radar/contracts.json", test.manifest)
			gitTest(t, root, "add", ".radar/contracts.json")
			gitTest(t, root, "commit", "-qm", "contract configuration")
			base := gitTest(t, root, "rev-parse", "HEAD")
			r, e := Preview(context.Background(), root, Options{Base: base, Branches: []string{base}})
			if e != nil {
				t.Fatal(e)
			}
			if r.Gate.Verdict != test.want {
				t.Fatalf("false gate %s: %+v", r.Gate.Verdict, r.Gate)
			}
		})
	}
}

func TestReviewedPlanMultiCWDAndLaterMutationInvalidatesSuite(t *testing.T) {
	if _, e := exec.LookPath("python3"); e != nil {
		t.Skip(e)
	}
	root := t.TempDir()
	gitTest(t, root, "init", "-q")
	put(t, root, ".radar/contracts.json", `{"version":1,"bindings":[]}`)
	pass := "import unittest\nclass Checks(unittest.TestCase):\n def test_invariant(self): self.assertEqual(1,1)\n"
	put(t, root, "backend/checks.py", pass)
	put(t, root, "frontend/checks.py", pass)
	gitTest(t, root, "add", ".")
	gitTest(t, root, "commit", "-qm", "base")
	gitTest(t, root, "branch", "baseline")
	argv := []string{"python3", "-m", "unittest", "checks"}
	p := reviewedIntegrationPlan(t, root, argv)
	p.Tasks[0].Components = []string{"file:backend/checks.py", "file:frontend/checks.py"}
	p.Tasks[0].Acceptance = []string{"backend-test", "frontend-test"}
	p.Acceptance = []planning.Criterion{{ID: "backend-test", Requirement: "budget", Intent: "backend verified", Rule: &planning.Rule{Kind: "test_run", Command: argv, CWD: "backend"}}, {ID: "frontend-test", Requirement: "budget", Intent: "frontend verified", Rule: &planning.Rule{Kind: "test_run", Command: argv, CWD: "frontend"}}}
	p.Approval.PlanDigest = planning.Digest(p)
	o := Options{Base: "baseline", Branches: []string{"baseline"}, Suite: "recommended", Verify: true, AllowExecution: true, Plan: &p, Timeout: 10 * time.Second, Policy: &gate.Policy{Version: 1, Require: []string{"integration_execution", "criterion:backend-test", "criterion:frontend-test"}}}
	r, e := Preview(context.Background(), root, o)
	if e != nil {
		t.Fatal(e)
	}
	checkStatus(t, r, "criterion:backend-test", model.StatusPassed)
	checkStatus(t, r, "criterion:frontend-test", model.StatusPassed)
	if r.Gate.Verdict != gate.Pass || len(r.Executions) != 2 {
		t.Fatalf("multi-cwd suite: %+v", r)
	}
	// Mutate whichever package's stable command ID sorts last, after the first
	// genuine passing observation. Selection order is derived, not hardcoded.
	later := r.Executions[1].CWD
	put(t, root, later+"/checks.py", "import unittest\nfrom pathlib import Path\nclass Checks(unittest.TestCase):\n def test_mutation(self): Path('checks.py').write_text('# rewritten source')\n")
	gitTest(t, root, "add", ".")
	gitTest(t, root, "commit", "-qm", "mutating suite test")
	gitTest(t, root, "branch", "-f", "baseline", "HEAD")
	p.BaseRevision = gitTest(t, root, "rev-parse", "baseline")
	p.Approval.Checkpoint = p.BaseRevision
	p.Approval.PlanDigest = planning.Digest(p)
	r, e = Preview(context.Background(), root, o)
	if e != nil {
		t.Fatal(e)
	}
	if len(r.Selected) != 2 || len(r.Executions) != 2 {
		t.Fatalf("selection/execution accounting: %+v", r)
	}
	if r.Gate.Verdict == gate.Pass {
		t.Fatal("mutating suite passed gate")
	}
	checkStatus(t, r, "criterion:backend-test", model.StatusUnknown)
	checkStatus(t, r, "criterion:frontend-test", model.StatusUnknown)
	for _, ev := range r.Executions {
		if ev.Status == model.StatusPassed || ev.PlanRecord == nil || ev.PlanRecord.Candidate.SourceUnchanged {
			t.Fatal("earlier candidate record survived later mutation")
		}
	}
}

func TestMalformedBaselineCannotBecomeNoBreakingPassAfterManifestRepair(t *testing.T) {
	root := fixture(t)
	put(t, root, ".radar/contracts.json", `{"version":`)
	gitTest(t, root, "add", ".radar/contracts.json")
	gitTest(t, root, "commit", "-qm", "malformed base")
	base := gitTest(t, root, "rev-parse", "HEAD")
	put(t, root, ".radar/contracts.json", `{"version":1,"bindings":[]}`)
	gitTest(t, root, "commit", "-qam", "valid current manifest")
	r, e := Preview(context.Background(), root, Options{Base: base, Branches: []string{"HEAD"}})
	if e != nil {
		t.Fatal(e)
	}
	if r.Gate.Verdict != gate.Error {
		t.Fatalf("malformed baseline passed gate: %+v", r.Gate)
	}
}

func TestProgressReportsStagesWithoutChangingTheReport(t *testing.T) {
	root := fixture(t)
	ctx := context.Background()
	quiet, e := Preview(ctx, root, opts("backend", "frontend"))
	if e != nil {
		t.Fatal(e)
	}
	steps := []Step{}
	o := opts("backend", "frontend")
	o.Progress = func(s Step) { steps = append(steps, s) }
	observed, e := Preview(ctx, root, o)
	if e != nil {
		t.Fatal(e)
	}
	stages := []string{}
	for _, s := range steps {
		stages = append(stages, s.Stage)
	}
	if strings.Join(stages, ",") != "combine,analyze,test" || steps[2].Index != 1 || steps[2].Total != 1 || steps[2].Command[0] != "python3" {
		t.Fatalf("steps=%+v", steps)
	}
	if quiet.CandidateTree != observed.CandidateTree || quiet.Status != observed.Status || len(quiet.Findings) != len(observed.Findings) {
		t.Fatalf("progress changed the report: %+v vs %+v", quiet, observed)
	}
}
