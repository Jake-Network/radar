package cli

import (
	"context"
	"encoding/json"
	"testing"
)

func TestMCPWorkspaceGateMatchesCLI(t *testing.T) {
	s := newShop(t)
	a := &app{ctx: context.Background(), root: s.orders}
	call := func(arguments map[string]any) map[string]any {
		t.Helper()
		result := a.callTool("radar_gate", arguments)
		text := result["content"].([]map[string]any)[0]["text"].(string)
		var v map[string]any
		if err := json.Unmarshal([]byte(text), &v); err != nil {
			t.Fatal(err, text)
		}
		return v
	}
	// The fixed root bounds what MCP reads: --with would reach any checkout
	// on the machine, so only repositories a human registered take part.
	for _, with := range []any{[]any{"../payments"}, []any{s.payments}, "../payments"} {
		if result := a.callTool("radar_gate", map[string]any{"with": with}); result["isError"] != true {
			t.Fatal("radar_gate accepted with", with, result)
		}
	}
	s.register()
	viaMCP := call(map[string]any{"targets": "orders:agent/api,payments:agent/client"})
	_, viaCLI := gateJSON(t, s.orders, "orders:agent/api", "payments:agent/client")
	if viaMCP["digest"] != viaCLI["digest"] || viaMCP["next"] != viaCLI["next"] || len(viaMCP["repos"].([]any)) != 2 {
		t.Fatal("MCP targets", viaMCP, viaCLI["digest"])
	}
	// No execution path: --run is not a tool argument, and a target value
	// can never become a flag.
	if result := a.callTool("radar_gate", map[string]any{"run": true}); result["isError"] != true {
		t.Fatal("radar_gate accepted run")
	}
	viaMCP = call(map[string]any{"targets": "--run"})
	for _, raw := range viaMCP["repos"].([]any) {
		if tests, ok := raw.(map[string]any)["tests"].(map[string]any); ok && tests["executed_count"] != 0.0 {
			t.Fatal("MCP executed repository code", viaMCP)
		}
	}
}
