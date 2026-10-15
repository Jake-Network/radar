// Package overlap checks pinned worktree pairs with Radar's existing integration
// detectors. Its findings are static repair leads, never runtime proof.
package overlap

import (
	"context"
	"fmt"
	"strings"

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

// Analyze checks every pair up to twelve inputs. More inputs are explicitly
// incomplete rather than silently interpreting unchecked pairs as independent.
func Analyze(ctx context.Context, root, base string, inputs []Input, caches ...*indexer.ParseCache) (Report, error) {
	cache := indexer.NewParseCache()
	if len(caches) > 0 && caches[0] != nil {
		cache = caches[0]
	}
	out := Report{Findings: []model.Finding{}, Limitations: []string{"Supported static detectors only; absent drift is not proof of compatibility. Dynamic references and runtime behavior remain unverified."}}
	if len(inputs) > 12 {
		return out, fmt.Errorf("overlap supports at most 12 worktrees; select fewer branches")
	}
	standalone := map[int]map[string]bool{}
	for i := 0; i < len(inputs); i++ {
		for j := i + 1; j < len(inputs); j++ {
			if err := ctx.Err(); err != nil {
				return out, err
			}
			pair, err := analyze(ctx, root, base, []Input{inputs[i], inputs[j]}, cache)
			if err != nil {
				return out, err
			}
			out.Pairs++
			relevant := []model.Finding{}
			for _, f := range pair.Findings {
				if supported(f) {
					relevant = append(relevant, f)
				}
			}
			if len(relevant) == 0 {
				if len(pair.Conflicts) > 0 {
					out.Limitations = append(out.Limitations, fmt.Sprintf("%s + %s: textual conflicts prevent combined symbol analysis: %s", inputs[i].Branch, inputs[j].Branch, strings.Join(pair.Conflicts, ", ")))
				}
				continue
			}
			for _, idx := range []int{i, j} {
				if standalone[idx] == nil {
					r, e := analyze(ctx, root, base, []Input{inputs[idx]}, cache)
					if e != nil {
						return out, e
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
	}
	return out, nil
}

func supported(f model.Finding) bool {
	return f.Severity == model.SeverityError && (f.Code == "worktree_textual_conflict" || f.Code == "duplicate_unique_key" || f.Code == "duplicate_route" || strings.HasPrefix(f.Code, "symbol_") || strings.HasPrefix(f.Code, "migration_") || strings.HasPrefix(f.Code, "unique_"))
}
func identity(f model.Finding) string { return f.Code + "\x00" + f.Explanation }

func analyze(ctx context.Context, root, base string, inputs []Input, cache *indexer.ParseCache) (integration.Report, error) {
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
	c, err := integration.BuildDirtyCandidate(ctx, root, base, heads, snaps)
	if c != nil {
		defer c.Close()
	}
	if err != nil {
		return integration.Report{}, err
	}
	r, err := integration.Analyze(ctx, c, integration.Options{Base: base, Branches: heads, ParseCache: cache})
	if err != nil {
		return r, err
	}
	if len(c.Conflicts) > 0 {
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
	before, err := Analyze(ctx, root, base, baseline, caches...)
	if err != nil {
		return nil, err
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
