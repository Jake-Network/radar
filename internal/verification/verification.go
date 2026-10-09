// Package verification evaluates only explicit, bounded deterministic rules.
package verification

import (
	"context"
	"github.com/radar-engine/radar/internal/evidence"

	gitrepo "github.com/radar-engine/radar/internal/git"
	"github.com/radar-engine/radar/internal/model"
	"github.com/radar-engine/radar/internal/planning"
)

// Verify reads the implementation revision, while approval remains bound to the
// design's baseline. WORKTREE results are always informational.
func Verify(ctx context.Context, p planning.Plan, s model.Snapshot, root string) planning.Report {
	return VerifyWithEvidence(ctx, p, s, root, nil)
}

// VerifyWithEvidence binds observed test results to the analyzed commit and design.
func VerifyWithEvidence(ctx context.Context, p planning.Plan, s model.Snapshot, root string, records []evidence.Record) planning.Report {
	r := planning.Report{Revision: s.Revision, BaseRevision: p.BaseRevision, PlanDigest: planning.Digest(p), Status: "passed", Findings: []model.Finding{}, Checks: []planning.Check{}}
	commit, err := gitrepo.Resolve(ctx, root, s.Revision)
	baseline, baseErr := gitrepo.Resolve(ctx, root, p.BaseRevision)
	r.Authoritative = err == nil && commit == s.Revision && baseErr == nil && baseline == p.BaseRevision && planning.Approved(p)
	historyOK := true
	if r.Authoritative {
		historyOK, err = gitrepo.IsAncestor(ctx, root, baseline, commit)
		if err != nil {
			historyOK = false
		}
		r.Authoritative = historyOK
	}
	add := func(c planning.Check) {
		r.Checks = append(r.Checks, c)
		if c.Status == "failed" {
			r.Status = "failed"
		} else if c.Status == "unknown" && r.Status == "passed" {
			r.Status = "unknown"
		}
		if c.Status != "passed" {
			f := model.NewFinding("verification_"+c.Status, c.ID+": "+c.Explanation, c.Evidence)
			f.ID = model.StableID(s.Repository, r.PlanDigest, s.Revision, c.ID, c.Status, c.Explanation)
			if c.Status == "failed" {
				f.Severity = "error"
			}
			if c.Location != nil {
				f.Locations = []model.Provenance{*c.Location}
			}
			r.Findings = append(r.Findings, f)
		}
	}
	if !historyOK {
		add(planning.Check{ID: "checkpoint_history", Status: "unknown", Evidence: model.Unknown, Explanation: "Implementation checkpoint is not a verified descendant of the approved baseline."})
	}
	// Validate the plan's referential integrity independently of the implementation
	// revision and projected baseline; those legitimately differ after coding.
	structural := p
	structural.BaseRevision = s.Revision
	structural.GraphDeltas = nil
	structural.Approval = nil
	structural.Tasks = append([]planning.Task(nil), p.Tasks...)
	for i := range structural.Tasks {
		structural.Tasks[i].Consequential = false
	}
	validation := planning.Validate(structural, s)
	for _, f := range validation.Findings {
		r.Findings = append(r.Findings, f)
		if f.Severity == "error" {
			r.Status = "failed"
		} else {
			r.Checks = append(r.Checks, planning.Check{ID: "plan:" + f.Code + ":" + f.ID, Status: "unknown", Evidence: model.Unknown, Explanation: f.Explanation})
			if r.Status == "passed" {
				r.Status = "unknown"
			}
		}
	}
	for _, c := range VerifyContracts(ctx, root, p, s) {
		add(c)
	}

	if !planning.Approved(p) {
		add(planning.Check{ID: "design_review", Status: "unknown", Explanation: "No valid digest-bound design review; implementation results are informational.", Evidence: model.Unknown})
	}
	if p.SchemaVersion != planning.SchemaVersion || len(p.Requirements) == 0 {
		add(planning.Check{ID: "plan_schema", Status: "failed", Explanation: "Unsupported schema or missing requirements.", Evidence: model.VerifiedStatic})
	}
	if _, err := planning.Tasks(p); err != nil {
		add(planning.Check{ID: "task_dag", Status: "failed", Explanation: err.Error(), Evidence: model.VerifiedStatic})
	}
	for _, c := range p.Acceptance {
		add(evaluateWithEvidence(ctx, c.ID, c.Rule, s, root, p, records))
	}
	for _, c := range p.Constraints {
		add(evaluateWithEvidence(ctx, "constraint:"+c.ID, c.Rule, s, root, p, records))
	}
	for _, q := range p.Requirements {
		covered := false
		for _, c := range p.Acceptance {
			if c.Requirement == q.ID {
				covered = true
			}
		}
		if !covered {
			add(planning.Check{ID: "requirement:" + q.ID, Status: "unknown", Explanation: "Requirement has no acceptance evidence.", Evidence: model.Unknown})
		}
	}
	for _, d := range p.GraphDeltas {
		add(evaluateDelta(d, s))
	}

	if len(r.Checks) == 0 {
		add(planning.Check{ID: "evidence", Status: "unknown", Explanation: "No verification rules declared.", Evidence: model.Unknown})
	}
	ApplyTaskResults(p, &r)
	return r
}
