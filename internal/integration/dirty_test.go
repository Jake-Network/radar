package integration

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	gitrepo "github.com/Jake-Network/radar/internal/git"
)

func dirtyFixture(t *testing.T) string {
	root := t.TempDir()
	gitTest(t, root, "init", "-q")
	put(t, root, "tracked.txt", "base\n")
	put(t, root, "deleted.txt", "remove\n")
	put(t, root, "renamed.txt", "rename\n")
	put(t, root, ".gitignore", "secrets/\nnode_modules/\n")
	gitTest(t, root, "add", ".")
	gitTest(t, root, "commit", "-qm", "base")
	gitTest(t, root, "branch", "baseline")
	gitTest(t, root, "checkout", "-qb", "feature")
	return root
}
func observeTestDirty(t *testing.T, root string) DirtySnapshot {
	t.Helper()
	head := gitTest(t, root, "rev-parse", "HEAD")
	s, err := ObserveDirty(context.Background(), gitrepo.WorktreeStatus{Path: root, Commit: head})
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func TestDirtySnapshotProvenanceAndSourceImmutability(t *testing.T) {
	root := dirtyFixture(t)
	put(t, root, "tracked.txt", "staged\n")
	gitTest(t, root, "add", "tracked.txt")
	put(t, root, "tracked.txt", "unstaged final\n")
	gitTest(t, root, "mv", "renamed.txt", "newname.txt")
	if err := os.Remove(filepath.Join(root, "deleted.txt")); err != nil {
		t.Fatal(err)
	}
	put(t, root, "added.txt", "new\n")
	put(t, root, "secrets/token", "must not copy\n")
	put(t, root, "node_modules/dep/index.js", "ignored\n")
	before := gitTest(t, root, "status", "--porcelain=v1", "-z")
	indexBefore := gitTest(t, root, "ls-files", "--stage", "-z")
	refs := gitTest(t, root, "show-ref")
	rawIndexBefore, err := os.ReadFile(filepath.Join(root, ".git", "index"))
	if err != nil {
		t.Fatal(err)
	}
	s := observeTestDirty(t, root)
	if len(s.Staged) < 2 || len(s.Unstaged) < 2 || len(s.Untracked) != 1 {
		t.Fatalf("provenance=%+v", s)
	}
	c, err := BuildDirtyCandidate(context.Background(), root, "baseline", []string{"feature"}, []DirtySnapshot{s})
	if err != nil {
		t.Fatal(err)
	}
	dir := c.Dir
	defer c.Close()
	for key, want := range map[string]string{"maintenance.auto": "false", "maintenance.autoDetach": "false", "gc.auto": "0", "gc.autoDetach": "false"} {
		got, err := gitrepo.RunPrivate(context.Background(), c.Dir, "config", "--get", key)
		if err != nil || got != want {
			t.Fatalf("private candidate permits unowned background writes: %s=%q: %v", key, got, err)
		}
	}
	if len(c.Conflicts) != 0 {
		t.Fatal(c.Conflicts)
	}
	b, err := os.ReadFile(filepath.Join(c.Dir, "tracked.txt"))
	if err != nil || string(b) != "unstaged final\n" {
		t.Fatalf("final bytes %q %v", b, err)
	}
	for _, p := range []string{"deleted.txt", "renamed.txt", "secrets/token", "node_modules/dep/index.js"} {
		if _, err := os.Lstat(filepath.Join(c.Dir, p)); !os.IsNotExist(err) {
			t.Fatalf("unexpected candidate path %s: %v", p, err)
		}
	}
	for _, p := range []string{"newname.txt", "added.txt"} {
		if _, err := os.Stat(filepath.Join(c.Dir, p)); err != nil {
			t.Fatal(err)
		}
	}
	if gitTest(t, root, "status", "--porcelain=v1", "-z") != before || gitTest(t, root, "ls-files", "--stage", "-z") != indexBefore || gitTest(t, root, "show-ref") != refs {
		t.Fatal("source Git state mutated")
	}
	rawIndexAfter, err := os.ReadFile(filepath.Join(root, ".git", "index"))
	if err != nil || string(rawIndexBefore) != string(rawIndexAfter) {
		t.Fatal("source index bytes mutated", err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("temporary candidate not removed")
	}
}
func TestDirtyDigestTracksIndexSeparatelyAndSupersededSnapshot(t *testing.T) {
	root := dirtyFixture(t)
	put(t, root, "tracked.txt", "working\n")
	a := observeTestDirty(t, root)
	gitTest(t, root, "add", "tracked.txt")
	b := observeTestDirty(t, root)
	if a.Digest == b.Digest {
		t.Fatal("index provenance absent from digest")
	}
	c, err := BuildDirtyCandidate(context.Background(), root, "baseline", []string{"feature"}, []DirtySnapshot{a})
	if c != nil {
		c.Close()
	}
	if err == nil {
		t.Fatal("obsolete snapshot accepted")
	}
	put(t, root, "tracked.txt", "changed again\n")
	if _, err := BuildDirtyCandidate(context.Background(), root, "baseline", []string{"feature"}, []DirtySnapshot{b}); err == nil {
		t.Fatal("changing files accepted")
	}
}
func TestDirtySnapshotRefusesUnsupportedEntries(t *testing.T) {
	for _, kind := range []string{"symlink", "parent symlink", "unmerged", "sparse", "submodule"} {
		t.Run(kind, func(t *testing.T) {
			root := dirtyFixture(t)
			switch kind {
			case "symlink":
				if err := os.Symlink("tracked.txt", filepath.Join(root, "link")); err != nil {
					t.Skip(err)
				}
			case "parent symlink":
				gitTest(t, root, "rm", "--cached", "tracked.txt")
				if err := os.Remove(filepath.Join(root, "tracked.txt")); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(t.TempDir(), filepath.Join(root, "tracked.txt")); err != nil {
					t.Skip(err)
				}
			case "unmerged":
				sha := gitTest(t, root, "rev-parse", "HEAD:tracked.txt")
				cmd := gitrepo.PrivateCommand(context.Background(), root, "update-index", "--index-info")
				cmd.Stdin = strings.NewReader("100644 " + sha + " 1\ttracked.txt\n")
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatal(err, string(out))
				}
			case "sparse":
				gitTest(t, root, "update-index", "--skip-worktree", "tracked.txt")
			case "submodule":
				sha := gitTest(t, root, "rev-parse", "HEAD")
				gitTest(t, root, "update-index", "--add", "--cacheinfo", "160000,"+sha+",nested")
			}
			if _, err := ObserveDirty(context.Background(), gitrepo.WorktreeStatus{Path: root}); err == nil {
				t.Fatal("unsupported snapshot accepted")
			}
		})
	}
}
func TestDirtySnapshotsCombineConflictAndDetachedWorktree(t *testing.T) {
	root := dirtyFixture(t)
	other := filepath.Join(t.TempDir(), "other")
	gitTest(t, root, "worktree", "add", "--detach", other, "baseline")
	put(t, root, "tracked.txt", "first\n")
	put(t, other, "tracked.txt", "second\n")
	a := observeTestDirty(t, root)
	b := observeTestDirty(t, other)
	c, err := BuildDirtyCandidate(context.Background(), root, "baseline", nil, []DirtySnapshot{a, b})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if len(c.Conflicts) != 1 {
		t.Fatalf("conflicts=%v", c.Conflicts)
	}
}
func TestDirtySnapshotRejectsRemovedWorktreeAndLargeFiles(t *testing.T) {
	root := dirtyFixture(t)
	put(t, root, "large", strings.Repeat("x", gitrepo.MaxFileBytes+1))
	if _, err := ObserveDirty(context.Background(), gitrepo.WorktreeStatus{Path: root}); err == nil {
		t.Fatal("oversized file accepted")
	}
	if _, err := ObserveDirty(context.Background(), gitrepo.WorktreeStatus{Path: filepath.Join(root, "removed")}); err == nil {
		t.Fatal("removed worktree accepted")
	}
}

func TestDirtySnapshotCaptureRace(t *testing.T) {
	root := dirtyFixture(t)
	put(t, root, "tracked.txt", "first\n")
	_, err := observeDirty(context.Background(), gitrepo.WorktreeStatus{Path: root}, func() { put(t, root, "tracked.txt", "second\n") })
	if err == nil {
		t.Fatal("capture race accepted")
	}
}
func TestDirtySnapshotsAssociateSharedHeadInputs(t *testing.T) {
	root := dirtyFixture(t)
	other := filepath.Join(t.TempDir(), "other")
	gitTest(t, root, "worktree", "add", "-b", "other", other, "baseline")
	put(t, root, "tracked.txt", "first\n")
	put(t, other, "tracked.txt", "second\n")
	a := observeTestDirty(t, root)
	b := observeTestDirty(t, other)
	a.InputIndex = 0
	b.InputIndex = 1
	c, err := BuildDirtyCandidate(context.Background(), root, "baseline", []string{a.Head, b.Head}, []DirtySnapshot{a, b})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if len(c.Conflicts) != 1 || c.Inputs[0] == c.Inputs[1] {
		t.Fatalf("lost shared-head snapshot: %+v", c)
	}
	a.InputIndex = -1
	b.InputIndex = -1
	if c, err := BuildDirtyCandidate(context.Background(), root, "baseline", []string{a.Head, b.Head}, []DirtySnapshot{a, b}); err == nil {
		c.Close()
		t.Fatal("ambiguous shared-head association accepted")
	}
}

func TestDirtyCandidateIdentityIncludesIndexProvenance(t *testing.T) {
	root := dirtyFixture(t)
	put(t, root, "tracked.txt", "working\n")
	a := observeTestDirty(t, root)
	build := func(s DirtySnapshot) (string, string) {
		t.Helper()
		c, err := BuildDirtyCandidate(context.Background(), root, "baseline", []string{"feature"}, []DirtySnapshot{s})
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		return c.Commit, c.Tree
	}
	first, tree := build(a)
	repeat, _ := build(a)
	if first != repeat {
		t.Fatal("same snapshot has unstable candidate identity")
	}
	gitTest(t, root, "add", "tracked.txt")
	b := observeTestDirty(t, root)
	second, secondTree := build(b)
	if first == second || tree != secondTree {
		t.Fatal("index provenance must change commit identity while preserving final tree")
	}
}

func TestDirtySnapshotIncludesModeChangeHiddenByGit(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Skip("executable mode requires POSIX filesystem")
	}
	root := dirtyFixture(t)
	gitTest(t, root, "config", "core.fileMode", "false")
	if err := os.Chmod(filepath.Join(root, "tracked.txt"), 0700); err != nil {
		t.Fatal(err)
	}
	if status := gitTest(t, root, "status", "--porcelain"); status != "" {
		t.Fatalf("fixture must have status-hidden executable change: %s", status)
	}
	s := observeTestDirty(t, root)
	if len(s.Unstaged) != 1 || s.Unstaged[0] != "tracked.txt" {
		t.Fatalf("hidden mode provenance absent: %+v", s)
	}
	c, err := BuildDirtyCandidate(context.Background(), root, "baseline", []string{"feature"}, []DirtySnapshot{s})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	entries, err := gitrepo.Entries(context.Background(), c.Dir, c.Commit)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, entry := range entries {
		if entry.Path == "tracked.txt" {
			found = true
			if entry.Mode != "100755" {
				t.Fatalf("lost executable change: %+v", entry)
			}
		}
	}
	if !found {
		t.Fatal("tracked file disappeared")
	}
	if status := gitTest(t, root, "status", "--porcelain"); status != "" {
		t.Fatal("source status mutated", status)
	}
}

