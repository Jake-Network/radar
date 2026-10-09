package cli

import "testing"

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
