package cli

import (
	"errors"
	"github.com/radar-engine/radar/internal/languages"
	"github.com/radar-engine/radar/internal/planning"
	"github.com/radar-engine/radar/internal/project"
	"github.com/radar-engine/radar/internal/verification"
	"strings"
	"time"
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
	a.emit(map[string]any{"plan": p, "plan_path": path, "context_path": contextPath, "status": "incomplete", "message": "Grounded bundle created. Supply architecture decisions, alternatives, task proofs and verification criteria before design review."})
	return 0
}
func (a *app) planCommand(command string, o options) int {
	p, e := a.loadPlan(o.plan)
	if e != nil {
		return a.fail(e)
	}
	if command == "tasks" {
		schedule, e := planning.Tasks(p)
		if e != nil {
			return a.fail(e)
		}
		a.emit(schedule)
		return 0
	}
	if command == "approve" {
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
		if r.Status == "failed" || !planning.Approved(candidate) {
			a.emit(r)
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
		a.emit(map[string]any{"status": "review_declared", "plan_path": path, "plan": candidate, "message": "Local review declaration recorded; reviewer identity is not authenticated and this does not authorize Git mutations."})
		return 0
	}
	s, e := a.snapshot(o.ref)
	if e != nil {
		return a.fail(e)
	}
	var r planning.Report
	if command == "verify" {
		ids := o.evidence
		for _, c := range p.Acceptance {
			if c.Rule != nil && c.Rule.EvidenceID != "" {
				if ids != "" {
					ids += ","
				}
				ids += c.Rule.EvidenceID
			}
		}
		for _, c := range p.Constraints {
			if c.Rule != nil && c.Rule.EvidenceID != "" {
				if ids != "" {
					ids += ","
				}
				ids += c.Rule.EvidenceID
			}
		}
		records, e := a.records(ids)
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
	a.emit(r)
	if r.Status == "failed" {
		return 1
	}
	return 0
}
