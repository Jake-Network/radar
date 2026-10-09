package checkpoint

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	gitrepo "github.com/radar-engine/radar/internal/git"
	"github.com/radar-engine/radar/internal/indexer"
	"github.com/radar-engine/radar/internal/model"
)

func repository(t *testing.T) (string, func(...string)) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip(err)
	}
	root := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		if b, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, b)
		}
	}
	run("init", "-q")
	run("config", "user.name", "Radar")
	run("config", "user.email", "radar@example.invalid")
	return root, run
}
func write(t *testing.T, root, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(filepath.Join(root, path)), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, path), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}
func named(s model.Snapshot, name string) bool {
	for _, n := range s.Nodes {
		if n.Name == name {
			return true
		}
	}
	return false
}
func TestCommittedSnapshotIgnoresDirtyFilesAndPinsRevision(t *testing.T) {
	root, git := repository(t)
	write(t, root, "api.py", "def original():\n    return 1\n")
	git("add", ".")
	git("commit", "-qm", "base")
	git("branch", "approved")
	base, err := Index(context.Background(), root, "approved")
	if err != nil {
		t.Fatal(err)
	}
	write(t, root, "api.py", "def original():\n    return 2\n\ndef dirty():\n    return 3\n")
	dirty, err := Index(context.Background(), root, "approved")
	if err != nil {
		t.Fatal(err)
	}
	if !named(dirty, "original") || named(dirty, "dirty") {
		t.Fatalf("checkpoint used working tree: %+v", dirty.Nodes)
	}
	if base.Revision != dirty.Revision || len(base.Revision) != 40 {
		t.Fatal("revision not pinned SHA")
	}
	for _, n := range dirty.Nodes {
		if n.Provenance.Revision != base.Revision {
			t.Fatalf("unversioned node: %+v", n)
		}
	}
	git("add", ".")
	git("commit", "-qm", "change")
	git("branch", "-f", "approved", "HEAD")
	pinned, err := Index(context.Background(), root, base.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if !named(pinned, "original") || named(pinned, "dirty") {
		t.Fatal("resolved checkpoint changed when branch moved")
	}
	head, err := Index(context.Background(), root, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	var originalID string
	for _, n := range base.Nodes {
		if n.Name == "original" {
			originalID = n.ID
		}
	}
	matched := false
	for _, n := range head.Nodes {
		if n.Name == "original" && n.ID == originalID {
			matched = true
		}
	}
	if !matched {
		t.Fatal("symbol identity changed with function body")
	}
	fileID := model.StableID(base.Repository, "file", "api.py")
	found := 0
	for _, s := range []model.Snapshot{base, head} {
		for _, n := range s.Nodes {
			if n.ID == fileID {
				found++
			}
		}
	}
	if found != 2 {
		t.Fatal("file identity differs across revisions")
	}
	working, err := indexer.Index(context.Background(), root, "WORKTREE")
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range working.Nodes {
		if n.ID == fileID {
			return
		}
	}
	t.Fatal("committed and working-tree IDs differ")
}
func TestCommittedSnapshotBoundsAndExcludedSources(t *testing.T) {
	root, git := repository(t)
	write(t, root, "good.rs", "pub fn useful() {}\n")
	write(t, root, "huge.py", strings.Repeat("#", indexer.MaxSourceBytes+1))
	write(t, root, "node_modules/generated.ts", "export function generated() {}")
	write(t, root, "syntax.py", "def :\n")
	if err := os.Symlink("good.rs", filepath.Join(root, "link.rs")); err != nil {
		t.Log(err)
	}
	git("add", ".")
	git("commit", "-qm", "sources")
	sha, e := gitrepo.Resolve(context.Background(), root, "HEAD")
	if e != nil {
		t.Fatal(e)
	}
	git("update-index", "--add", "--cacheinfo", "160000,"+sha+",external")
	git("commit", "-qm", "submodule")
	s, err := Index(context.Background(), root, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if !named(s, "useful") || named(s, "generated") {
		t.Fatalf("exclusion failure: %+v", s.Nodes)
	}
	foundLarge, foundSyntax, foundSubmodule := false, false, false
	for _, d := range s.Diagnostics {
		if d.Path == "external" && strings.Contains(d.Message, "submodule") {
			foundSubmodule = true
		}
		if d.Path == "huge.py" {
			foundLarge = true
		}
		if d.Path == "syntax.py" {
			foundSyntax = true
		}
	}
	if !foundLarge || !foundSyntax || !foundSubmodule {
		t.Fatalf("missing partial diagnostics: %+v", s.Diagnostics)
	}
	for _, n := range s.Nodes {
		if n.Provenance.Path == "link.rs" || n.Provenance.Path == "huge.py" {
			t.Fatal("unsafe/oversize file parsed")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Index(ctx, root, "HEAD"); err == nil {
		t.Fatal("ignored cancellation")
	}
	if _, err := Index(context.Background(), root, "missing-ref"); err == nil {
		t.Fatal("accepted missing ref")
	}
}
