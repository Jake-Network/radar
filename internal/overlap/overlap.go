// Package overlap checks pinned worktree pairs with Radar's existing integration
// detectors. Its findings are static repair leads, never runtime proof.
package overlap

import (
	"context"
	"fmt"
	"strings"

	gitrepo "github.com/Jake-Network/radar/internal/git"
	"github.com/Jake-Network/radar/internal/indexer"
	"github.com/Jake-Network/radar/internal/integration"
	"github.com/Jake-Network/radar/internal/model"
)

type Input struct {
	Branch   string                     `json:"branch"`
	Head     string                     `json:"head"`
	Snapshot *integration.DirtySnapshot `json:"snapshot,omitempty"`
}
type Report struct {
	Findings    []model.Finding `json:"drift"`
	Pairs       int             `json:"pairs_checked"`
	Limitations []string        `json:"limitations"`
}

// Analyze checks a bounded number of pairs. Omitted pairs remain explicitly
// incomplete rather than silently being treated as independent.
func Analyze(ctx context.Context, root, base string, inputs []Input, caches ...*indexer.ParseCache) (Report, error) {
	return analyzePairs(ctx, root, base, inputs, "", caches...)
}

// AnalyzeCurrent checks only pairs involving the current worktree. Stop hooks
// do not need peer-to-peer findings and must not pay their quadratic cost.
func AnalyzeCurrent(ctx context.Context, root, base, current string, inputs []Input, caches ...*indexer.ParseCache) (Report, error) {
	if current == "" {
		return Report{Findings: []model.Finding{}, Limitations: []string{"Overlap incomplete: detached current worktree has no named peer pair scope."}}, nil
	}
	return analyzePairs(ctx, root, base, inputs, current, caches...)
}

