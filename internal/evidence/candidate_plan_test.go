package evidence

import (
	"context"
	"encoding/json"
	"github.com/Jake-Network/radar/internal/model"
	"github.com/Jake-Network/radar/internal/planning"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func candidateCheckpoint(t *testing.T, root string) CandidateCheckpoint {
	t.Helper()
	identity, e := RepositoryIdentity(context.Background(), root)
	if e != nil {
		t.Fatal(e)
	}
	sha := git(t, root, "rev-parse", "HEAD")
	return CandidateCheckpoint{Repository: identity, Base: sha, Revision: sha, Tree: git(t, root, "rev-parse", "HEAD^{tree}"), Inputs: []string{sha}}
}
func reviewedPlan(root string, c CandidateCheckpoint, argv []string, cwd string) planning.Plan {
	p := plan(argv)
	p.BaseRevision = c.Base
	p.Acceptance[0].Rule.CWD = cwd
	p.Approval = &planning.Approval{Reviewer: "human-reviewer", ReviewedAt: "2026-10-09T00:00:00Z", Checkpoint: p.BaseRevision, PlanDigest: planning.Digest(p)}
	return p
}
func review(p *planning.Plan) {
	p.Approval = &planning.Approval{Reviewer: "human-reviewer", ReviewedAt: "2026-10-09T00:00:00Z", Checkpoint: p.BaseRevision, PlanDigest: planning.Digest(*p)}
}
func TestCandidateReviewedEvidenceAndBindingMismatches(t *testing.T) {
	needPython(t)
	root := fixture(t, map[string]string{"test_example.py": pythonTest})
	c := candidateCheckpoint(t, root)
	argv := []string{"python3", "-m", "unittest", "test_example"}
	p := reviewedPlan(root, c, argv, ".")
	run, e := RunCandidate(context.Background(), root, c, p, argv, ".", 10*time.Second)
	if e != nil {
		t.Fatal(e)
	}
	if run.Record.Status != model.StatusPassed || run.Record.TestsRun != 1 || !run.Record.Candidate.SourceUnchanged {
		t.Fatalf("%+v", run)
	}
	if e = ValidateCandidate(run.Record, root, c, p, "criterion", argv); e != nil {
		t.Fatal(e)
	}
	for _, change := range []func(*CandidateCheckpoint){func(x *CandidateCheckpoint) { x.Tree = strings.Repeat("a", 40) }, func(x *CandidateCheckpoint) { x.Base = strings.Repeat("b", 40) }, func(x *CandidateCheckpoint) { x.Inputs = []string{strings.Repeat("c", 40)} }} {
		other := c
		change(&other)
		if ValidateCandidate(run.Record, root, other, p, "criterion", argv) == nil {
			t.Fatal("checkpoint mismatch accepted")
		}
	}
	other := p
	approval := *p.Approval
	approval.Reviewer = "different-reviewer"
	other.Approval = &approval
	if ValidateCandidate(run.Record, root, c, other, "criterion", argv) == nil {
		t.Fatal("changed review declaration accepted")
	}
	if ValidateCandidate(run.Record, root, c, p, "criterion", []string{"python3", "-m", "unittest", "discover"}) == nil {
		t.Fatal("different command accepted")
	}
	if _, e = RunCandidate(context.Background(), root, c, p, argv, "../outside", 10*time.Second); e == nil {
		t.Fatal("escaping cwd accepted")
	}
	stale := p
	stale.Intent = "different feature"
	if MatchesCandidateCommand(stale, argv, ".") {
		t.Fatal("stale approval accepted")
	}
	legacy, e := Run(context.Background(), root, c.Revision, p, argv, 10*time.Second)
	if e != nil {
		t.Fatal(e)
	}
	if ValidateCandidate(legacy, root, c, p, "criterion", argv) == nil {
		t.Fatal("branch evidence promoted")
	}
	badEnv := run.Record
	badEnv.Env = []string{"EXTRA_ENV"}
	badEnv.IntegrityDigest = integrity(badEnv)
	badEnv.ID = "evidence:" + badEnv.IntegrityDigest
	if ValidateCandidate(badEnv, root, c, p, "criterion", argv) == nil {
		t.Fatal("different environment declarations accepted")
	}
	invalid := InvalidateCandidateRecord(run.Record)
	if Validate(invalid, root, c.Revision, p, "criterion", argv) == nil {
		t.Fatal("legacy validation accepted source-invalid candidate record")
	}
	if Validate(run.Record, root, c.Revision, other, "criterion", argv) == nil {
		t.Fatal("legacy validation accepted changed review declaration")
	}
	if ValidateCandidate(invalid, root, c, p, "criterion", argv) == nil {
		t.Fatal("revoked record accepted")
	}
}

func TestCandidateSetupEnvCWDAndFreshJUnit(t *testing.T) {
	needPython(t)
	root := fixture(t, map[string]string{"services/api/runner.py": "import os\nfrom pathlib import Path\nassert os.environ['RADAR_SETTING'] == 'secret-value-not-in-record'\nassert Path('build/ready').exists()\nprint(os.environ['RADAR_SETTING'])\nPath('results.xml').write_text('<testsuite><testcase name=\"one\"/><testcase name=\"two\"/></testsuite>')\n"})
	c := candidateCheckpoint(t, root)
	argv := []string{"python3", "runner.py"}
	p := reviewedPlan(root, c, argv, "services/api")
	p.Acceptance[0].Rule.Env = []string{"RADAR_SETTING"}
	p.Acceptance[0].Rule.JUnit = "results.xml"
	p.Acceptance[0].Rule.Setup = [][]string{{"python3", "-c", "from pathlib import Path; Path('build').mkdir(); Path('build/ready').write_text('ready')"}}
	review(&p)
	t.Setenv("RADAR_SETTING", "secret-value-not-in-record")
	run, e := RunCandidate(context.Background(), root, c, p, argv, "services/api", 10*time.Second)
	if e != nil {
		t.Fatal(e)
	}
	if run.Record.Status != model.StatusPassed || run.Record.TestsRun != 2 || run.Record.Harness != "junit" {
		t.Fatalf("%+v", run)
	}
	encoded, _ := json.Marshal(run)
	if strings.Contains(string(encoded), "secret-value-not-in-record") {
		t.Fatal("environment value leaked")
	}
	if e = ValidateCandidate(run.Record, root, c, p, "criterion", argv); e != nil {
		t.Fatal(e)
	}
	if _, e = RunCandidate(context.Background(), root, c, p, argv, "services/api", 10*time.Second); e == nil || !strings.Contains(e.Error(), "stale JUnit") {
		t.Fatalf("stale report accepted: %v", e)
	}
}

func TestCandidateSetupCannotSupplyStaleJUnitAndLinksRejected(t *testing.T) {
	needPython(t)
	root := fixture(t, map[string]string{"test_example.py": pythonTest})
	c := candidateCheckpoint(t, root)
	argv := []string{"python3", "-c", "pass"}
	p := reviewedPlan(root, c, argv, ".")
	p.Acceptance[0].Rule.JUnit = "results.xml"
	p.Acceptance[0].Rule.Setup = [][]string{{"python3", "-c", "from pathlib import Path; Path('results.xml').write_text('<testsuite><testcase name=\"stale\"/></testsuite>')"}}
	review(&p)
	run, e := RunCandidate(context.Background(), root, c, p, argv, ".", 10*time.Second)
	if e != nil {
		t.Fatal(e)
	}
	if run.Record.Status != model.StatusError || run.Record.TestsRun != 0 {
		t.Fatal("setup report became passing command evidence")
	}
	_ = os.Remove(filepath.Join(root, "results.xml"))
	p.Acceptance[0].Rule.JUnit = ""
	p.Acceptance[0].Rule.Setup = nil
	p.Acceptance[0].Rule.Link = []string{"node_modules"}
	review(&p)
	if _, e = RunCandidate(context.Background(), root, c, p, argv, ".", 10*time.Second); e == nil || !strings.Contains(e.Error(), "links are unsupported") {
		t.Fatalf("candidate link accepted: %v", e)
	}
}

func TestCandidateNoTestsTimeoutAndSourceMutation(t *testing.T) {
	needPython(t)
	for _, test := range []struct {
		name, code string
		want       model.Status
		timeout    time.Duration
	}{{"zero", "", model.StatusUnknown, 10 * time.Second}, {"timeout", "import time; time.sleep(2)\n", model.StatusTimeout, 100 * time.Millisecond}, {"mutation", "import unittest\nfrom pathlib import Path\nclass Test(unittest.TestCase):\n def test_mutate(self):\n  Path('tracked.txt').write_text('modified')\n", model.StatusUnknown, 10 * time.Second}} {
		t.Run(test.name, func(t *testing.T) {
			root := fixture(t, map[string]string{"test_case.py": test.code, "tracked.txt": "original"})
			c := candidateCheckpoint(t, root)
			argv := []string{"python3", "-m", "unittest", "test_case"}
			p := reviewedPlan(root, c, argv, ".")
			run, e := RunCandidate(context.Background(), root, c, p, argv, ".", test.timeout)
			if e != nil {
				t.Fatal(e)
			}
			if run.Record.Status != test.want {
				t.Fatalf("%+v", run.Record)
			}
			if test.name == "mutation" && ValidateCandidate(run.Record, root, c, p, "criterion", argv) == nil {
				t.Fatal("mutated source accepted")
			}
		})
	}
}

func TestCandidateSourceGuardModesSymlinksAndLongInventories(t *testing.T) {
	root := fixture(t, map[string]string{"source/file.py": "VALUE = 1\n"})
	c := candidateCheckpoint(t, root)
	if runtime.GOOS != "windows" {
		if e := os.Chmod(filepath.Join(root, "source/file.py"), 0600); e != nil {
			t.Fatal(e)
		}
		state, e := InspectCandidateSource(context.Background(), root, c, nil)
		if e != nil {
			t.Fatal(e)
		}
		if state.Matches {
			t.Fatal("executable mode mutation hidden")
		}
		_ = os.Chmod(filepath.Join(root, "source/file.py"), 0700)
	}
	if e := os.Rename(filepath.Join(root, "source"), filepath.Join(root, "backup")); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink("backup", filepath.Join(root, "source")); e == nil {
		state, e := InspectCandidateSource(context.Background(), root, c, nil)
		if e != nil {
			t.Fatal(e)
		}
		if state.Matches {
			t.Fatal("internal parent symlink hidden")
		}
		_ = os.Remove(filepath.Join(root, "source"))
	}
	_ = os.Rename(filepath.Join(root, "backup"), filepath.Join(root, "source"))
	if e := os.Mkdir(filepath.Join(root, "zz_inputs"), 0700); e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 300; i++ {
		name := filepath.Join(root, "zz_inputs", strings.Repeat("longname", 10)+string(rune(0x100+i)))
		if e := os.WriteFile(name, []byte("x"), 0600); e != nil {
			t.Fatal(e)
		}
	}
	state, e := InspectCandidateSource(context.Background(), root, c, nil)
	if e != nil {
		t.Fatal(e)
	}
	if state.Matches {
		t.Fatal("long untracked inventory hidden")
	}
}

func TestCandidateDeclarationUsesCWDAndRejectsConflicts(t *testing.T) {
	needPython(t)
	root := fixture(t, map[string]string{"backend/test_example.py": pythonTest, "frontend/test_example.py": pythonTest})
	c := candidateCheckpoint(t, root)
	argv := []string{"python3", "-m", "unittest", "test_example"}
	p := reviewedPlan(root, c, argv, "backend")
	p.Acceptance = append(p.Acceptance, planning.Criterion{ID: "frontend", Intent: "frontend tests", Rule: &planning.Rule{Kind: "test_run", Command: argv, CWD: "frontend"}})
	review(&p)
	for _, cwd := range []string{"backend", "frontend"} {
		run, e := RunCandidate(context.Background(), root, c, p, argv, cwd, 10*time.Second)
		if e != nil {
			t.Fatal(e)
		}
		if len(run.Record.Criteria) != 1 || run.Record.Status != model.StatusPassed {
			t.Fatalf("wrong criterion: %+v", run)
		}
		if e = ValidateCandidate(run.Record, root, c, p, run.Record.Criteria[0], argv); e != nil {
			t.Fatal(e)
		}
	}
	p.Acceptance = append(p.Acceptance, planning.Criterion{ID: "conflicting", Intent: "bad duplicate", Rule: &planning.Rule{Kind: "test_run", Command: argv, CWD: "backend", Env: []string{"MISSING_ENV"}}})
	review(&p)
	match, e := CandidateDeclaration(p, argv, "backend")
	if !match || e == nil {
		t.Fatal("conflicting reviewed configs became generic")
	}
}

func TestCandidateDeclaredMissingEnvironmentIsError(t *testing.T) {
	needPython(t)
	root := fixture(t, map[string]string{"test_example.py": pythonTest})
	c := candidateCheckpoint(t, root)
	argv := []string{"python3", "-m", "unittest", "test_example"}
	p := reviewedPlan(root, c, argv, ".")
	p.Acceptance[0].Rule.Env = []string{"RADAR_TEST_ABSENT_DECLARED_ENV"}
	review(&p)
	_ = os.Unsetenv("RADAR_TEST_ABSENT_DECLARED_ENV")
	if _, e := RunCandidate(context.Background(), root, c, p, argv, ".", time.Second); e == nil || !strings.Contains(e.Error(), "RADAR_TEST_ABSENT_DECLARED_ENV") {
		t.Fatalf("missing env did not reject execution: %v", e)
	}
}

func TestCandidateTimeoutRetainedWhenJUnitAbsent(t *testing.T) {
	needPython(t)
	root := fixture(t, map[string]string{"runner.py": "import time; time.sleep(3)\n"})
	c := candidateCheckpoint(t, root)
	argv := []string{"python3", "runner.py"}
	p := reviewedPlan(root, c, argv, ".")
	p.Acceptance[0].Rule.JUnit = "results.xml"
	review(&p)
	run, e := RunCandidate(context.Background(), root, c, p, argv, ".", 100*time.Millisecond)
	if e != nil {
		t.Fatal(e)
	}
	if run.Record.Status != model.StatusTimeout || run.Record.TestsRun != 0 {
		t.Fatalf("missing report concealed timeout: %+v", run.Record)
	}
}

func TestCandidateEquivalentEmptyDeclarationsAndFailureLocations(t *testing.T) {
	needPython(t)
	root := fixture(t, map[string]string{"test_example.py": "import unittest\nclass Checks(unittest.TestCase):\n def test_budget(self): self.assertEqual(1,2)\n"})
	c := candidateCheckpoint(t, root)
	argv := []string{"python3", "-m", "unittest", "test_example"}
	p := reviewedPlan(root, c, argv, "")
	p.Acceptance = append(p.Acceptance, planning.Criterion{ID: "equivalent", Intent: "same command", Rule: &planning.Rule{Kind: "test_run", Command: argv, CWD: ".", Env: []string{}, Setup: [][]string{}, Link: []string{}}})
	review(&p)
	run, e := RunCandidate(context.Background(), root, c, p, argv, ".", 10*time.Second)
	if e != nil {
		t.Fatal(e)
	}
	if run.Record.Status != model.StatusFailed || len(run.Record.Criteria) != 2 || len(run.Observation.FailedCases) != 1 || len(run.Observation.Locations) != 1 {
		t.Fatalf("failure repair evidence lost: %+v", run)
	}
	if run.Observation.Locations[0].Path != "test_example.py" {
		t.Fatal("failure location incorrect")
	}
}

func TestCandidateJUnitDirectoryDoesNotExemptSourceInputs(t *testing.T) {
	needPython(t)
	for _, extra := range []string{"none", "source", "nested", "symlink"} {
		t.Run(extra, func(t *testing.T) {
			root := fixture(t, map[string]string{"README": "fixture"})
			c := candidateCheckpoint(t, root)
			script := "from pathlib import Path; Path('reports').mkdir(); "
			switch extra {
			case "source":
				script += "Path('reports/new_module.py').write_text('VALUE = 42'); import reports.new_module; assert reports.new_module.VALUE == 42; "
			case "nested":
				script += "Path('reports/nested').mkdir(); Path('reports/nested/TEST-ignored.xml').write_text('<testsuite><testcase/></testsuite>'); "
			case "symlink":
				script += "Path('reports/TEST-link.xml').symlink_to('../README'); "
			}
			script += "Path('reports/TEST-ok.xml').write_text('<testsuite><testcase name=\"ok\"/></testsuite>')"
			argv := []string{"python3", "-c", script}
			p := reviewedPlan(root, c, argv, ".")
			p.Acceptance[0].Rule.JUnit = "reports"
			review(&p)
			r, e := RunCandidate(context.Background(), root, c, p, argv, ".", 10*time.Second)
			if e != nil {
				t.Fatal(e)
			}
			if extra == "none" {
				if r.Record.Status != model.StatusPassed || !r.Record.Candidate.SourceUnchanged {
					t.Fatalf("fresh directory report rejected: %+v", r.Record)
				}
				if e := ValidateCandidate(r.Record, root, c, p, "criterion", argv); e != nil {
					t.Fatal(e)
				}
			} else {
				if r.Record.Status != model.StatusUnknown || r.Record.Candidate.SourceUnchanged {
					t.Fatalf("untracked input certified as candidate evidence: %+v", r.Record)
				}
				if e := ValidateCandidate(r.Record, root, c, p, "criterion", argv); e == nil {
					t.Fatal("mutated candidate evidence accepted")
				}
			}
		})
	}
}
