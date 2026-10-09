package cli

import (
	"encoding/json"
	"github.com/Jake-Network/radar/internal/planning"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckWithoutStateOrManifest(t *testing.T) {
	root := gitRepo(t)
	put(t, root, "lib.ts", "export const count = 1;\n")
	put(t, root, "client.ts", "import {count} from './lib';\nexport const total = count;\n")
	gitTest(t, root, "add", ".")
	gitTest(t, root, "commit", "-qm", "baseline")
	put(t, root, "lib.ts", "export const count = 2;\n")
	r := invoke(t, root, 0, "check", "--base", "HEAD")
	if r["status"] != "incomplete" {
		t.Fatal("missing coverage passed", r)
	}
	impact := r["impact"].(map[string]any)
	if len(impact["changed"].([]any)) != 1 || len(impact["affected"].([]any)) != 1 {
		t.Fatal("missing inferred impact", impact)
	}
	if _, err := os.Stat(filepath.Join(root, ".radar")); !os.IsNotExist(err) {
		t.Fatal("check created state", err)
	}
	invoke(t, root, 1, "check", "--base", "HEAD", "--require-complete")
	first := r["feedback_digest"]
	again := invoke(t, root, 0, "check", "--base", "HEAD")
	if again["feedback_digest"] != first {
		t.Fatal("unstable feedback digest")
	}
	put(t, root, "lib.ts", "export const count = 3;\n")
	changed := invoke(t, root, 0, "check", "--base", "HEAD")
	if changed["feedback_digest"] == first {
		t.Fatal("stale repair identity")
	}
	invoke(t, root, 2, "check", "--base", "missing-ref")
}

func TestCheckMalformedManifestFails(t *testing.T) {
	root := gitRepo(t)
	put(t, root, "svc.py", "def count(): return 1\n")
	gitTest(t, root, "add", ".")
	gitTest(t, root, "commit", "-qm", "baseline")
	put(t, root, ".radar/contracts.json", "{invalid")
	r := invoke(t, root, 1, "check", "--base", "HEAD")
	if r["status"] != "failed" {
		t.Fatal(r)
	}
}

func TestCheckLexicalManifestWarningDoesNotBecomeError(t *testing.T) {
	root := gitRepo(t)
	put(t, root, "schema.json", `{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"total":{"type":"integer"}}}`)
	put(t, root, "client.ts", "export const untouched = true;\n")
	put(t, root, ".radar/contracts.json", `{"version":1,"bindings":[{"id":"total","schema":"schema.json","pointer":"","consumer":"client.ts","fields":["total"],"direction":"response"}]}`)
	gitTest(t, root, "add", ".")
	gitTest(t, root, "commit", "-qm", "baseline")
	r := invoke(t, root, 0, "check", "--base", "HEAD")
	found := false
	for _, raw := range r["findings"].([]any) {
		f := raw.(map[string]any)
		if f["code"] == "manifest_lint" {
			found = true
			if f["severity"] != "warning" {
				t.Fatal("lexical uncertainty upgraded", f)
			}
		}
	}
	if !found {
		t.Fatal("missing useful lint warning", r)
	}
}

func TestVerifyPropagatesObservedEnvironmentError(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip(err)
	}
	root := gitRepo(t)
	put(t, root, "svc.py", "def run(): return 1\n")
	gitTest(t, root, "add", ".")
	gitTest(t, root, "commit", "-qm", "baseline")
	sha := gitTest(t, root, "rev-parse", "HEAD")
	invoke(t, root, 0, "init")
	p := planning.Plan{SchemaVersion: "1", FeatureID: "execution-error", Intent: "do not hide environment errors", BaseRevision: sha,
		Requirements: []planning.Requirement{{ID: "r", Intent: "tests execute"}},
		Acceptance:   []planning.Criterion{{ID: "tests", Requirement: "r", Intent: "run real tests", Rule: &planning.Rule{Kind: "test_run", Command: []string{"python3", "-c", "import radar_nonexistent_test_dependency"}}}},
		Tasks:        []planning.Task{{ID: "implementation", Intent: "implement", Requirements: []string{"r"}, Components: []string{"file:svc.py"}, Acceptance: []string{"tests"}}}}
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	put(t, root, "candidate.json", string(raw))
	invoke(t, root, 0, "approve", "--plan", "candidate.json", "--reviewer", "fixture", "--output", "approved.json")
	// Keep the legacy test command's informational exit compatibility; verify
	// must still distinguish the stored environment error from passing evidence.
	record := invoke(t, root, 0, "test", "--plan", "approved.json", "--ref", sha, "--allow-execution", "--", "python3", "-c", "import radar_nonexistent_test_dependency")
	if record["status"] != "error" {
		t.Fatal(record)
	}
	verified := invoke(t, root, 2, "verify", "--plan", "approved.json", "--ref", sha, "--evidence", record["id"].(string))
	if verified["status"] != "error" || verified["tasks"].([]any)[0].(map[string]any)["status"] != "error" {
		t.Fatal("execution error hidden", verified)
	}
}

func TestCheckAggregatesDiscoveredRemovedFieldWithoutManifest(t *testing.T) {
	root := gitRepo(t)
	schema := `{"openapi":"3.0.0","paths":{"/users":{"get":{"responses":{"200":{"content":{"application/json":{"schema":{"$ref":"#/components/schemas/User"}}}}}}}},"components":{"schemas":{"User":{"type":"object","properties":{"email":{"type":"string"}}}}}}`
	put(t, root, "openapi.json", schema)
	put(t, root, "types.ts", "export interface User { email: string; }\n")
	put(t, root, "client.ts", "import type { User } from './types';\nasync function load() { const user: User = await (await fetch('/users')).json(); return user.email; }\n")
	gitTest(t, root, "add", ".")
	gitTest(t, root, "commit", "-qm", "baseline")
	discovered := invoke(t, root, 0, "discover")
	if len(discovered["candidates"].([]any)) == 0 {
		t.Fatal("discovery missing", discovered)
	}
	put(t, root, "openapi.json", strings.Replace(schema, `"email":{"type":"string"}`, `"display":{"type":"string"}`, 1))
	report := invoke(t, root, 0, "check", "--base", "HEAD")
	found := false
	for _, raw := range report["findings"].([]any) {
		f := raw.(map[string]any)
		if f["code"] == "discovered_response_field_removed" {
			found = true
			if f["severity"] != "warning" || f["consumer"] != "client.ts" {
				t.Fatal(f)
			}
		}
	}
	if !found {
		t.Fatal("discover findings lost by check", report)
	}
	if _, err := os.Stat(filepath.Join(root, ".radar/contracts.json")); !os.IsNotExist(err) {
		t.Fatal("proposal auto-accepted", err)
	}
}
