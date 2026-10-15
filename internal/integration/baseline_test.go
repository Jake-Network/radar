package integration

import (
	"context"
	"fmt"
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
}
