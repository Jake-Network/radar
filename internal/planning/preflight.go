// Package planning validates declared intent against evidence, without inventing a design.
package planning

import (
	"encoding/json"
	"fmt"
	"github.com/Jake-Network/radar/internal/model"
	"strings"
)

// Validate checks a plan against the indexed snapshot. Findings are reported
// in a fixed order: identity, requirements and criteria, constraints,
// coverage, tasks and their coordination, contract deltas, decisions,
// assumptions and review state.
func Validate(p Plan, s model.Snapshot) Report {
	r := Report{Status: model.StatusPassed, Revision: s.Revision, BaseRevision: p.BaseRevision, PlanDigest: Digest(p), Findings: []model.Finding{}, Checks: []Check{}}
	v := &validator{p: p, s: s, r: &r, req: map[string]bool{}, criteria: map[string]Criterion{}, nodes: map[string]model.Node{}}
	v.identity()
	for _, n := range s.Nodes {
		v.nodes[n.ID] = n
	}
	v.requirementsAndCriteria()
	v.constraints()
	v.coverage()
	v.tasks()
	if _, err := Tasks(p); err != nil {
		v.add("task_dag", err.Error(), model.SeverityError)
	}
	v.coordination()
	if _, err := Project(p, s); err != nil {
		v.add("graph_projection", err.Error(), model.SeverityError)
	}
	v.contractDeltas()
	for _, f := range projectedConsumers(p, s) {
		r.Findings = append(r.Findings, f)
		r.Status = model.StatusFailed
	}
	v.decisions()
	v.assumptions()
	for _, field := range p.Incomplete {
		v.add("incomplete_design", "design field incomplete: "+field, model.SeverityWarning)
	}
	if p.Approval != nil && !Approved(p) {
		v.add("invalid_review", "review declaration does not match plan digest/checkpoint or design is incomplete", model.SeverityError)
	}
	r.Authoritative = false
	r.NextSteps = NextSteps(r.Findings)
	return r
}

// validator accumulates preflight findings for one plan and snapshot.
type validator struct {
	p        Plan
	s        model.Snapshot
	r        *Report
	req      map[string]bool
	criteria map[string]Criterion
	nodes    map[string]model.Node
}

func (v *validator) add(code, msg string, severity model.Severity) {
	s, r := v.s, v.r
	evidence := model.VerifiedStatic
	switch code {
	case "component_owner_conflict", "integration_verification_missing", "unresolved_assumption", "incomplete_design", "unknown_component", "unresolved_consumer", "unverified_criterion", "requirement_verification":
		evidence = model.Unknown
	}
	f := model.NewFinding(code, msg, evidence)
	f.ID = model.StableID(s.Repository, r.PlanDigest, s.Revision, code, msg)
	f.Severity = severity
	f.Remediation = nextStepHints[code]
	if f.Remediation != "" {
		f.Verification = "Run radar preflight again against the same baseline after repair; implementation checks still require radar check and exact-state test evidence."
	}
	r.Findings = append(r.Findings, f)
	if severity == model.SeverityError {
		r.Status = model.StatusFailed
	} else if r.Status == model.StatusPassed {
		r.Status = model.StatusWarning
	}
}

// identity checks the plan schema, identity and baseline.
func (v *validator) identity() {
	p, s, add := v.p, v.s, v.add
	if p.SchemaVersion != SchemaVersion {
		add("plan_schema", "unsupported schema_version; expected 1", model.SeverityError)
	}
	if strings.TrimSpace(p.Intent) == "" || p.FeatureID == "" {
		add("plan_identity", "feature_id and intent are required", model.SeverityError)
	}
	if p.BaseRevision == "" || p.BaseRevision != s.Revision {
		add("stale_context", "plan base_revision does not match indexed revision", model.SeverityError)
	}
}

