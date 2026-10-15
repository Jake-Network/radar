package integration

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/Jake-Network/radar/internal/evidence"
	"github.com/Jake-Network/radar/internal/model"
	"github.com/Jake-Network/radar/internal/pathutil"
	"github.com/Jake-Network/radar/internal/runners"
)

// compareBase runs only failed commands, once, under the verification's shared
// deadline and command budget. Its observation cannot establish a candidate pass.
func compareBase(ctx context.Context, temp string, r Report, ev ExecutionEvidence, o Options, profile *runners.Profile, maxCacheBytes int64) *BaseComparison {
	b := &BaseComparison{Explanation: "Base comparison unavailable; the candidate failure is retained."}
	if ev.SourceAfterExecution != "unchanged" {
		b.Explanation += " Candidate source changed or could not be checked."
		return b
	}
	if ctx.Err() != nil {
		b.Explanation += " Verification time budget exhausted."
		return b
	}
	if o.budget != nil && o.budget.left < 1 {
		b.Explanation += " Shared command budget exhausted."
		return b
	}
	c, err := BuildCandidate(ctx, temp, r.Base, nil)
	if c != nil {
		defer c.Close()
	}
	if err != nil {
		b.Explanation += " Could not build the private base candidate."
		return b
	}
	options := o
	// A reviewed candidate plan cannot supply criterion evidence for the base.
	options.Plan, options.PlanDigest = nil, ""
	options.ObserveCoverage, options.coverageReport = false, nil
	options.artifacts = nil
	if profile != nil {
		identity, identityErr := evidence.RepositoryIdentity(ctx, c.Dir)
		state, stateErr := runtimeState()
		cacheDir, cacheErr := pathutil.StatePath(state, "runner-cache")
		if identityErr != nil || stateErr != nil || cacheErr != nil {
			return b
		}
		prep, err := evidence.PrepareRunnerBudget(ctx, c.Dir, identity, *profile, runners.Cache{Dir: cacheDir, MaxBytes: maxCacheBytes}, options.Timeout, func() bool { return options.budget == nil || options.budget.take(1) })
		if err != nil || prep.Status != model.StatusPassed {
			b.Explanation += " Declared runner preparation on base was unavailable."
			return b
		}
		inspection, err := evidence.InspectCandidateSource(ctx, c.Dir, evidence.CandidateCheckpoint{Repository: identity, Base: c.Base, Revision: c.Commit, Tree: c.Tree, Inputs: c.Inputs}, prep.Artifacts)
		if err != nil || !inspection.Matches {
			b.Explanation += " Declared runner preparation changed base source or source integrity is unknown."
			return b
		}
		options.artifacts = prep.Artifacts
	}
	if ctx.Err() != nil {
		b.Explanation += " Verification time budget exhausted."
		return b
	}
	if o.budget != nil && !o.budget.take(1) {
		b.Explanation += " Shared command budget exhausted."
		return b
	}
	if deadline, ok := ctx.Deadline(); ok {
		options.Timeout = min(options.Timeout, time.Until(deadline))
	}
	baseline := execute(ctx, c.Dir, Report{Base: c.Base, Inputs: append([]string{}, c.Inputs...), CandidateCommit: c.Commit, CandidateTree: c.Tree}, options)
	b.Execution = &baseline
	if baseline.SourceAfterExecution != "unchanged" {
		b.Explanation += " Base execution changed source or source integrity is unknown."
		return b
	}
	if baseline.Status == model.StatusPassed {
		b.Explanation = "The same command passed on unchanged base; candidate failures remain failures."
		return b
	}
	if baseline.Status != model.StatusFailed {
		return b
	}
	if ev.Observation.Diagnosis == nil && baseline.Observation.Diagnosis == nil {
		for _, name := range ev.Observation.FailedCases {
			if slices.Contains(baseline.Observation.FailedCases, name) {
				b.ExistingCases = append(b.ExistingCases, name)
			}
		}
		b.AllExisting = len(b.ExistingCases) > 0 && len(b.ExistingCases) == len(ev.Observation.FailedCases) && len(ev.Observation.FailedCases) >= ev.Observation.TestsFailed
		if len(ev.Observation.FailedCases) < ev.Observation.TestsFailed {
			b.Explanation += " Some candidate failures lack recorded identities; complete comparison is unavailable."
			return b
		}
	} else {
		result, _ := compareFailure(ev, baseline)
		b.AllExisting = result == SubsetReproduced
		// A build diagnosis must not hide separately observed test failures.
		if ev.Observation.TestsFailed > 0 || len(ev.Observation.FailedCases) > 0 {
			b.AllExisting = false
		}
	}
	if b.AllExisting {
		b.Explanation = "Every identified candidate failure also failed on unchanged base; integration verification is incomplete, not passed."
	} else if len(b.ExistingCases) > 0 {
		b.Explanation = fmt.Sprintf("%d candidate failure(s) also failed on unchanged base; only remaining cases are candidate failure leads.", len(b.ExistingCases))
	}
	return b
}

// IntegrationStatus separates the observed command failure from its integration
// meaning: failures already present on base leave verification incomplete.
func (ev ExecutionEvidence) IntegrationStatus() model.Status {
	if ev.Status == model.StatusFailed && ev.BaseComparison != nil && ev.BaseComparison.AllExisting {
		return model.StatusIncomplete
	}
	return ev.Status
}

// NewFailedCases excludes identities also observed on unchanged base, while
// preserving the complete original observation for evidence consumers.
func (ev ExecutionEvidence) NewFailedCases() []string {
	out := []string{}
	for _, c := range ev.Observation.FailedCases {
		if ev.BaseComparison == nil || !slices.Contains(ev.BaseComparison.ExistingCases, c) {
			out = append(out, c)
		}
	}
	return out
}

// FailurePaths narrows repair leads after a partial baseline match. Case names
// vary by runner; only paths explicitly represented by a remaining case can be
// retained. When they cannot be resolved, omit the lead rather than blame A.
func (ev ExecutionEvidence) FailurePaths(paths []string) []string {
	if ev.BaseComparison == nil || len(ev.BaseComparison.ExistingCases) == 0 {
		return paths
	}
	out := []string{}
	for _, p := range paths {
		module := strings.TrimSuffix(strings.ReplaceAll(p, "/", "."), ".py")
		for _, name := range ev.NewFailedCases() {
			if strings.Contains(name, p) || strings.Contains(name, module+".") || strings.HasPrefix(name, module+"::") {
				out = append(out, p)
				break
			}
		}
	}
	return out
}
