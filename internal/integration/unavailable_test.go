package integration

import (
	"context"
	"encoding/json"
	"time"

	"github.com/Jake-Network/radar/internal/gate"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Jake-Network/radar/internal/testselection"
)

// An absent repository-relative runner is an environment gap either way;
// the reason names node_modules only when that is the cause.
func TestUnavailableNamesTheReasonARunnerIsAbsent(t *testing.T) {
	root := t.TempDir()
	for _, tc := range []struct{ tool, want string }{
		{"./node_modules/.bin/jest", "node_modules are not copied"},
		{"./gradlew", "not committed, or removed by a branch"},
	} {
		got := unavailable(root, testselection.Command{Command: []string{tc.tool}, CWD: "."})
		if !strings.Contains(got, "is absent from the private candidate") || !strings.Contains(got, tc.want) {
			t.Fatalf("%s: %q", tc.tool, got)
		}
	}
}

func TestUnavailableWindowsNativeFileKinds(t *testing.T) {
	for _, name := range []string{"runner.exe", "runner.EXE", "runner.com", "runner.CoM"} {
		if why := windowsRunnerUnavailable(name); why != "" {
			t.Fatalf("native executable %s: %s", name, why)
		}
	}
	for _, name := range []string{"runner.cmd", "runner.CMD", "runner.bat", "runner.BAT"} {
		if why := windowsRunnerUnavailable(name); !strings.Contains(why, "direct batch execution is unsupported") {
			t.Fatalf("batch %s: %s", name, why)
		}
	}
	for _, name := range []string{"runner", "runner.py", "runner.ps1", "runner.exe.txt"} {
		if why := windowsRunnerUnavailable(name); !strings.Contains(why, "supported native Windows executable extension") {
			t.Fatalf("non-native file %s: %s", name, why)
		}
	}
}

func TestUnavailableNativeWindowsCandidateRunners(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("native Windows file mode and command lookup")
	}
	root := t.TempDir()
	for _, name := range []string{"tool.exe", "tool.com", "custom.cmd", "custom.bat", "plain.txt", "mvnw.cmd", "gradlew.bat"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("@echo off\r\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	// Availability is a preflight file check, not proof that bytes form a valid
	// PE executable. Actual launch failures remain execution errors.
	for _, name := range []string{"tool.exe", "tool.com"} {
		if why := unavailable(root, testselection.Command{Command: []string{"./" + name}, CWD: "."}); why != "" {
			t.Fatalf("native runner blocked by Unix mode: %s", why)
		}
	}
	for _, name := range []string{"custom.cmd", "custom.bat"} {
		if why := unavailable(root, testselection.Command{Command: []string{"./" + name}, CWD: "."}); !strings.Contains(why, "direct batch execution is unsupported") {
			t.Fatalf("batch wrongly advertised available: %s", why)
		}
	}
	for _, name := range []string{"mvnw.cmd", "gradlew.bat"} {
		for _, prefix := range []string{"./", `.\`} {
			if why := unavailable(root, testselection.Command{Command: []string{prefix + name}, CWD: "."}); !strings.Contains(why, "wrapper properties") {
				t.Fatalf("wrapper guard bypassed for %s%s: %s", prefix, name, why)
			}
		}
	}
	if why := unavailable(root, testselection.Command{Command: []string{"./plain.txt"}, CWD: "."}); !strings.Contains(why, "supported native Windows executable extension") {
		t.Fatalf("arbitrary file accepted: %s", why)
	}
	t.Setenv("PATH", root)
	t.Setenv("PATHEXT", ".COM;.EXE;.BAT;.CMD")
	for _, name := range []string{"mvnw.cmd", "gradlew.bat"} {
		if why := unavailable(root, testselection.Command{Command: []string{name}, CWD: "."}); !strings.Contains(why, "wrapper properties") {
			t.Fatalf("PATH wrapper bypassed guard: %s", why)
		}
	}
	if err := os.Mkdir(filepath.Join(root, "directory.exe"), 0700); err != nil {
		t.Fatal(err)
	}
	if why := unavailable(root, testselection.Command{Command: []string{"./directory.exe"}, CWD: "."}); why == "" {
		t.Fatal("directory accepted as executable")
	}
}

func TestWindowsBatchSelectionRemainsBlockedWithoutExecution(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("native Windows batch selection")
	}
	t.Setenv("RADAR_STATE_DIR", t.TempDir())
	root := t.TempDir()
	gitTest(t, root, "init", "-q")
	put(t, root, "test_native.py", "import unittest\nclass Native(unittest.TestCase):\n def test_case(self): self.assertTrue(True)\n")
	put(t, root, "custom.cmd", "@echo off\r\necho ran>batch-ran\r\n")
	put(t, root, ".radar/runners.json", `{"version":1,"profiles":[{"root":".","prefix":["./custom.cmd"]}]}`)
	gitTest(t, root, "add", ".")
	gitTest(t, root, "commit", "-qm", "batch runner fixture")
	gitTest(t, root, "branch", "baseline")
	gitTest(t, root, "checkout", "-qb", "feature")
	put(t, root, "test_native.py", "import unittest\nclass Native(unittest.TestCase):\n def test_case(self): self.assertTrue(True)\n# selected change\n")
	gitTest(t, root, "commit", "-qam", "select native fixture test")
	gitTest(t, root, "checkout", "baseline")
	before := gitTest(t, root, "show-ref")
	status := gitTest(t, root, "status", "--porcelain")
	r, err := Preview(context.Background(), root, Options{Base: "baseline", Branches: []string{"feature"}, Verify: true, AllowExecution: true, Suite: "targeted", MaxCommands: 4, Timeout: 20 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if r.Gate.Verdict != gate.Blocked || gate.Exit(r.Gate) != 1 || len(r.Executions) != 0 || r.Selection == nil || len(r.Selection.Omitted) != 1 || r.Selection.Omitted[0].Reason != "environment_unavailable" {
		t.Fatalf("batch not retained as blocked gap: gate=%+v selection=%+v executions=%+v", r.Gate, r.Selection, r.Executions)
	}
	encoded, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), "direct batch execution is unsupported") {
		t.Fatal("JSON omitted unsupported batch reason")
	}
	if before != gitTest(t, root, "show-ref") || status != gitTest(t, root, "status", "--porcelain") {
		t.Fatal("batch preflight mutated source refs or worktree")
	}
}