func TestDirtySnapshotRejectsNormalizationHiddenByGit(t *testing.T) {
	for _, autocrlf := range []string{"true", "input"} {
		t.Run("autocrlf-"+autocrlf, func(t *testing.T) {
			root := dirtyFixture(t)
			gitTest(t, root, "config", "core.autocrlf", autocrlf)
			gitTest(t, root, "add", "--renormalize", "tracked.txt")
			put(t, root, "tracked.txt", "base\r\n")
			gitTest(t, root, "add", "tracked.txt")
			if status := gitTest(t, root, "status", "--porcelain"); status != "" {
				t.Fatalf("fixture must have normalized status-clean bytes: %s", status)
			}
			if _, err := ObserveDirty(context.Background(), gitrepo.WorktreeStatus{Path: root}); err == nil || !strings.Contains(err.Error(), "core.autocrlf") {
				t.Fatalf("unsupported autocrlf must fail closed: %v", err)
			}
		})
	}
	for _, attribute := range []string{"text", "text=auto", "eol=crlf", "working-tree-encoding=UTF-8", "ident"} {
		t.Run(attribute, func(t *testing.T) {
			root := dirtyFixture(t)
			put(t, root, ".gitattributes", "tracked.txt "+attribute+"\n")
			if _, err := ObserveDirty(context.Background(), gitrepo.WorktreeStatus{Path: root}); err == nil || !strings.Contains(err.Error(), "normalization") {
				t.Fatalf("unsupported attribute must fail closed: %v", err)
			}
		})
	}
	root := dirtyFixture(t)
	put(t, root, ".gitattributes", "tracked.txt -text\n")
	put(t, root, "tracked.txt", "base\r\n")
	s := observeTestDirty(t, root)
	if string(s.files["tracked.txt"].data) != "base\r\n" {
		t.Fatal("raw bytes changed for explicitly non-normalized file")
	}
}

