package landing

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStandaloneMergeTreeUsesOnlyPrivateObjects(t *testing.T) {
	root := fixture(t)
	branch(t, root, "stale", "baseline", "service.py", "def charge():\n return 2\n")
	branch(t, root, "new-base", "baseline", "service.py", "def charge():\n return 3\n")
	branch(t, root, "clean", "new-base", "notes.md", "clean\n")
	base, stale, clean := git(t, root, "rev-parse", "new-base"), git(t, root, "rev-parse", "stale"), git(t, root, "rev-parse", "clean")
	before := git(t, root, "show-ref") + git(t, root, "status", "--porcelain=v1") + git(t, root, "count-objects", "-v")
	index, err := os.ReadFile(filepath.Join(root, ".git", "index"))
	if err != nil {
		t.Fatal(err)
	}
	p := newStandaloneProbe(context.Background(), root, base, Options{})
	defer p.Close()
	if p.private == nil {
		t.Fatal("private probe preparation failed")
	}
	// A missing source makes a full-builder fallback fail. Successful probes
	// below therefore exercise merge-tree in the already copied private store.
	p.root = filepath.Join(t.TempDir(), "source-unavailable")
	paths, err := p.conflicts(context.Background(), stale)
	if err != nil || len(paths) != 1 || paths[0] != "service.py" {
		t.Fatalf("private conflict probe: %v %v", paths, err)
	}
	paths, err = p.conflicts(context.Background(), clean)
	if err != nil || len(paths) != 0 {
		t.Fatalf("private clean probe: %v %v", paths, err)
	}
	if git(t, p.private.Dir, "rev-parse", "HEAD") != base || git(t, p.private.Dir, "status", "--porcelain=v1") != "" {
		t.Fatal("merge-tree changed the reusable checkout or index")
	}
	afterIndex, err := os.ReadFile(filepath.Join(root, ".git", "index"))
	if err != nil || string(index) != string(afterIndex) || before != git(t, root, "show-ref")+git(t, root, "status", "--porcelain=v1")+git(t, root, "count-objects", "-v") {
		t.Fatal("standalone probe changed source refs/index/worktree/objects", err)
	}
}

func TestStandaloneProbeOutputRejectsIncompleteAndUnsafeResults(t *testing.T) {
	oid := strings.Repeat("a", 40)
	for _, tc := range []struct {
		name, output string
		conflict     bool
	}{
		{"missing tree", "", false}, {"truncated", oid + "\x00file", true},
		{"error without paths", oid + "\x00", true}, {"success with paths", oid + "\x00file\x00", false},
		{"absolute", oid + "\x00/escape\x00", true}, {"parent", oid + "\x00../escape\x00", true},
		{"empty path", oid + "\x00\x00", true}, {"invalid tree", "invalid\x00file\x00", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := parseProbeOutput([]byte(tc.output), tc.conflict); err == nil {
				t.Fatal("invalid probe accepted")
			}
		})
	}
	var output probeOutput
	// LimitedReader has no WriterTo, so io.Copy would select an accidentally
	// inherited bytes.Buffer.ReadFrom and bypass the bounded Write method.
	_, _ = io.Copy(&output, io.LimitReader(strings.NewReader(strings.Repeat("x", (1<<20)+10)), (1<<20)+10))
	if !output.overflow || output.Len() != 1<<20 {
		t.Fatal("probe output limit lost")
	}
}
