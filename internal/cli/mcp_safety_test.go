package cli

import (
	"encoding/json"
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
		if tool.name != "radar_merge_check" && tool.name != "radar_check" && tool.name != "radar_contracts_discover" {
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
		for _, arg := range []string{"verify", "allow_execution", "allow-execution", "reviewer", "approve"} {
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
