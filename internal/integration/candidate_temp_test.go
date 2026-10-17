package integration

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestCandidateCanonicalizesSymlinkTempRoot(t *testing.T) {
	root := fixture(t)
	physical, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "tmp-link")
	if err := os.Symlink(physical, link); err != nil {
		t.Skip(err)
	}
	t.Setenv("TMPDIR", link)
	c, err := BuildCandidate(context.Background(), root, "baseline", []string{"backend", "frontend"})
	if c != nil {
		defer c.Close()
	}
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := filepath.EvalSymlinks(c.Dir)
	if err != nil || canonical != c.Dir {
		t.Fatalf("candidate root is not canonical: %q %q %v", c.Dir, canonical, err)
	}
	if len(c.Conflicts) != 0 || c.Tree == "" {
		t.Fatalf("candidate not built: %+v", c)
	}
}

func TestCandidatePreservesCommittedBytesWithAutoCRLF(t *testing.T) {
	root := fixture(t)
	gitTest(t, root, "checkout", "baseline")
	files := map[string]string{"lf.txt": "one\ntwo\n", "crlf.txt": "one\r\ntwo\r\n", "binary.dat": "\x00one\r\ntwo\n"}
	for name, body := range files {
		put(t, root, name, body)
	}
	gitTest(t, root, "-c", "core.autocrlf=false", "add", ".")
	gitTest(t, root, "-c", "core.autocrlf=false", "commit", "-qm", "byte-preserving fixtures")
	gitTest(t, root, "config", "core.autocrlf", "true")
	c, err := BuildCandidate(context.Background(), root, "HEAD", nil)
	if c != nil {
		defer c.Close()
	}
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range files {
		got, err := os.ReadFile(filepath.Join(c.Dir, name))
		if err != nil || string(got) != want {
			t.Fatalf("%s changed bytes: %q want %q err=%v", name, got, want, err)
		}
	}
}
