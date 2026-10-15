package integration

import (
	"context"
	"fmt"
	"github.com/Jake-Network/radar/internal/evidence"
	"github.com/Jake-Network/radar/internal/runners"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Jake-Network/radar/internal/gate"
	"github.com/Jake-Network/radar/internal/model"
)

func TestAutomaticBaseComparison(t *testing.T) {
	for _, tc := range []struct {
		name              string
		baseFail, newFail bool
		budget            int
		verdict           gate.Verdict
		allExisting       bool
		existing, fresh   int
	}{
		{"base failure", true, false, 16, gate.Blocked, true, 1, 0},
		{"candidate failure", false, true, 16, gate.Fail, false, 0, 1},
		{"mixed failures", true, true, 16, gate.Fail, false, 1, 1},
		{"no budget", true, false, 1, gate.Fail, false, 0, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := fixture(t)
			gitTest(t, root, "checkout", "baseline")
			put(t, root, "state.py", "EXISTING = "+map[bool]string{true: "False", false: "True"}[tc.baseFail]+"\nNEW = True\n")
			put(t, root, "test_base.py", "import unittest\nimport state\nclass Baseline(unittest.TestCase):\n def test_existing(self):\n  self.assertTrue(state.EXISTING)\n def test_new(self):\n  self.assertTrue(state.NEW)\n")
			gitTest(t, root, "add", ".")
			gitTest(t, root, "commit", "-qm", "baseline tests")
			gitTest(t, root, "checkout", "-qb", "change")
			if tc.newFail {
				put(t, root, "state.py", "EXISTING = "+map[bool]string{true: "False", false: "True"}[tc.baseFail]+"\nNEW = False\n")
			} else {
				put(t, root, "note.txt", "branch\n")
			}
			gitTest(t, root, "add", ".")
			gitTest(t, root, "commit", "-qm", "candidate")
			o := opts("change")
			o.Command = []string{"python3", "-m", "unittest", "test_base"}
			o.MaxCommands = tc.budget
			o.Timeout = 20 * time.Second
			r, err := Preview(context.Background(), root, o)
			if err != nil {
				t.Fatal(err)
			}
			if r.Gate.Verdict != tc.verdict {
				t.Fatalf("gate=%+v findings=%+v", r.Gate, r.Findings)
			}
			ev := r.Executions[0]
			b := ev.BaseComparison
			if b == nil || b.AllExisting != tc.allExisting || len(b.ExistingCases) != tc.existing || len(ev.NewFailedCases()) != tc.fresh {
				t.Fatalf("execution=%+v baseline=%+v", ev, b)
			}
			if ev.Status != model.StatusFailed || ev.Observation.Status != model.StatusFailed {
				t.Fatal("candidate failure observation was overwritten")
			}
			if tc.budget == 1 {
				if b.Execution != nil || !strings.Contains(b.Explanation, "budget exhausted") {
					t.Fatalf("%+v", b)
				}
			} else if b.Execution == nil || b.Execution.CandidateCommit != r.Base || b.Execution.SourceAfterExecution != "unchanged" {
				t.Fatalf("unbound base: %+v", b)
			}
		})
	}
}

func TestBaseComparisonRejectsMutatedSource(t *testing.T) {
	root := fixture(t)
	gitTest(t, root, "checkout", "baseline")
	put(t, root, "test_mutating.py", "import unittest\nfrom pathlib import Path\nclass Bad(unittest.TestCase):\n def test_bad(self):\n  Path('backend.py').write_text('changed source')\n  self.fail('existing failure')\n")
	gitTest(t, root, "add", ".")
	gitTest(t, root, "commit", "-qm", "mutating test")
	gitTest(t, root, "checkout", "-qb", "change")
	put(t, root, "note.txt", "change")
	gitTest(t, root, "add", ".")
	gitTest(t, root, "commit", "-qm", "candidate")
	o := opts("change")
	o.Command = []string{"python3", "-m", "unittest", "test_mutating"}
	r, err := Preview(context.Background(), root, o)
	if err != nil {
		t.Fatal(err)
	}
	if r.Gate.Verdict != gate.Fail || r.Executions[0].BaseComparison.AllExisting || r.Executions[0].BaseComparison.Execution != nil {
		t.Fatalf("%+v", r.Executions)
	}
}

