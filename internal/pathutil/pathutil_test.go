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

func TestStatePathInfoPreservesValidatedLeafMetadata(t *testing.T) {
	root := t.TempDir()
	missing, info, err := StatePathInfo(root, "missing/child")
	if err != nil || info != nil || missing != filepath.Join(root, "missing", "child") {
		t.Fatal(missing, info, err)
	}
	want := filepath.Join(root, "regular")
	if err := os.WriteFile(want, []byte("file"), 0600); err != nil {
		t.Fatal(err)
	}
	actual, info, err := StatePathInfo(root, "regular")
	expected, statErr := os.Lstat(want)
	if err != nil || statErr != nil || info == nil || actual != want || !os.SameFile(info, expected) || !info.Mode().IsRegular() {
		t.Fatal("validated leaf metadata differs", actual, info, err, statErr)
	}
}
