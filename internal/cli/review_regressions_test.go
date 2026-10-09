package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Jake-Network/radar/internal/integration"
	"github.com/Jake-Network/radar/internal/planning"
)

func TestGateWorktreeInspectionFailureIsWarning(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX Git shim")
	}
	root := reliabilityRepo(t, false)
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	shim := t.TempDir()
	script := "#!/bin/sh\ncase \"$*\" in *'worktree list --porcelain -z'*) echo 'unsupported -z' >&2; exit 129;; esac\nexec " + shellArg(realGit) + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(shim, "git"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", shim+string(os.PathListSeparator)+os.Getenv("PATH"))
	for _, run := range []bool{false, true} {
		args := []string{"gate", "--base", "main", "feature/model"}
		if run {
			args = append(args, "--run")
		}
		result := invoke(t, root, 0, args...)
		if result["gate"].(map[string]any)["verdict"] != "pass" || result["worktree_inspection_error"] == nil {
			t.Fatal(result)
		}
		raw, _ := json.Marshal(result)
		var compact map[string]any
		if err := json.Unmarshal([]byte(compactVerification(string(raw))), &compact); err != nil {
			t.Fatal(err)
		}
		if compact["worktree_inspection_error"] == nil {
			t.Fatal("MCP warning missing", compact)
		}
		var out, errs bytes.Buffer
		if code := Run(context.Background(), append([]string{"--root", root}, args...), &out, &errs); code != 0 || !strings.Contains(out.String(), "worktree inspection unavailable") {
			t.Fatal(code, out.String(), errs.String())
		}
	}
}

func TestMergeCheckUnstableConfigurationDoesNotPersistEvidence(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip(err)
	}
	for _, kind := range []string{"policy", "plan"} {
		t.Run(kind, func(t *testing.T) {
			root := gitRepo(t)
			path := filepath.Join(root, ".radar", kind+".json")
			quoted, _ := json.Marshal(path)
			put(t, root, "test_real.py", "import unittest\nfrom pathlib import Path\nclass Real(unittest.TestCase):\n def test_real(self):\n  with Path("+string(quoted)+").open('a') as f: f.write('\\n')\n  self.assertEqual(1+1,2)\n")
			gitTest(t, root, "add", ".")
			gitTest(t, root, "commit", "-qm", "baseline")
			argv := []string{"python3", "-m", "unittest", "test_real"}
			p := planning.Plan{SchemaVersion: "1", FeatureID: "review", Intent: "verify actual tests", BaseRevision: gitTest(t, root, "rev-parse", "HEAD"), Requirements: []planning.Requirement{{ID: "r", Intent: "test"}}, Acceptance: []planning.Criterion{{ID: "test", Requirement: "r", Intent: "run", Rule: &planning.Rule{Kind: "test_run", Command: argv}}}, Tasks: []planning.Task{{ID: "test-task", Intent: "verify", Requirements: []string{"r"}, Components: []string{"file:test_real.py"}, Acceptance: []string{"test"}}}}
			p.Approval = &planning.Approval{Reviewer: "human", ReviewedAt: "2026-10-09T00:00:00Z", Checkpoint: p.BaseRevision, PlanDigest: planning.Digest(p)}
			plan, _ := json.Marshal(p)
			put(t, root, ".radar/plan.json", string(plan))
			put(t, root, ".radar/policy.json", `{"version":1,"require":["textual_merge","integration_execution"]}`)
			// Establish that this real execution would produce reusable approved
			// plan evidence before the invocation-artifact boundary invalidation.
			observed, err := integration.Preview(context.Background(), root, integration.Options{Base: "HEAD", Branches: []string{"HEAD"}, Plan: &p, Verify: true, AllowExecution: true, Command: argv, CWD: "."})
			if err != nil || observed.Execution == nil || observed.Execution.PlanRecord == nil {
				t.Fatalf("fixture did not produce approved plan evidence: %v %+v", err, observed.Execution)
			}
			put(t, root, ".radar/plan.json", string(plan))
			put(t, root, ".radar/policy.json", `{"version":1,"require":["textual_merge","integration_execution"]}`)
			args := []string{"merge-check", "--base", "HEAD", "--branches", "HEAD", "--plan", ".radar/plan.json", "--policy", ".radar/policy.json", "--verify", "--allow-execution", "--evidence-output", ".radar/evidence/rejected.json", "--"}
			result := invoke(t, root, 1, append(args, argv...)...)
			if result["gate"].(map[string]any)["verdict"] != "blocked" {
				t.Fatal(result)
			}
			if _, err := os.Stat(filepath.Join(root, ".radar/evidence/rejected.json")); !os.IsNotExist(err) {
				t.Fatal("rejected invocation persisted reusable evidence", err)
			}
			ev := result["execution"].(map[string]any)
			if ev["status"] != "passed" || ev["plan_record"] != nil {
				t.Fatal("actual observation or reusable record incorrect", ev)
			}
			if !strings.Contains(strings.Join(anyStrings(result["limitations"]), " "), "Evidence output omitted") {
				t.Fatal(result)
			}
		})
	}
}

func anyStrings(v any) []string {
	out := []string{}
	for _, s := range v.([]any) {
		out = append(out, s.(string))
	}
	return out
}

func TestStaticGateConfigurationChangeSuggestsSameGate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX Git shim")
	}
	root := reliabilityRepo(t, false)
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	policyPath := filepath.Join(root, ".radar/policy.json")
	original := `{"version":1,"require":["textual_merge"]}`
	put(t, root, ".radar/policy.json", original)
	shim := t.TempDir()
	script := "#!/bin/sh\ncase \"$*\" in *'rev-parse --verify'*) printf '\\n' >> " + shellArg(policyPath) + ";; esac\nexec " + shellArg(realGit) + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(shim, "git"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", shim+string(os.PathListSeparator)+os.Getenv("PATH"))
	result := invoke(t, root, 1, "gate", "--base", "main", "--policy", ".radar/policy.json", "feature/model")
	next := result["next"].(string)
	if strings.Contains(next, "--run") || !strings.Contains(next, "radar gate --base main --policy .radar/policy.json feature/model") {
		t.Fatal(next)
	}
	if _, ok := result["executions"]; ok {
		if ev, ok := result["executions"].([]any); ok && len(ev) > 0 {
			t.Fatal("static gate executed", ev)
		}
	}
}
