package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/Jake-Network/radar/internal/gate"
	"github.com/Jake-Network/radar/internal/integration"
	"github.com/Jake-Network/radar/internal/model"
	"github.com/Jake-Network/radar/internal/overlap"
	"github.com/Jake-Network/radar/internal/watch"
)

func (a *app) overlap(o options) int {
	if err := a.repository(); err != nil {
		return a.fail(err)
	}
	o.includeDirty = true
	state, err := a.observeWatch(a.ctx, o)
	if err != nil {
		return a.fail(err)
	}
	in := state.Value.(watchInput)
	parseCache, saveParseCache := savedSyntaxCache(a.root)
	defer saveParseCache()
	report, err := overlap.Analyze(a.ctx, a.root, in.Base, overlapInputs(in), parseCache)
	if err != nil {
		return a.fail(err)
	}
	filtered := report.Findings[:0]
	for _, f := range report.Findings {
		match := o.symbol == "" || overlapFindingSymbol(f) == o.symbol
		if o.file != "" {
			matchFile := false
			for _, p := range f.Locations {
				if p.Path == o.file {
					matchFile = true
				}
			}
			match = match && matchFile
		}
		if match {
			filtered = append(filtered, f)
		}
	}
	report.Findings = filtered
	candidate, err := integration.BuildDirtyCandidate(a.ctx, a.root, in.Base, in.SHAs, in.Snapshots)
	if candidate != nil {
		defer candidate.Close()
	}
	if err != nil {
		return a.fail(err)
	}
	combined, err := integration.Analyze(a.ctx, candidate, integration.Options{Base: in.Base, Branches: in.SHAs, Policy: in.config.Policy, ParseCache: parseCache})
	if err != nil {
		return a.fail(err)
	}
	combined.Drift = report.Findings
	combined.Limitations = append(combined.Limitations, report.Limitations...)
	check := gate.Check{ID: "worktree_overlap", Status: model.StatusUnknown, Evidence: model.Unknown, Explanation: "Pairwise supported static detectors ran; absence of findings does not verify all cross-worktree relationships."}
	if len(report.Findings) > 0 {
		check.Status = model.StatusFailed
		check.Evidence = model.Inferred
		check.Explanation = "Supported pairwise static drift was found."
	}
	combined.Gate.Required = append(combined.Gate.Required, check)
	combined.Checks = append(combined.Checks, check)
	combined.Coverage = append(combined.Coverage, gate.Coverage{Analyzer: check.ID, Status: check.Status, Scope: append([]string{}, in.Refs...), Excluded: []string{}, Unavailable: []string{"dynamic references and runtime compatibility"}, Uncertainty: append([]string{}, report.Limitations...)})
	combined.Gate.Policy.Require = append(combined.Gate.Policy.Require, check.ID)
	if len(report.Findings) > 0 {
		combined.Gate.Verdict = gate.Fail
		combined.Status = model.StatusFailed
		combined.Gate.Explanation = check.Explanation
	} else if combined.Gate.Verdict == gate.Pass {
		combined.Gate.Verdict = gate.Blocked
		combined.Status = model.StatusUnknown
		combined.Gate.Explanation = check.Explanation
	}
	result := struct {
		integration.Report
		Overlap overlap.Report `json:"overlap"`
	}{combined, report}
	latest, err := a.observeWatch(a.ctx, o)
	if err != nil || latest.Key != state.Key {
		return a.fail(fmt.Errorf("worktrees changed during overlap inspection; retry after saving changes"))
	}
	a.report(result, func(w io.Writer) {
		fmt.Fprintf(w, "Cross-worktree drift: %d findings across %d pairs (static repair leads)\n", len(report.Findings), report.Pairs)
		for _, f := range report.Findings {
			fmt.Fprintf(w, "! %s [%s]: %s\n", f.Code, strings.Join(f.Branches, ", "), f.Explanation)
			for _, p := range f.Locations {
				fmt.Fprintf(w, "  %s:%d\n", p.Path, p.Line)
			}
		}
		for _, s := range report.Limitations {
			fmt.Fprintln(w, "? "+s)
		}
	})
	if combined.Gate.Verdict == gate.Fail || o.policy != "" && combined.Gate.Verdict != gate.Pass {
		return 1
	}
	return 0
}

func overlapInputs(in watchInput) []overlap.Input {
	out := []overlap.Input{}
	for i, head := range in.SHAs {
		v := overlap.Input{Branch: in.Refs[i], Head: head}
		for j := range in.Snapshots {
			if in.Snapshots[j].InputIndex == i {
				snapshot := in.Snapshots[j]
				v.Snapshot = &snapshot
				break
			}
		}
		out = append(out, v)
	}
	return out
}

// Include every peer's exact saved digest in hook caching, even though the
// original hook candidate keeps its selected-branch semantics.
func (a *app) observeHook(ctx context.Context, o options) (watch.State, error) {
	state, err := a.observeWatch(ctx, o)
	if err != nil {
		return state, err
	}
	in := state.Value.(watchInput)
	for _, w := range in.Worktrees {
		if w.Detached {
			continue
		}
		snap, e := integration.ObserveDirty(ctx, w)
		if e != nil {
			return state, fmt.Errorf("peer worktree %s: %w", w.Branch, e)
		}
		input := overlap.Input{Branch: w.Branch, Head: w.Commit}
		if len(snap.Staged)+len(snap.Unstaged)+len(snap.Untracked) > 0 {
			input.Snapshot = &snap
		}
		// Unchanged base worktrees cannot introduce drift, but still remain in
		// Worktrees metadata to invalidate worktree creation/removal.
		if w.Commit == in.Base && input.Snapshot == nil {
			continue
		}
		in.OverlapInputs = append(in.OverlapInputs, input)
	}
	data, _ := json.Marshal(in)
	state.Key = configurationDigest(data)
	state.Value = in
	return state, nil
}

// The supported symbol detectors have a fixed diagnostic grammar. Extract only
// their declaration/reference name; prose, paths and partial names are not
// symbol matches. Unsupported finding kinds deliberately do not match.
func overlapFindingSymbol(f model.Finding) string {
	text := strings.TrimPrefix(f.Explanation, "Cross-worktree drift: ")
	switch f.Code {
	case "symbol_dangling_reference":
		_, name, ok := strings.Cut(text, " newly references removed name ")
		if ok {
			name, _, ok = strings.Cut(name, " from ")
			if ok {
				return name
			}
		}
	case "symbol_argument_count":
		_, name, ok := strings.Cut(text, " newly calls ")
		if ok {
			name, _, ok = strings.Cut(name, " with ")
			if ok {
				return name
			}
		}
	case "symbol_duplicate_definition":
		name, ok := strings.CutPrefix(text, "New duplicate declaration ")
		if ok {
			name, ok = strings.CutSuffix(name, " in the combined tree.")
			if ok {
				return name[strings.LastIndex(name, ":")+1:]
			}
		}
	}
	return ""
}
