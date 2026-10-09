package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

func reliabilityRepo(t *testing.T, uncovered bool) string {
	root, agent := agentRepo(t, map[string]string{
		"model.py":      "VALUE = 1\n",
		"test_model.py": "import unittest\nfrom model import VALUE\nclass Model(unittest.TestCase):\n def test_value(self): self.assertEqual(VALUE, 1)\n",
		"other.py":      "OTHER = 1\n",
	})
	changes := map[string]string{"model.py": "VALUE = 1 # edited\n"}
	if uncovered {
		changes["other.py"] = "OTHER = 2\n"
	}
	agent("feature/model", changes)
	return root
}

func reliabilityReport(t *testing.T, root string, args ...string) (int, gateReport) {
	t.Helper()
	var output, errors bytes.Buffer
	code := Run(context.Background(), append([]string{"--root", root, "--json"}, args...), &output, &errors)
	out, errs := output.String(), errors.String()
	var r gateReport
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatalf("%d: %s %s: %v", code, out, errs, err)
	}
	return code, r
}

func TestGateDefaultRequiresSelectionEvidence(t *testing.T) {
	root := reliabilityRepo(t, true)
	code, r := reliabilityReport(t, root, "gate", "--run", "feature/model")
	if code != 1 || r.Gate.Verdict != "blocked" {
		t.Fatalf("uncovered change passed: code=%d gate=%+v selection=%+v", code, r.Gate, r.Selection)
	}
	if len(r.Executions) != 1 || r.Executions[0].Status != "passed" {
		t.Fatal("real selected test must pass", r.Executions)
	}
	if len(r.Selection.Uncovered) != 1 || r.Selection.Uncovered[0] != "other.py" {
		t.Fatal(r.Selection)
	}
}

func TestGateInterspersedFlags(t *testing.T) {
	root := reliabilityRepo(t, false)
	gitTest(t, root, "branch", "feature/web", "feature/model")
	for _, args := range [][]string{
		{"gate", "--run", "feature/model", "feature/web"},
		{"gate", "feature/model", "feature/web", "--run"},
		{"gate", "--base", "main", "--run", "feature/model", "feature/web"},
		{"gate", "feature/model", "feature/web", "--base", "main", "--run"},
	} {
		code, r := reliabilityReport(t, root, args...)
		if code != 0 || r.Gate.Verdict != "pass" || len(r.Executions) != 1 {
			t.Fatalf("%v: code=%d report=%+v", args, code, r)
		}
	}
	for _, args := range [][]string{{"gate", "feature/model", "--wat"}, {"gate", "feature/model", "--base"}} {
		if code, _, _ := run(t, root, args...); code != 2 {
			t.Fatal(args, code)
		}
	}
	var out, errs bytes.Buffer
	if code := Run(context.Background(), []string{"--root", root, "gate", "--", "feature/model"}, &out, &errs); code != 0 || !strings.Contains(out.String(), "static") {
		t.Fatal(code, out.String(), errs.String())
	}
}

func TestGateLiteralReferenceNames(t *testing.T) {
	root := reliabilityRepo(t, false)
	for _, branch := range []string{"feature/a,b", "feature/$literal;name", "feature/--run"} {
		gitTest(t, root, "branch", branch, "feature/model")
		code, r := reliabilityReport(t, root, "gate", "--", branch)
		if code != 0 || len(r.Branches) != 1 || r.Branches[0].Ref != branch {
			t.Fatalf("literal ref %q: %d %+v", branch, code, r)
		}
	}
}

func TestGateSuggestedReferencesSurviveShellParsing(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip(err)
	}
	root := reliabilityRepo(t, false)
	for _, branch := range []string{"feature/{left,right}", "#feature", "feature/$literal;name"} {
		gitTest(t, root, "branch", branch, "feature/model")
		code, report := reliabilityReport(t, root, "gate", branch)
		if code != 0 || !strings.HasSuffix(report.Next, shellArg(branch)) {
			t.Fatal(code, report.Next)
		}
		out, err := exec.Command("bash", "-c", "set -- "+shellArg(branch)+"; printf '%s\\n' \"$#\" \"$1\"").CombinedOutput()
		if err != nil || string(out) != "1\n"+branch+"\n" {
			t.Fatalf("copyable reference %q: %s %v", branch, out, err)
		}
	}
}

func TestMCPGateRetainsExcludedWorktreeState(t *testing.T) {
	root, agent := agentRepo(t, map[string]string{"model.py": "VALUE=1\n"})
	dir := agent("feature", map[string]string{"model.py": "VALUE=2\n"})
	put(t, dir, "model.py", "VALUE=3\n")
	a := &app{ctx: context.Background(), root: root}
	result := a.callTool("radar_gate", map[string]any{})
	raw := result["content"].([]map[string]any)[0]["text"].(string)
	var brief map[string]any
	if err := json.Unmarshal([]byte(raw), &brief); err != nil {
		t.Fatal(err)
	}
	if brief["dirty_worktree_count"] != float64(1) || !strings.Contains(brief["worktree_scope"].(string), "excluded") {
		t.Fatal(raw)
	}
}
