package indexer

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, root, path, content string) {
	t.Helper()
	p := filepath.Join(root, path)
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}
func TestMultilingualSnapshot(t *testing.T) {
	root := t.TempDir()
	for p, s := range map[string]string{"web/app.ts": "import { x } from './x'; export function run() { return 1; }", "server/app.py": "import os\ndef run():\n    return 1\n", "cmd/main.go": "package main\nfunc main() {}", "engine/lib.rs": "pub struct Engine {}", "node_modules/hidden.ts": "export function hidden() {}", ".radar/hidden.py": "def hidden(): pass"} {
		write(t, root, p, s)
	}
	snapshot, err := Index(context.Background(), root, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	files := 0
	languages := map[string]bool{}
	for _, n := range snapshot.Nodes {
		if n.Kind == "file" {
			files++
			languages[n.Language] = true
		}
		if n.Name == "hidden" {
			t.Fatal("generated directory indexed")
		}
	}
	if files != 4 || len(languages) != 4 {
		t.Fatalf("files=%d languages=%v", files, languages)
	}
	if len(snapshot.Edges) < 5 {
		t.Fatal("no graph relationships")
	}
	for _, e := range snapshot.Edges {
		if e.Provenance.Revision != "HEAD" {
			t.Fatal("revision lost")
		}
	}
}
func TestEmptyCancellationAndMissingRoot(t *testing.T) {
	root := t.TempDir()
	s, err := Index(context.Background(), root, "rev")
	if err != nil || len(s.Nodes) != 1 {
		t.Fatalf("empty repo: %v %+v", err, s)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = Index(ctx, root, "rev")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: %v", err)
	}
	_, err = Index(context.Background(), filepath.Join(root, "absent"), "rev")
	if err == nil {
		t.Fatal("missing root accepted")
	}
}
func TestSourceLimitsAndSymlinks(t *testing.T) {
	root := t.TempDir()
	write(t, root, "large.ts", string(make([]byte, MaxSourceBytes+1)))
	outside := t.TempDir()
	write(t, outside, "external.py", "def private(): pass")
	if err := os.Symlink(filepath.Join(outside, "external.py"), filepath.Join(root, "link.py")); err != nil {
		t.Skip(err)
	}
	s, err := Index(context.Background(), root, "rev")
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Diagnostics) != 2 {
		t.Fatalf("missing diagnostics: %+v", s.Diagnostics)
	}
	if len(s.Nodes) != 1 {
		t.Fatal("skipped source indexed")
	}
}
func TestConcurrentIndex(t *testing.T) {
	root := t.TempDir()
	write(t, root, "x.py", "def run(): pass")
	errors := make(chan error, 4)
	for i := 0; i < 4; i++ {
		go func() { _, err := Index(context.Background(), root, "rev"); errors <- err }()
	}
	for i := 0; i < 4; i++ {
		if err := <-errors; err != nil {
			t.Fatal(err)
		}
	}
}

func BenchmarkOrganizationFixture(b *testing.B) {
	root := filepath.Join("..", "..", "examples", "organization")
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		s, err := Index(context.Background(), root, "WORKTREE")
		if err != nil {
			b.Fatal(err)
		}
		if len(s.Nodes) < 20 {
			b.Fatal("fixture not indexed")
		}
	}
}
