package integration

import (
	"context"
	"os/exec"
	"testing"
	"time"

	"github.com/Jake-Network/radar/internal/gate"
	"github.com/Jake-Network/radar/internal/model"
)

func TestPytestDifferentCollectionErrorsDoNotClearCandidateFailure(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("Python unavailable")
	}
	if err := exec.Command(python, "-c", "import pytest").Run(); err != nil {
		t.Skip("pytest unavailable")
	}
	root := fixture(t)
	gitTest(t, root, "checkout", "baseline")
	put(t, root, "tests/test_collection.py", "raise ValueError('existing collection error')\n")
	gitTest(t, root, "add", ".")
	gitTest(t, root, "commit", "-qm", "existing collection failure")
	gitTest(t, root, "checkout", "-qb", "collection-change")
	put(t, root, "tests/test_collection.py", "from backend import removed_name\n")
	gitTest(t, root, "commit", "-qam", "new import regression in same collector")
	o := opts("collection-change")
	o.Command = []string{"python3", "-m", "pytest", "-q", "tests/test_collection.py"}
	o.Timeout = 20 * time.Second
	r, err := Preview(context.Background(), root, o)
	if err != nil || len(r.Executions) != 1 {
		t.Fatalf("%v %+v", err, r)
	}
	ev := r.Executions[0]
	if r.Gate.Verdict != gate.Fail || ev.IntegrationStatus() != model.StatusFailed || ev.UnidentifiedFailureCount() != 1 || len(ev.Observation.FailedCases) != 0 {
		t.Fatalf("new collection cause was suppressed: %+v %+v", r.Gate, ev)
	}
	b := ev.BaseComparison
	if b == nil || b.Execution == nil || b.AllExisting || len(b.ExistingCases) != 0 || b.Execution.Status != model.StatusFailed {
		t.Fatalf("collection comparison wrongly trusted file identity: %+v", b)
	}
}
