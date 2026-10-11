package integration

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/Jake-Network/radar/internal/evidence"
	"github.com/Jake-Network/radar/internal/gate"
	"github.com/Jake-Network/radar/internal/model"
)

func TestCombinationsBySize(t *testing.T) {
	for _, c := range []struct {
		n, k int
		want string
	}{{3, 0, "[[]]"}, {3, 1, "[[0] [1] [2]]"}, {3, 2, "[[0 1] [0 2] [1 2]]"}, {4, 3, "[[0 1 2] [0 1 3] [0 2 3] [1 2 3]]"}} {
		if got := fmt.Sprint(combinations(c.n, c.k)); got != c.want {
			t.Fatalf("combinations(%d, %d) = %s, want %s", c.n, c.k, got, c.want)
		}
	}
}

func TestCompareFailure(t *testing.T) {
	failed := func(cases []string, d *evidence.Diagnosis) ExecutionEvidence {
		return ExecutionEvidence{Status: model.StatusFailed, SourceAfterExecution: "unchanged", Observation: evidence.CandidateObservation{Status: model.StatusFailed, FailedCases: cases, Diagnosis: d}}
	}
	build := func(name string) *evidence.Diagnosis { return &evidence.Diagnosis{Kind: "build_failed", Name: name} }
	original := failed([]string{"test_x", "test_z"}, nil)
	for name, c := range map[string]struct {
		original, subset ExecutionEvidence
		want             string
	}{
		"same case":              {original, failed([]string{"test_z"}, nil), SubsetReproduced},
		"other case":             {original, failed([]string{"test_y"}, nil), SubsetDifferentFailure},
		"no cases recognized":    {failed(nil, nil), failed([]string{"test_y"}, nil), SubsetReproduced},
		"same build error":       {failed(nil, build("Price")), failed(nil, build("Price")), SubsetReproduced},
		"other build error":      {failed(nil, build("Price")), failed(nil, build("Tax")), SubsetDifferentFailure},
		"build versus test":      {original, failed([]string{"test_x"}, build("Price")), SubsetDifferentFailure},
		"pass":                   {original, ExecutionEvidence{Status: model.StatusPassed, SourceAfterExecution: "unchanged"}, SubsetPassed},
		"pass that wrote source": {original, ExecutionEvidence{Status: model.StatusPassed, SourceAfterExecution: "modified"}, SubsetNotComparable},
		"timeout":                {original, ExecutionEvidence{Status: model.StatusTimeout}, SubsetNotComparable},
		"runner missing":         {original, ExecutionEvidence{Status: model.StatusError, Observation: evidence.CandidateObservation{Diagnosis: &evidence.Diagnosis{Kind: "runner_missing", Name: "pytest"}}}, SubsetNotComparable},
		"no recognized result":   {original, ExecutionEvidence{Status: model.StatusUnknown}, SubsetNotComparable},
	} {
		if got := compareFailure(c.original, c.subset); got != c.want {
			t.Errorf("%s: got %s, want %s", name, got, c.want)
		}
	}
}

func TestAttributionRunsRequireVerification(t *testing.T) {
	if _, err := validate(Options{AttributionRuns: 4}); err == nil || !strings.Contains(err.Error(), "requires --verify") {
		t.Fatal("attribution executes code and needs the same consent", err)
	}
	if _, err := validate(Options{AttributionRuns: 65, Verify: true, AllowExecution: true, Command: []string{"true"}}); err == nil {
		t.Fatal("bounded")
	}
}

func TestAttributionObservesTheBreakingCombination(t *testing.T) {
	root := fixture(t)
	o := opts("backend", "frontend")
	o.AttributionRuns = DefaultAttributionRuns
	r, err := Preview(context.Background(), root, o)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Attributions) != 1 {
		t.Fatal("one failed command", r.Attributions)
	}
	a := r.Attributions[0]
	if a.Outcome != AttributionIsolated || fmt.Sprint(a.Reproducing) != "[[0 1]]" || len(a.Runs) != 3 || a.FindingID == "" || r.ObservedAttribution(a.FindingID) == nil {
		t.Fatal("base, backend and frontend each pass; only both fail", a)
	}
	for _, run := range a.Runs {
		if run.CandidateCommit == "" || run.CandidateCommit == r.CandidateCommit {
			t.Fatal("each subset is its own candidate", run)
		}
	}
	if r.Gate.Verdict != gate.Fail {
		t.Fatal("attribution never changes the gate", r.Gate)
	}
	// The repaired branches pass, so there is nothing to attribute.
	o = opts("backend", "repaired")
	o.AttributionRuns = DefaultAttributionRuns
	if r, err = Preview(context.Background(), root, o); err != nil || len(r.Attributions) != 0 {
		t.Fatal(err, r.Attributions)
	}
}

func TestEnvironmentFailureIsNotAttributed(t *testing.T) {
	root := fixture(t)
	o := opts("backend", "frontend")
	o.Command = []string{"radar-runner-that-does-not-exist"}
	o.AttributionRuns = DefaultAttributionRuns
	r, err := Preview(context.Background(), root, o)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Executions) != 1 || r.Executions[0].Status == model.StatusFailed {
		t.Fatal("a missing runner is not a source failure", r.Executions)
	}
	if len(r.Attributions) != 0 {
		t.Fatal("an environment problem fails every subset the same way; no reruns", r.Attributions)
	}
}
