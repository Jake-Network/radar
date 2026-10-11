package integration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/Jake-Network/radar/internal/evidence"
	gitrepo "github.com/Jake-Network/radar/internal/git"
	"github.com/Jake-Network/radar/internal/model"
	"github.com/Jake-Network/radar/internal/pathutil"
)

// DefaultAttributionRuns is a suggested budget when opting in to observed
// attribution: enough for every subset of three branches. The CLI defaults
// to zero additional executions; callers must explicitly request this budget.
const DefaultAttributionRuns = 8

const attributionLimitation = "Observed attribution runs each subset of branches once; a flaky test can make a subset look clean or broken."

// RunObservedAttribution runs bounded diagnostic subset executions after Fast
// Gate has completed. It neither repeats analysis nor changes the report's
// verdict, checks, selection or execution evidence. The caller retains and
// closes c, and may publish the gate before starting this separate path.
// Execution requires the same explicit authorization as Analyze. The supplied
// report and its failed observations must belong to this exact pinned candidate.
func RunObservedAttribution(ctx context.Context, c *Candidate, r Report, o Options) ([]Attribution, error) {
	o, err := validate(o)
	if err != nil {
		return nil, err
	}
	if !o.Verify || !o.AllowExecution {
		return nil, errors.New("observed attribution requires explicit execution authorization")
	}
	if c == nil || c.Dir == "" || c.Commit == "" || c.Tree == "" || c.Commit != r.CandidateCommit || c.Tree != r.CandidateTree || c.Base != r.Base || !slices.Equal(c.Inputs, r.Inputs) {
		return nil, errors.New("observed attribution report does not match retained candidate")
	}
	targets := []attributionTarget{}
	for _, ev := range r.Executions {
		if ev.SourceAfterExecution != "unchanged" {
			return nil, errors.New("observed attribution requires unchanged candidate execution source")
		}
		if ev.Status != model.StatusFailed {
			continue
		}
		if o.Suite == "" && (!slices.Equal(ev.Command, o.Command) || (o.CWD != "" && ev.CWD != o.CWD)) {
			return nil, errors.New("observed attribution command differs from authorized execution")
		}
		bound := ev
		bound.ID = ""
		data, _ := json.Marshal(bound)
		if ev.ID != model.StableID("integration-evidence-v1", string(data)) || ev.CandidateCommit != c.Commit || ev.CandidateTree != c.Tree || ev.Base != c.Base || !slices.Equal(ev.Inputs, c.Inputs) || ev.PlanDigest != o.PlanDigest {
			return nil, errors.New("observed attribution failure evidence is stale or does not match candidate and plan")
		}
		options := o
		options.Command, options.CWD = append([]string(nil), ev.Command...), ev.CWD
		if r.Selection != nil {
			for _, selected := range r.Selection.Commands {
				if selected.ID == ev.SelectionID && slices.Equal(selected.Command, ev.Command) && selected.CWD == ev.CWD {
					options.report = selected.JUnit
				}
			}
		}
		findingID := model.StableID(r.CandidateTree, ev.CWD, strings.Join(ev.Command, "\x00"), strings.Join(ev.Observation.FailedCases, "\x00"), "integration_execution_"+string(ev.Status))
		if !slices.ContainsFunc(r.Findings, func(f model.Finding) bool { return f.ID == findingID }) {
			return nil, errors.New("observed attribution failure has no matching gate finding")
		}
		targets = append(targets, attributionTarget{findingID: findingID, selection: ev.SelectionID, options: options, failure: ev})
	}
	if len(targets) == 0 || o.AttributionRuns == 0 {
		return []Attribution{}, nil
	}
	// Pinned objects are the subset inputs; verify the retained checkout still
	// names the analyzed commit before using it as the private object source.
	if ctx.Err() == nil {
		head, err := gitrepo.Resolve(ctx, c.Dir, "HEAD")
		if err != nil || head != c.Commit {
			return nil, errors.New("observed attribution retained candidate revision changed")
		}
		tree, err := gitrepo.Tree(ctx, c.Dir, c.Commit)
		if err != nil || tree != c.Tree {
			return nil, errors.New("observed attribution retained candidate tree changed")
		}
	}
	ctx, cancel := context.WithTimeout(ctx, o.Timeout)
	defer cancel()
	o.step(Step{Stage: StageAttribute, Total: len(targets)})
	// Keep mutations confined to a detached report holding only attribution.
	result := Report{Base: r.Base, Inputs: append([]string(nil), r.Inputs...)}
	attribute(ctx, c.Dir, &result, targets, o.AttributionRuns)
	return result.Attributions, nil
}

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
		r.Limitations = append(r.Limitations, attributionLimitation)
	}
}

// searchSubsets walks subsets by size: the base alone, each branch alone,
// then pairs and so on. The first size with a reproducing subset ends the
// search; the full combination is the original observation and is not rerun.
func searchSubsets(ctx context.Context, temp string, r Report, t attributionTarget, a *Attribution, budget int) int {
	n := len(r.Inputs)
	clean := true // every subset run so far passed
	for size := 0; size < n; size++ {
		// Enumerate lazily: the run budget must also bound work when many
		// branches are watched, rather than materializing n-choose-k subsets.
		subset := make([]int, size)
		for i := range subset {
			subset[i] = i
		}
		for {
			if budget <= 0 || ctx.Err() != nil {
				a.Explanation = fmt.Sprintf("Stopped after %d subset runs: the attribution budget or the --run time budget ran out before every smaller combination was tried.", len(a.Runs))
				return budget
			}
			budget--
			run := runSubset(ctx, temp, r, t, append([]int{}, subset...))
			a.Runs = append(a.Runs, run)
			switch run.Result {
			case SubsetReproduced:
				a.Reproducing = append(a.Reproducing, append([]int{}, subset...))
			case SubsetPassed:
			default:
				clean = false
			}
			if !nextCombination(subset, n) {
				break
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

// nextCombination advances an ordered subset in place, retaining O(n) memory.
func nextCombination(subset []int, n int) bool {
	for i := len(subset) - 1; i >= 0; i-- {
		if subset[i] < n-len(subset)+i {
			subset[i]++
			for j := i + 1; j < len(subset); j++ {
				subset[j] = subset[j-1] + 1
			}
			return true
		}
	}
	return false
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
