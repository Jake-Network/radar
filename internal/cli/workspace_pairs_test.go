package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
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

func TestWorkspaceLandingRestackKeepsExcludedScopeBlocked(t *testing.T) {
	s := stage2Shop(t)
	s.agent(s.orders, "agent/stale", map[string]string{"collision.txt": "stale\n"})
	put(t, s.orders, "collision.txt", "base\n")
	gitTest(t, s.orders, "add", "collision.txt")
	gitTest(t, s.orders, "commit", "-qm", "advance base")
	s.agent(s.orders, "agent/clean", map[string]string{"notes.md": "clean\n"})
	for _, refs := range [][]string{{"orders:agent/stale", "orders:agent/clean", "payments:agent/client"}, {"orders:agent/stale", "payments:agent/client"}} {
		code, report := workspacePairTest(t, s, options{args: refs}, false)
		if code != 1 || report["verdict"] != "blocked" {
			t.Fatal(code, report)
		}
		p := report["landing"].(map[string]any)["orders"].(map[string]any)
		if len(p["restack_first"].([]any)) != 1 {
			t.Fatal(p)
		}
		for _, raw := range report["repos"].([]any) {
			repo := raw.(map[string]any)
			if repo["id"] != "orders" {
				continue
			}
			r := repo["report"].(map[string]any)
			if repo["verdict"] != "blocked" {
				t.Fatal(repo)
			}
			if len(refs) == 2 && (r["candidate_commit"] != nil || r["candidate_tree"] != nil || len(r["inputs"].([]any)) != 0) {
				t.Fatal(r)
			}
		}
		checkStage2Schema(t, report)
		code, text, errs := run(t, s.orders, append([]string{"land"}, refs...)...)
		if code != 1 || !strings.Contains(text, "Restack first (orders): agent/stale (behind base: 1;") || strings.Index(text, "Restack first (orders)") > strings.Index(text, "Land order (orders)") {
			t.Fatal("workspace restack priority/behind lost", code, text, errs)
		}
	}
}