// requirementsAndCriteria checks requirement and acceptance criterion identities and links.
func (v *validator) requirementsAndCriteria() {
	p, add, req, criteria := v.p, v.add, v.req, v.criteria
	for _, q := range p.Requirements {
		if q.ID == "" || q.Intent == "" || req[q.ID] {
			add("requirement_identity", "requirements need unique IDs and intent", model.SeverityError)
		}
		req[q.ID] = true
	}
	for _, c := range p.Acceptance {
		if c.Rule != nil {
			if err := ValidateRule(*c.Rule); err != nil {
				add("verification_rule", err.Error(), model.SeverityError)
			}
		}
		if c.ID == "" || c.Intent == "" {
			add("criterion_identity", "acceptance criteria need ID and intent", model.SeverityError)
		}
		if _, ok := criteria[c.ID]; ok {
			add("criterion_identity", "duplicate acceptance criterion "+c.ID, model.SeverityError)
		}
		criteria[c.ID] = c
		if !req[c.Requirement] {
			add("criterion_requirement", "unknown requirement "+c.Requirement, model.SeverityError)
		}
		if c.Rule == nil {
			add("unverified_criterion", "criterion "+c.ID+" has no deterministic verification rule", model.SeverityWarning)
		}
	}
	if len(p.Requirements) == 0 {
		add("requirements_missing", "at least one requirement is required", model.SeverityError)
	}
}

// constraints checks constraint identities and rules.
func (v *validator) constraints() {
	p, add := v.p, v.add
	constraintIDs := map[string]bool{}
	for _, c := range p.Constraints {
		if c.ID == "" || c.Intent == "" || constraintIDs[c.ID] {
			add("constraint_identity", "constraints need ID and intent", model.SeverityError)
		}
		constraintIDs[c.ID] = true
		if c.Rule != nil {
			if err := ValidateRule(*c.Rule); err != nil {
				add("constraint_rule", err.Error(), model.SeverityError)
			}
		}
	}
}

// coverage requires a verification criterion for every requirement.
func (v *validator) coverage() {
	p, add := v.p, v.add
	for _, q := range p.Requirements {
		covered := false
		for _, c := range p.Acceptance {
			if c.Requirement == q.ID && c.Rule != nil {
				covered = true
			}
		}
		if !covered {
			sev := model.SeverityWarning
			if q.SecuritySensitive {
				sev = model.SeverityError
			}
			add("requirement_verification", "requirement "+q.ID+" lacks a verification criterion", sev)
		}
	}
}

// tasks checks that each task is provable, linked and reviewed.
func (v *validator) tasks() {
	p, add, req, criteria, nodes := v.p, v.add, v.req, v.criteria, v.nodes
	for _, t := range p.Tasks {
		if t.Intent == "" || len(t.Requirements) == 0 || len(t.Acceptance) == 0 {
			add("task_proof", "task "+t.ID+" needs intent, requirements and acceptance", model.SeverityError)
		}
		for _, id := range t.Requirements {
			if !req[id] {
				add("task_requirement", "task "+t.ID+" references missing requirement "+id, model.SeverityError)
			}
		}
		for _, id := range t.Acceptance {
			if criterion, ok := criteria[id]; !ok {
				add("task_acceptance", "task "+t.ID+" references missing criterion "+id, model.SeverityError)
			} else {
				linked := false
				for _, requirement := range t.Requirements {
					if criterion.Requirement == requirement {
						linked = true
					}
				}
				if !linked {
					add("task_acceptance", "task "+t.ID+" criterion "+id+" verifies a requirement not declared by that task", model.SeverityError)
				}
			}
		}
		for _, id := range t.Components {
			if _, ok := nodes[id]; !ok {
				add("unknown_component", "task "+t.ID+" component is unresolved: "+id, model.SeverityWarning)
			}
		}
		if t.Consequential && !Approved(p) {
			add("review_required", "consequential task "+t.ID+" requires a digest-bound checkpoint review declaration", model.SeverityError)
		}
	}
}

