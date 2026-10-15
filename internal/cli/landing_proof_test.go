package cli

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Jake-Network/radar/internal/gate"
	"github.com/Jake-Network/radar/internal/integration"
	"github.com/Jake-Network/radar/internal/model"
)

func TestLandingCLIPrivateRefAndMCP(t *testing.T) {
	root, agent := checkoutRepo(t)
	agent("agent-api", map[string]string{"backend.py": "def price():\n    return 2\n"})
	agent("agent-docs", map[string]string{"NOTES.md": "notes\n"})
	before := gitTest(t, root, "status", "--porcelain=v1")
	branches := gitTest(t, root, "branch", "--format=%(refname):%(objectname)")
	report := invoke(t, root, 0, "land", "agent-api", "agent-docs", "--base", "main", "--apply")
	if report["landing"] == nil || report["integration_ref"] == nil {
		t.Fatal(report)
	}
	ref := report["integration_ref"].(map[string]any)["ref"].(string)
	if !strings.HasPrefix(ref, "refs/radar/integration/") {
		t.Fatal(ref)
	}
	if gitTest(t, root, "status", "--porcelain=v1") != before || gitTest(t, root, "branch", "--format=%(refname):%(objectname)") != branches {
		t.Fatal("landing changed source state")
	}
	if _, out, _ := run(t, root, "land", "agent-api", "agent-docs"); !strings.Contains(out, "Land order:") || !strings.Contains(out, "Any time:") {
		t.Fatal(out)
	}
	a := &app{root: root, ctx: context.Background()}
	tool := a.callTool("radar_land", map[string]any{"branches": "agent-api,agent-docs"})
	data, _ := json.Marshal(tool)
	if tool["isError"] == true || !strings.Contains(string(data), "landing") {
		t.Fatal(string(data))
	}
}

func TestTrailerRecordsHistoricalGate(t *testing.T) {
	root, agent := checkoutRepo(t)
	agent("agent-api", map[string]string{"backend.py": "def price():\n    return 2\n"})
	code, out, errout := run(t, root, "gate", "agent-api", "--run", "--trailer")
	if code != 0 || !strings.HasPrefix(out, "Radar-Verified:") || !strings.Contains(out, "selected commands") {
		t.Fatal(code, out, errout)
	}
	_, id, ok := strings.Cut(strings.TrimSpace(out), "run=")
	if !ok {
		t.Fatal(out)
	}
	report := invoke(t, root, 0, "show", id)
	if report["gate"].(map[string]any)["verdict"] != "pass" {
		t.Fatal(report)
	}
	tree := report["candidate_tree"]
	put(t, root, "later.txt", "later\n")
	gitTest(t, root, "add", "later.txt")
	gitTest(t, root, "commit", "-qm", "later main commit")
	if newer := invoke(t, root, 0, "show", id); newer["candidate_tree"] != tree {
		t.Fatal("historical report rebound to HEAD")
	}
	if _, out, _ := run(t, root, "gate", "agent-api", "--json", "--trailer"); !strings.Contains(out, "error") {
		t.Fatal(out)
	}
	if code, _, _ := run(t, root, "show", "../escape"); code != 2 {
		t.Fatal("unsafe ID accepted")
	}
}

func TestTrailerNeverClaimsMissingContractsOrTests(t *testing.T) {
	r := integration.Report{Checks: []integration.Check{{ID: "textual_merge", Status: model.StatusPassed, Evidence: model.VerifiedTool}, {ID: "no_breaking_contracts", Status: model.StatusPassed, Evidence: model.VerifiedStatic}}, Gate: gate.Result{Verdict: gate.Pass}}
	text := gateTrailer(r, false, "run")
	if strings.Contains(text, "contracts") || !strings.Contains(text, "tests(not_run)") {
		t.Fatal(text)
	}
}

