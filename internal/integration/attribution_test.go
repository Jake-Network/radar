package integration

import (
	"context"
	"encoding/json"
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
	at := func(path string, line int) model.Provenance { return model.Provenance{Path: path, Line: line} }
	failed := func(cases []string, d *evidence.Diagnosis, locations ...model.Provenance) ExecutionEvidence {
		return ExecutionEvidence{Status: model.StatusFailed, SourceAfterExecution: "unchanged", Observation: evidence.CandidateObservation{Status: model.StatusFailed, FailedCases: cases, Diagnosis: d, Locations: locations}}
	}
	build := func(name string) *evidence.Diagnosis { return &evidence.Diagnosis{Kind: "build_failed", Name: name} }
	original := failed([]string{"test_x", "test_z"}, nil)
	modified := failed([]string{"test_x", "test_z"}, nil)
	modified.SourceAfterExecution = "modified"
	for name, c := range map[string]struct {
		original, subset ExecutionEvidence
		want             string
	}{
		"every case":                   {original, failed([]string{"test_z", "test_x", "test_y"}, nil), SubsetReproduced},
		"only some cases":              {original, failed([]string{"test_x"}, nil), SubsetDifferentFailure},
		"other case":                   {original, failed([]string{"test_y"}, nil), SubsetDifferentFailure},
		"failure that wrote source":    {original, modified, SubsetNotComparable},
		"original names no case":       {failed(nil, nil), failed(nil, nil), SubsetNotComparable},
		"same build error":             {failed(nil, build("main.go"), at("main.go", 7)), failed(nil, build("main.go"), at("main.go", 7)), SubsetReproduced},
		"same file, other line":        {failed(nil, build("main.go"), at("main.go", 7)), failed(nil, build("main.go"), at("main.go", 3)), SubsetDifferentFailure},
		"other symbol":                 {failed(nil, build("Price"), at("a.go", 1)), failed(nil, build("Tax"), at("a.go", 1)), SubsetDifferentFailure},
		"build error without location": {failed(nil, build("Price")), failed(nil, build("Price")), SubsetNotComparable},
		"build versus test":            {original, failed([]string{"test_x", "test_z"}, build("Price"), at("a.go", 1)), SubsetDifferentFailure},
		"pass":                         {original, ExecutionEvidence{Status: model.StatusPassed, SourceAfterExecution: "unchanged"}, SubsetPassed},
		"pass that wrote source":       {original, ExecutionEvidence{Status: model.StatusPassed, SourceAfterExecution: "modified"}, SubsetNotComparable},
		"pass of unidentified failure": {failed(nil, nil), ExecutionEvidence{Status: model.StatusPassed, SourceAfterExecution: "unchanged"}, SubsetPassed},
		"timeout":                      {original, ExecutionEvidence{Status: model.StatusTimeout, SourceAfterExecution: "unchanged"}, SubsetNotComparable},
		"no recognized result":         {original, ExecutionEvidence{Status: model.StatusUnknown, SourceAfterExecution: "unchanged"}, SubsetNotComparable},
	} {
		if got, _ := compareFailure(c.original, c.subset); got != c.want {
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

func TestObservedAttributionSeparateFromFastGate(t *testing.T) {
	root := fixture(t)
	o := opts("backend", "frontend")
	c, err := BuildCandidate(context.Background(), root, o.Base, o.Branches)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	r, err := Analyze(context.Background(), c, o)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Attributions) != 0 || len(r.Executions) != 1 || r.Gate.Verdict != gate.Fail {
		t.Fatal("Fast Gate must complete without subset executions", r)
	}
	before, _ := json.Marshal(r)
	o.AttributionRuns = DefaultAttributionRuns
	attributions, err := RunObservedAttribution(context.Background(), c, r, o)
	if err != nil {
		t.Fatal(err)
	}
	if len(attributions) != 1 || attributions[0].Outcome != AttributionIsolated || len(attributions[0].Runs) != 3 {
		t.Fatal("separate runner observes only subsets", attributions)
	}
	after, _ := json.Marshal(r)
	if string(before) != string(after) {
		t.Fatal("observed attribution changed Fast Gate report")
	}
	for name, options := range map[string]Options{
		"no authorization": {Verify: true, Command: o.Command, AttributionRuns: 1},
		"static":           {},
		"other command":    {Verify: true, AllowExecution: true, Command: []string{"true"}, AttributionRuns: 1},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := RunObservedAttribution(context.Background(), c, r, options); err == nil {
				t.Fatal("unauthorized invocation accepted")
			}
		})
	}
	stale := r
	stale.CandidateTree = strings.Repeat("0", 40)
	if _, err := RunObservedAttribution(context.Background(), c, stale, o); err == nil {
		t.Fatal("stale report accepted")
	}
	stale = r
	stale.Executions = append([]ExecutionEvidence(nil), r.Executions...)
	stale.Executions[0].OutputDigest = "tampered"
	if _, err := RunObservedAttribution(context.Background(), c, stale, o); err == nil {
		t.Fatal("tampered failure accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	attributions, err = RunObservedAttribution(ctx, c, r, o)
	if err != nil || len(attributions) != 1 || attributions[0].Outcome != AttributionInconclusive || len(attributions[0].Runs) != 0 {
		t.Fatal("cancelled attribution must remain inconclusive without executions", err, attributions)
	}
}

func TestLazyAttributionCombinations(t *testing.T) {
	for n := 0; n <= 8; n++ {
		for size := 0; size <= n; size++ {
			subset := make([]int, size)
			for i := range subset {
				subset[i] = i
			}
			got := [][]int{}
			for {
				got = append(got, append([]int{}, subset...))
				if !nextCombination(subset, n) {
					break
				}
			}
			if fmt.Sprint(got) != fmt.Sprint(combinations(n, size)) {
				t.Fatalf("n=%d size=%d: lazy=%v eager=%v", n, size, got, combinations(n, size))
			}
		}
	}
}

func TestCompareFailureRequiresCompleteReliableIdentities(t *testing.T) {
	failed := func(harness string, count int, cases ...string) ExecutionEvidence {
		return ExecutionEvidence{Status: model.StatusFailed, SourceAfterExecution: "unchanged", Observation: evidence.CandidateObservation{Harness: harness, TestsFailed: count, FailedCases: cases}}
	}
	for _, tc := range []struct {
		name             string
		original, subset ExecutionEvidence
		want             string
	}{
		{"pytest collection file", failed("pytest", 1, "tests/test_x.py"), failed("pytest", 1, "tests/test_x.py"), SubsetNotComparable},
		{"historical collection file without count", failed("pytest", 0, "tests/test_x.py"), failed("pytest", 0, "tests/test_x.py"), SubsetNotComparable},
		{"truncated cases", failed("unittest", 2, "pkg.Cases.test_old"), failed("unittest", 1, "pkg.Cases.test_old"), SubsetNotComparable},
		{"duplicated cases", failed("unittest", 2, "pkg.Cases.test_old", "pkg.Cases.test_old"), failed("unittest", 1, "pkg.Cases.test_old"), SubsetNotComparable},
		{"known JUnit case", failed("junit", 1, "Suite.test_old"), failed("junit", 1, "Suite.test_old"), SubsetReproduced},
		{"known pytest node", failed("pytest", 1, "tests/test_x.py::test_old"), failed("pytest", 1, "tests/test_x.py::test_old"), SubsetReproduced},
		{"unknown original still permits observed pass", failed("unittest", 2, "pkg.Cases.test_old"), ExecutionEvidence{Status: model.StatusPassed, SourceAfterExecution: "unchanged"}, SubsetPassed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if result, why := compareFailure(tc.original, tc.subset); result != tc.want {
				t.Fatalf("result=%s want=%s reason=%s", result, tc.want, why)
			}
		})
	}
	original := failed("junit", 2, "Suite.test_old", "Suite.test_new")
	original.Observation.Diagnosis = &evidence.Diagnosis{Kind: "build_failed", Name: "Price"}
	original.Observation.Locations = []model.Provenance{{Path: "source.go", Line: 3}}
	subset := failed("junit", 1, "Suite.test_old")
	subset.Observation.Diagnosis = original.Observation.Diagnosis
	subset.Observation.Locations = original.Observation.Locations
	if result, _ := compareFailure(original, subset); result != SubsetDifferentFailure {
		t.Fatal("same build diagnosis hid a missing failed case", result)
	}
}
