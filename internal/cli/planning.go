package cli

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/Jake-Network/radar/internal/languages"
	"github.com/Jake-Network/radar/internal/model"
	"github.com/Jake-Network/radar/internal/planning"
	"github.com/Jake-Network/radar/internal/project"
	"github.com/Jake-Network/radar/internal/verification"
)

func (a *app) plan(o options) int {
	intent := strings.TrimSpace(strings.Join(o.args, " "))
	if intent == "" {
		return a.fail(errors.New("plan requires a feature description"))
	}
	s, e := a.snapshot(o.ref)
	if e != nil {
		return a.fail(e)
	}
	if e = a.store.SaveSnapshot(a.ctx, s); e != nil {
		return a.fail(e)
	}
	p := planning.Generate(intent, s)
	rel := o.output
	if rel == "" {
		rel = ".radar/plans/" + p.FeatureID + ".plan.json"
	}
	path, e := project.SafePath(a.root, rel)
	if e != nil {
		return a.fail(e)
	}
	if e = writeNewJSON(path, p); e != nil {
		return a.fail(e)
	}
	contextPath := strings.TrimSuffix(path, ".json") + ".context.json"
	bundle := map[string]any{"snapshot": s, "capabilities": languages.Capabilities(), "context": p.Context, "design_status": "incomplete: agent investigation and review required"}
	if e = writeNewJSON(contextPath, bundle); e != nil {
		return a.fail(e)
	}
	if e = a.store.SaveArtifact(a.ctx, p.FeatureID, "plan", p); e != nil {
		return a.fail(e)
	}
	message := "Grounded bundle created. Supply architecture decisions, alternatives, task proofs and verification criteria before design review."
	a.report(map[string]any{"plan": p, "plan_path": path, "context_path": contextPath, "status": "incomplete", "message": message}, func(w io.Writer) {
		fmt.Fprintf(w, "Plan:    %s\nContext: %s\nStatus:  incomplete (%d context matches at %s)\n%s\n", path, contextPath, len(p.Context.Matches), s.Revision, message)
		fmt.Fprintf(w, "Next: fill the plan, then run `radar preflight --plan %s`.\n", path)
	})
	return 0
}

// planEvidenceIDs merges --evidence with IDs attached to plan rules.
func planEvidenceIDs(p planning.Plan, flag string) []string {
	ids := []string{}
	for _, id := range strings.Split(flag, ",") {
		if id = strings.TrimSpace(id); id != "" {
			ids = append(ids, id)
		}
	}
	for _, c := range p.Acceptance {
		if c.Rule != nil && c.Rule.EvidenceID != "" {
			ids = append(ids, c.Rule.EvidenceID)
		}
	}
	for _, c := range p.Constraints {
		if c.Rule != nil && c.Rule.EvidenceID != "" {
			ids = append(ids, c.Rule.EvidenceID)
		}
	}
	return ids
}

func planCommand(command string) func(*app, options) int {
	return func(a *app, o options) int {
		p, e := a.loadPlan(o.plan)
		if e != nil {
			return a.fail(e)
		}
		switch command {
		case "tasks":
			schedule, e := planning.Tasks(p)
			if e != nil {
				return a.fail(e)
			}
			a.report(schedule, func(w io.Writer) { renderSchedule(w, schedule) })
			return 0
		case "approve":
			return a.approve(p, o)
		}
		s, e := a.snapshot(o.ref)
		if e != nil {
			return a.fail(e)
		}
		var r planning.Report
		if command == "verify" {
			records, e := a.records(planEvidenceIDs(p, o.evidence))
			if e != nil {
				return a.fail(e)
			}
			r = verification.VerifyWithEvidence(a.ctx, p, s, a.root, records)
		} else {
			r = planning.Validate(p, s)
		}
		r.Revision = s.Revision
		r.BaseRevision = p.BaseRevision
		r.PlanDigest = planning.Digest(p)
		if e = a.store.SaveFindings(a.ctx, r.Findings); e != nil {
			return a.fail(e)
		}
		if e = a.store.SaveArtifact(a.ctx, p.FeatureID+":"+command+":"+s.Revision, command, r); e != nil {
			return a.fail(e)
		}
		a.report(r, func(w io.Writer) { renderReport(w, command, r) })
		if r.Status == model.StatusError || r.Status == model.StatusTimeout {
			return 2
		}
		if r.Status == model.StatusFailed {
			return 1
		}
		return 0
	}
}

func (a *app) approve(p planning.Plan, o options) int {
	if strings.TrimSpace(o.reviewer) == "" {
		return a.fail(errors.New("--reviewer is required for an explicit local review declaration"))
	}
	s, e := a.snapshot(p.BaseRevision)
	if e != nil {
		return a.fail(e)
	}
	candidate := p
	candidate.Approval = &planning.Approval{Reviewer: o.reviewer, ReviewedAt: time.Now().UTC().Format(time.RFC3339), Checkpoint: p.BaseRevision, PlanDigest: planning.Digest(p)}
	r := planning.Validate(candidate, s)
	if r.Status == model.StatusFailed || !planning.Approved(candidate) {
		if r.Status != model.StatusFailed && len(candidate.Incomplete) > 0 {
			r.NextSteps = append([]string{"Approval requires an empty `incomplete` list."}, r.NextSteps...)
		}
		a.report(r, func(w io.Writer) { renderReport(w, "approve", r) })
		return 1
	}
	rel := o.output
	if rel == "" {
		rel = ".radar/plans/" + candidate.FeatureID + ".approved." + planning.Digest(candidate)[:12] + ".json"
	}
	path, e := project.SafePath(a.root, rel)
	if e != nil {
		return a.fail(e)
	}
	if e = writeNewJSON(path, candidate); e != nil {
		return a.fail(e)
	}
	message := "Local review declaration recorded; reviewer identity is not authenticated and this does not authorize Git mutations."
	a.report(map[string]any{"status": "review_declared", "plan_path": path, "plan": candidate, "message": message}, func(w io.Writer) {
		fmt.Fprintf(w, "Reviewed plan written to %s\n%s\n", path, message)
	})
	return 0
}