// coordination flags unordered tasks that share components, contracts or only
// separate test criteria.
func (v *validator) coordination() {
	p, add, criteria := v.p, v.add, v.criteria
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
			ordered := depends(a.ID, b.ID, map[string]bool{}) || depends(b.ID, a.ID, map[string]bool{})
			if !ordered {
				for _, component := range a.Components {
					for _, other := range b.Components {
						if component == other {
							add("component_owner_conflict", fmt.Sprintf("unordered tasks %s and %s both declare component %s; write compatibility is unproven", a.ID, b.ID, component), model.SeverityWarning)
						}
					}
				}
				// Separate passing test commands cannot establish their combined behavior.
				separateTests := false
				sharedTest := false
				for _, ac := range a.Acceptance {
					ca := criteria[ac]
					for _, bc := range b.Acceptance {
						cb := criteria[bc]
						if ca.Rule == nil || cb.Rule == nil || ca.Rule.Kind != "test_run" || cb.Rule.Kind != "test_run" {
							continue
						}
						if ac == bc {
							sharedTest = true
						} else {
							separateTests = true
						}
					}
				}
				if separateTests && !sharedTest {
					add("integration_verification_missing", fmt.Sprintf("parallel tasks %s and %s declare separate test criteria without a shared integration criterion; combined behavior remains unknown", a.ID, b.ID), model.SeverityWarning)
				}
			}
			for _, c := range a.Contracts {
				for _, d := range b.Contracts {
					if c == d && !depends(a.ID, b.ID, map[string]bool{}) && !depends(b.ID, a.ID, map[string]bool{}) {
						add("contract_owner_conflict", fmt.Sprintf("unordered tasks %s and %s both own contract %s", a.ID, b.ID, c), model.SeverityError)
					}
				}
			}
		}
	}
}

// contractDeltas checks projected contract changes.
func (v *validator) contractDeltas() {
	p, add, nodes := v.p, v.add, v.nodes
	deltaIDs := map[string]bool{}
	for _, d := range p.ContractDeltas {
		if deltaIDs[d.Contract] {
			add("contract_delta_duplicate", "contract has multiple projections: "+d.Contract, model.SeverityError)
		}
		deltaIDs[d.Contract] = true
		if d.Schema != "" {
			if err := ValidateRule(Rule{Kind: "file_exists", Path: d.Schema}); err != nil {
				add("contract_delta", err.Error(), model.SeverityError)
			}
			if err := ValidateRule(Rule{Kind: "json_property", Path: d.Schema, Pointer: d.Pointer, Property: "_validation"}); err != nil {
				add("contract_delta", err.Error(), model.SeverityError)
			}
			if d.Operation != "remove" {
				var value map[string]any
				if json.Unmarshal(d.Expected, &value) != nil || value == nil {
					add("contract_delta", "projected schema add/modify requires expected JSON object", model.SeverityError)
				}
			}
			if d.Operation == "remove" && len(d.Expected) > 0 {
				add("contract_delta", "remove delta must omit expected schema", model.SeverityError)
			}
		} else if d.Pointer != "" || len(d.Expected) > 0 {
			add("contract_delta", "projected pointer/expected require schema path", model.SeverityError)
		}
		if d.Operation != "add" && d.Operation != "remove" && d.Operation != "modify" {
			add("contract_delta", "unsupported contract operation "+d.Operation, model.SeverityError)
		}
		if d.Contract == "" || d.Description == "" {
			add("contract_delta", "contract delta requires contract and description", model.SeverityError)
		}
		for _, c := range d.Consumers {
			if _, ok := nodes[c]; !ok {
				add("unresolved_consumer", "contract consumer is unresolved: "+c, model.SeverityWarning)
			}
		}
	}
}

