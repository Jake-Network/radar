package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
)

func workspacePairTest(t *testing.T, s *shop, o options, dirty bool) (int, map[string]any) {
	t.Helper()
	var out, errs bytes.Buffer
	a := app{ctx: context.Background(), root: s.orders, out: &out, errout: &errs, machine: true}
	inv, err := a.workspaceInvocation(o)
	if err != nil || inv == nil {
		t.Fatalf("invocation %v %+v", err, inv)
	}
	var code int
	if dirty {
		code = a.workspaceOverlap(o, inv)
	} else {
		code = a.workspaceLanding(o, inv)
	}
	var report map[string]any
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatalf("exit %d: %v %s %s", code, err, out.String(), errs.String())
	}
	return code, report
}
func TestWorkspacePairLandingAndDirtyContractDrift(t *testing.T) {
	s := stage2Shop(t)
	s.agent(s.orders, "agent/compatible", map[string]string{"marker.txt": "api"})
	o := options{args: []string{"orders:agent/compatible", "payments:agent/client"}}
	code, report := workspacePairTest(t, s, o, false)
	if code != 0 || report["verdict"] != "pass" {
		t.Fatal(code, report)
	}
	pairs := report["cross_repo_pairs"].(map[string]any)
	if pairs["status"] != "passed" || len(pairs["pairs"].([]any)) != 1 {
		t.Fatal(pairs)
	}
	if len(report["landing"].(map[string]any)) != 2 {
		t.Fatal("per-repository landing orders missing")
	}
	checkStage2Schema(t, report)
	put(t, s.wt(s.orders, "agent/compatible"), "openapi.json", `{"type":"object","properties":{"status":{"type":"string"}}}`)
	code, report = workspacePairTest(t, s, o, true)
	if code != 1 || report["verdict"] != "fail" {
		t.Fatal(code, report)
	}
	pairs = report["cross_repo_pairs"].(map[string]any)
	if pairs["status"] != "failed" || pairs["include_dirty"] != true {
		t.Fatal(pairs)
	}
	before := gitTest(t, s.orders, "status", "--porcelain")
	workspacePairTest(t, s, o, true)
	if gitTest(t, s.orders, "status", "--porcelain") != before {
		t.Fatal("source state changed")
	}
}
func TestWorkspacePairWithoutDeclarationsIsNotVerified(t *testing.T) {
	s := newShop(t)
	s.register()
	code, report := workspacePairTest(t, s, options{args: []string{"orders:agent/api", "payments:agent/client"}}, false)
	if code != 1 || report["verdict"] != "blocked" || report["cross_repo_pairs"].(map[string]any)["status"] != "incomplete" {
		t.Fatal(code, report)
	}
}
