package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Jake-Network/radar/internal/composition"
	"github.com/Jake-Network/radar/internal/discovery"
	"github.com/Jake-Network/radar/internal/gate"
	gitrepo "github.com/Jake-Network/radar/internal/git"
	"github.com/Jake-Network/radar/internal/workspace"
)

const stage2Schema = `{"type":"object","properties":{"total":{"type":"number"},"status":{"type":"string"}}}`

func stage2Shop(t *testing.T) *shop {
	t.Helper()
	s := newShop(t)
	team := workspace.TeamFile{Version: 1, Repos: []workspace.TeamRepo{
		{ID: "orders", Identity: gitrepo.Identity(context.Background(), s.orders), Base: "main"},
		{ID: "payments", Identity: gitrepo.Identity(context.Background(), s.payments)},
	}, Links: []workspace.Link{{ID: "order-response", Direction: "response", Producer: "orders:openapi.json#", Consumer: "payments"}}}
	data, _ := json.Marshal(team)
	s.commit(s.orders, map[string]string{workspace.TeamPath: string(data), "openapi.json": stage2Schema})
	s.commit(s.payments, map[string]string{workspace.ConsumesPath: `{"version":1,"consumes":[{"contract":"order-response","fields":["total"],"source":"missing.ts"}]}`})
	s.register()
	return s
}

func checkStage2Schema(t *testing.T, report map[string]any) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "schemas", "workspace-report.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var schema map[string]any
	json.Unmarshal(data, &schema)
	if err := conforms(schema, schema, report, "$"); err != nil {
		t.Fatal(err)
	}
}

func TestWorkspaceStage2LinksAndReplay(t *testing.T) {
	s := stage2Shop(t)
	code, passed := gateJSON(t, s.orders, "orders:agent/api")
	if code != 0 || passed["cross_repo"].(map[string]any)["status"] != "passed" || passed["scope_source"] != "team file" {
		t.Fatal(code, passed)
	}
	if repoOf(t, passed, "orders")["base_source"] != "team file" {
		t.Fatal(passed)
	}
	checkStage2Schema(t, passed)
	s.agent(s.orders, "agent/schema", map[string]string{"openapi.json": `{"type":"object","properties":{"status":{"type":"string"}}}`})
	code, failed := gateJSON(t, s.payments, "orders:agent/schema")
	if code != 1 || failed["verdict"] != "fail" || failed["cross_repo"].(map[string]any)["status"] != "failed" {
		t.Fatal(code, failed)
	}
	checkStage2Schema(t, failed)
	digest := failed["digest"]
	s.commit(s.orders, map[string]string{workspace.TeamPath: `{"version":1,"repos":[{"id":"orders","identity":"wrong"}],"links":[]}`})
	code, replay := gateJSON(t, s.payments, "--replay", "last")
	if code != 1 || replay["digest"] != digest {
		t.Fatal(code, replay, digest)
	}
	checkStage2Schema(t, replay)
}

func TestWorkspaceStage2PartialAndUncommittedTeam(t *testing.T) {
	s := stage2Shop(t)
	put(t, s.orders, workspace.TeamPath, "uncommitted invalid JSON")
	code, partial := gateJSON(t, s.orders, "--only", "payments", "payments:agent/client")
	if code != 0 || partial["verdict"] != "blocked" || partial["cross_repo"].(map[string]any)["status"] != "incomplete" {
		t.Fatal(code, partial)
	}
	if partial["team_file"].(map[string]any)["uncommitted"] != true {
		t.Fatal(partial)
	}
	checkStage2Schema(t, partial)
}

func TestWorkspaceStage2RenderingAndMCP(t *testing.T) {
	s := stage2Shop(t)
	s.agent(s.orders, "agent/schema", map[string]string{"openapi.json": `{"type":"object","properties":{"status":{"type":"string"}}}`})
	code, text, _ := run(t, s.orders, "gate", "orders:agent/schema")
	for _, want := range []string{"1/1 cross-repo links checked", "✗ link order-response", "producer first fails", "orders:agent/schema  openapi.json", "payments:main  .radar/consumes.json", "Next: repair and commit"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in %s", want, text)
		}
	}
	if !strings.Contains(text, "missing.ts source file is absent") {
		t.Fatal("missing source diagnostic", text)
	}
	if code != 1 {
		t.Fatal(code, text)
	}
	_, full := gateJSON(t, s.orders, "orders:agent/schema")
	var compact map[string]any
	json.Unmarshal([]byte(compactWorkspace(full)), &compact)
	links := compact["links"].([]any)
	if len(links) != 1 || len(links[0].(map[string]any)["unverified_cells"].([]any)) == 0 {
		t.Fatal(compact)
	}
}

