package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Jake-Network/radar/internal/gate"
	"github.com/Jake-Network/radar/internal/model"
	"github.com/Jake-Network/radar/internal/testselection"
)

// These tests exercise the public CLI against committed candidates and real
// standard-library unittest tests, including recognized runner observations.
func TestGateExecutableVerificationScenarios(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip(err)
	}
	type scenario struct {
		name                                       string
		setup                                      func(*testing.T) (string, []string)
		verdict                                    gate.Verdict
		code                                       int
		selection, execution                       model.Status
		text                                       string
		uncovered, inventory, selected, executions int
	}
	scenarios := []scenario{
		{"A_supported_pass", func(t *testing.T) (string, []string) { return reliabilityRepo(t, false), []string{"feature/model"} }, gate.Pass, 0, model.StatusPassed, model.StatusPassed, "bounded verification", 0, 1, 1, 1},
		{"B_partial_relationships", func(t *testing.T) (string, []string) { return reliabilityRepo(t, true), []string{"feature/model"} }, gate.Blocked, 1, model.StatusIncomplete, model.StatusPassed, "Uncovered changes", 1, 1, 1, 1},
		{"C_required_runner_missing", missingRunnerScenario, gate.Blocked, 1, model.StatusIncomplete, model.StatusIncomplete, "unavailable", 0, 2, 2, 1},
		{"D_inventory_incomplete", func(t *testing.T) (string, []string) {
			root, agent := agentRepo(t, map[string]string{"model.py": "VALUE = 1\n", "test_model.py": "import unittest\nfrom model import VALUE\nclass Model(unittest.TestCase):\n def test_value(self): self.assertEqual(VALUE, 1)\n", "test_large.py": "#" + strings.Repeat("x", int(testselection.MaxFileBytes)) + "\n"})
			agent("feature/model", map[string]string{"model.py": "VALUE = 1 # edited\n"})
			return root, []string{"feature/model"}
		}, gate.Blocked, 1, model.StatusIncomplete, model.StatusIncomplete, "inventory incomplete", 0, 1, 1, 1},
		{"E_declared_contract_break", func(t *testing.T) (string, []string) {
			root, agent := agentRepo(t, map[string]string{"schema.json": `{"type":"object","properties":{"total":{"type":"integer"}}}`, "consumer.ts": "export function use(x:any){return x.total};\n", ".radar/contracts.json": `{"version":1,"bindings":[{"id":"summary","schema":"schema.json","pointer":"","consumer":"consumer.ts","fields":["total"],"direction":"response"}]}`, "test_schema.py": "import json, unittest\nfrom pathlib import Path\nclass Schema(unittest.TestCase):\n def test_object(self): self.assertEqual(json.loads(Path('schema.json').read_text())['type'], 'object')\n"})
			agent("feature/schema", map[string]string{"schema.json": `{"type":"object","properties":{}}`})
			return root, []string{"feature/schema"}
		}, gate.Fail, 1, model.StatusPassed, model.StatusPassed, "contract_field_removed", 0, 1, 1, 1},
		{"F_combined_runtime_failure", func(t *testing.T) (string, []string) {
			root, agent := checkoutRepo(t)
			agent("agent-backend", map[string]string{"backend.py": "def price():\n    return 2\n"})
			agent("agent-frontend", map[string]string{"frontend.ts": "export const QUANTITY = 2;\n"})
			return root, []string{"agent-backend", "agent-frontend"}
		}, gate.Fail, 1, model.StatusIncomplete, model.StatusFailed, "integration_execution_failed", 1, 1, 1, 1},
	}
	for _, tc := range scenarios {
		t.Run(tc.name, func(t *testing.T) {
			root, branches := tc.setup(t)
			// Both default policy and an explicit equivalent policy must fail closed.
			policy := filepath.Join(t.TempDir(), "strict.json")
			if err := os.WriteFile(policy, []byte(`{"version":1,"name":"explicit-strict","require":["textual_merge","no_breaking_contracts","integration_execution","test_selection"]}`), 0600); err != nil {
				t.Fatal(err)
			}
			for _, explicit := range []bool{false, true} {
				t.Run(map[bool]string{false: "default", true: "explicit"}[explicit], func(t *testing.T) {
					args := []string{"gate", "--run"}
					if explicit {
						args = append(args, "--policy", policy)
					}
					args = append(args, branches...)
					code, r := reliabilityReport(t, root, args...)
					if code != tc.code || r.Gate.Verdict != tc.verdict {
						t.Fatalf("code=%d verdict=%s required=%+v", code, r.Gate.Verdict, r.Gate.Required)
					}
					assertScenarioCheck(t, r, "textual_merge", model.StatusPassed)
					if tc.name == "E_declared_contract_break" {
						assertScenarioCheck(t, r, "no_breaking_contracts", model.StatusFailed)
					}
					assertScenarioCheck(t, r, "test_selection", tc.selection)
					assertScenarioCheck(t, r, "integration_execution", tc.execution)
					if r.Selection == nil || len(r.Selection.Uncovered) != tc.uncovered || r.Selection.Inventory != tc.inventory || r.Selection.TestFiles != tc.selected || len(r.Executions) != tc.executions || len(r.Selected) != len(r.Selection.Commands) {
						t.Fatalf("selection=%+v executions=%+v", r.Selection, r.Executions)
					}
					for _, ev := range r.Executions {
						if ev.Observation.TestsRun != 1 {
							t.Fatalf("command success alone is insufficient: %+v", ev)
						}
					}
					if tc.name == "C_required_runner_missing" {
						omitted := false
						for _, omission := range r.Selection.Omitted {
							if omission.Reason == "environment_unavailable" && len(omission.TestFiles) == 1 && omission.TestFiles[0] == "frontend/model.test.js" {
								omitted = true
							}
						}
						if !omitted {
							t.Fatalf("missing runner silently dropped: %+v", r.Selection)
						}
					}
					for _, id := range []string{"test_selection", "integration_execution"} {
						found := false
						for _, coverage := range r.Coverage {
							if coverage.Analyzer == id {
								found = true
								if coverage.Status != scenarioCheck(t, r, id) {
									t.Fatalf("coverage=%+v", coverage)
								}
							}
						}
						if !found {
							t.Fatalf("missing coverage %s: %+v", id, r.Coverage)
						}
					}
					humanCode, out, errs := run(t, root, args...)
					if humanCode != tc.code || !strings.Contains(out, tc.text) || !strings.Contains(out, "recognized test") || !strings.Contains(out, "not behavioral coverage") {
						t.Fatalf("human code=%d\n%s\n%s", humanCode, out, errs)
					}
				})
			}
		})
	}
	t.Run("G_intentionally_limited", func(t *testing.T) {
		root := reliabilityRepo(t, true)
		policy := filepath.Join(t.TempDir(), "limited.json")
		if err := os.WriteFile(policy, []byte(`{"version":1,"name":"limited","require":["textual_merge","integration_execution"]}`), 0600); err != nil {
			t.Fatal(err)
		}
		args := []string{"gate", "--run", "--policy", policy, "feature/model"}
		code, r := reliabilityReport(t, root, args...)
		if code != 0 || r.Gate.Verdict != gate.Pass || len(r.Gate.Required) != 2 || r.Selection == nil || len(r.Selection.Uncovered) != 1 || len(r.Executions) != 1 || r.Executions[0].Observation.TestsRun != 1 {
			t.Fatalf("limited policy: code=%d report=%+v", code, r)
		}
		assertScenarioCheck(t, r, "test_selection", model.StatusIncomplete)
		assertScenarioCheck(t, r, "integration_execution", model.StatusPassed)
		code, out, errs := run(t, root, args...)
		if code != 0 || !strings.Contains(out, "Limited policy:") || !strings.Contains(out, "Uncovered changes") || !strings.Contains(out, "bounded verification") {
			t.Fatalf("%d %s %s", code, out, errs)
		}
	})
}

