package planning

import (
	"encoding/json"
	"github.com/radar-engine/radar/internal/model"
	"strings"
	"testing"
)

func TestGroundedContextRanksMatchesWithoutSemanticClaims(t *testing.T) {
	s := model.Snapshot{Revision: "base", Nodes: []model.Node{{ID: "unrelated", Name: "Payments"}, {ID: "export", Name: "organizationExport", Provenance: model.Provenance{Path: "exports/service.py", Evidence: model.VerifiedStatic}}}}
	p := Generate("organization exports", s)
	if len(p.Context.Matches) != 1 || p.Context.Matches[0].Entity != "export" || p.Context.PlanningDepth != "extended" {
		t.Fatal(p.Context)
	}
	if len(p.Context.Unknown) == 0 || !strings.Contains(p.Context.Matches[0].Explanation, "unverified") {
		t.Fatal("semantic certainty implied")
	}
	if len(p.Evidence) != 1 {
		t.Fatal("unrelated source polluted evidence")
	}
}
func TestProjectedRemovalUsesExplicitConsumedField(t *testing.T) {
	p := validPlan()
	p.ContractDeltas = []ContractDelta{{Contract: "orders", Schema: "openapi.json", Pointer: "/components/schemas/Order", Operation: "modify", Description: "migrate field", Expected: json.RawMessage(`{"type":"object","properties":{"total_cents":{"type":"integer"}}}`)}}
	s := model.Snapshot{Revision: "base", Nodes: []model.Node{{ID: "consumer", Kind: "file", Name: "consumer.ts"}, {ID: "field", Kind: "schema_field", Name: "total", Properties: map[string]string{"json_pointer": "/components/schemas/Order/properties/total"}, Provenance: model.Provenance{Path: "openapi.json", Evidence: model.VerifiedStatic}}}, Edges: []model.Edge{{ID: "e", From: "consumer", To: "field", Kind: "CONSUMES", Provenance: model.Provenance{Path: ".radar/contracts.json", Evidence: model.VerifiedStatic}}}}
	r := Validate(p, s)
	found := false
	for _, f := range r.Findings {
		if f.Code == "projected_consumed_field_removed" {
			found = true
			if f.Consumer != "consumer" || f.Evidence != model.VerifiedStatic || len(f.Locations) != 2 {
				t.Fatal(f)
			}
		}
	}
	if !found {
		t.Fatal("declared removed field not found", r)
	}
	p.ContractDeltas[0].Expected = json.RawMessage(`{"properties":{"total":{}}}`)
	if r := Validate(p, s); r.Status != "passed" {
		t.Fatal("preserved field false positive", r)
	}
	s.Edges = nil
	p.ContractDeltas[0].Expected = json.RawMessage(`{"properties":{}}`)
	if r := Validate(p, s); r.Status != "passed" {
		t.Fatal("invented consumer", r)
	}
}
func TestProjectedDeltaAndDecisionValidation(t *testing.T) {
	p := validPlan()
	p.ContractDeltas = []ContractDelta{{Contract: "orders", Schema: "../escape", Operation: "modify", Description: "change"}}
	if Validate(p, model.Snapshot{Revision: "base"}).Status != "failed" {
		t.Fatal("invalid expected/path accepted")
	}
	p = validPlan()
	p.Tasks[0].Consequential = true
	p.Approval = &Approval{Reviewer: "reviewer", ReviewedAt: "2026-10-09T00:00:00Z", Checkpoint: p.BaseRevision, PlanDigest: Digest(p)}
	if Validate(p, model.Snapshot{Revision: "base"}).Status != "failed" {
		t.Fatal("missing design alternatives accepted")
	}
	p.Decisions = []Decision{{ID: "design", Intent: "choose", Alternatives: []string{"alternative"}, Tradeoffs: []string{"cost"}}}
	p.Approval.PlanDigest = Digest(p)
	if r := Validate(p, model.Snapshot{Revision: "base"}); r.Status != "passed" {
		t.Fatal(r)
	}
	p.Constraints = []Constraint{{ID: "same", Intent: "one"}, {ID: "same", Intent: "two"}}
	if Validate(p, model.Snapshot{Revision: "base"}).Status != "failed" {
		t.Fatal("duplicate constraints accepted")
	}
}
func TestTestRuleRequiresArgv(t *testing.T) {
	for _, r := range []Rule{{Kind: "test_run"}, {Kind: "test_run", Command: []string{""}}, {Kind: "test_run", Command: []string{"go", "bad\x00arg"}}} {
		if ValidateRule(r) == nil {
			t.Fatal("invalid argv accepted", r)
		}
	}
	if e := ValidateRule(Rule{Kind: "test_run", Command: []string{"go", "test", "-json", "./..."}}); e != nil {
		t.Fatal(e)
	}
}

