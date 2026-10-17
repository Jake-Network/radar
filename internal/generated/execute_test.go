package generated

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"syscall"
	"testing"
	"time"

	"github.com/Jake-Network/radar/internal/model"
)

func TestRegenerationObservesDriftAndSourceMutation(t *testing.T) {
	command := generatorFixtureCommand(t)
	for _, tc := range []struct {
		name             string
		args             []string
		status           model.Status
		outputs, sources []string
		exit             int
	}{
		{"unchanged", []string{"copy", "spec", "out"}, model.StatusPassed, nil, nil, 0},
		{"drift", []string{"write", "out", "new"}, model.StatusFailed, []string{"out"}, nil, 0},
		{"source mutation", []string{"write", "spec", "changed"}, model.StatusIncomplete, nil, []string{"spec"}, 0},
		{"command failure", []string{"exit"}, model.StatusFailed, nil, nil, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			put(t, root, "spec", "old")
			put(t, root, "out", "old")
			r, e := Run(context.Background(), root, Rule{Name: tc.name, Sources: []string{"spec"}, Outputs: []string{"out"}, Command: append(append([]string{}, command...), tc.args...), CWD: "."}, time.Second*5)
			if e != nil {
				t.Fatal(e)
			}
			if r.Status != tc.status || r.Observation.ExitCode != tc.exit || !slices.Equal(r.ChangedOutputs, tc.outputs) || !slices.Equal(r.ChangedSources, tc.sources) {
				t.Fatalf("got %+v", r)
			}
			observed := model.StatusPassed
			if tc.exit != 0 {
				observed = model.StatusFailed
			}
			if r.Observation.Status != observed {
				t.Fatalf("generator result was not observed: %+v", r)
			}
			if r.Observation.TestsRun != 0 {
				t.Fatal("generator claimed test observations")
			}
			if r.Observation.OutputDigest == "" {
				t.Fatal("command evidence has no digest")
			}
		})
	}
}
func TestSpecGenerationAllowsOnlyDeclaredSpec(t *testing.T) {
	command := generatorFixtureCommand(t)
	root := t.TempDir()
	put(t, root, "app.go", "source")
	r, b, e := RunSpec(context.Background(), root, "api", "api.json", append(append([]string{}, command...), "write", "api.json", `{"type":"object"}`), time.Second*5)
	if e != nil {
		t.Fatal(e)
	}
	if r.Status != model.StatusPassed || string(b) != `{"type":"object"}` {
		t.Fatalf("%+v %s", r, b)
	}
	bad, _, e := RunSpec(context.Background(), root, "bad", "api.json", append(append([]string{}, command...), "write", "app.go", "changed"), time.Second*5)
	if e == nil || bad.Status != model.StatusIncomplete || bad.Observation.Status != model.StatusPassed || !slices.Equal(bad.ChangedSources, []string{"app.go"}) {
		t.Fatalf("source mutation was not rejected after successful generation: %+v %v", bad, e)
	}
}
func TestRegenerationRejectsNoOutputAndSymlink(t *testing.T) {
	command := generatorFixtureCommand(t)
	root := t.TempDir()
	put(t, root, "spec", "old")
	r, e := Run(context.Background(), root, Rule{Sources: []string{"spec"}, Outputs: []string{"missing"}, Command: append(command, "write", "unexpected", "ran")}, time.Second)
	if e != nil {
		t.Fatal(e)
	}
	if r.Status != model.StatusIncomplete || r.Observation.OutputDigest != "" {
		t.Fatalf("empty output glob executed or passed: %+v", r)
	}
	if _, e := os.Stat(filepath.Join(root, "unexpected")); !os.IsNotExist(e) {
		t.Fatal("missing outputs did not prevent generator execution")
	}
	if e := os.Symlink("spec", filepath.Join(root, "link")); e != nil {
		if runtime.GOOS == "windows" && errors.Is(e, syscall.Errno(1314)) {
			t.Log("symlink refusal unavailable: Windows symlink privilege not held")
			return
		}
		t.Fatal(e)
	}
	if _, e := fingerprint(root); e == nil {
		t.Fatal("symlink snapshot accepted")
	}
}

// Exercise the declared argv and file-drift boundary with a native process.
// POSIX shell redirection is not a portable fixture for native Windows.
func generatorFixtureCommand(t *testing.T) []string {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", filepath.Dir(executable)+string(os.PathListSeparator)+os.Getenv("PATH"))
	return []string{filepath.Base(executable), "-test.run=^TestGeneratorFixtureProcess$", "--", "radar-generator-fixture"}
}

func TestGeneratorFixtureProcess(t *testing.T) {
	marker := -1
	for i, arg := range os.Args {
		if arg == "--" {
			marker = i
			break
		}
	}
	if marker < 0 || marker+1 >= len(os.Args) || os.Args[marker+1] != "radar-generator-fixture" {
		return
	}
	args := os.Args[marker+2:]
	fail := func(err error) { fmt.Fprintln(os.Stderr, err); os.Exit(3) }
	if len(args) == 1 && args[0] == "exit" {
		os.Exit(2)
	}
	if len(args) != 3 {
		fail(fmt.Errorf("invalid fixture argv: %v", args))
	}
	var data []byte
	switch args[0] {
	case "copy":
		var err error
		data, err = os.ReadFile(args[1])
		if err != nil {
			fail(err)
		}
	case "write":
		data = []byte(args[2])
	default:
		fail(fmt.Errorf("unknown fixture operation"))
	}
	output := args[1]
	if args[0] == "copy" {
		output = args[2]
	}
	if err := os.WriteFile(output, data, 0600); err != nil {
		fail(err)
	}
	os.Exit(0)
}
