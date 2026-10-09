package pathutil

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRepoRelative(t *testing.T) {
	for _, bad := range []string{"", "/etc/passwd", "../x", "a/../../x", "a\\b", "a\x00", "a/../b"} {
		if _, err := RepoRelative(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
	if clean, err := RepoRelative("a/./b"); err != nil || clean != "a/b" {
		t.Fatal(clean, err)
	}
}

func TestResolveInsideAndStatePath(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Skip(err)
	}
	os.WriteFile(filepath.Join(outside, "secret"), []byte("x"), 0600)
	if _, err := ResolveInside(root, "link/secret"); err == nil {
		t.Fatal("symlink escape accepted")
	}
	if _, err := StatePath(root, "link/state.db"); err == nil {
		t.Fatal("symlinked state path accepted")
	}
	if _, err := StatePath(root, "../state.db"); err == nil {
		t.Fatal("escaping state path accepted")
	}
}
