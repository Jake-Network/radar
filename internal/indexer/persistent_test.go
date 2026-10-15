package indexer

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Jake-Network/radar/internal/languages"
	"github.com/Jake-Network/radar/internal/model"
)

func TestPersistentParseReuseAndInvalidation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "syntax.json")
	cache, err := LoadParseCache(path)
	if err != nil {
		t.Fatal(err)
	}
	underlying, _ := languages.ForPath("a.py")
	adapter := &countingAdapter{adapter: underlying}
	src := languages.Source{Repository: "repo", Revision: "first", Path: "a.py", Content: []byte("def f(): pass\n")}
	if _, err := cache.Parse(context.Background(), adapter, src); err != nil {
		t.Fatal(err)
	}
	if err := cache.Save(path); err != nil {
		t.Fatal(err)
	}
	restored, err := LoadParseCache(path)
	if err != nil {
		t.Fatal(err)
	}
	src.Revision = "next"
	if _, err := restored.Parse(context.Background(), adapter, src); err != nil {
		t.Fatal(err)
	}
	if adapter.calls != 1 || restored.Stats().Hits != 1 {
		t.Fatal("persisted syntax reparsed", adapter.calls, restored.Stats())
	}
	src.Path = "renamed.py"
	restored.Parse(context.Background(), adapter, src)
	src.Repository = "other"
	restored.Parse(context.Background(), adapter, src)
	src.Content = []byte("def changed(): pass\n")
	restored.Parse(context.Background(), adapter, src)
	if adapter.calls != 3 {
		t.Fatal("rename/content failed to invalidate or candidate root failed to reuse", adapter.calls)
	}
	if err := os.WriteFile(path, []byte(`{"version":"stale","entries":{}}`), 0600); err != nil {
		t.Fatal(err)
	}
	empty, err := LoadParseCache(path)
	if err == nil || empty.Stats().Entries != 0 {
		t.Fatal("stale persisted syntax accepted")
	}
}
func TestPersistentCacheRejectsSymlinksAndTampering(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	os.WriteFile(target, []byte("{}"), 0600)
	link := filepath.Join(dir, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skip(err)
	}
	if _, err := LoadParseCache(link); err == nil {
		t.Fatal("symlink cache loaded")
	}
	if err := NewParseCache().Save(link); err == nil {
		t.Fatal("symlink destination overwritten")
	}
	path := filepath.Join(dir, "cache")
	NewParseCache().Save(path)
	raw, _ := os.ReadFile(path)
	for i := range raw {
		if raw[i] == 'a' {
			raw[i] = 'b'
			break
		}
	}
	os.WriteFile(path, raw, 0600)
	if _, err := LoadParseCache(path); err == nil {
		t.Fatal("tampered cache accepted")
	}
}

func TestCachedSyntaxRebuildsManifestResolutionAndDeletedFiles(t *testing.T) {
	root := t.TempDir()
	write(t, root, "package.json", `{"workspaces":["libs/*"]}`)
	write(t, root, "libs/pkg/package.json", `{"name":"pkg","source":"a.ts"}`)
	write(t, root, "libs/pkg/a.ts", "export function a() {}\n")
	write(t, root, "libs/pkg/b.ts", "export function b() {}\n")
	write(t, root, "app.ts", "import {a} from 'pkg';\n")
	provider, err := NewWorktree(root)
	if err != nil {
		t.Fatal(err)
	}
	cache := NewParseCache()
	first, err := BuildWithCache(context.Background(), root, "one", provider, cache)
	if err != nil {
		t.Fatal(err)
	}
	has := func(s model.Snapshot, target string) bool {
		for _, e := range s.Edges {
			if e.Kind == "DEPENDS_ON" && e.From == model.FileID("app.ts") && e.To == model.FileID(target) {
				return true
			}
		}
		return false
	}
	if !has(first, "libs/pkg/a.ts") {
		t.Fatal("missing original package source")
	}
	write(t, root, "libs/pkg/package.json", `{"name":"pkg","source":"b.ts"}`)
	second, err := BuildWithCache(context.Background(), root, "two", provider, cache)
	if err != nil {
		t.Fatal(err)
	}
	if has(second, "libs/pkg/a.ts") || !has(second, "libs/pkg/b.ts") {
		t.Fatal("cached stale manifest resolution")
	}
	if cache.Stats().Hits != 3 {
		t.Fatal("unchanged syntax was not reused", cache.Stats())
	}
	if err := os.Remove(filepath.Join(root, "libs/pkg/b.ts")); err != nil {
		t.Fatal(err)
	}
	third, err := BuildWithCache(context.Background(), root, "three", provider, cache)
	if err != nil {
		t.Fatal(err)
	}
	if has(third, "libs/pkg/b.ts") {
		t.Fatal("deleted target survived incremental rebuild")
	}
	for _, n := range third.Nodes {
		if n.ID == model.FileID("libs/pkg/b.ts") {
			t.Fatal("deleted file survived cache")
		}
	}
}
