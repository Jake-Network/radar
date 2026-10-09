package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Jake-Network/radar/internal/evidence"
	"github.com/Jake-Network/radar/internal/gate"
	"github.com/Jake-Network/radar/internal/integration"
	"github.com/Jake-Network/radar/internal/model"
	"github.com/Jake-Network/radar/internal/planning"
	"github.com/Jake-Network/radar/internal/testselection"
)

func TestSelectedConfigurationChangesCannotPass(t *testing.T) {
	for _, kind := range []string{"policy", "plan"} {
		for _, external := range []bool{false, true} {
			for _, machine := range []bool{false, true} {
				t.Run(kind+"/"+map[bool]string{false: "repository", true: "external"}[external]+"/"+map[bool]string{false: "human", true: "json"}[machine], func(t *testing.T) {
					root := gitRepo(t)
					put(t, root, "svc.py", "VALUE = 1\n")
					gitTest(t, root, "add", ".")
					gitTest(t, root, "commit", "-qm", "baseline")
					path := filepath.Join(root, ".radar", kind+".json")
					if external {
						path = filepath.Join(t.TempDir(), kind+".json")
					}
					original := `{"version":1,"require":["dependency_impact"]}`
					o := options{base: "HEAD", head: "HEAD", suggestTests: true}
					if kind == "policy" {
						o.policy = path
					} else {
						original = `{"schema_version":"1"}`
						o.plan = path
					}
					if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(path, []byte(original), 0600); err != nil {
						t.Fatal(err)
					}
					var out, errout bytes.Buffer
					a := &app{ctx: context.Background(), root: root, out: &out, errout: &errout, machine: machine}
					code := a.checkWithRecommendation(o, func(ctx context.Context, root, ref string, s model.Snapshot, changed []string, p *planning.Plan) (testselection.Proposal, error) {
						if err := os.WriteFile(path, []byte(original+"\n"), 0600); err != nil {
							t.Fatal(err)
						}
						return testselection.Recommend(ctx, root, ref, s, changed, p)
					})
					if code != 1 {
						t.Fatalf("exit %d: %s %s", code, &out, &errout)
					}
					if !machine {
						if !strings.Contains(out.String(), "Gate: blocked") || !strings.Contains(out.String(), "configuration_stability: incomplete") {
							t.Fatal(out.String())
						}
						return
					}
					var r checkReport
					if err := json.Unmarshal(out.Bytes(), &r); err != nil {
						t.Fatal(err)
					}
					if r.Gate.Verdict != "blocked" || len(r.Configuration) != 1 {
						t.Fatal(r)
					}
					input := r.Configuration[0]
					if input.Digest != configurationDigest([]byte(original)) || input.Digest == input.FinalDigest || input.Status != model.StatusIncomplete {
						t.Fatal(input)
					}
					for _, check := range r.Checks {
						if check.Status == model.StatusPassed {
							t.Fatal("mixed observations passed", check)
						}
					}
				})
			}
		}
	}
}

func TestStableConfigurationCLICompatibility(t *testing.T) {
	root, branch := agentRepo(t, map[string]string{"svc.py": "VALUE = 1\n"})
	branch("feature", map[string]string{"svc.py": "VALUE = 2\n"})
	policy := `{"version":1,"require":["textual_merge","no_breaking_contracts","configuration_stability"]}`
	put(t, root, ".radar/policy.json", policy)
	for _, args := range [][]string{
		{"gate", "--base", "main", "--policy", ".radar/policy.json", "feature"},
		{"merge-check", "--base", "main", "--branches", "feature", "--policy", ".radar/policy.json"},
	} {
		r := invoke(t, root, 0, args...)
		if r["gate"].(map[string]any)["verdict"] != "pass" {
			t.Fatal(r)
		}
		inputs := r["configuration_inputs"].([]any)
		input := inputs[0].(map[string]any)
		if input["digest"] != configurationDigest([]byte(policy)) || input["final_digest"] != input["digest"] || input["status"] != "passed" {
			t.Fatal(input)
		}
	}
}