func TestWorkspaceTrailerRecordsFullHistoricalReport(t *testing.T) {
	s := stage2Shop(t)
	s.agent(s.orders, "agent/compatible", map[string]string{"marker.txt": "api"})
	code, out, errs := run(t, s.orders, "gate", "orders:agent/compatible", "payments:agent/client", "--trailer")
	if code != 0 || !strings.HasPrefix(out, "Radar-Verified:") {
		t.Fatal(code, out, errs)
	}
	_, id, ok := strings.Cut(strings.TrimSpace(out), "run=")
	if !ok {
		t.Fatal(out)
	}
	report := invoke(t, s.orders, 0, "show", id)
	if report["verdict"] != "pass" || len(report["repos"].([]any)) != 2 {
		t.Fatal(report)
	}
}

func TestFailedOrUnverifiedGateCannotEmitTrailer(t *testing.T) {
	root, agent := checkoutRepo(t)
	agent("agent-api", map[string]string{"backend.py": "def price():\n    return 2\n"})
	agent("agent-client", map[string]string{"frontend.ts": "export const QUANTITY = 2;\n"})
	if code, out, errs := run(t, root, "gate", "agent-api", "agent-client", "--run", "--trailer"); code != 1 || strings.Contains(out, "Radar-Verified:") {
		t.Fatal(code, out, errs)
	}
	agent("agent-docs", map[string]string{"NOTES.md": "notes\n"})
	if code, out, errs := run(t, root, "gate", "agent-docs", "--run", "--trailer"); code != 1 || strings.Contains(out, "Radar-Verified:") {
		t.Fatal(code, out, errs)
	}
}

func TestConflictBriefMCPRejectsExecution(t *testing.T) {
	for _, name := range []string{"radar_land", "radar_conflict_brief", "radar_overlap"} {
		for _, spec := range mcpTools {
			if spec.name != name {
				continue
			}
			args, err := spec.args(map[string]any{"branches": "a,b", "apply": true})
			if err == nil {
				t.Fatal(name, args)
			}
		}
	}
	root, agent := checkoutRepo(t)
	agent("a", map[string]string{"backend.py": "def price():\n    return 2\n"})
	agent("b", map[string]string{"backend.py": "def price():\n    return 3\n"})
	report := invoke(t, root, 1, "gate", "a", "b", "--conflict-brief")
	b, _ := json.Marshal(report["conflict_brief"])
	if !strings.Contains(string(b), "backend.py") || !strings.Contains(string(b), "hunks") {
		t.Fatal(string(b))
	}
}

func TestLandingUsesSelectedGatePolicy(t *testing.T) {
	root, agent := checkoutRepo(t)
	agent("feature", map[string]string{"backend.py": "def price():\n    return 2\n"})
	for _, required := range []string{"future_capability", "integration_execution", "configuration_stability"} {
		t.Run(required, func(t *testing.T) {
			put(t, root, ".radar/landing-policy.json", `{"version":1,"require":["textual_merge","`+required+`"]}`)
			want := 1
			if required == "configuration_stability" {
				want = 0
			}
			g := invoke(t, root, want, "gate", "feature", "--base", "main", "--policy", ".radar/landing-policy.json")
			before := gitTest(t, root, "for-each-ref", "--format=%(refname):%(objectname)", "refs/radar/integration/")
			l := invoke(t, root, want, "land", "feature", "--base", "main", "--apply", "--policy", ".radar/landing-policy.json")
			if g["gate"].(map[string]any)["verdict"] != l["gate"].(map[string]any)["verdict"] {
				t.Fatalf("gate and landing disagree: gate=%v land=%v", g["gate"], l["gate"])
			}
			if want != 0 && (l["integration_ref"] != nil || before != gitTest(t, root, "for-each-ref", "--format=%(refname):%(objectname)", "refs/radar/integration/")) {
				t.Fatal("blocked policy published an integration ref", l)
			}
			if inputs, ok := l["configuration_inputs"].([]any); !ok || len(inputs) != 1 || inputs[0].(map[string]any)["status"] != "passed" {
				t.Fatal("landing lost selected configuration provenance", l)
			}
		})
	}
}