func TestIncompleteFailureIdentitiesCannotClearCandidateFailure(t *testing.T) {
	root := fixture(t)
	gitTest(t, root, "checkout", "baseline")
	content := "import unittest\nfrom backend import UNIT_PRICE\nclass Cases(unittest.TestCase):\n"
	for i := 0; i < 20; i++ {
		content += fmt.Sprintf(" def test_%02d(self): self.fail('existing')\n", i)
	}
	content += " def test_zz_new(self): self.assertEqual(UNIT_PRICE, 1)\n"
	put(t, root, "test_many.py", content)
	gitTest(t, root, "add", ".")
	gitTest(t, root, "commit", "-qm", "existing failures")
	gitTest(t, root, "checkout", "-qb", "change")
	put(t, root, "backend.py", "UNIT_PRICE = 2\n")
	gitTest(t, root, "commit", "-qam", "new failure")
	o := opts("change")
	o.Command = []string{"python3", "-m", "unittest", "test_many"}
	r, err := Preview(context.Background(), root, o)
	if err != nil {
		t.Fatal(err)
	}
	ev := r.Executions[0]
	if r.Gate.Verdict != gate.Fail || ev.BaseComparison.AllExisting || ev.Observation.TestsFailed != 21 || !strings.Contains(ev.BaseComparison.Explanation, "lack recorded identities") {
		t.Fatalf("gate=%+v ev=%+v baseline=%+v", r.Gate, ev, ev.BaseComparison)
	}
	if len(ev.BaseComparison.ExistingCases) != 0 || len(ev.NewFailedCases()) != 20 {
		t.Fatalf("unknown 21st failure was hidden by partial matching: existing=%v new=%v", ev.BaseComparison.ExistingCases, ev.NewFailedCases())
	}
	paths := []string{"test_many.py", "backend.py"}
	if got := ev.FailurePaths(paths); !slices.Equal(got, paths) {
		t.Fatalf("unknown failure lost potential leads: %v", got)
	}
}

func TestFailurePathsRequireExactCaseModule(t *testing.T) {
	ev := ExecutionEvidence{CWD: ".", Observation: evidence.CandidateObservation{FailedCases: []string{"pkg.test_old.Case.test_old", "other.test_api.Case.test_new", "tests/test_foobar.py::test_new"}}, BaseComparison: &BaseComparison{ExistingCases: []string{"pkg.test_old.Case.test_old"}}}
	paths := []string{"test_api.py", "test_foo.py", "tests/test_foo.py", "other/test_api.py", "tests/test_foobar.py"}
	if got := ev.FailurePaths(paths); !slices.Equal(got, []string{"other/test_api.py", "tests/test_foobar.py"}) {
		t.Fatalf("unrelated suffix/prefix files became leads: %v", got)
	}
	ev.CWD = "other"
	ev.Observation.FailedCases = []string{"test_api.Case.test_new"}
	if got := ev.FailurePaths([]string{"test_api.py", "other/test_api.py", "other/test_api/extra.py"}); !slices.Equal(got, []string{"other/test_api.py"}) {
		t.Fatalf("CWD module match=%v", got)
	}
	ev.CWD = "."
	ev.Command = []string{"python3", "-m", "unittest", "discover", "-s", "other"}
	if got := ev.FailurePaths([]string{"test_api.py", "other/test_api.py"}); !slices.Equal(got, []string{"other/test_api.py"}) {
		t.Fatalf("discover module match=%v", got)
	}
}

func TestBaseComparisonsDoNotConsumeCandidateCommandBudget(t *testing.T) {
	root := t.TempDir()
	gitTest(t, root, "init", "-q")
	for _, name := range []string{"a", "b"} {
		put(t, root, "source_"+name+".py", "VALUE = 1\n")
		put(t, root, "test_"+name+".py", "import unittest\nfrom source_"+name+" import VALUE\nclass Cases(unittest.TestCase):\n def test_failure(self): self.assertEqual(VALUE, 0)\n")
	}
	gitTest(t, root, "add", ".")
	gitTest(t, root, "commit", "-qm", "base")
	gitTest(t, root, "branch", "baseline")
	gitTest(t, root, "checkout", "-qb", "change")
	for _, name := range []string{"a", "b"} {
		put(t, root, "source_"+name+".py", "VALUE = 2\n")
	}
	gitTest(t, root, "commit", "-qam", "changes")
	o := opts("change")
	o.Command = nil
	o.Suite = "full"
	o.MaxCommands = 2
	r, err := Preview(context.Background(), root, o)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Executions) != 2 || len(r.Selection.Omitted) != 0 {
		t.Fatalf("baseline reruns displaced selected candidate commands: executions=%d selection=%+v", len(r.Executions), r.Selection)
	}
	for _, ev := range r.Executions {
		if ev.BaseComparison == nil || ev.BaseComparison.Execution != nil || !strings.Contains(ev.BaseComparison.Explanation, "budget exhausted") {
			t.Fatalf("%+v", ev.BaseComparison)
		}
	}
}

