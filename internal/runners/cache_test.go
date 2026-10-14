package runners

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestCacheInterpreterUsesSelectedExecutableIdentity(t *testing.T) {
	toolcache := filepath.Join(t.TempDir(), "hostedtoolcache", "Python", "3.12.9", "x64", "bin")
	if err := os.MkdirAll(toolcache, 0700); err != nil {
		t.Fatal(err)
	}
	python := filepath.Join(toolcache, "python3")
	if err := os.WriteFile(python, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", toolcache)
	p := Profile{Root: ".", Prefix: []string{".venv/bin/python"}, Lockfiles: []string{"uv.lock"}, Artifacts: []string{".venv"}}
	key, err := CacheKey(p, "repo", map[string][]byte{"uv.lock": []byte("lock")})
	if err != nil {
		t.Fatal(err)
	}
	cache := Cache{Dir: t.TempDir(), MaxBytes: 1 << 20}
	root := t.TempDir()
	bin := filepath.Join(root, ".venv", "bin")
	if err := os.MkdirAll(bin, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(python, filepath.Join(bin, "python")); err != nil {
		t.Fatal(err)
	}
	if err := cache.Store(context.Background(), key, root, p); err != nil {
		t.Fatal("selected hosted interpreter refused", err)
	}
	restored := t.TempDir()
	if hit, err := cache.Restore(context.Background(), key, restored, p); err != nil || !hit {
		t.Fatal(hit, err)
	}
	link, err := os.Readlink(filepath.Join(restored, ".venv", "bin", "python"))
	if err != nil || link != python {
		t.Fatal(link, err)
	}
	// A similarly named executable outside the selected installation does not
	// become trusted because it has a Python-shaped name or executable mode.
	other := filepath.Join(t.TempDir(), "python3")
	if err := os.WriteFile(other, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if currentPythonInterpreter(other) {
		t.Fatal("unselected interpreter trusted")
	}
	if err := os.Remove(filepath.Join(bin, "python")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(other, filepath.Join(bin, "python")); err != nil {
		t.Fatal(err)
	}
	if err := cache.Store(context.Background(), key, root, p); err == nil {
		t.Fatal("unselected external executable cached")
	}
}