// decisions requires reviewed architecture decisions for consequential work.
func (v *validator) decisions() {
	p, add := v.p, v.add
	consequential := false
	for _, t := range p.Tasks {
		if t.Consequential {
			consequential = true
		}
	}
	decisionIDs := map[string]bool{}
	for _, d := range p.Decisions {
		if d.ID == "" || d.Intent == "" || decisionIDs[d.ID] {
			add("decision_identity", "architecture decisions need unique IDs and intent", model.SeverityError)
		}
		decisionIDs[d.ID] = true
		if consequential && (len(d.Alternatives) == 0 || len(d.Tradeoffs) == 0) {
			add("decision_review", "consequential design decision "+d.ID+" requires alternatives and tradeoffs", model.SeverityError)
		}
	}
	if consequential && len(p.Decisions) == 0 {
		add("decision_review", "consequential work requires an architecture decision with alternatives and tradeoffs", model.SeverityError)
	}
}

// assumptions checks assumption identity, status and resolution.
func (v *validator) assumptions() {
	p, add := v.p, v.add
	assumptionIDs := map[string]bool{}
	for _, a := range p.Assumptions {
		if strings.TrimSpace(a.Text) == "" {
			add("assumption_identity", "assumptions need text", model.SeverityError)
		}
		if a.ID != "" {
			if assumptionIDs[a.ID] {
				add("assumption_identity", "duplicate assumption "+a.ID, model.SeverityError)
			}
			assumptionIDs[a.ID] = true
		}
		switch a.Status {
		case "", AssumptionOpen:
			add("unresolved_assumption", assumptionLabel(a)+a.Text, model.SeverityWarning)
		case AssumptionAccepted, AssumptionResolved:
			if strings.TrimSpace(a.Resolution) == "" {
				add("assumption_resolution", "assumption "+assumptionLabel(a)+"is "+a.Status+" without a resolution explaining the evidence or accepted risk", model.SeverityError)
			}
		default:
			add("assumption_status", "assumption "+assumptionLabel(a)+"has unsupported status "+a.Status+"; use open, accepted or resolved", model.SeverityError)
		}
	}
}

func assumptionLabel(a Assumption) string {
	if a.ID == "" {
		return ""
	}
	return "[" + a.ID + "] "
}

var nextStepHints = map[string]string{
	"component_owner_conflict":         "Assign one owner to shared components, split ownership at resolvable symbols, or order the tasks with depends_on.",
	"integration_verification_missing": "Declare a shared test_run acceptance criterion and run it against the exact combined source state using radar merge-check --verify --allow-execution.",
	"stale_context":                    "Re-index and regenerate context, or set base_revision to the analyzed revision (`radar index --ref SHA`).",
	"incomplete_design":                "Fill the missing design fields, then remove them from `incomplete`.",
	"unresolved_assumption":            "Investigate each open assumption; set status to `resolved` (with evidence) or `accepted` (with the risk owner's rationale) in `resolution`.",
	"requirement_verification":         "Add an acceptance criterion with a deterministic `rule` (file_exists, json_property, graph_entity or test_run) for each uncovered requirement.",
	"unverified_criterion":             "Attach a `rule` to criteria that only have prose intent.",
	"unknown_component":                "Use `radar resolve QUERY` to find valid entity IDs for task components.",
	"unresolved_consumer":              "Use `radar resolve QUERY` to find the consumer file IDs.",
	"review_required":                  "Ask a human to review the plan, then run `radar approve --plan PATH --reviewer NAME`.",
	"invalid_review":                   "The plan changed after review; request a new review and re-run `radar approve`.",
	"contract_owner_conflict":          "Order tasks that share a contract with `depends_on`, or assign the contract to one task.",
	"decision_review":                  "Record alternatives and tradeoffs for consequential decisions.",
	"task_dag":                         "Fix task IDs and `depends_on` so tasks form an acyclic graph.",
	"projected_consumed_field_removed": "Keep the field in the projected schema or migrate the declared consumer first.",
}

// NextSteps turns findings into ordered, de-duplicated actions for an agent.
func NextSteps(findings []model.Finding) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, f := range findings {
		hint, ok := nextStepHints[f.Code]
		if !ok || seen[hint] {
			continue
		}
		seen[hint] = true
		out = append(out, hint)
	}
	return out
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