func analyzePairs(ctx context.Context, root, base string, inputs []Input, current string, caches ...*indexer.ParseCache) (Report, error) {
	cache := indexer.NewParseCache()
	if len(caches) > 0 && caches[0] != nil {
		cache = caches[0]
	}
	out := Report{Findings: []model.Finding{}, Limitations: []string{"Supported static detectors only; absent drift is not proof of compatibility. Dynamic references and runtime behavior remain unverified."}}
	budget := 66 // all pairs for twelve worktrees
	if current != "" {
		budget = 11
	}
	eligible := 0
	pairs := []pairIndices{}
	for i := range inputs {
		for j := i + 1; j < len(inputs); j++ {
			if current == "" || inputs[i].Branch == current || inputs[j].Branch == current {
				eligible++
				if len(pairs) < budget {
					pairs = append(pairs, pairIndices{i, j})
				}
			}
		}
	}
	if eligible > budget {
		out.Limitations = append(out.Limitations, fmt.Sprintf("Overlap incomplete: %d of %d eligible pairs omitted by the %d-pair inspection limit.", eligible-budget, eligible, budget))
	}
	standalone := map[int]map[string]bool{}
	standaloneConflicted := map[int]bool{}
	standaloneFull := map[int]bool{}
	workers := 2
	if !immutableAnalysisInputs(base, inputs) {
		// Do not overlap mutable-ref observations.
		workers = 1
	}
	results, batchErr := inspectPairBatch(ctx, len(pairs), workers, func(ctx context.Context, index int) (integration.Report, error) {
		pair := pairs[index]
		return analyze(ctx, root, base, []Input{inputs[pair.left], inputs[pair.right]}, cache)
	})
	for index, indices := range pairs {
		i, j := indices.left, indices.right
		if !results[index].complete {
			if batchErr == nil {
				return out, fmt.Errorf("overlap pair inspection incomplete")
			}
			return out, batchErr
		}
		if err := ctx.Err(); err != nil {
			return out, err
		}
		pair := results[index].report
		out.Pairs++
		for _, limitation := range pair.Limitations {
			if strings.HasPrefix(limitation, "Overlap incomplete:") {
				out.Limitations = append(out.Limitations, fmt.Sprintf("%s (%s + %s)", limitation, inputs[i].Branch, inputs[j].Branch))
			}
		}
		relevant := []model.Finding{}
		for _, f := range pair.Findings {
			if supported(f) {
				relevant = append(relevant, f)
			}
		}
		if len(pair.Conflicts) > 0 {
			out.Limitations = append(out.Limitations, fmt.Sprintf("%s + %s: textual conflicts prevent combined symbol analysis: %s", inputs[i].Branch, inputs[j].Branch, strings.Join(pair.Conflicts, ", ")))
		}
		if len(relevant) == 0 {
			continue
		}
		needsStandaloneAnalysis := false
		for _, finding := range relevant {
			if finding.Code != "worktree_textual_conflict" {
				needsStandaloneAnalysis = true
			}
		}
		for _, idx := range []int{i, j} {
			if needsStandaloneAnalysis && standaloneConflicted[idx] {
				return out, fmt.Errorf("standalone comparison unavailable for %s: textual conflicts prevent combined analysis", inputs[idx].Branch)
			}
			if standalone[idx] == nil || needsStandaloneAnalysis && !standaloneFull[idx] {
				// The full pair above has validated this exact snapshot. A
				// standalone head at base (or its synthetic dirty child) is
				// a descendant of base, so merging it cannot create textual
				// conflict hunks. This establishes no semantic check result.
				if !needsStandaloneAnalysis && inputs[idx].Head == base && immutableAnalysisInputs(base, []Input{inputs[idx]}) {
					standalone[idx] = map[string]bool{}
					continue
				}
				r, e := analyzeComparison(ctx, root, base, []Input{inputs[idx]}, cache, !needsStandaloneAnalysis)
				if e != nil {
					return out, e
				}
				standaloneConflicted[idx] = len(r.Conflicts) > 0
				standaloneFull[idx] = needsStandaloneAnalysis
				if needsStandaloneAnalysis && len(r.Conflicts) > 0 {
					return out, fmt.Errorf("standalone comparison unavailable for %s: textual conflicts prevent combined analysis", inputs[idx].Branch)
				}
				for _, limitation := range r.Limitations {
					if strings.HasPrefix(limitation, "Overlap incomplete:") {
						return out, fmt.Errorf("standalone comparison unavailable for %s: %s", inputs[idx].Branch, limitation)
					}
				}
				standalone[idx] = map[string]bool{}
				for _, f := range r.Findings {
					standalone[idx][identity(f)] = true
				}
			}
		}
		for _, f := range relevant {
			if standalone[i][identity(f)] || standalone[j][identity(f)] {
				continue
			}
			f.Branches = []string{inputs[i].Branch, inputs[j].Branch}
			f.ID = model.StableID("worktree_drift", f.ID, inputs[i].Branch, inputs[j].Branch)
			f.Explanation = "Cross-worktree drift: " + f.Explanation
			out.Findings = append(out.Findings, f)
		}
	}
	return out, batchErr
}

func supported(f model.Finding) bool {
	return f.Severity == model.SeverityError && (f.Code == "worktree_textual_conflict" || f.Code == "duplicate_unique_key" || f.Code == "duplicate_route" || strings.HasPrefix(f.Code, "contract_") || strings.HasPrefix(f.Code, "symbol_") || strings.HasPrefix(f.Code, "migration_") || strings.HasPrefix(f.Code, "unique_"))
}
func identity(f model.Finding) string {
	if f.Code == "worktree_textual_conflict" {
		return f.ID
	}
	return f.Code + "\x00" + f.Explanation
}

func analyze(ctx context.Context, root, base string, inputs []Input, cache *indexer.ParseCache) (integration.Report, error) {
	return analyzeComparison(ctx, root, base, inputs, cache, false)
}

