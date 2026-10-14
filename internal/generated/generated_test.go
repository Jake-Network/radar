package generated

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func git(t *testing.T, root string, args ...string) string {
	t.Helper()
	c := exec.Command("git", args...)
	c.Dir = root
	c.Env = append(os.Environ(), "GIT_AUTHOR_NAME=fixture", "GIT_AUTHOR_EMAIL=fixture@example.test", "GIT_COMMITTER_NAME=fixture", "GIT_COMMITTER_EMAIL=fixture@example.test")
	b, e := c.CombinedOutput()
	if e != nil {
		t.Fatalf("git %v: %v %s", args, e, b)
	}
	return string(b)
}
func put(t *testing.T, root, path, body string) {
	t.Helper()
	p := filepath.Join(root, path)
	if e := os.MkdirAll(filepath.Dir(p), 0755); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(p, []byte(body), 0644); e != nil {
		t.Fatal(e)
	}
}
func TestStaticWarnsWithoutExecuting(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init", "-q")
	put(t, root, ManifestPath, `{"sources":["spec/**/*.json"],"outputs":["client/**/*.go"],"command":["sh","-c","touch execution-marker"]}`)
	put(t, root, "spec/api.json", "old")
	put(t, root, "client/api.go", "old")
	git(t, root, "add", ".")
	git(t, root, "commit", "-qm", "base")
	git(t, root, "branch", "base")
	put(t, root, "spec/api.json", "new")
	git(t, root, "add", ".")
	git(t, root, "commit", "-qm", "source")
	r, e := Analyze(context.Background(), root, "base", "HEAD", []string{"spec/api.json"})
	if e != nil {
		t.Fatal(e)
	}
	if !r.Applicable || len(r.Findings) != 1 || string(r.Status) != "unknown" {
		t.Fatalf("unexpected static evidence %+v", r)
	}
	if _, e := os.Stat(filepath.Join(root, "execution-marker")); !os.IsNotExist(e) {
		t.Fatal("static analysis executed code")
	}
}
func TestManifestRejectsUnsafeAndUnknown(t *testing.T) {
	for _, src := range []string{`{"sources":["../spec"],"outputs":["out"]}`, `{"sources":["spec"],"outputs":[".git/**"]}`, `{"sources":["[bad"],"outputs":["out"]}`, `{"sources":["spec"],"outputs":["out"],"commnad":["go"]}`, `{"version":2,"sources":["spec"],"outputs":["out"]}`, `{"sources":["spec"],"outputs":["out"],"command":["../run"]}`, `{"sources":["spec"],"outputs":["out"]} {}`} {
		if _, e := Decode([]byte(src)); e == nil {
			t.Fatalf("accepted %s", src)
		}
	}
}
func TestGlob(t *testing.T) {
	for _, tc := range []struct {
		pattern, file string
		want          bool
	}{{"spec/**/*.json", "spec/api.json", true}, {"spec/**/*.json", "spec/nested/api.json", true}, {"spec/*.json", "spec/nested/api.json", false}, {"out/**", "other/out.go", false}} {
		if got := Match(tc.pattern, tc.file); got != tc.want {
			t.Fatalf("%s %s = %v", tc.pattern, tc.file, got)
		}
	}
}
func TestRemovedDeclarationIsIncomplete(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init", "-q")
	put(t, root, ManifestPath, `{"sources":["spec"],"outputs":["out"]}`)
	git(t, root, "add", ".")
	git(t, root, "commit", "-qm", "base")
	git(t, root, "branch", "base")
	git(t, root, "rm", ManifestPath)
	git(t, root, "commit", "-qm", "removed")
	r, e := Analyze(context.Background(), root, "base", "HEAD", []string{ManifestPath})
	if e != nil {
		t.Fatal(e)
	}
	if !r.Applicable || string(r.Status) != "incomplete" {
		t.Fatalf("removed config: %+v", r)
	}
}

func TestOutputEditBeforeSourceWarns(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init", "-q")
	put(t, root, ManifestPath, `{"sources":["spec"],"outputs":["out"]}`)
	put(t, root, "spec", "old")
	put(t, root, "out", "old")
	git(t, root, "add", ".")
	git(t, root, "commit", "-qm", "base")
	git(t, root, "branch", "base")
	put(t, root, "out", "intermediate")
	git(t, root, "commit", "-qam", "output")
	put(t, root, "spec", "new")
	git(t, root, "commit", "-qam", "source")
	r, e := Analyze(context.Background(), root, "base", "HEAD", []string{"spec", "out"})
	if e != nil {
		t.Fatal(e)
	}
	if len(r.Findings) != 1 {
		t.Fatalf("stale output edit not warned %+v", r)
	}
}

func TestRecursiveGlobNearMissIsBounded(t *testing.T) {
	pattern := strings.Repeat("**/", 30) + "missing.json"
	file := strings.Repeat("nested/", 60) + "present.json"
	if Match(pattern, file) {
		t.Fatal("near miss matched")
	}
	if Match(strings.Repeat("**/", 65)+"*.json", file) {
		t.Fatal("over-budget glob accepted")
	}
	_, e := Decode([]byte(`{"sources":["` + strings.Repeat("**/", 65) + `*.json"],"outputs":["out"]}`))
	if e == nil {
		t.Fatal("oversized recursive manifest glob accepted")
	}
}
