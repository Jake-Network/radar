package integration

import (
	"context"
	"fmt"
	"os"
	"slices"
	"time"

	"github.com/Jake-Network/radar/internal/evidence"
	"github.com/Jake-Network/radar/internal/model"
	"github.com/Jake-Network/radar/internal/pathutil"
)

// DefaultAttributionRuns bounds how many subset executions one gate spends
// on observed attribution: enough for every subset of three branches.
const DefaultAttributionRuns = 8

// Attribution outcomes. Only "isolated" names branches as observed causes.
const (
	// AttributionIsolated: the smallest reproducing subsets were found and
	// every smaller subset ran and passed.
	AttributionIsolated = "isolated"
	// AttributionBase: the failure reproduces on the base without any branch.
	AttributionBase = "base"
	// AttributionInconclusive: the budget ran out, or a smaller subset could
	// not be compared (error, timeout, different failure, conflict).
	AttributionInconclusive = "inconclusive"
)

// Subset run results.
const (
	SubsetReproduced       = "reproduced"
	SubsetPassed           = "passed"
	SubsetDifferentFailure = "different_failure"
	SubsetNotComparable    = "not_comparable"
)

// Attribution reruns one failed command on the base plus subsets of the
// input branches. Every run is a single observation of a separately built
// candidate; a flaky test can make a subset look clean or broken.
type Attribution struct {
	FindingID   string         `json:"finding_id"`
	SelectionID string         `json:"selection_id,omitempty"`
	Command     []string       `json:"command"`
	CWD         string         `json:"cwd"`
	Outcome     string         `json:"outcome"`
	Evidence    model.Evidence `json:"evidence"`
	// Reproducing lists the smallest subsets, as indexes into Report.Inputs,
	// that reproduced the failure. With outcome isolated they are minimal.
	Reproducing [][]int     `json:"reproducing"`
	Runs        []SubsetRun `json:"runs"`
	Explanation string      `json:"explanation"`
}

// SubsetRun is one execution of the failed command on base plus Branches.
type SubsetRun struct {
	// Branches are indexes into Report.Inputs; empty means the base alone.
	Branches        []int        `json:"branches"`
	Inputs          []string     `json:"inputs"`
	CandidateCommit string       `json:"candidate_commit,omitempty"`
	CandidateTree   string       `json:"candidate_tree,omitempty"`
	Conflicts       []string     `json:"conflicts,omitempty"`
	Status          model.Status `json:"status"`
	ExitCode        int          `json:"exit_code"`
	OutputDigest    string       `json:"output_digest,omitempty"`
	FailedCases     []string     `json:"failed_cases,omitempty"`
	Result          string       `json:"result"`
	Error           string       `json:"error,omitempty"`
}

// attributionTarget is a failed command waiting for attribution.
type attributionTarget struct {
	findingID string
	selection string
	options   Options
	failure   ExecutionEvidence
}

// attribute reruns each failed command on smaller combinations, smallest
// first, until it finds the subsets that reproduce the failure. It spends at
// most budget executions across all targets and stops when ctx ends. It never
// changes the gate: attribution is a repair lead backed by observations.
func attribute(ctx context.Context, temp string, r *Report, targets []attributionTarget, budget int) {
	for _, t := range targets {
		a := Attribution{FindingID: t.findingID, SelectionID: t.selection, Command: t.options.Command, CWD: t.options.CWD, Outcome: AttributionInconclusive, Evidence: model.ObservedTest, Reproducing: [][]int{}, Runs: []SubsetRun{}}
		budget = searchSubsets(ctx, temp, *r, t, &a, budget)
		r.Attributions = append(r.Attributions, a)
	}
	if len(r.Attributions) > 0 {
		r.Limitations = append(r.Limitations, "Observed attribution runs each subset of branches once; a flaky test can make a subset look clean or broken.")
	}
}

// searchSubsets walks subsets by size: the base alone, each branch alone,
// then pairs and so on. The first size with a reproducing subset ends the
// search; the full combination is the original observation and is not rerun.
func searchSubsets(ctx context.Context, temp string, r Report, t attributionTarget, a *Attribution, budget int) int {
	n := len(r.Inputs)
	clean := true // every subset run so far passed
	for size := 0; size < n; size++ {
		for _, subset := range combinations(n, size) {
			if budget <= 0 || ctx.Err() != nil {
				a.Explanation = fmt.Sprintf("Stopped after %d subset runs: the attribution budget or the --run time budget ran out before every smaller combination was tried.", len(a.Runs))
				return budget
			}
			budget--
			run := runSubset(ctx, temp, r, t, subset)
			a.Runs = append(a.Runs, run)
			switch run.Result {
			case SubsetReproduced:
				a.Reproducing = append(a.Reproducing, subset)
			case SubsetPassed:
			default:
				clean = false
			}
		}
		if len(a.Reproducing) == 0 {
			continue
		}
		switch {
		case size == 0:
			a.Outcome = AttributionBase
			a.Explanation = "The same command fails the same way on the base without any branch; the branches did not introduce this failure."
		case clean:
			a.Outcome = AttributionIsolated
			a.Explanation = "Reproduced with the listed branches; every smaller combination ran and passed."
		default:
			a.Explanation = "Reproduced with the listed branches, but a smaller combination could not be compared (see runs), so they are not shown to be minimal."
		}
		return budget
	}
	all := make([]int, n)
	for i := range all {
		all[i] = i
	}
	a.Reproducing = append(a.Reproducing, all)
	if clean {
		a.Outcome = AttributionIsolated
		a.Explanation = "Fails only when all of these branches are combined; every smaller combination ran and passed."
		if n == 1 {
			a.Explanation = "Fails with this branch and passes on the base alone."
		}
	} else {
		a.Explanation = "No smaller combination reproduced the failure, but some could not be compared (see runs), so the full combination is not shown to be minimal."
	}
	return budget
}

