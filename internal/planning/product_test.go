package planning

import (
	"encoding/json"
	"github.com/Jake-Network/radar/internal/model"
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
	p.Assumptions = []Assumption{{Text: "Runtime organization authorization unverified"}}
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

func TestAssumptionLifecycle(t *testing.T) {
	var p Plan
	raw := `{"schema_version":"1","feature_id":"f","intent":"i","base_revision":"base","requirements":[],"decisions":[],"constraints":[],"contract_deltas":[],"tasks":[],"acceptance":[],"graph_deltas":[],"assumptions":["legacy string",{"id":"auth","text":"Org scope enforced","status":"accepted","resolution":"Owner accepts until load test"}],"evidence":[],"incomplete":[]}`
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		t.Fatal(err)
	}
	if !p.Assumptions[0].Open() || p.Assumptions[1].Open() {
		t.Fatal("assumption state", p.Assumptions)
	}
	if err := json.Unmarshal([]byte(`{"assumptions":[{"text":"x","unknown":1}]}`), &p); err == nil {
		t.Fatal("unknown assumption field accepted")
	}
	valid := validPlan()
	valid.Assumptions = []Assumption{{ID: "auth", Text: "Org scope enforced", Status: AssumptionResolved, Resolution: "Covered by test_auth"}}
	if r := Validate(valid, model.Snapshot{Revision: "base"}); r.Status != "passed" {
		t.Fatal("resolved assumption still blocks", r)
	}
	valid.Assumptions[0].Resolution = ""
	if r := Validate(valid, model.Snapshot{Revision: "base"}); r.Status != "failed" {
		t.Fatal("resolution not required", r)
	}
	valid.Assumptions[0] = Assumption{Text: "open"}
	r := Validate(valid, model.Snapshot{Revision: "base"})
	if r.Status != "warning" || len(r.NextSteps) == 0 {
		t.Fatal("open assumption must warn with next steps", r)
	}
}

func TestParallelComponentOwnershipAndIntegrationProof(t *testing.T) {
	p := validPlan()
	p.Tasks[0].Components = []string{"file:shared.go"}
	p.Acceptance[0].Rule = &Rule{Kind: "test_run", Command: []string{"go", "test", "./backend"}}
	second := p.Tasks[0]
	second.ID = "frontend"
	second.Acceptance = []string{"frontend-test"}
	p.Tasks = append(p.Tasks, second)
	p.Acceptance = append(p.Acceptance, Criterion{ID: "frontend-test", Requirement: "r", Intent: "frontend works", Rule: &Rule{Kind: "test_run", Command: []string{"go", "test", "./frontend"}}})
	snapshot := model.Snapshot{Revision: "base", Nodes: []model.Node{{ID: "file:shared.go", Kind: "file"}}}
	r := Validate(p, snapshot)
	seen := map[string]bool{}
	for _, f := range r.Findings {
		seen[f.Code] = true
		if f.Code == "component_owner_conflict" || f.Code == "integration_verification_missing" {
			if f.Evidence != model.Unknown || f.Remediation == "" || f.Verification == "" {
				t.Fatal(f)
			}
		}
	}
	if r.Status != model.StatusWarning || !seen["component_owner_conflict"] || !seen["integration_verification_missing"] {
		t.Fatal(r)
	}
	p.Tasks[1].DependsOn = []string{"t"}
	if r := Validate(p, snapshot); r.Status != model.StatusPassed {
		t.Fatal(r)
	}
	p.Tasks[1].DependsOn = nil
	p.Tasks[1].Components = nil
	p.Tasks[1].Acceptance = []string{"a"}
	if r := Validate(p, snapshot); r.Status != model.StatusPassed {
		t.Fatal("shared criterion should not fabricate missing integration", r)
	}
}

func TestContextExpandsContractNeighborAndPreservesEvidence(t *testing.T) {
	s := model.Snapshot{Nodes: []model.Node{{ID: "field", Name: "price", Provenance: model.Provenance{Path: "schema.json", Evidence: model.VerifiedStatic}}, {ID: "client", Name: "display", Provenance: model.Provenance{Path: "client.ts", Evidence: model.VerifiedStatic}}}, Edges: []model.Edge{{ID: "consumes", From: "client", To: "field", Kind: "CONSUMES", Provenance: model.Provenance{Evidence: model.VerifiedStatic}}}}
	c := GroundedContext("price", s)
	if len(c.Matches) != 2 || c.Matches[1].Entity != "client" || c.Matches[1].Score != 0 || len(c.Relationships) != 1 || !strings.Contains(c.Matches[1].Explanation, "unverified") {
		t.Fatal(c)
	}
	if s.Edges[0].Provenance.Evidence != model.VerifiedStatic {
		t.Fatal("mutated provenance")
	}
}