func TestBaseVerifierReusesCheckoutAndPreparation(t *testing.T) {
	root := fixture(t)
	base := gitTest(t, root, "rev-parse", "baseline")
	r := Report{Base: base}
	ev := ExecutionEvidence{Status: model.StatusFailed, SourceAfterExecution: "unchanged", Observation: evidence.CandidateObservation{Status: model.StatusFailed, TestsFailed: 1, FailedCases: []string{"test_failure.Cases.test_failure"}}}
	// Add a failing test to the baseline, and count declared setup outside the
	// private source tree so reuse is directly observable.
	gitTest(t, root, "checkout", "baseline")
	put(t, root, "test_failure.py", "import unittest\nfrom pathlib import Path\nclass Cases(unittest.TestCase):\n def test_failure(self):\n  Path('report.xml').write_text('<testsuite tests=\"1\" failures=\"1\"><testcase name=\"test_failure\"><failure/></testcase></testsuite>')\n  self.fail('existing')\n")
	gitTest(t, root, "add", ".")
	gitTest(t, root, "commit", "-qm", "existing failure")
	r.Base = gitTest(t, root, "rev-parse", "HEAD")
	setupLog := filepath.Join(t.TempDir(), "setup-count")
	t.Setenv("RADAR_STATE_DIR", t.TempDir())
	profile := &runners.Profile{Root: ".", Prefix: []string{"python3"}, Setup: [][]string{{"python3", "-c", fmt.Sprintf("from pathlib import Path; p=Path(%q); p.write_text(p.read_text()+'run\\n' if p.exists() else 'run\\n')", setupLog)}}}
	o := opts()
	o.Command = []string{"python3", "-m", "unittest", "test_failure"}
	o.CWD = "."
	o.report = "report.xml"
	o.budget = &commandBudget{left: 3}
	v := baseVerifier{}
	defer v.close()
	first := v.compare(context.Background(), root, r, ev, o, profile, 1<<20)
	if first.Execution == nil || !first.AllExisting {
		t.Fatalf("first=%+v", first)
	}
	candidate := v.candidate
	second := v.compare(context.Background(), root, r, ev, o, profile, 1<<20)
	if second.Execution == nil || !second.AllExisting || v.candidate != candidate {
		t.Fatalf("second=%+v reused=%v", second, v.candidate == candidate)
	}
	data, err := os.ReadFile(setupLog)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "run\n" || o.budget.left != 0 {
		t.Fatalf("setup=%q remaining budget=%d", data, o.budget.left)
	}
}

func TestBaseVerifierRejectsLaterRunsAfterSourceMutation(t *testing.T) {
	root := fixture(t)
	gitTest(t, root, "checkout", "baseline")
	put(t, root, "test_failure.py", "import unittest\nfrom pathlib import Path\nclass Cases(unittest.TestCase):\n def test_failure(self):\n  Path('backend.py').write_text('changed')\n  self.fail('existing')\n")
	gitTest(t, root, "add", ".")
	gitTest(t, root, "commit", "-qm", "mutating base failure")
	r := Report{Base: gitTest(t, root, "rev-parse", "HEAD")}
	ev := ExecutionEvidence{Status: model.StatusFailed, SourceAfterExecution: "unchanged"}
	o := opts()
	o.Command = []string{"python3", "-m", "unittest", "test_failure"}
	o.budget = &commandBudget{left: 2}
	v := baseVerifier{}
	defer v.close()
	first := v.compare(context.Background(), root, r, ev, o, nil, 1<<20)
	second := v.compare(context.Background(), root, r, ev, o, nil, 1<<20)
	if first.Execution == nil || first.Execution.SourceAfterExecution != "modified" || second.Execution != nil || o.budget.left != 1 {
		t.Fatalf("first=%+v second=%+v budget=%d", first, second, o.budget.left)
	}
}