func TestDirtySnapshotRejectsLocalAttributesFileNormalization(t *testing.T) {
	root := dirtyFixture(t)
	attrs := filepath.Join(t.TempDir(), "attributes")
	if err := os.WriteFile(attrs, []byte("tracked.txt text\n"), 0600); err != nil {
		t.Fatal(err)
	}
	gitTest(t, root, "config", "core.attributesFile", attrs)
	gitTest(t, root, "add", "--renormalize", "tracked.txt")
	put(t, root, "tracked.txt", "base\r\n")
	gitTest(t, root, "add", "tracked.txt")
	if status := gitTest(t, root, "status", "--porcelain"); status != "" {
		t.Fatalf("fixture must hide normalization: %s", status)
	}
	if _, err := ObserveDirty(context.Background(), gitrepo.WorktreeStatus{Path: root}); err == nil || !strings.Contains(err.Error(), "normalization") {
		t.Fatalf("local attributes normalization must fail closed: %v", err)
	}
}

func TestDirtyCandidateResetsPrivateIndexAcrossDifferentHeads(t *testing.T) {
	root := dirtyFixture(t)
	put(t, root, "a-committed.txt", "a committed\n")
	gitTest(t, root, "add", "a-committed.txt")
	gitTest(t, root, "commit", "-qm", "a committed")
	other := filepath.Join(t.TempDir(), "other")
	gitTest(t, root, "worktree", "add", "-b", "other", other, "baseline")
	put(t, other, "b-committed.txt", "b committed\n")
	gitTest(t, other, "add", "b-committed.txt")
	gitTest(t, other, "commit", "-qm", "b committed")
	clean := filepath.Join(t.TempDir(), "clean")
	gitTest(t, root, "worktree", "add", "-b", "clean", clean, "baseline")
	put(t, clean, "c-committed.txt", "c committed\n")
	gitTest(t, clean, "add", "c-committed.txt")
	gitTest(t, clean, "commit", "-qm", "c committed")
	put(t, root, "a-dirty.txt", "a saved\n")
	put(t, other, "b-dirty.txt", "b saved\n")
	a, b := observeTestDirty(t, root), observeTestDirty(t, other)
	a.InputIndex, b.InputIndex = 0, 1
	refs := gitTest(t, root, "show-ref")
	indexBytes := func(dir string) []byte {
		t.Helper()
		path := gitTest(t, dir, "rev-parse", "--git-path", "index")
		if !filepath.IsAbs(path) {
			path = filepath.Join(dir, path)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	beforeA, beforeB, beforeC := indexBytes(root), indexBytes(other), indexBytes(clean)
	c, err := BuildDirtyCandidate(context.Background(), root, "baseline", []string{"feature", "other", "clean"}, []DirtySnapshot{a, b})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if len(c.Inputs) != 3 || c.Inputs[2] != gitTest(t, clean, "rev-parse", "HEAD") || len(c.Conflicts) > 0 {
		t.Fatalf("input scope lost: %+v", c)
	}
	for i, prefix := range []string{"a", "b"} {
		files := gitTest(t, c.Dir, "ls-tree", "-r", "--name-only", c.Inputs[i])
		for _, suffix := range []string{"-committed.txt", "-dirty.txt"} {
			if !strings.Contains(files, prefix+suffix) {
				t.Fatalf("snapshot %d missing own input: %s", i, files)
			}
		}
		for _, otherPrefix := range []string{"a", "b", "c"} {
			if otherPrefix != prefix && strings.Contains(files, otherPrefix+"-committed.txt") {
				t.Fatalf("snapshot %d inherited unrelated index content: %s", i, files)
			}
		}
	}
	for _, file := range []string{"a-committed.txt", "a-dirty.txt", "b-committed.txt", "b-dirty.txt", "c-committed.txt"} {
		if _, err := os.Stat(filepath.Join(c.Dir, file)); err != nil {
			t.Fatalf("combined input %s absent: %v", file, err)
		}
	}
	if gitTest(t, root, "show-ref") != refs || string(indexBytes(root)) != string(beforeA) || string(indexBytes(other)) != string(beforeB) || string(indexBytes(clean)) != string(beforeC) {
		t.Fatal("private index materialization changed source refs/index bytes")
	}
}

func TestDirtyCandidateInShallowClonePreservesBoundary(t *testing.T) {
	clone := shallowClone(t, shallowSource(t), "2")
	gitTest(t, clone, "checkout", "-qb", "saved", "origin/a")
	put(t, clone, "saved.txt", "saved\n")
	s := observeTestDirty(t, clone)
	s.InputIndex = 0
	c, err := BuildDirtyCandidate(context.Background(), clone, "origin/main", []string{"origin/a", "origin/b"}, []DirtySnapshot{s})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	for _, path := range []string{"a.txt", "b.txt", "saved.txt"} {
		if _, err := os.Stat(filepath.Join(c.Dir, path)); err != nil {
			t.Fatal(path, err)
		}
	}
	if gitTest(t, clone, "rev-parse", "--is-shallow-repository") != "true" || gitTest(t, c.Dir, "rev-parse", "--is-shallow-repository") != "true" {
		t.Fatal("shallow provenance lost")
	}
}

func TestReobserveDirtyRequiresStableCapturedInput(t *testing.T) {
	for _, kind := range []string{"unchanged", "same-size edit", "index edit", "head moved", "canceled", "uncaptured"} {
		t.Run(kind, func(t *testing.T) {
			root := dirtyFixture(t)
			put(t, root, "tracked.txt", "first\n")
			prior := observeTestDirty(t, root)
			ctx := context.Background()
			switch kind {
			case "same-size edit":
				put(t, root, "tracked.txt", "other\n")
			case "index edit":
				gitTest(t, root, "add", "tracked.txt")
			case "head moved":
				gitTest(t, root, "add", "tracked.txt")
				gitTest(t, root, "commit", "-qm", "move head")
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "uncaptured":
				prior = DirtySnapshot{Worktree: root, Head: prior.Head, Digest: prior.Digest}
			}
			after, err := ReobserveDirty(ctx, prior)
			if kind == "unchanged" {
				if err != nil || after.Digest != prior.Digest {
					t.Fatalf("unchanged pin rejected: %+v %v", after, err)
				}
			} else if err == nil {
				t.Fatal("unverified input accepted", kind)
			}
		})
	}
}

func TestFailedDoubleCaptureCannotBecomeStableRevalidation(t *testing.T) {
	root := dirtyFixture(t)
	put(t, root, "tracked.txt", "first\n")
	failed, err := observeDirty(context.Background(), gitrepo.WorktreeStatus{Path: root}, func() { put(t, root, "tracked.txt", "other\n") })
	if err == nil || failed.Digest == "" || failed.files == nil {
		t.Fatal("fixture must return populated but unstable second capture", failed, err)
	}
	if _, err := ReobserveDirty(context.Background(), failed); err == nil {
		t.Fatal("failed double-capture snapshot accepted as stable prior")
	}
	if c, err := BuildDirtyCandidate(context.Background(), root, "baseline", []string{"feature"}, []DirtySnapshot{failed}); err == nil {
		c.Close()
		t.Fatal("failed observation entered candidate builder")
	}
}

func TestDirtySnapshotPreservesTrackedIndexExecutableMode(t *testing.T) {
	root := dirtyFixture(t)
	if runtime.GOOS != "windows" {
		if err := os.Chmod(filepath.Join(root, "tracked.txt"), 0755); err != nil {
			t.Fatal(err)
		}
	}
	gitTest(t, root, "update-index", "--chmod=+x", "tracked.txt")
	gitTest(t, root, "commit", "-qm", "committed executable mode")
	clean := observeTestDirty(t, root)
	if len(clean.Staged)+len(clean.Unstaged)+len(clean.Untracked) != 0 {
		t.Fatalf("committed mode became a fake saved change: %+v", clean)
	}
	put(t, root, "tracked.txt", "saved executable bytes\n")
	put(t, root, "new.exe", "untracked bytes\n")
	saved := observeTestDirty(t, root)
	c, err := BuildDirtyCandidate(context.Background(), root, "baseline", []string{"feature"}, []DirtySnapshot{saved})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	entries, err := gitrepo.Entries(context.Background(), c.Dir, c.Commit)
	if err != nil {
		t.Fatal(err)
	}
	modes := map[string]string{}
	for _, entry := range entries {
		modes[entry.Path] = entry.Mode
	}
	if modes["tracked.txt"] != "100755" || modes["new.exe"] != "100644" {
		t.Fatalf("saved input modes changed: %+v", modes)
	}
}

func TestDirtyCandidateConcurrentPreparationFailureCleansPrivateState(t *testing.T) {
	for _, kind := range []string{"invalid base", "snapshot moved", "canceled"} {
		t.Run(kind, func(t *testing.T) {
			root := dirtyFixture(t)
			put(t, root, "tracked.txt", "saved\n")
			snapshot := observeTestDirty(t, root)
			base := "baseline"
			ctx := context.Background()
			switch kind {
			case "invalid base":
				base = "missing-base"
			case "snapshot moved":
				put(t, root, "tracked.txt", "moved\n")
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			beforeStatus := gitTest(t, root, "status", "--porcelain=v1", "-z")
			beforeRefs := gitTest(t, root, "show-ref")
			private := t.TempDir()
			for _, name := range []string{"TMPDIR", "TMP", "TEMP"} {
				t.Setenv(name, private)
			}
			candidate, err := BuildDirtyCandidate(ctx, root, base, []string{"feature"}, []DirtySnapshot{snapshot})
			if candidate != nil {
				candidate.Close()
			}
			if err == nil {
				t.Fatal("failed preparation accepted", kind)
			}
			entries, readErr := os.ReadDir(private)
			if readErr != nil || len(entries) != 0 {
				t.Fatal("failed preparation leaked private state", entries, readErr)
			}
			if gitTest(t, root, "status", "--porcelain=v1", "-z") != beforeStatus || gitTest(t, root, "show-ref") != beforeRefs {
				t.Fatal("failed concurrent preparation changed source")
			}
		})
	}
}

func TestDirtyConcurrentRevalidationPreservesFirstFailureAndJoins(t *testing.T) {
	started := make(chan struct{})
	joined := make(chan struct{})
	moved := errors.New("snapshot superseded")
	err := revalidateDirtySnapshotsWith(context.Background(), []DirtySnapshot{{Worktree: "slow"}, {Worktree: "moved"}}, func(ctx context.Context, snapshot DirtySnapshot) error {
		if snapshot.Worktree == "slow" {
			close(started)
			<-ctx.Done()
			close(joined)
			return ctx.Err()
		}
		<-started
		return moved
	})
	if !errors.Is(err, moved) {
		t.Fatal("primary snapshot failure hidden by sibling cancellation", err)
	}
	select {
	case <-joined:
	default:
		t.Fatal("revalidation returned before canceled sibling joined")
	}
}