// Textual-only attribution needs complete private merge and hunk inspection,
// but semantic indexing cannot change the identity of a Git conflict hunk.
// Partial textual reports must never populate the full-analysis cache.
func analyzeComparison(ctx context.Context, root, base string, inputs []Input, cache *indexer.ParseCache, textualOnly bool) (integration.Report, error) {
	if report, ok := cachedAnalysis(ctx, root, base, inputs); ok {
		return report, nil
	}
	heads := []string{}
	snaps := []integration.DirtySnapshot{}
	for i, in := range inputs {
		heads = append(heads, in.Head)
		if in.Snapshot != nil {
			s := *in.Snapshot
			s.InputIndex = i
			snaps = append(snaps, s)
		}
	}
	var c *integration.Candidate
	var err error
	if len(snaps) == 0 {
		c, err = integration.BuildCandidate(ctx, root, base, heads)
	} else {
		c, err = integration.BuildDirtyCandidate(ctx, root, base, heads, snaps)
	}
	if c != nil {
		defer c.Close()
	}
	if err != nil {
		return integration.Report{}, err
	}
	r := integration.Report{Conflicts: append([]string(nil), c.Conflicts...)}
	if !textualOnly {
		r, err = integration.Analyze(ctx, c, integration.Options{Base: base, Branches: heads, ParseCache: cache})
		if err != nil {
			return r, err
		}
	}
	if len(c.Conflicts) > 0 {
		stopped, err := gitrepo.Resolve(ctx, c.Dir, "MERGE_HEAD")
		if err != nil {
			return r, err
		}
		for i, input := range c.Inputs {
			if input == stopped {
				if i < len(c.Inputs)-1 {
					r.Limitations = append(r.Limitations, "Overlap incomplete: merge stopped before the later pair input was inspected.")
				}
				break
			}
		}
		for _, p := range c.Conflicts {
			hunks, e := ConflictHunks(ctx, c, p)
			if e != nil {
				return r, e
			}
			for _, hunk := range hunks {
				f := model.NewFinding("worktree_textual_conflict", "Saved branch snapshots have a textual conflict in "+p+".", model.VerifiedTool)
				f.ID = model.StableID(f.ID, hunk)
				f.Severity = model.SeverityError
				f.Locations = []model.Provenance{{Path: p, Method: "git_private_merge", Evidence: model.VerifiedTool}}
				f.Remediation = "Reconcile this change with the peer worktree, then repeat the static preview."
				r.Findings = append(r.Findings, f)
			}
		}
	}

	if !textualOnly {
		cacheAnalysis(ctx, root, base, inputs, r)
	}
	return r, nil
}

// NewWithDirty reports whether a finding depends on this worktree's saved
// changes. Peer snapshots stay fixed while the current snapshot is removed.
func NewWithDirty(ctx context.Context, root, base, current string, inputs []Input, observed Report, caches ...*indexer.ParseCache) ([]model.Finding, error) {
	hasDirty := false
	baseline := append([]Input(nil), inputs...)
	for i := range baseline {
		if baseline[i].Branch == current && baseline[i].Snapshot != nil {
			hasDirty = true
			baseline[i].Snapshot = nil
		}
	}
	if !hasDirty || len(observed.Findings) == 0 {
		return nil, nil
	}
	// If removing current's saved edits leaves exactly the base, the pair is
	// identical to the already inspected peer standalone. It has no cross-input
	// drift to subtract. This is an identity of pinned trees, not a clean result
	// inferred from an unavailable analysis.
	if len(baseline) == 2 && immutableAnalysisInputs(base, baseline) {
		for _, input := range baseline {
			if input.Branch == current && input.Snapshot == nil && input.Head == base {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				out := []model.Finding{}
				for _, finding := range observed.Findings {
					for _, branch := range finding.Branches {
						if branch == current {
							out = append(out, finding)
							break
						}
					}
				}
				return out, nil
			}
		}
	}
	before, err := AnalyzeCurrent(ctx, root, base, current, baseline, caches...)
	if err != nil {
		return nil, err
	}
	needsCombinedAnalysis := false
	for _, finding := range observed.Findings {
		if finding.Code != "worktree_textual_conflict" {
			needsCombinedAnalysis = true
		}
	}
	for _, limitation := range before.Limitations {
		if strings.HasPrefix(limitation, "Overlap incomplete:") || needsCombinedAnalysis && strings.Contains(limitation, "textual conflicts prevent combined symbol analysis") {
			return nil, fmt.Errorf("dirty baseline comparison unavailable: %s", limitation)
		}
	}
	old := map[string]int{}
	for _, f := range before.Findings {
		old[f.ID]++
	}
	out := []model.Finding{}
	for _, f := range observed.Findings {
		if old[f.ID] > 0 {
			old[f.ID]--
			continue
		}
		for _, b := range f.Branches {
			if b == current {
				out = append(out, f)
				break
			}
		}
	}
	return out, nil
}
