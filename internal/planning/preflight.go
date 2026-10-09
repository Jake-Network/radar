// Package planning validates declared intent against evidence, without inventing a design.
package planning

import (
	"encoding/json"
	"fmt"
	"github.com/radar-engine/radar/internal/model"
	"strings"
)

func Validate(p Plan, s model.Snapshot) Report {
	r := Report{Status: "passed", Revision: s.Revision, BaseRevision: p.BaseRevision, PlanDigest: Digest(p), Findings: []model.Finding{}, Checks: []Check{}}
	add := func(code, msg, severity string) {
		evidence := model.VerifiedStatic
		switch code {
		case "unresolved_assumption", "incomplete_design", "unknown_component", "unresolved_consumer", "unverified_criterion", "requirement_verification":
			evidence = model.Unknown
		}
		f := model.NewFinding(code, msg, evidence)
		f.ID = model.StableID(s.Repository, r.PlanDigest, s.Revision, code, msg)
		f.Severity = severity
		r.Findings = append(r.Findings, f)
		if severity == "error" {
			r.Status = "failed"
		} else if r.Status == "passed" {
			r.Status = "warning"
		}
	}
	if p.SchemaVersion != SchemaVersion {
		add("plan_schema", "unsupported schema_version; expected 1", "error")
	}
	if strings.TrimSpace(p.Intent) == "" || p.FeatureID == "" {
		add("plan_identity", "feature_id and intent are required", "error")
	}
	if p.BaseRevision == "" || p.BaseRevision != s.Revision {
		add("stale_context", "plan base_revision does not match indexed revision", "error")
	}
	req := map[string]bool{}
	criteria := map[string]Criterion{}
	nodes := map[string]model.Node{}
	for _, n := range s.Nodes {
		nodes[n.ID] = n
	}
	for _, q := range p.Requirements {
		if q.ID == "" || q.Intent == "" || req[q.ID] {
			add("requirement_identity", "requirements need unique IDs and intent", "error")
		}
		req[q.ID] = true
	}
	for _, c := range p.Acceptance {
		if c.Rule != nil {
			if err := ValidateRule(*c.Rule); err != nil {
				add("verification_rule", err.Error(), "error")
			}
		}
		if c.ID == "" || c.Intent == "" {
			add("criterion_identity", "acceptance criteria need ID and intent", "error")
		}
		if _, ok := criteria[c.ID]; ok {
			add("criterion_identity", "duplicate acceptance criterion "+c.ID, "error")
		}
		criteria[c.ID] = c
		if !req[c.Requirement] {
			add("criterion_requirement", "unknown requirement "+c.Requirement, "error")
		}
		if c.Rule == nil {
			add("unverified_criterion", "criterion "+c.ID+" has no deterministic verification rule", "warning")
		}
	}
	if len(p.Requirements) == 0 {
		add("requirements_missing", "at least one requirement is required", "error")
	}
	constraintIDs := map[string]bool{}
	for _, c := range p.Constraints {
		if c.ID == "" || c.Intent == "" || constraintIDs[c.ID] {
			add("constraint_identity", "constraints need ID and intent", "error")
		}
		constraintIDs[c.ID] = true
		if c.Rule != nil {
			if err := ValidateRule(*c.Rule); err != nil {
				add("constraint_rule", err.Error(), "error")
			}
		}
	}
	for _, q := range p.Requirements {
		covered := false
		for _, c := range p.Acceptance {
			if c.Requirement == q.ID && c.Rule != nil {
				covered = true
			}
		}
		if !covered {
			sev := "warning"
			if q.SecuritySensitive {
				sev = "error"
			}
			add("requirement_verification", "requirement "+q.ID+" lacks a verification criterion", sev)
		}
	}
	for _, t := range p.Tasks {
		if t.Intent == "" || len(t.Requirements) == 0 || len(t.Acceptance) == 0 {
			add("task_proof", "task "+t.ID+" needs intent, requirements and acceptance", "error")
		}
		for _, id := range t.Requirements {
			if !req[id] {
				add("task_requirement", "task "+t.ID+" references missing requirement "+id, "error")
			}
		}
		for _, id := range t.Acceptance {
			if criterion, ok := criteria[id]; !ok {
				add("task_acceptance", "task "+t.ID+" references missing criterion "+id, "error")
			} else {
				linked := false
				for _, requirement := range t.Requirements {
					if criterion.Requirement == requirement {
						linked = true
					}
				}
				if !linked {
					add("task_acceptance", "task "+t.ID+" criterion "+id+" verifies a requirement not declared by that task", "error")
				}
			}
		}
		for _, id := range t.Components {
			if _, ok := nodes[id]; !ok {
				add("unknown_component", "task "+t.ID+" component is unresolved: "+id, "warning")
			}
		}
		if t.Consequential && !Approved(p) {
			add("review_required", "consequential task "+t.ID+" requires a digest-bound checkpoint review declaration", "error")
		}
	}
	if _, err := Tasks(p); err != nil {
		add("task_dag", err.Error(), "error")
	}
	// Unordered writers of a shared contract require explicit coordination.
	byID := map[string]Task{}
	for _, t := range p.Tasks {
		byID[t.ID] = t
	}
	var depends func(string, string, map[string]bool) bool
	depends = func(a, b string, seen map[string]bool) bool {
		if seen[a] {
			return false
		}
		seen[a] = true
		for _, d := range byID[a].DependsOn {
			if d == b || depends(d, b, seen) {
				return true
			}
		}
		return false
	}
	for i, a := range p.Tasks {
		for _, b := range p.Tasks[i+1:] {
			for _, c := range a.Contracts {
				for _, d := range b.Contracts {
					if c == d && !depends(a.ID, b.ID, map[string]bool{}) && !depends(b.ID, a.ID, map[string]bool{}) {
						add("contract_owner_conflict", fmt.Sprintf("unordered tasks %s and %s both own contract %s", a.ID, b.ID, c), "error")
					}
				}
			}
		}
	}
	if _, err := Project(p, s); err != nil {
		add("graph_projection", err.Error(), "error")
	}
	deltaIDs := map[string]bool{}
	for _, d := range p.ContractDeltas {
		if deltaIDs[d.Contract] {
			add("contract_delta_duplicate", "contract has multiple projections: "+d.Contract, "error")
		}
		deltaIDs[d.Contract] = true
		if d.Schema != "" {
			if err := ValidateRule(Rule{Kind: "file_exists", Path: d.Schema}); err != nil {
				add("contract_delta", err.Error(), "error")
			}
			if err := ValidateRule(Rule{Kind: "json_property", Path: d.Schema, Pointer: d.Pointer, Property: "_validation"}); err != nil {
				add("contract_delta", err.Error(), "error")
			}
			if d.Operation != "remove" {
				var value map[string]any
				if json.Unmarshal(d.Expected, &value) != nil || value == nil {
					add("contract_delta", "projected schema add/modify requires expected JSON object", "error")
				}
			}
			if d.Operation == "remove" && len(d.Expected) > 0 {
				add("contract_delta", "remove delta must omit expected schema", "error")
			}
		} else if d.Pointer != "" || len(d.Expected) > 0 {
			add("contract_delta", "projected pointer/expected require schema path", "error")
		}
		if d.Operation != "add" && d.Operation != "remove" && d.Operation != "modify" {
			add("contract_delta", "unsupported contract operation "+d.Operation, "error")
		}
		if d.Contract == "" || d.Description == "" {
			add("contract_delta", "contract delta requires contract and description", "error")
		}
		for _, c := range d.Consumers {
			if _, ok := nodes[c]; !ok {
				add("unresolved_consumer", "contract consumer is unresolved: "+c, "warning")
			}
		}
	}
	for _, f := range projectedConsumers(p, s) {
		r.Findings = append(r.Findings, f)
		r.Status = "failed"
	}
	consequential := false
	for _, t := range p.Tasks {
		if t.Consequential {
			consequential = true
		}
	}
	decisionIDs := map[string]bool{}
	for _, d := range p.Decisions {
		if d.ID == "" || d.Intent == "" || decisionIDs[d.ID] {
			add("decision_identity", "architecture decisions need unique IDs and intent", "error")
		}
		decisionIDs[d.ID] = true
		if consequential && (len(d.Alternatives) == 0 || len(d.Tradeoffs) == 0) {
			add("decision_review", "consequential design decision "+d.ID+" requires alternatives and tradeoffs", "error")
		}
	}
	if consequential && len(p.Decisions) == 0 {
		add("decision_review", "consequential work requires an architecture decision with alternatives and tradeoffs", "error")
	}
	for _, a := range p.Assumptions {
		add("unresolved_assumption", a, "warning")
	}
	for _, field := range p.Incomplete {
		add("incomplete_design", "design field incomplete: "+field, "warning")
	}
	if p.Approval != nil && !Approved(p) {
		add("invalid_review", "review declaration does not match plan digest/checkpoint or design is incomplete", "error")
	}
	r.Authoritative = false
	return r
}