func TestProofTaskCriteriaBelongToDeclaredRequirements(t *testing.T) {
	p := validPlan()
	p.Requirements = append(p.Requirements, Requirement{ID: "other", Intent: "unrelated"})
	p.Acceptance[0].Requirement = "other"
	if r := Validate(p, model.Snapshot{Revision: "base"}); r.Status != "failed" {
		t.Fatal("unrelated criterion accepted as task proof", r)
	}
	p = validPlan()
	p.Assumptions = []string{"Runtime organization authorization unverified"}
	r := Validate(p, model.Snapshot{Revision: "base"})
	for _, f := range r.Findings {
		if f.Code == "unresolved_assumption" && f.Evidence != model.Unknown {
			t.Fatal("assumption mislabeled as static evidence", f)
		}
	}
}

func TestDigestEvidenceAttachmentDoesNotChangeDesign(t *testing.T) {
	p := validPlan()
	p.Acceptance[0].Rule = &Rule{Kind: "test_run", Command: []string{"go", "test", "-json", "./..."}}
	before := Digest(p)
	p.Acceptance[0].Rule.EvidenceID = "record-1"
	if Digest(p) != before || p.Acceptance[0].Rule.EvidenceID != "record-1" {
		t.Fatal("evidence linkage changed design or mutated input")
	}
	p.Acceptance[0].Rule.Command = []string{"go", "test", "-json", "./different"}
	if Digest(p) == before {
		t.Fatal("test command change not bound to digest")
	}
}

func TestDuplicateProjectionRejectedAndPacketContainsDesign(t *testing.T) {
	p := Plan{SchemaVersion: "1", FeatureID: "f", Intent: "intent", BaseRevision: "base", Requirements: []Requirement{{ID: "r", Intent: "requirement"}}, ContractDeltas: []ContractDelta{{Contract: "api", Operation: "modify", Description: "one"}, {Contract: "api", Operation: "remove", Description: "two"}}, Tasks: []Task{{ID: "t", Intent: "task", Requirements: []string{"r"}, Acceptance: []string{"c"}, Contracts: []string{"api"}}}, Acceptance: []Criterion{{ID: "c", Intent: "criterion", Requirement: "r", Rule: &Rule{Kind: "file_exists", Path: "x"}}}}
	r := Validate(p, model.Snapshot{Revision: "base"})
	found := false
	for _, f := range r.Findings {
		if f.Code == "contract_delta_duplicate" {
			found = true
		}
	}
	if !found || r.Status != "failed" {
		t.Fatal("contradictory projections accepted", r)
	}
	s, e := Tasks(p)
	if e != nil {
		t.Fatal(e)
	}
	if len(s.Instructions[0].ContractDeltas) != 2 {
		t.Fatal("task packet loses projected design")
	}
}

func TestIntentGraphKeepsProposalsSeparate(t *testing.T) {
	source := model.Node{ID: "file", Kind: "file", Name: "api.py", Provenance: model.Provenance{Evidence: model.VerifiedStatic}}
	s := model.Snapshot{Repository: "repo", Revision: "base", Nodes: []model.Node{source}}
	p := Plan{FeatureID: "f", Intent: "feature", BaseRevision: "base", Requirements: []Requirement{{ID: "r", Intent: "requirement"}}, Acceptance: []Criterion{{ID: "a", Requirement: "r", Intent: "check", Rule: &Rule{Kind: "file_exists", Path: "api.py"}}}, Tasks: []Task{{ID: "t", Intent: "implement", Requirements: []string{"r"}, Acceptance: []string{"a"}, Components: []string{"file"}}}}
	g, e := IntentGraph(p, s, false)
	if e != nil {
		t.Fatal(e)
	}
	if len(s.Nodes) != 1 || len(s.Edges) != 0 {
		t.Fatal("mutated source graph")
	}
	found := false
	for _, edge := range g.Edges {
		if edge.Kind == "AFFECTS" && edge.To == "file" {
			found = true
		}
		if edge.Provenance.Evidence != model.Proposed {
			t.Fatal("plan relationship became verified")
		}
	}
	if !found {
		t.Fatal("intent not connected to code")
	}
	for _, n := range g.Nodes {
		if n.ID != "file" && n.Provenance.Evidence != model.Proposed {
			t.Fatal("planning entity misrepresented")
		}
	}
}
