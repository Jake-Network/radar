package overlap

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	gitrepo "github.com/Jake-Network/radar/internal/git"
	"github.com/Jake-Network/radar/internal/integration"
)

func TestSharedHeadNamedDirtySnapshotsRemainDistinct(t *testing.T) {
	root := t.TempDir()
	git := func(dir string, args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=fixture", "GIT_COMMITTER_NAME=fixture", "GIT_AUTHOR_EMAIL=t@example.invalid", "GIT_COMMITTER_EMAIL=t@example.invalid")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
		return string(out)
	}
	put := func(dir, path, contents string) {
		t.Helper()
		full := filepath.Join(dir, path)
		if err := os.MkdirAll(filepath.Dir(full), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
	}
	git(root, "init", "-q")
	git(root, "checkout", "-qb", "main")
	put(root, "db/env.py", "# migration environment\n")
	put(root, "db/versions/base.py", "revision = 'root'\ndown_revision = None\n")
	git(root, "add", ".")
	git(root, "commit", "-qm", "base")
	first := filepath.Join(t.TempDir(), "first")
	second := filepath.Join(t.TempDir(), "second")
	git(root, "worktree", "add", "-qb", "agent/first", first, "main")
	git(root, "worktree", "add", "-qb", "agent/second", second, "main")
	put(first, "db/versions/first.py", "revision = 'first'\ndown_revision = 'root'\n")
	put(second, "db/versions/second.py", "revision = 'second'\ndown_revision = 'root'\n")
	ctx := context.Background()
	a, err := integration.ObserveDirty(ctx, gitrepo.WorktreeStatus{Path: first})
	if err != nil {
		t.Fatal(err)
	}
	b, err := integration.ObserveDirty(ctx, gitrepo.WorktreeStatus{Path: second})
	if err != nil {
		t.Fatal(err)
	}
	if a.Head != b.Head || a.Digest == b.Digest {
		t.Fatal("fixture must share clean commit but differ in saved content")
	}
	base, err := gitrepo.Resolve(ctx, root, "main")
	if err != nil {
		t.Fatal(err)
	}
	report, err := Analyze(ctx, root, base, []Input{{Branch: "agent/first", Head: a.Head, Snapshot: &a}, {Branch: "agent/second", Head: b.Head, Snapshot: &b}})
	if err != nil || report.Pairs != 1 {
		t.Fatalf("overlap %+v %v", report, err)
	}
	found := false
	for _, finding := range report.Findings {
		if finding.Code == "migration_multiple_heads" {
			found = true
			if len(finding.Branches) != 2 || finding.Branches[0] != "agent/first" || finding.Branches[1] != "agent/second" {
				t.Fatalf("source identities lost: %+v", finding)
			}
		}
	}
	if !found {
		t.Fatalf("distinct dirty revisions were lost: %+v", report)
	}
}