func scenarioCheck(t *testing.T, r gateReport, id string) model.Status {
	t.Helper()
	for _, c := range r.Checks {
		if c.ID == id {
			return c.Status
		}
	}
	for _, c := range r.Gate.Required {
		if c.ID == id {
			return c.Status
		}
	}
	t.Fatalf("missing check %s", id)
	return ""
}
func assertScenarioCheck(t *testing.T, r gateReport, id string, want model.Status) {
	t.Helper()
	if got := scenarioCheck(t, r, id); got != want {
		t.Fatalf("%s=%s want %s; checks=%+v", id, got, want, r.Checks)
	}
}
func missingRunnerScenario(t *testing.T) (string, []string) {
	t.Helper()
	root, agent := agentRepo(t, map[string]string{
		"backend/pyproject.toml": "[project]\nname='example'\n", "backend/model.py": "VALUE=1\n", "backend/test_model.py": "import unittest\nfrom model import VALUE\nclass Model(unittest.TestCase):\n def test_value(self): self.assertEqual(VALUE, 1)\n",
		"frontend/package.json": `{"name":"example","type":"module"}`, "frontend/model.js": "export const value=1;\n", "frontend/model.test.js": "import test from 'node:test';\nimport assert from 'node:assert/strict';\nimport { value } from './model.js';\ntest('value', () => assert.equal(value, 1));\n",
	})
	agent("feature/multi", map[string]string{"backend/model.py": "VALUE=1 # edit\n", "frontend/model.js": "export const value=1; // edit\n"})
	bin := t.TempDir()
	for _, tool := range []string{"git", "python3"} {
		resolved, err := exec.LookPath(tool)
		if err != nil {
			t.Fatal(err)
		}
		if err = os.Symlink(resolved, filepath.Join(bin, tool)); err != nil {
			t.Skip(err)
		}
	}
	t.Setenv("PATH", bin)
	return root, []string{"feature/multi"}
}

