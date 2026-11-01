package integration

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Jake-Network/radar/internal/model"
	"github.com/Jake-Network/radar/internal/typecheck"
)

func tsTypecheckRepo(t *testing.T, config string) string {
	t.Helper()
	if _, err := exec.LookPath("node"); err != nil {
		if os.Getenv("RADAR_TEST_TYPESCRIPT_DIR") != "" {
			t.Fatal("configured TypeScript tests require node")
		}
		t.Skip("node unavailable")
	}
	compiler := os.Getenv("RADAR_TEST_TYPESCRIPT_DIR")
	if compiler == "" {
		compiler = "/tmp/radar-typescript-toolchain/node_modules/typescript"
	}
	if _, err := os.Stat(filepath.Join(compiler, "lib", "typescript.js")); err != nil {
		if os.Getenv("RADAR_TEST_TYPESCRIPT_DIR") != "" {
			t.Fatalf("configured TypeScript compiler unavailable: %v", err)
		}
		t.Skip("real TypeScript package unavailable; set RADAR_TEST_TYPESCRIPT_DIR")
	}
	root := t.TempDir()
	gitTest(t, root, "init", "-q")
	setup := []string{"node", "-e", "require('fs').mkdirSync('node_modules',{recursive:true});require('fs').cpSync(process.argv[1],'node_modules/typescript',{recursive:true})", compiler}
	runners := map[string]any{"version": 1, "profiles": []any{map[string]any{"root": ".", "setup": [][]string{setup}, "lockfiles": []string{"package-lock.json"}, "artifacts": []string{"node_modules"}}}}
	manifest, _ := json.Marshal(runners)
	if config == "" {
		config = `{"compilerOptions":{"strict":true,"target":"ES2020","module":"commonjs"},"include":["src/**/*.ts"]}`
	}
	for file, contents := range map[string]string{
		".radar/runners.json": string(manifest),
		"package-lock.json":   `{"name":"typecheck-fixture","lockfileVersion":3,"typescript":"5.9.3"}`,
		"tsconfig.json":       config,
		"src/model.ts":        "export interface User { name: string }\n",
		"src/view.ts":         "import {User} from './model';\nexport const view = (u: User): string => u.name;\n",
	} {
		put(t, root, file, contents)
	}
	gitTest(t, root, "add", ".")
	gitTest(t, root, "commit", "-qm", "baseline")
	gitTest(t, root, "branch", "baseline")
	t.Setenv("RADAR_STATE_DIR", t.TempDir())
	return root
}

func TestTypeScriptFailsOnlyCombinedChangesWithDeclaredSetup(t *testing.T) {
	root := tsTypecheckRepo(t, "")
	branch(t, root, "rename", "baseline", map[string]string{
		"src/model.ts": "export interface User { fullName: string }\n",
		"src/view.ts":  "import {User} from './model';\nexport const view = (u: User): string => u.fullName;\n",
	})
	branch(t, root, "consumer", "baseline", map[string]string{"src/report.ts": "import {User} from './model';\nexport const report = (u: User): string => u.name.toUpperCase();\n"})
	for _, alone := range []string{"rename", "consumer"} {
		r, err := Preview(context.Background(), root, typecheckOptions(alone))
		if err != nil {
			t.Fatal(err)
		}
		if c := findCheck(r, typecheck.CheckID); c == nil || c.Status != model.StatusPassed {
			t.Fatalf("%s alone: check=%+v executions=%+v gaps=%+v omitted=%+v", alone, c, r.TypecheckExecutions, r.TypecheckGaps, r.TypecheckOmitted)
		}
	}
	r, err := Preview(context.Background(), root, typecheckOptions("rename", "consumer"))
	if err != nil {
		t.Fatal(err)
	}
	ev := pythonExecution(r, typecheck.ToolTSC)
	if ev == nil || ev.Status != model.StatusFailed || len(ev.NewDiagnostics) != 1 || ev.NewDiagnostics[0].Path != "src/report.ts" || ev.BaseComparison == nil || !ev.BaseComparison.Compared {
		t.Fatalf("combined execution=%+v gaps=%+v omitted=%+v", ev, r.TypecheckGaps, r.TypecheckOmitted)
	}
	if len(r.RunnerPreparations) != 1 || r.RunnerPreparations[0].SetupCommands != 1 {
		t.Fatalf("candidate setup=%+v", r.RunnerPreparations)
	}
	// Exact candidate-tree dependency artifacts are reusable, but the check
	// itself still executes and reports the same newly introduced diagnostic.
	again, err := Preview(context.Background(), root, typecheckOptions("rename", "consumer"))
	if err != nil {
		t.Fatal(err)
	}
	if len(again.RunnerPreparations) != 1 || !again.RunnerPreparations[0].CacheHit || len(again.TypecheckExecutions) != 1 || again.TypecheckExecutions[0].Status != model.StatusFailed {
		t.Fatalf("cache skipped evidence: prep=%+v executions=%+v", again.RunnerPreparations, again.TypecheckExecutions)
	}
}

func TestTypeScriptExcludedChangedFileKeepsCheckIncomplete(t *testing.T) {
	root := tsTypecheckRepo(t, `{"compilerOptions":{"strict":true},"files":["src/model.ts","src/view.ts"]}`)
	branch(t, root, "excluded", "baseline", map[string]string{"src/excluded.ts": "const value: string = 42;\n"})
	r, err := Preview(context.Background(), root, typecheckOptions("excluded"))
	if err != nil {
		t.Fatal(err)
	}
	if check := findCheck(r, typecheck.CheckID); check == nil || check.Status == model.StatusPassed {
		t.Fatalf("excluded file passed: %+v gaps=%+v", check, r.TypecheckGaps)
	}
	if len(r.TypecheckGaps) != 1 || r.TypecheckGaps[0].Path != "src/excluded.ts" {
		t.Fatalf("gaps=%+v", r.TypecheckGaps)
	}
}

func TestTypeScriptMissingCompilerIsAnOmission(t *testing.T) {
	root := tsTypecheckRepo(t, "")
	gitTest(t, root, "rm", ".radar/runners.json")
	gitTest(t, root, "commit", "-qm", "remove setup")
	gitTest(t, root, "branch", "-f", "baseline", "HEAD")
	branch(t, root, "change", "baseline", map[string]string{"src/model.ts": "export interface User { name: string; age?: number }\n"})
	r, err := Preview(context.Background(), root, typecheckOptions("change"))
	if err != nil {
		t.Fatal(err)
	}
	if len(r.TypecheckOmitted) != 1 || r.TypecheckOmitted[0].Reason != "environment_unavailable" || !strings.Contains(r.TypecheckOmitted[0].Explanation, "TypeScript") {
		t.Fatalf("omitted=%+v executions=%+v", r.TypecheckOmitted, r.TypecheckExecutions)
	}
}
