package generated

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/Jake-Network/radar/internal/model"
)

func TestRegenerationObservesDriftAndSourceMutation(t *testing.T) {
	if _, e := exec.LookPath("sh"); e != nil {
		t.Skip("sh unavailable")
	}
	for _, tc := range []struct {
		name, script string
		status       model.Status
	}{{"unchanged", "cat spec > out", model.StatusPassed}, {"drift", "printf new > out", model.StatusFailed}, {"source mutation", "printf changed > spec", model.StatusIncomplete}, {"command failure", "exit 2", model.StatusFailed}} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			put(t, root, "spec", "old")
			put(t, root, "out", "old")
			r, e := Run(context.Background(), root, Rule{Name: tc.name, Sources: []string{"spec"}, Outputs: []string{"out"}, Command: []string{"sh", "-c", tc.script}, CWD: "."}, time.Second*5)
			if e != nil {
				t.Fatal(e)
			}
			if r.Status != tc.status {
				t.Fatalf("got %+v", r)
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
	if _, e := exec.LookPath("sh"); e != nil {
		t.Skip("sh unavailable")
	}
	root := t.TempDir()
	put(t, root, "app.go", "source")
	r, b, e := RunSpec(context.Background(), root, "api", "api.json", []string{"sh", "-c", `printf '{"type":"object"}' > api.json`}, time.Second*5)
	if e != nil {
		t.Fatal(e)
	}
	if r.Status != model.StatusPassed || string(b) != `{"type":"object"}` {
		t.Fatalf("%+v %s", r, b)
	}
	if _, _, e = RunSpec(context.Background(), root, "bad", "api.json", []string{"sh", "-c", "printf changed > app.go"}, time.Second*5); e == nil {
		t.Fatal("generator source mutation accepted")
	}
}
func TestRegenerationRejectsNoOutputAndSymlink(t *testing.T) {
	root := t.TempDir()
	put(t, root, "spec", "old")
	r, e := Run(context.Background(), root, Rule{Sources: []string{"spec"}, Outputs: []string{"missing"}, Command: []string{"false"}}, time.Second)
	if e != nil {
		t.Fatal(e)
	}
	if r.Status != model.StatusIncomplete || r.Observation.OutputDigest != "" {
		t.Fatalf("empty output glob executed or passed: %+v", r)
	}
	if e := os.Symlink("spec", filepath.Join(root, "link")); e != nil {
		t.Skip(e)
	}
	if _, e := fingerprint(root); e == nil {
		t.Fatal("symlink snapshot accepted")
	}
}
