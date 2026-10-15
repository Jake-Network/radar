package integration

import (
	"context"
	"fmt"
	"os"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/Jake-Network/radar/internal/evidence"
	"github.com/Jake-Network/radar/internal/model"
	"github.com/Jake-Network/radar/internal/pathutil"
	"github.com/Jake-Network/radar/internal/runners"
)

// baseVerifier shares one pinned base checkout and each declared preparation
// across comparisons. Once any command changes source, that checkout is never
// used for another observation.
type baseVerifier struct {
	candidate   *Candidate
	initialized bool
	unavailable string
	prepared    map[string]bool
	artifacts   []string
}

func (v *baseVerifier) close() {
	if v.candidate != nil {
		_ = v.candidate.Close()
	}
}

// compare runs after candidate commands, using only their remaining budget.
func (v *baseVerifier) compare(ctx context.Context, temp string, r Report, ev ExecutionEvidence, o Options, profile *runners.Profile, maxCacheBytes int64) *BaseComparison {
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
	if !v.initialized {
		v.initialized = true
		var err error
		v.candidate, err = BuildCandidate(ctx, temp, r.Base, nil)
		if err != nil {
			v.unavailable = "Could not build the private base candidate."
		}
		v.prepared = map[string]bool{}
	}
	if v.unavailable != "" {
		b.Explanation += " " + v.unavailable
		return b
	}
	c := v.candidate
	options := o
	// Candidate plan records cannot supply criterion evidence for the base.
	options.Plan, options.PlanDigest = nil, ""
	options.ObserveCoverage, options.coverageReport = false, nil
	if profile != nil {
		ready, seen := v.prepared[profile.Root]
		if !seen {
			v.prepared[profile.Root] = false
			identity, identityErr := evidence.RepositoryIdentity(ctx, c.Dir)
			state, stateErr := runtimeState()
			cacheDir, cacheErr := pathutil.StatePath(state, "runner-cache")
			if identityErr != nil || stateErr != nil || cacheErr != nil {
				return b
			}
			prep, err := evidence.PrepareRunnerBudget(ctx, c.Dir, identity, *profile, runners.Cache{Dir: cacheDir, MaxBytes: maxCacheBytes}, options.Timeout, func() bool { return options.budget == nil || options.budget.take(1) })
			v.artifacts = append(v.artifacts, prep.Artifacts...)
			inspection, inspectErr := evidence.InspectCandidateSource(ctx, c.Dir, evidence.CandidateCheckpoint{Repository: identity, Base: c.Base, Revision: c.Commit, Tree: c.Tree, Inputs: c.Inputs}, v.artifacts)
			if inspectErr != nil || !inspection.Matches {
				v.unavailable = "Declared runner preparation changed base source or source integrity is unknown."
				b.Explanation += " " + v.unavailable
				return b
			}
			ready = err == nil && prep.Status == model.StatusPassed
			v.prepared[profile.Root] = ready
		}
		if !ready {
			b.Explanation += " Declared runner preparation on base was unavailable."
			return b
		}
	}
	options.artifacts = append([]string{}, v.artifacts...)
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
	// A report can be reused only after removing the output produced by this run.
	// StatePath rejects symlinks, and an initially absent directory cannot contain
	// tracked source. Never remove a report that existed before execution.
	reportPath, freshReport := "", false
	if options.report != "" {
		if p, err := pathutil.StatePath(c.Dir, path.Join(options.CWD, options.report)); err == nil {
			_, statErr := os.Lstat(p)
			if os.IsNotExist(statErr) {
				reportPath, freshReport = p, true
			}
		}
	}
	baseline := execute(ctx, c.Dir, Report{Base: c.Base, Inputs: append([]string{}, c.Inputs...), CandidateCommit: c.Commit, CandidateTree: c.Tree}, options)
	b.Execution = &baseline
	if baseline.SourceAfterExecution != "unchanged" {
		v.unavailable = "Base execution changed source or source integrity is unknown."
		b.Explanation += " " + v.unavailable
		return b
	}
	if freshReport && baseline.ExecutionError == "" {
		checked, err := pathutil.StatePath(c.Dir, path.Join(options.CWD, options.report))
		if err != nil || checked != reportPath {
			v.unavailable = "Base report cleanup could not validate its path."
		} else if err := os.RemoveAll(checked); err != nil {
			v.unavailable = "Base report cleanup failed."
		}
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
		for _, name := range ev.NewFailedCases() {
			if failureCasePath(name, p, ev.CWD, unittestModuleRoot(ev)) {
				out = append(out, p)
				break
			}
		}
	}
	return out
}

// failureCasePath compares supported identities at module/path boundaries.
// Unittest identities end in Class.method; pytest IDs begin with a file path.
func failureCasePath(name, file, cwd, moduleRoot string) bool {
	if strings.Contains(name, "::") {
		nodePath := strings.SplitN(name, "::", 2)[0]
		if _, err := pathutil.RepoRelative(nodePath); err != nil {
			return false
		}
		if cwd != "" && cwd != "." && !strings.HasPrefix(nodePath, strings.TrimSuffix(cwd, "/")+"/") {
			nodePath = path.Join(cwd, nodePath)
		}
		return nodePath == file
	}
	if !strings.HasSuffix(file, ".py") {
		return false
	}
	parts := strings.Split(name, ".")
	if len(parts) < 3 {
		return false
	}
	module := strings.Join(parts[:len(parts)-2], ".")
	relative := file
	if moduleRoot != "" && moduleRoot != "." {
		prefix := strings.TrimSuffix(moduleRoot, "/") + "/"
		if !strings.HasPrefix(file, prefix) {
			return false
		}
		relative = strings.TrimPrefix(file, prefix)
	}
	return module == strings.TrimSuffix(strings.ReplaceAll(relative, "/", "."), ".py")
}

func unittestModuleRoot(ev ExecutionEvidence) string {
	root := ev.CWD
	// unittest discover reports names relative to its explicit top-level root,
	// or to the start directory when no top-level root was provided.
	start, top := "", ""
	for i := 0; i < len(ev.Command); i++ {
		arg := ev.Command[i]
		switch {
		case (arg == "-s" || arg == "--start-directory") && i+1 < len(ev.Command):
			i++
			start = ev.Command[i]
		case (arg == "-t" || arg == "--top-level-directory") && i+1 < len(ev.Command):
			i++
			top = ev.Command[i]
		case strings.HasPrefix(arg, "--start-directory="):
			start = strings.TrimPrefix(arg, "--start-directory=")
		case strings.HasPrefix(arg, "--top-level-directory="):
			top = strings.TrimPrefix(arg, "--top-level-directory=")
		}
	}
	if top != "" {
		root = path.Join(root, top)
	} else if start != "" {
		root = path.Join(root, start)
	}
	if root == "" {
		return "."
	}
	return root
}