// Execute a real candidate test that changes the caller's selected policy.
// Candidate files stay intact, so only invocation artifact stability can block.
func TestExecutedCandidateCannotSilentlyReplacePolicy(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip(err)
	}
	root := gitRepo(t)
	gitTest(t, root, "checkout", "-q", "-b", "main")
	policyPath := filepath.Join(root, ".radar", "policy.json")
	quoted, err := json.Marshal(policyPath)
	if err != nil {
		t.Fatal(err)
	}
	put(t, root, "test_real.py", "import unittest\nfrom pathlib import Path\nclass Real(unittest.TestCase):\n def test_mutate_policy(self):\n  with Path("+string(quoted)+").open('a') as f: f.write('\\n')\n  self.assertEqual(1 + 1, 2)\n")
	gitTest(t, root, "add", ".")
	gitTest(t, root, "commit", "-qm", "baseline")
	gitTest(t, root, "checkout", "-q", "-b", "feature")
	content, err := os.ReadFile(filepath.Join(root, "test_real.py"))
	if err != nil {
		t.Fatal(err)
	}
	put(t, root, "test_real.py", string(content)+"# changed test\n")
	gitTest(t, root, "add", ".")
	gitTest(t, root, "commit", "-qm", "changed test")
	gitTest(t, root, "checkout", "-q", "main")
	original := `{"version":1,"require":["textual_merge"]}`
	for _, args := range [][]string{
		{"gate", "--base", "main", "--run", "--policy", ".radar/policy.json", "feature"},
		{"merge-check", "--base", "main", "--branches", "feature", "--policy", ".radar/policy.json", "--verify", "--allow-execution", "--", "python3", "-m", "unittest", "test_real"},
	} {
		put(t, root, ".radar/policy.json", original)
		r := invoke(t, root, 1, args...)
		if r["gate"].(map[string]any)["verdict"] != "blocked" {
			t.Fatal(r)
		}
		executions := r["executions"].([]any)
		if len(executions) != 1 || executions[0].(map[string]any)["status"] != "passed" {
			t.Fatal("actual test result lost", r)
		}
		inputs := r["configuration_inputs"].([]any)
		if inputs[0].(map[string]any)["status"] != "incomplete" {
			t.Fatal(r)
		}
	}
}

func TestConfigurationAbsentDoesNotAddChecks(t *testing.T) {
	root, branch := agentRepo(t, map[string]string{"svc.py": "VALUE = 1\n"})
	branch("feature", map[string]string{"svc.py": "VALUE = 2\n"})
	for _, args := range [][]string{
		{"gate", "--base", "main", "feature"},
		{"check", "--base", "main", "--head", "feature"},
	} {
		r := invoke(t, root, 0, args...)
		for _, raw := range r["checks"].([]any) {
			if raw.(map[string]any)["id"] == "configuration_stability" {
				t.Fatal("unsupplied configuration generated check", r)
			}
		}
	}
}

func TestMissingRequiredEvidenceIsNotAnObservedCheck(t *testing.T) {
	root, branch := agentRepo(t, map[string]string{"svc.py": "VALUE = 1\n"})
	branch("feature", map[string]string{"svc.py": "VALUE = 2\n"})
	put(t, root, ".radar/policy.json", `{"version":1,"require":["future_capability"]}`)
	for _, args := range [][]string{
		{"gate", "--base", "main", "--policy", ".radar/policy.json", "feature"},
		{"check", "--base", "main", "--head", "feature", "--policy", ".radar/policy.json"},
	} {
		r := invoke(t, root, 1, args...)
		for _, raw := range r["checks"].([]any) {
			if raw.(map[string]any)["id"] == "future_capability" {
				t.Fatal("missing requirement materialized as real observation", r)
			}
		}
		required := r["gate"].(map[string]any)["required_checks"].([]any)
		if len(required) != 1 || required[0].(map[string]any)["status"] != "unknown" {
			t.Fatal(r)
		}
	}
}

func TestConfigurationStreamCLI(t *testing.T) {
	if root := os.Getenv("RADAR_STREAM_TEST_ROOT"); root != "" {
		args := []string{"--root", root, "--json", os.Getenv("RADAR_STREAM_TEST_COMMAND"), "--base", "main", "--policy", "/dev/stdin"}
		if args[3] == "gate" {
			args = append(args, "feature")
		} else {
			args = append(args, "--head", "feature")
		}
		os.Exit(Run(context.Background(), args, os.Stdout, os.Stderr))
	}
	if _, err := os.Stat("/dev/stdin"); err != nil {
		t.Skip("stdin device unavailable")
	}
	root, branch := agentRepo(t, map[string]string{"svc.py": "VALUE = 1\n"})
	branch("feature", map[string]string{"svc.py": "VALUE = 2\n"})
	for _, command := range []string{"gate", "check"} {
		for _, requireStability := range []bool{false, true} {
			policy := `{"version":1,"require":["source_stability"]}`
			if command == "gate" {
				policy = `{"version":1,"require":["textual_merge"]}`
			}
			if requireStability {
				policy = `{"version":1,"require":["configuration_stability"]}`
			}
			cmd := exec.Command(os.Args[0], "-test.run=^TestConfigurationStreamCLI$")
			cmd.Env = append(os.Environ(), "RADAR_STREAM_TEST_ROOT="+root, "RADAR_STREAM_TEST_COMMAND="+command)
			cmd.Stdin = strings.NewReader(policy)
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			err := cmd.Run()
			if requireStability {
				if e, ok := err.(*exec.ExitError); !ok || e.ExitCode() != 1 {
					t.Fatalf("required stability exit %v: %s %s", err, &stdout, &stderr)
				}
			} else if err != nil {
				t.Fatalf("stream rejected: %v %s %s", err, &stdout, &stderr)
			}
			var r map[string]any
			if err := json.Unmarshal(stdout.Bytes(), &r); err != nil {
				t.Fatal(err, stdout.String())
			}
			input := r["configuration_inputs"].([]any)[0].(map[string]any)
			if input["status"] != "unknown" || input["digest"] != configurationDigest([]byte(policy)) || input["final_digest"] != nil {
				t.Fatal(input)
			}
		}
	}
}

