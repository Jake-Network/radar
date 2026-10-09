package cli

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestMCPRejectsWrongArgumentTypes(t *testing.T) {
	spec := toolSpec{command: "graph", params: []toolParam{p("depth", "depth", "integer", "", false), p("ref", "ref", "string", "", false), p("reverse", "reverse", "boolean", "", false)}}
	for _, args := range []map[string]any{{"depth": "2"}, {"depth": 1.5}, {"ref": 2.0}, {"reverse": "true"}} {
		if _, err := spec.args(args); err == nil {
			t.Fatal("wrong type accepted", args)
		}
	}
	if _, err := spec.args(map[string]any{"depth": 2.0, "ref": "main", "reverse": true}); err != nil {
		t.Fatal(err)
	}
}

func TestMCPIntegrationToolsCannotAuthorizeExecutionOrReview(t *testing.T) {
	for _, tool := range mcpTools {
		if tool.name != "radar_gate" && tool.name != "radar_merge_check" && tool.name != "radar_check" && tool.name != "radar_contracts_discover" {
			continue
		}
		valid := map[string]any{}
		for _, param := range tool.params {
			if param.required {
				valid[param.name] = "main"
			}
		}
		if _, err := tool.args(valid); err != nil {
			t.Fatal("valid analysis tool arguments rejected", tool.name, err)
		}
		for _, arg := range []string{"run", "verify", "allow_execution", "allow-execution", "reviewer", "approve"} {
			attempted := map[string]any{}
			for k, v := range valid {
				attempted[k] = v
			}
			attempted[arg] = true
			if _, err := tool.args(attempted); err == nil {
				t.Fatal("consequential argument accepted", tool.name, arg)
			}
		}
	}
}

func TestMCPVerificationSummaryPreservesGateAndBoundsInventory(t *testing.T) {
	raw := `{"gate":{"verdict":"blocked"},"status":"incomplete","checks":[{"id":"integration_execution","status":"unknown"}],"coverage":[{"analyzer":"runtime","status":"unknown"}],"verification_proposal":{"status":"incomplete","commands":[],"inventory":{"tests":["large"]},"limitations":["inferred"]}}`
	compact := compactVerification(raw)
	if strings.Contains(compact, "inventory") && strings.Contains(compact, "\"inventory\"") {
		t.Fatal(compact)
	}
	var report map[string]any
	if err := json.Unmarshal([]byte(compact), &report); err != nil {
		t.Fatal(err)
	}
	if report["gate"].(map[string]any)["verdict"] != "blocked" || report["checks"].([]any)[0].(map[string]any)["status"] != "unknown" {
		t.Fatal(report)
	}
	for _, tool := range mcpTools {
		if tool.name == "radar_check" {
			args, err := tool.args(map[string]any{"base": "main", "detail": true, "suggest_tests": true, "policy": ".radar/policy.json"})
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(strings.Join(args, " "), "__detail") {
				t.Fatal(args)
			}
		}
	}
}

func TestMCPAgentBriefIsBoundedAndExplainsSelection(t *testing.T) {
	commands := []string{}
	for i := 0; i < 40; i++ {
		commands = append(commands, fmt.Sprintf(`{"id":"c%d","tier":"required","command":["python3","-m","pytest","./a.py","./b.py","./c.py","./d.py","./e.py","./f.py","./g.py"],"test_files":["a","b"],"evidence_reasons":[{"code":"dependency_impact","explanation":"imports changed file","locations":[{"path":"x"}]}]}`, i))
	}
	raw := `{"gate":{"verdict":"blocked","required_checks":[{"id":"integration_execution","status":"incomplete","explanation":"2 required omitted"}]},` +
		`"checks":[{"id":"test:c0","status":"failed"},{"id":"test:c1","status":"blocked","explanation":"Not executed (environment_unavailable)"}],` +
		`"findings":[{"code":"contract_obligation_removed","contract":"summary","severity":"error","explanation":"x","remediation":"restore"}],` +
		`"contract_obligations":[{"binding":"summary","kind":"removed"}],` +
		`"selection":{"mode":"targeted","commands":[` + strings.Join(commands, ",") + `],"omitted":[{"id":"o1","tier":"required","reason":"budget_exceeded","test_files":["t"]}],"uncovered_changes":["src/x.py"],"blocking":["required command omitted by budget"]}}`
	compact := compactVerification(raw)
	if len(compact) > 12000 {
		t.Fatal("unbounded agent summary", len(compact))
	}
	var report map[string]any
	if err := json.Unmarshal([]byte(compact), &report); err != nil {
		t.Fatal(err)
	}
	brief := report["agent_brief"].(map[string]any)
	tests := brief["tests"].(map[string]any)
	if tests["essential_count"].(float64) != 40 || len(tests["essential"].([]any)) != 8 || tests["omitted_count"].(float64) != 1 || len(tests["failed"].([]any)) != 1 || len(tests["not_run"].([]any)) != 1 {
		t.Fatal(tests)
	}
	if brief["must_repair_count"].(float64) != 1 || len(brief["contract_obligation_changes"].([]any)) != 1 || len(brief["verify_next"].([]any)) != 2 {
		t.Fatal(brief)
	}
}
