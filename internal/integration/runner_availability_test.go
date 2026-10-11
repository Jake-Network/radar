package integration

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/Jake-Network/radar/internal/gate"
	"github.com/Jake-Network/radar/internal/model"
	"github.com/Jake-Network/radar/internal/testselection"
)

func TestObservedRunnerPreservesOtherSelectionBlockers(t *testing.T) {
	first := testselection.Command{ID: "first", Command: []string{"./node_modules/.bin/vitest", "run"}, CWD: "first", Tier: testselection.TierRequired}
	second := first
	second.ID, second.CWD = "second", "second"
	blocker := "required command runner unavailable or unverified: ./node_modules/.bin/vitest run"
	s := testselection.Selection{Commands: []testselection.Command{first, second}, Blocking: []string{blocker, blocker, "inventory incomplete"}}
	confirmExecutedRunner(&s, first)
	if len(s.Blocking) != 2 || s.Blocking[0] != blocker || s.Blocking[1] != "inventory incomplete" || !s.Commands[0].ToolAvailable || s.Commands[1].ToolAvailable {
		t.Fatalf("another command or inventory gap was resolved: %+v", s)
	}
}

// These trusted runner stand-ins test reconciliation of static uncertainty
// with recognized execution evidence, rather than third-party runner behavior.
func TestSuiteRunnerAvailabilityUsesObservedCandidateExecution(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture uses a POSIX shell")
	}
	for _, framework := range []string{"jest", "vitest"} {
		for _, mode := range []string{"targeted", "balanced", "full"} {
			for _, outcome := range []string{"passed", "missing", "zero-tests"} {
				t.Run(framework+"/"+mode+"/"+outcome, func(t *testing.T) {
					root := t.TempDir()
					gitTest(t, root, "init", "-q")
					put(t, root, "package.json", `{"devDependencies":{"`+framework+`":"1.0.0"}}`)
					put(t, root, "core.test.ts", "export const VALUE = 1;\n")
					if outcome != "missing" {
						output := "Tests: 1 passed, 1 total"
						if framework == "vitest" {
							output = "Tests 1 passed (1)"
						}
						if outcome == "zero-tests" {
							output = "No test files found"
						}
						name := filepath.Join("node_modules", ".bin", framework)
						put(t, root, name, "#!/bin/sh\nprintf '%s\\n' '"+output+"'\n")
						if err := os.Chmod(filepath.Join(root, name), 0700); err != nil {
							t.Fatal(err)
						}
					}
					gitTest(t, root, "add", ".")
					gitTest(t, root, "commit", "-qm", "baseline")
					gitTest(t, root, "branch", "baseline")
					put(t, root, "core.test.ts", "export const VALUE = 2;\n")
					gitTest(t, root, "commit", "-qam", "change test")
					r, err := Preview(context.Background(), root, Options{Base: "baseline", Branches: []string{"HEAD"}, Verify: true, AllowExecution: true, Suite: mode, Timeout: 10 * time.Second, Policy: &gate.Policy{Version: 1, Require: []string{"integration_execution", "test_selection"}}})
					if err != nil {
						t.Fatal(err)
					}
					if outcome == "passed" {
						checkStatus(t, r, "integration_execution", model.StatusPassed)
						checkStatus(t, r, "test_selection", model.StatusPassed)
						if r.Gate.Verdict != gate.Pass || len(r.Selection.Blocking) != 0 || !r.Selection.Commands[0].ToolAvailable {
							t.Fatalf("observed runner remained blocked: %+v", r)
						}
					} else if r.Gate.Verdict == gate.Pass || len(r.Selection.Blocking) == 0 {
						t.Fatalf("unverified runner passed: %+v", r)
					}
				})
			}
		}
	}
}