func TestConfigurationPipePlanIsCapturedOnce(t *testing.T) {
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer read.Close()
	path := fmt.Sprintf("/dev/fd/%d", read.Fd())
	if _, err := os.Stat(path); err != nil {
		write.Close()
		t.Skip("file descriptor paths unavailable")
	}
	plan := `{"schema_version":"1"}`
	if _, err := write.Write([]byte(plan)); err != nil {
		t.Fatal(err)
	}
	write.Close()
	a := &app{root: t.TempDir()}
	config, err := a.captureConfiguration(options{plan: path})
	if err != nil {
		t.Fatal(err)
	}
	if config.Plan == nil || config.Inputs[0].Digest != configurationDigest([]byte(plan)) {
		t.Fatal(config)
	}
	read.Close() // A one-shot stream must not be reopened at completion.
	check := config.finish()
	if check.Status != model.StatusUnknown || config.Inputs[0].FinalDigest != "" {
		t.Fatal(check, config)
	}
	checks, result := applyConfiguration(check, nil, gate.Evaluate(gate.Policy{Version: 1, Require: []string{"optional_absent"}}, nil))
	if len(checks) != 1 || result.Verdict != gate.Blocked {
		t.Fatal(checks, result)
	}
}

func TestConfigurationInputLimitsUseParserBounds(t *testing.T) {
	root := t.TempDir()
	for _, item := range []struct {
		kind  string
		limit int
	}{{"policy", gate.MaxBytes}, {"plan", planning.MaxBytes}} {
		path := filepath.Join(root, item.kind+".json")
		if err := os.WriteFile(path, bytes.Repeat([]byte(" "), item.limit+1), 0600); err != nil {
			t.Fatal(err)
		}
		o := options{}
		if item.kind == "policy" {
			o.policy = path
		} else {
			o.plan = path
		}
		if _, err := (&app{root: root}).captureConfiguration(o); err == nil || !strings.Contains(err.Error(), "exceeds") {
			t.Fatal("size bound missing", item.kind, err)
		}
	}
}

func TestConfigurationInstabilityRemovesReusablePlanRecords(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "policy.json")
	original := `{"version":1,"require":["integration_execution"]}`
	if err := os.WriteFile(path, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	config, err := (&app{root: root}).captureConfiguration(options{policy: path})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(original+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	execution := integration.ExecutionEvidence{ID: "original-id", Status: model.StatusPassed, PlanRecord: &evidence.Record{}}
	r := integration.Report{Status: model.StatusPassed, Executions: []integration.ExecutionEvidence{execution}, Execution: &execution, Checks: []gate.Check{{ID: "integration_execution", Status: model.StatusPassed}}}
	r.Gate = gate.Evaluate(*config.Policy, r.Checks)
	config.applyIntegration(&r)
	if r.Gate.Verdict != gate.Blocked || r.Status != model.StatusIncomplete {
		t.Fatal(r)
	}
	for _, ev := range []integration.ExecutionEvidence{r.Executions[0], *r.Execution} {
		if ev.PlanRecord != nil || ev.Status != model.StatusPassed || ev.ID == "original-id" {
			t.Fatal(ev)
		}
		id := ev.ID
		ev.ID = ""
		data, err := json.Marshal(ev)
		if err != nil {
			t.Fatal(err)
		}
		if id != model.StableID("integration-evidence-v1", string(data)) {
			t.Fatal("stale execution identity", id)
		}
	}
}
