package landing

import (
	"context"

	"github.com/Jake-Network/radar/internal/gate"
	"github.com/Jake-Network/radar/internal/integration"
	"github.com/Jake-Network/radar/internal/model"
)

// EnforceInputScope keeps excluded original inputs visible even under a custom
// policy. It never downgrades a confirmed failure in the remaining candidate.
func (p Report) EnforceInputScope(r *integration.Report) {
	if len(p.RestackFirst) == 0 {
		return
	}
	const id = "landing_input_scope"
	found := false
	for _, c := range r.Checks {
		if c.ID == id {
			found = true
		}
	}
	if !found {
		r.Checks = append(r.Checks, integration.Check{ID: id, Status: model.StatusUnknown, Evidence: model.Unknown, Explanation: "Selected branches conflict with base alone and require restack; the analyzed candidate excludes these inputs."})
	}
	policy := r.Gate.Policy
	policy.Require = append([]string{}, policy.Require...)
	required := false
	for _, check := range policy.Require {
		if check == id {
			required = true
		}
	}
	if !required {
		policy.Require = append(policy.Require, id)
	}
	previous := r.Gate.Verdict
	r.Gate = gate.Evaluate(policy, r.Checks)
	// The mandatory scope guard is blocked even when policy missing-evidence
	// behavior is fail; existing policy failures retain their verdict.
	if previous == gate.Pass && r.Gate.Verdict == gate.Fail {
		r.Gate.Verdict = gate.Blocked
	}
	if r.Status != model.StatusFailed && r.Status != model.StatusError {
		r.Status = model.StatusIncomplete
	}
	r.Coverage = gate.CoverageFor(r.Checks, r.Limitations)
}

// PreviewPlan analyzes only the eligible, pinned order. With no eligible inputs
// it supplies no candidate identity or passing base-only observation.
func PreviewPlan(ctx context.Context, root string, p Report, o Options) (integration.Report, error) {
	if _, err := analysisOptions(o); err != nil {
		return integration.Report{}, err
	}
	var r integration.Report
	var err error
	if len(p.Order) == 0 && len(p.RestackFirst) > 0 {
		r = integration.Report{Base: p.Base, Status: model.StatusIncomplete, Inputs: []string{}, Conflicts: []string{}, Changed: []string{}, Affected: []string{}, Findings: []model.Finding{}, Diagnostics: []model.Diagnostic{}, Limitations: []string{"No eligible landing candidate exists; all selected branches require restack."}, Checks: []integration.Check{{ID: "textual_merge", Status: model.StatusUnknown, Evidence: model.Unknown, Explanation: "No combined candidate was constructed because every selected input requires restack."}}}
		policy := gate.Policy{Version: 1, Name: "supported-integration", Require: []string{"textual_merge", "no_breaking_contracts"}}
		if o.Analysis.Policy != nil {
			policy = *o.Analysis.Policy
		}
		r.Gate = gate.Evaluate(policy, r.Checks)
		if o.Finalize != nil {
			err = o.Finalize(ctx, &r)
		}
	} else {
		pins := map[string]string{}
		for _, b := range p.Branches {
			pins[b.Name] = b.Commit
		}
		o.Base = p.Base
		o.Branches = nil
		for _, name := range p.Order {
			o.Branches = append(o.Branches, pins[name])
		}
		r, err = PreviewWithOptions(ctx, root, o)
	}
	if err == nil {
		p.EnforceInputScope(&r)
		if len(p.Order) == 0 && len(p.RestackFirst) > 0 && r.Gate.Verdict == gate.Fail {
			r.Gate.Verdict = gate.Blocked
		}
	}
	return r, err
}
