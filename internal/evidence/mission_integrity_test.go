package evidence

import (
	"context"
	"testing"
	"time"

	"github.com/Jake-Network/radar/internal/model"
)

func TestCandidateMissingJUnitPreservesObservedFailure(t *testing.T) {
	needPython(t)
	for _, failing := range []bool{false, true} {
		name, source, want := "passing", pythonTest, model.StatusError
		if failing {
			name, source, want = "failing", "import unittest\nclass Example(unittest.TestCase):\n def test_failure(self): self.fail('regression')\n", model.StatusFailed
		}
		t.Run(name, func(t *testing.T) {
			root := fixture(t, map[string]string{"test_example.py": source})
			c := candidateCheckpoint(t, root)
			argv := []string{"python3", "-m", "unittest", "test_example"}
			p := reviewedPlan(root, c, argv, ".")
			p.Acceptance[0].Rule.JUnit = "results.xml"
			review(&p)
			run, err := RunCandidate(context.Background(), root, c, p, argv, ".", 10*time.Second)
			if err != nil {
				t.Fatal(err)
			}
			if run.Record.Status != want || run.Observation.Status != want || run.Record.TestsRun != 1 {
				t.Fatalf("missing report changed observed result: %+v", run)
			}
			if failing && run.Record.TestsFailed != 1 {
				t.Fatalf("failure counts erased: %+v", run.Record)
			}
		})
	}
}

func TestObservedFailureSurvivesLaterTimeout(t *testing.T) {
	if got := outcome(true, -1, harnessResult{Run: 1, Failed: 1}, false, []string{"node", "--test"}, nil); got != model.StatusFailed {
		t.Fatalf("observed failure became %s", got)
	}
	if got := outcome(true, -1, harnessResult{}, false, []string{"node", "--test"}, nil); got != model.StatusTimeout {
		t.Fatalf("unobserved timeout became %s", got)
	}
}