// runSubset builds base plus the subset in a fresh private candidate and runs
// the failed command there with the same options.
func runSubset(ctx context.Context, temp string, r Report, t attributionTarget, subset []int) SubsetRun {
	run := SubsetRun{Branches: subset, Inputs: []string{}, Status: model.StatusError, ExitCode: -1, Result: SubsetNotComparable}
	for _, i := range subset {
		run.Inputs = append(run.Inputs, r.Inputs[i])
	}
	c, err := BuildCandidate(ctx, temp, r.Base, run.Inputs)
	defer c.Close()
	if err != nil {
		run.Error = err.Error()
		return run
	}
	if len(c.Conflicts) > 0 {
		run.Conflicts = c.Conflicts
		run.Error = "this subset does not merge without conflicts"
		return run
	}
	run.CandidateCommit, run.CandidateTree = c.Commit, c.Tree
	if why := unavailableDir(c.Dir, t.options.CWD); why != "" {
		run.Error = why
		return run
	}
	subsetReport := Report{Base: c.Base, Inputs: c.Inputs, CandidateCommit: c.Commit, CandidateTree: c.Tree}
	options := t.options
	if deadline, ok := ctx.Deadline(); ok {
		options.Timeout = min(options.Timeout, time.Until(deadline))
	}
	ev := execute(ctx, c.Dir, subsetReport, options)
	run.Status, run.ExitCode, run.OutputDigest = ev.Status, ev.ExitCode, ev.OutputDigest
	run.FailedCases = ev.Observation.FailedCases
	run.Error = ev.ExecutionError
	var why string
	run.Result, why = compareFailure(t.failure, ev)
	if run.Error == "" {
		run.Error = why
	}
	return run
}

// compareFailure says whether ev is the same failure as the original, and
// why not when it cannot tell. Only runs that left the candidate source
// unchanged count. A reproduction must show every failure the original showed:
// all of its failed cases, or a build diagnosis of the same kind and name at
// every original location. A subset that shows only part of it is a different
// failure, so a failure already on the base cannot hide one a branch added.
// When the original failure has no identifiable case or location, a failing
// subset cannot be compared at all; only passes remain informative.
func compareFailure(original, ev ExecutionEvidence) (string, string) {
	if ev.SourceAfterExecution != "unchanged" {
		return SubsetNotComparable, "the command changed the candidate source or it could not be checked"
	}
	switch ev.Status {
	case model.StatusPassed:
		return SubsetPassed, ""
	case model.StatusFailed:
	default:
		return SubsetNotComparable, ""
	}
	want, got := original.Observation, ev.Observation
	if !identifiable(want) {
		return SubsetNotComparable, "the original failure names no failed case or source location to compare"
	}
	if want.Diagnosis != nil || got.Diagnosis != nil {
		if want.Diagnosis == nil || got.Diagnosis == nil || want.Diagnosis.Kind != got.Diagnosis.Kind || want.Diagnosis.Name != got.Diagnosis.Name {
			return SubsetDifferentFailure, ""
		}
		for _, l := range want.Locations {
			if !slices.ContainsFunc(got.Locations, func(g model.Provenance) bool { return g.Path == l.Path && g.Line == l.Line }) {
				return SubsetDifferentFailure, ""
			}
		}
		return SubsetReproduced, ""
	}
	for _, c := range want.FailedCases {
		if !slices.Contains(got.FailedCases, c) {
			return SubsetDifferentFailure, ""
		}
	}
	return SubsetReproduced, ""
}

// identifiable reports whether a failure can be recognized again: by failed
// case names, or by a diagnosis with at least one located line.
func identifiable(o evidence.CandidateObservation) bool {
	if o.Diagnosis != nil {
		return slices.ContainsFunc(o.Locations, func(l model.Provenance) bool { return l.Path != "" && l.Line > 0 })
	}
	return len(o.FailedCases) > 0
}

// unavailableDir reports a working directory that a subset does not have,
// which happens when a branch outside the subset added it.
func unavailableDir(root, cwd string) string {
	if cwd == "" || cwd == "." {
		return ""
	}
	dir, err := pathutil.ResolveInside(root, cwd)
	if err != nil {
		return "working directory " + cwd + " is outside the candidate"
	}
	if info, e := os.Stat(dir); e != nil || !info.IsDir() {
		return "working directory " + cwd + " does not exist in this subset"
	}
	return ""
}

// combinations returns the size-k subsets of 0..n-1 in lexicographic order.
func combinations(n, k int) [][]int {
	out := [][]int{}
	var walk func(start int, prefix []int)
	walk = func(start int, prefix []int) {
		if len(prefix) == k {
			out = append(out, append([]int{}, prefix...))
			return
		}
		for i := start; i < n; i++ {
			walk(i+1, append(prefix, i))
		}
	}
	walk(0, []int{})
	return out
}

// ObservedAttribution returns the attribution recorded for a finding.
func (r Report) ObservedAttribution(findingID string) *Attribution {
	for i := range r.Attributions {
		if r.Attributions[i].FindingID == findingID {
			return &r.Attributions[i]
		}
	}
	return nil
}
