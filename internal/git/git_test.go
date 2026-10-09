package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestSafePath(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if e := os.WriteFile(filepath.Join(outside, "secret"), []byte("secret"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink(outside, filepath.Join(root, "link")); e != nil {
		t.Skip(e)
	}
	for _, p := range []string{"../secret", "/etc/passwd", "link/secret", "x\\y"} {
		if _, e := ReadFile(context.Background(), root, "WORKTREE", p); e == nil {
			t.Errorf("unsafe path accepted: %s", p)
		}
	}
	if e := os.WriteFile(filepath.Join(root, "ok"), []byte("ok"), 0600); e != nil {
		t.Fatal(e)
	}
	b, e := ReadFile(context.Background(), root, "WORKTREE", "ok")
	if e != nil || string(b) != "ok" {
		t.Fatalf("%s %v", b, e)
	}
}
func TestInvalidReference(t *testing.T) {
	for _, ref := range []string{"", "--help", "HEAD\n", "HEAD\x00"} {
		if _, e := Resolve(context.Background(), t.TempDir(), ref); e == nil {
			t.Fatalf("accepted %q", ref)
		}
	}
}

func TestInspectDoesNotExecuteFSMonitor(t *testing.T) {
	if _, e := exec.LookPath("git"); e != nil {
		t.Skip(e)
	}
	root := t.TempDir()
	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		if b, e := cmd.CombinedOutput(); e != nil {
			t.Fatalf("%v %s", e, b)
		}
	}
	runGit("init", "-q")
	runGit("config", "user.email", "radar@example.invalid")
	runGit("config", "user.name", "Radar")
	os.WriteFile(filepath.Join(root, "source"), []byte("fixture"), 0600)
	runGit("add", "source")
	runGit("commit", "-qm", "baseline")
	os.WriteFile(filepath.Join(root, "monitor.sh"), []byte("#!/bin/sh\ntouch unauthorized-script-ran\n"), 0700)
	runGit("config", "core.fsmonitor", "./monitor.sh")
	if _, e := Inspect(context.Background(), root); e != nil {
		t.Fatal(e)
	}
	if _, e := os.Stat(filepath.Join(root, "unauthorized-script-ran")); !os.IsNotExist(e) {
		t.Fatal("repository script executed")
	}
}

func TestImmutableTreeMetadata(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip(err)
	}
	root := t.TempDir()
	command := func(args ...string) {
		t.Helper()
		if b, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("%v: %v %s", args, err, b)
		}
	}
	command("init", "-q")
	command("config", "user.name", "Radar")
	command("config", "user.email", "radar@example.invalid")
	if err := os.WriteFile(filepath.Join(root, "api.py"), []byte("def original(): pass\n"), 0644); err != nil {
		t.Fatal(err)
	}
	command("add", ".")
	command("commit", "-qm", "base")
	entries, err := Entries(context.Background(), root, "HEAD")
	if err != nil || len(entries) != 1 {
		t.Fatalf("entries: %+v %v", entries, err)
	}
	e := entries[0]
	if e.Mode != "100644" || e.Type != "blob" || e.Path != "api.py" || e.Size != 21 {
		t.Fatalf("metadata: %+v", e)
	}
	if err := os.WriteFile(filepath.Join(root, "api.py"), []byte("dirty"), 0644); err != nil {
		t.Fatal(err)
	}
	b, err := ReadBlob(context.Background(), root, e.OID)
	if err != nil || string(b) != "def original(): pass\n" {
		t.Fatalf("immutable read: %q %v", b, err)
	}
	if err := os.Symlink("api.py", filepath.Join(root, "link.py")); err == nil {
		command("add", "link.py")
		command("commit", "-qm", "symlink")
		if _, err := ReadFile(context.Background(), root, "HEAD", "link.py"); err == nil {
			t.Fatal("committed symlink accepted")
		}
	}
	t.Setenv("GIT_DIR", filepath.Join(t.TempDir(), "foreign.git"))
	t.Setenv("GIT_WORK_TREE", t.TempDir())
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "invalid-key")
	t.Setenv("GIT_CONFIG_VALUE_0", "injected")
	b, err = ReadFile(context.Background(), root, "HEAD", "api.py")
	if err != nil || string(b) != "def original(): pass\n" {
		t.Fatalf("caller environment redirected reads: %q %v", b, err)
	}
	for _, oid := range []string{"--help", "HEAD", "000000000000000000000000000000000000000g"} {
		if _, err := ReadBlob(context.Background(), root, oid); err == nil {
			t.Fatalf("accepted object ID %q", oid)
		}
	}
}

func TestTreeReadsAreRelativeToExplicitDirectory(t *testing.T) {
	root := t.TempDir()
	command := func(args ...string) {
		t.Helper()
		if b, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("%v %s", err, b)
		}
	}
	command("init", "-q")
	command("config", "user.name", "Radar")
	command("config", "user.email", "radar@example.invalid")
	if err := os.Mkdir(filepath.Join(root, "nested"), 0755); err != nil {
		t.Fatal(err)
	}
	for path, value := range map[string]string{"outside.py": "outside", "nested/inside.py": "inside"} {
		if err := os.WriteFile(filepath.Join(root, path), []byte(value), 0644); err != nil {
			t.Fatal(err)
		}
	}
	command("add", ".")
	command("commit", "-qm", "nested")
	sub := filepath.Join(root, "nested")
	entries, err := Entries(context.Background(), sub, "HEAD")
	if err != nil || len(entries) != 1 || entries[0].Path != "inside.py" {
		t.Fatalf("crossed root: %+v %v", entries, err)
	}
	b, err := ReadFile(context.Background(), sub, "HEAD", "inside.py")
	if err != nil || string(b) != "inside" {
		t.Fatalf("relative read: %q %v", b, err)
	}
}