func TestWorkspaceStage2TeamMembershipAndBaseOverride(t *testing.T) {
	s := stage2Shop(t)
	extra := s.repo("catalog", map[string]string{"catalog.py": "ID = 1\n"})
	invoke(t, s.orders, 0, "workspace", "add", extra, "--name", "shop")
	code, report := gateJSON(t, s.orders, "orders:agent/api", "--base", "orders:main")
	if code != 0 || report["repo_count"] != float64(2) || repoOf(t, report, "orders")["base_source"] != "named" {
		t.Fatal(code, report)
	}
	excluded := report["excluded_repos"].([]any)
	if len(excluded) != 1 || excluded[0].(map[string]any)["id"] != "catalog" {
		t.Fatal(report)
	}
	checkStage2Schema(t, report)
	team := workspace.TeamFile{Version: 1, Repos: []workspace.TeamRepo{{ID: "orders", Identity: gitrepo.Identity(context.Background(), s.orders)}, {ID: "unknown", Identity: gitrepo.Identity(context.Background(), extra)}}}
	data, _ := json.Marshal(team)
	s.commit(s.orders, map[string]string{workspace.TeamPath: string(data)})
	// A team repo that is not registered here is that repo's error; the
	// registered repos are still checked.
	code, missing := gateJSON(t, s.orders, "orders:agent/api")
	unknown := repoOf(t, missing, "unknown")
	if code != 2 || unknown["verdict"] != "error" || !strings.Contains(unknown["next"].(string), "radar workspace add <NEW PATH> --id unknown") || repoOf(t, missing, "orders")["verdict"] != "pass" {
		t.Fatal(code, missing)
	}
	checkStage2Schema(t, missing)
	if code, _ := gateJSON(t, s.orders, "--only", "orders", "orders:agent/api"); code != 0 {
		t.Fatal("--only orders with an unregistered team repo", code)
	}
	show := invoke(t, s.orders, 0, "workspace", "show")
	if len(show["repos"].([]any)) != 2 {
		t.Fatal("workspace show must list the unregistered team repo", show)
	}
}

func TestCompactWorkspaceSuggestionsBounded(t *testing.T) {
	full := map[string]any{"repos": []any{}, "suggested_links": []any{1, 2, 3, 4}}
	var compact map[string]any
	json.Unmarshal([]byte(compactWorkspace(full)), &compact)
	if len(compact["suggested_links"].([]any)) != 3 {
		t.Fatal(compact)
	}
}

func TestWorkspaceStage2ConsumerOnlyFailure(t *testing.T) {
	s := stage2Shop(t)
	s.agent(s.payments, "agent/expectation", map[string]string{workspace.ConsumesPath: `{"version":1,"consumes":[{"contract":"order-response","fields":["missing"]}]}`})
	code, report := gateJSON(t, s.payments, "payments:agent/expectation")
	if code != 1 || report["cross_repo"].(map[string]any)["status"] != "failed" {
		t.Fatal(code, report)
	}
	checkStage2Schema(t, report)
}

func TestWorkspaceStage2MalformedConsumes(t *testing.T) {
	s := stage2Shop(t)
	s.agent(s.payments, "agent/malformed", map[string]string{workspace.ConsumesPath: "invalid JSON"})
	for _, ran := range []bool{false, true} {
		args := []string{"payments:agent/malformed"}
		want := 0
		if ran {
			args = append(args, "--run")
			want = 1
		}
		code, report := gateJSON(t, s.orders, args...)
		if code != want || report["cross_repo"].(map[string]any)["status"] != "incomplete" || report["verdict"] != "blocked" {
			t.Fatal(code, report)
		}
		checkStage2Schema(t, report)
	}
}

func TestWorkspaceStage2ConflictAndPartialReplay(t *testing.T) {
	s := stage2Shop(t)
	s.agent(s.orders, "agent/conflict", map[string]string{"api.py": "def total():\n    return 100\n"})
	code, report := gateJSON(t, s.orders, "orders:agent/api", "orders:agent/conflict")
	if code != 1 || report["cross_repo"].(map[string]any)["status"] != "incomplete" {
		t.Fatal(code, report)
	}
	_, partial := gateJSON(t, s.orders, "--only", "payments", "payments:agent/client")
	code, replay := gateJSON(t, s.payments, "--replay", "last")
	if code != 0 || partial["digest"] != replay["digest"] || replay["cross_repo"].(map[string]any)["status"] != "incomplete" {
		t.Fatal(code, partial, replay)
	}
}