func TestFailurePathsPytestIDsRespectCWD(t *testing.T) {
	for _, id := range []string{"tests/test_x.py::Case::test_new", "apps/svc/tests/test_x.py::Case::test_new"} {
		ev := ExecutionEvidence{CWD: "apps/svc", Observation: evidence.CandidateObservation{FailedCases: []string{id}}, BaseComparison: &BaseComparison{ExistingCases: []string{"tests/test_old.py::test_old"}}}
		paths := []string{"tests/test_x.py", "apps/svc/tests/test_x.py", "apps/svc2/tests/test_x.py", "apps/svc/tests/test_x_extra.py"}
		if got := ev.FailurePaths(paths); !slices.Equal(got, []string{"apps/svc/tests/test_x.py"}) {
			t.Fatalf("id=%s paths=%v", id, got)
		}
	}
}

func TestIncompleteFailureEvidenceCannotSuppressCasesOrPaths(t *testing.T) {
	for _, tc := range []struct {
		name    string
		failed  int
		cases   []string
		harness string
	}{
		{"missing identity", 2, []string{"tests.test_old.Cases.test_old"}, "unittest"},
		{"duplicate identity", 2, []string{"tests.test_old.Cases.test_old", "tests.test_old.Cases.test_old"}, "unittest"},
		{"pytest collection error", 1, []string{"tests/test_import.py"}, "pytest"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ev := ExecutionEvidence{Status: model.StatusFailed, Observation: evidence.CandidateObservation{TestsFailed: tc.failed, FailedCases: tc.cases, Harness: tc.harness}, BaseComparison: &BaseComparison{ExistingCases: append([]string{}, tc.cases...), AllExisting: true}}
			paths := []string{"tests/test_old.py", "tests/test_new.py", "app/new_import.py"}
			wantCases := tc.cases[:1]
			if tc.harness == "pytest" {
				wantCases = nil
			}
			if got := ev.NewFailedCases(); !slices.Equal(got, wantCases) {
				t.Fatalf("unidentified failure lost reliable cases or counted invalid cases: %v", got)
			}
			if got := ev.FailurePaths(paths); !slices.Equal(got, paths) {
				t.Fatalf("unidentified failure lost potential paths: %v", got)
			}
			if ev.IntegrationStatus() != model.StatusFailed || ev.UnidentifiedFailureCount() != 1 {
				t.Fatalf("partial identities downgraded failure or unknown count incorrect: status=%s unknown=%d", ev.IntegrationStatus(), ev.UnidentifiedFailureCount())
			}
		})
	}
}

func TestKnownBaseFailureAndUnidentifiedNewSubtestRemainFailures(t *testing.T) {
	root := fixture(t)
	gitTest(t, root, "checkout", "baseline")
	put(t, root, "test_subtests.py", "import unittest\nfrom backend import UNIT_PRICE\nclass Cases(unittest.TestCase):\n def test_a_existing(self): self.fail('existing')\n def test_z_new(self):\n  with self.subTest(price=UNIT_PRICE):\n   self.assertEqual(UNIT_PRICE, 1)\n")
	gitTest(t, root, "add", ".")
	gitTest(t, root, "commit", "-qm", "existing baseline failure")
	gitTest(t, root, "checkout", "-qb", "new-subtest-failure")
	put(t, root, "backend.py", "UNIT_PRICE = 2\n")
	gitTest(t, root, "commit", "-qam", "new subtest failure")
	o := opts("new-subtest-failure")
	o.Command = []string{"python3", "-m", "unittest", "test_subtests"}
	r, err := Preview(context.Background(), root, o)
	if err != nil {
		t.Fatal(err)
	}
	ev := r.Executions[0]
	if r.Gate.Verdict != gate.Fail || ev.BaseComparison == nil || ev.BaseComparison.AllExisting || len(ev.BaseComparison.ExistingCases) != 0 {
		t.Fatalf("gate=%+v ev=%+v baseline=%+v", r.Gate, ev, ev.BaseComparison)
	}
	if ev.Observation.TestsFailed != 2 || ev.UnidentifiedFailureCount() != 1 || len(ev.NewFailedCases()) != 1 {
		t.Fatalf("partial failure evidence was suppressed: %+v", ev)
	}
	paths := []string{"test_subtests.py", "backend.py"}
	if got := ev.FailurePaths(paths); !slices.Equal(got, paths) {
		t.Fatalf("unidentified candidate failure lost repair paths: %v", got)
	}
	found := false
	for _, f := range r.Findings {
		if f.Code == "integration_execution_failed" {
			found = true
			if len(f.Locations) == 0 {
				t.Fatal("unknown failure lost observed test locations")
			}
		}
	}
	if !found {
		t.Fatal("candidate failure finding missing")
	}
}
