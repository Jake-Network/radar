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