func TestWorkspaceStage2IdentityAndCheckoutDigest(t *testing.T) {
	s := stage2Shop(t)
	_, first := gateJSON(t, s.orders, "orders:agent/api", "payments:agent/client")
	for _, dir := range []string{s.payments, s.wt(s.orders, "agent/api")} {
		_, report := gateJSON(t, dir, "orders:agent/api", "payments:agent/client")
		if report["digest"] != first["digest"] {
			t.Fatal(first, report)
		}
	}
	data, err := os.ReadFile(filepath.Join(s.orders, workspace.TeamPath))
	if err != nil {
		t.Fatal(err)
	}
	team, err := workspace.ParseTeam(data)
	if err != nil {
		t.Fatal(err)
	}
	team.Repos[1].Identity = team.Repos[0].Identity
	data, _ = json.Marshal(team)
	s.commit(s.orders, map[string]string{workspace.TeamPath: string(data)})
	code, report := gateJSON(t, s.orders, "orders:agent/api")
	if code != 2 || !strings.Contains(report["error"].(string), "identity differs") {
		t.Fatal(code, report)
	}
}

func TestWorkspaceStage2SuggestedLinksDoNotAffectVerdict(t *testing.T) {
	p := composition.SuggestedLink{Producer: "orders", Consumer: "payments", Candidate: discovery.Candidate{SchemaPath: "openapi.json", Pointer: "/components/schemas/Order", Fields: []string{"total", "status"}, Consumer: "src/order.ts", Endpoint: "/orders"}}
	command := suggestionCommand(p)
	if command != "radar workspace connect 'orders:openapi.json#/components/schemas/Order' payments --direction response --fields total,status --source src/order.ts" {
		t.Fatal(command)
	}
	g := workspaceReport{Verdict: gate.Pass, CrossRepo: crossRepo{Status: "not_checked", Reason: perRepoOnly}}
	for range 4 {
		g.SuggestedLinks = append(g.SuggestedLinks, workspaceSuggestion{SuggestedLink: p, Command: command})
	}
	applyWorkspaceLinks(&g)
	if g.Verdict != gate.Pass {
		t.Fatal(g)
	}
	var output bytes.Buffer
	renderWorkspaceGate(&output, g)
	if strings.Count(output.String(), "proposed link") != 3 || !strings.Contains(output.String(), "PASS (static)") {
		t.Fatal(output.String())
	}
	data, _ := json.Marshal(g)
	var full map[string]any
	json.Unmarshal(data, &full)
	if len(full["suggested_links"].([]any)) != 4 {
		t.Fatal(full)
	}
}

func TestWorkspaceStage2WarningLinkIsCheckedAndPasses(t *testing.T) {
	s := stage2Shop(t)
	data, _ := os.ReadFile(filepath.Join(s.orders, workspace.TeamPath))
	team, err := workspace.ParseTeam(data)
	if err != nil {
		t.Fatal(err)
	}
	// Without a direction a changed consumed field is only an inferred risk.
	team.Links[0].Direction = ""
	data, _ = json.Marshal(team)
	s.commit(s.orders, map[string]string{workspace.TeamPath: string(data)})
	s.agent(s.orders, "agent/retype", map[string]string{"openapi.json": `{"type":"object","properties":{"total":{"type":"string"},"status":{"type":"string"}}}`})
	code, report := gateJSON(t, s.orders, "orders:agent/retype")
	link := report["links"].([]any)[0].(map[string]any)
	if code != 0 || report["verdict"] != "pass" || link["status"] != "warning" || report["cross_repo"].(map[string]any)["status"] != "passed" || !strings.Contains(report["scope_summary"].(string), "1/1 cross-repo links checked") {
		t.Fatal(code, report["verdict"], link["status"], report["cross_repo"], report["scope_summary"])
	}
	checkStage2Schema(t, report)
	_, text, _ := run(t, s.orders, "gate", "orders:agent/retype")
	if !strings.Contains(text, "✓ link order-response  orders → payments (warning: ") || strings.Contains(text, "link order-response  not checked") {
		t.Fatal(text)
	}
}

func TestWorkspaceStage2OnlyOutsideWorkspaceAndDuplicates(t *testing.T) {
	s := newShop(t)
	if code, _, errs := run(t, s.orders, "gate", "--only", "orders"); code != 2 || !strings.Contains(errs, "--only limits a workspace run, but this repository is not in a workspace") {
		t.Fatal("--only outside a workspace", code, errs)
	}
	s.register()
	_, report := gateJSON(t, s.orders, "--only", "orders", "--only", "orders")
	if only := report["only"].([]any); len(only) != 1 || report["selection"].(map[string]any)["only"].([]any)[0] != "orders" || len(report["selection"].(map[string]any)["only"].([]any)) != 1 {
		t.Fatal("duplicate --only values", report["only"], report["selection"])
	}
}