func TestGateMultipleDirtyWorktreesRemainUntouched(t *testing.T) {
	root, agent := agentRepo(t, map[string]string{"model.py": "VALUE=1\n", "other.py": "OTHER=1\n", ".gitignore": "ignored.tmp\n"})
	dirty := agent("feature/dirty", map[string]string{"model.py": "VALUE=2\n"})
	clean := agent("feature/clean", map[string]string{"other.py": "OTHER=2\n"})
	detached := filepath.Join(t.TempDir(), "detached")
	gitTest(t, root, "worktree", "add", "-q", "--detach", detached, "main")
	put(t, dirty, "model.py", "VALUE=3\n")
	put(t, dirty, "other.py", "OTHER=3\n")
	gitTest(t, dirty, "add", "other.py")
	put(t, dirty, "new.py", "NEW=1\n")
	put(t, dirty, "ignored.tmp", "ignored\n")
	put(t, detached, "model.py", "VALUE=99\n")
	before := map[string]string{}
	for _, path := range []string{root, dirty, clean, detached} {
		before[path] = gitTest(t, path, "status", "--porcelain=v1", "--untracked-files=all")
	}
	code, r := reliabilityReport(t, root, "gate")
	if code != 0 || r.Gate.Verdict != gate.Pass || len(r.Worktrees) != 4 {
		t.Fatalf("code=%d report=%+v", code, r)
	}
	for _, wt := range r.Worktrees {
		if wt.UncommittedIncluded {
			t.Fatal("uncommitted contents included", wt)
		}
		switch wt.Path {
		case dirty:
			if !wt.CommitIncluded || len(wt.Staged) != 1 || wt.Staged[0] != "other.py" || len(wt.Unstaged) != 1 || wt.Unstaged[0] != "model.py" || len(wt.Untracked) != 1 || wt.Untracked[0] != "new.py" {
				t.Fatal(wt)
			}
		case clean:
			if !wt.CommitIncluded || len(wt.Staged)+len(wt.Unstaged)+len(wt.Untracked) != 0 {
				t.Fatal(wt)
			}
		case detached:
			if !wt.Detached || len(wt.Unstaged) != 1 {
				t.Fatal(wt)
			}
		}
	}
	code, out, errs := run(t, root, "gate")
	if code != 0 || !strings.Contains(out, "uncommitted changes EXCLUDED") || !strings.Contains(out, "Commit intended changes and rerun") || !strings.Contains(out, "detached worktree") {
		t.Fatalf("%d %s %s", code, out, errs)
	}
	for path, want := range before {
		if got := gitTest(t, path, "status", "--porcelain=v1", "--untracked-files=all"); got != want {
			t.Fatalf("modified %s: before=%q after=%q", path, want, got)
		}
	}
}
