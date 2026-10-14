package coverage

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/Jake-Network/radar/internal/indexer"
)

func TestRuntimeObservationsBaselineAndStaleness(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	os.Mkdir(filepath.Join(root, "config"), 0700)
	os.WriteFile(filepath.Join(root, "config", "limits.json"), []byte(`{"limit":2}`), 0600)
	os.WriteFile(filepath.Join(root, "test_config.py"), []byte("import unittest"), 0600)
	p, err := indexer.NewWorktree(root)
	if err != nil {
		t.Fatal(err)
	}
	s, err := Open(filepath.Join(t.TempDir(), "coverage.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.RecordAtBaseline(ctx, p, "repo", "base", "candidate", "command", []string{"test_config.py"}, []string{"config/limits.json"}); err != nil {
		t.Fatal(err)
	}
	edges, err := s.Load(ctx, p, "repo")
	if err != nil || len(edges) != 1 || edges[0].Stale || edges[0].Baseline != "base" || edges[0].Commit != "candidate" {
		t.Fatal(edges, err)
	}
	other, err := s.Load(ctx, p, "another-repo")
	if err != nil || len(other) != 0 {
		t.Fatal("repository identity leak", other, err)
	}
	os.WriteFile(filepath.Join(root, "config", "limits.json"), []byte(`{"limit":3}`), 0600)
	edges, err = s.Load(ctx, p, "repo")
	if err != nil || !edges[0].Stale {
		t.Fatal("changed file not stale", edges, err)
	}
	os.WriteFile(filepath.Join(root, "config", "limits.json"), []byte(`{"limit":2}`), 0600)
	os.WriteFile(filepath.Join(root, "test_config.py"), []byte("changed test"), 0600)
	edges, err = s.Load(ctx, p, "repo")
	if err != nil || !edges[0].Stale {
		t.Fatal("changed test not stale", edges, err)
	}
	if err := s.Record(ctx, p, "repo", "candidate", "another", []string{"../secret"}, []string{"config/limits.json"}); err == nil {
		t.Fatal("unsafe observation accepted")
	}
}

func TestSupportedCoverageFormatsAndEscapes(t *testing.T) {
	root := t.TempDir()
	fixtures := map[string]string{
		"python": `{"files":{"src/a.py":{"executed_lines":[1]},"src/b.py":{"executed_lines":[]}}}`,
		"c8":     `{"src/a.py":{"path":"src/a.py","s":{"0":1}},"src/b.py":{"s":{"0":0}}}`,
		"go":     "mode: set\nsrc/a.py:1.1,2.2 1 1\nsrc/b.py:1.1,2.2 1 0\n",
		"jacoco": `<report><package name="src"><sourcefile name="a.py"><line nr="1" ci="1"/></sourcefile><sourcefile name="b.py"><line nr="1" ci="0"/></sourcefile></package></report>`,
		"llvm":   `{"data":[{"files":[{"filename":"src/a.py","segments":[[1,1,1,true,true]]},{"filename":"src/b.py","segments":[[1,1,0,true,true]]}]}]}`,
		"files":  "\"src/a.py\"\n\"src/a.py\"\n",
	}
	for format, data := range fixtures {
		t.Run(format, func(t *testing.T) {
			files, err := Parse(format, []byte(data), root)
			if err != nil || !reflect.DeepEqual(files, []string{"src/a.py"}) {
				t.Fatal(files, err)
			}
		})
	}
	for _, data := range []string{`"../secret"`, `"/outside/secret"`} {
		if _, err := Parse("files", []byte(data), root); err == nil {
			t.Fatal("escape accepted", data)
		}
	}
	entries := []indexer.Entry{{Path: "apps/a/src/Foo.java"}, {Path: "apps/b/src/Foo.java"}, {Path: "src/Bar.java"}}
	if got := Resolve([]string{"src/Foo.java", "src/Bar.java"}, entries); !reflect.DeepEqual(got, []string{"src/Bar.java"}) {
		t.Fatal("ambiguous suffix accepted", got)
	}
}

func TestCoverageOwnershipDoesNotInferExternalModuleFiles(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "pkg"), 0700)
	os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.org/owned\n"), 0600)
	os.WriteFile(filepath.Join(root, "pkg", "foo.go"), []byte("package pkg\n"), 0600)
	p, err := indexer.NewWorktree(root)
	if err != nil {
		t.Fatal(err)
	}
	paths := []string{"example.org/owned/pkg/foo.go", "example.org/external/pkg/foo.go"}
	files, err := ResolveOwned(context.Background(), "go", paths, p)
	if err != nil || !reflect.DeepEqual(files, []string{"pkg/foo.go"}) {
		t.Fatal(files, err)
	}
	entries, _ := p.Entries(context.Background())
	if got := Resolve([]string{"example.org/external/pkg/foo.go"}, entries); len(got) != 0 {
		t.Fatal("external module became local observation", got)
	}
	data, _ := json.Marshal(map[string]any{"files": map[string]any{"relative.py": map[string]any{"executed_lines": []int{1}}, filepath.Join(root, "shared.py"): map[string]any{"executed_lines": []int{1}}}})
	got, err := ParseAt("python", data, root, "apps/api")
	if err != nil || !reflect.DeepEqual(got, []string{"apps/api/relative.py", "shared.py"}) {
		t.Fatal("CWD or absolute path normalization", got, err)
	}
}