// projectedConsumers detects explicit field dependency contradictions in a proposed direct schema.
// The consumer relation is a declared dependency; runtime use is not asserted.
func projectedConsumers(p Plan, s model.Snapshot) []model.Finding {
	out := []model.Finding{}
	digest := Digest(p)
	for _, d := range p.ContractDeltas {
		if d.Schema == "" || (d.Operation != "remove" && len(d.Expected) == 0) {
			continue
		}
		var expected map[string]any
		if d.Operation != "remove" {
			if json.Unmarshal(d.Expected, &expected) != nil || expected == nil {
				continue
			}
			if _, ref := expected["$ref"]; ref {
				continue
			}
		}
		props, _ := expected["properties"].(map[string]any)
		for _, field := range s.Nodes {
			if field.Kind != "schema_field" || field.Provenance.Path != d.Schema {
				continue
			}
			prefix := d.Pointer + "/properties/"
			pointer := field.Properties["json_pointer"]
			if !strings.HasPrefix(pointer, prefix) || strings.Contains(strings.TrimPrefix(pointer, prefix), "/") {
				continue
			}
			if _, ok := props[field.Name]; ok {
				continue
			}
			for _, edge := range s.Edges {
				if edge.Kind != "CONSUMES" || edge.To != field.ID {
					continue
				}
				f := model.NewFinding("projected_consumed_field_removed", fmt.Sprintf("Proposed contract %s removes field %s required by declared consumer %s; runtime use remains unproven", d.Contract, field.Name, edge.From), model.VerifiedStatic)
				f.Severity = "error"
				f.ID = model.StableID(s.Repository, digest, s.Revision, d.Contract, field.ID, edge.From)
				f.Contract = d.Contract
				f.Consumer = edge.From
				f.Producer = d.Schema
				f.Locations = []model.Provenance{field.Provenance, edge.Provenance}
				f.Remediation = "Preserve the field or coordinate a compatible consumer migration and update the declared binding."
				f.Verification = "Inspect explicit contract binding and run consumer contract tests against projected schema."
				out = append(out, f)
			}
		}
	}
	return out
}
