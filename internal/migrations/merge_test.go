package migrations

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	c := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@example.invalid", "-c", "core.hooksPath=" + os.DevNull}, args...)...)
	out, e := c.CombinedOutput()
	if e != nil {
		t.Fatalf("git %v: %v %s", args, e, out)
	}
	return strings.TrimSpace(string(out))
}

func TestMergeOnto(t *testing.T) {
	dir := t.TempDir()
	gitIn(t, dir, "init", "-q", "-b", "main")
	write := func(name, content string) {
		if e := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); e != nil {
			t.Fatal(e)
		}
		gitIn(t, dir, "add", ".")
		gitIn(t, dir, "commit", "-qm", name)
	}
	write("a.txt", "base\n")
	base := gitIn(t, dir, "rev-parse", "HEAD")
	gitIn(t, dir, "checkout", "-q", "-b", "clean")
	write("b.txt", "b\n")
	clean := gitIn(t, dir, "rev-parse", "HEAD")
	gitIn(t, dir, "checkout", "-q", "-b", "conflict", base)
	write("a.txt", "theirs\n")
	conflict := gitIn(t, dir, "rev-parse", "HEAD")
	gitIn(t, dir, "checkout", "-q", "main")
	write("a.txt", "ours\n")
	main := gitIn(t, dir, "rev-parse", "HEAD")

	commit, why := mergeOnto(context.Background(), dir, main, clean)
	if why != "" || gitIn(t, dir, "show", commit+":b.txt") != "b" || gitIn(t, dir, "show", commit+":a.txt") != "ours" {
		t.Fatalf("clean merge %q %q", commit, why)
	}
	if _, why = mergeOnto(context.Background(), dir, main, conflict); why != "it conflicts with the base" {
		t.Fatalf("conflict reason %q", why)
	}
	if got, why := mergeOnto(context.Background(), dir, main, main); got != main || why != "" {
		t.Fatalf("same revision %q %q", got, why)
	}
	if gitIn(t, dir, "rev-parse", "HEAD") != main || gitIn(t, dir, "status", "--porcelain") != "" {
		t.Fatal("mergeOnto changed HEAD or the worktree")
	}
}
