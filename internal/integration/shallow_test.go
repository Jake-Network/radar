package integration

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	gitrepo "github.com/Jake-Network/radar/internal/git"
)

// shallowSource has a deep main line, two agent branches off its tip and an
// old branch that forked from the first commit.
func shallowSource(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	gitTest(t, root, "init", "-q")
	gitTest(t, root, "symbolic-ref", "HEAD", "refs/heads/main")
	for i := 0; i < 5; i++ {
		put(t, root, "history.txt", strings.Repeat("x", i+1)+"\n")
		gitTest(t, root, "add", ".")
		gitTest(t, root, "commit", "-qm", "history")
		if i == 0 {
			gitTest(t, root, "branch", "old")
		}
	}
	gitTest(t, root, "checkout", "-qb", "a", "main")
	put(t, root, "a.txt", "a\n")
	gitTest(t, root, "add", ".")
	gitTest(t, root, "commit", "-qm", "a")
	gitTest(t, root, "checkout", "-qb", "b", "main")
	put(t, root, "b.txt", "b\n")
	gitTest(t, root, "add", ".")
	gitTest(t, root, "commit", "-qm", "b")
	gitTest(t, root, "checkout", "-q", "old")
	put(t, root, "old.txt", "old\n")
	gitTest(t, root, "add", ".")
	gitTest(t, root, "commit", "-qm", "old")
	gitTest(t, root, "checkout", "-q", "main")
	return root
}

func shallowClone(t *testing.T, source string, depth string) string {
	t.Helper()
	clone := filepath.Join(t.TempDir(), "clone")
	gitTest(t, source, "clone", "-q", "--no-single-branch", "--depth", depth, "file://"+filepath.ToSlash(source), clone)
	if gitTest(t, clone, "rev-parse", "--is-shallow-repository") != "true" {
		t.Fatal("clone is not shallow")
	}
	return clone
}

func TestShallowCloneCombinesWithinItsHistory(t *testing.T) {
	clone := shallowClone(t, shallowSource(t), "2")
	c, e := BuildCandidate(context.Background(), clone, "origin/main", []string{"origin/a", "origin/b"})
	defer c.Close()
	if e != nil {
		t.Fatalf("shallow clone with reachable merge bases: %v", e)
	}
	if c.Commit == "" || len(c.Conflicts) != 0 {
		t.Fatalf("candidate %+v", c)
	}
	files := gitTest(t, c.Dir, "ls-tree", "--name-only", "HEAD")
	if !strings.Contains(files, "a.txt") || !strings.Contains(files, "b.txt") {
		t.Fatalf("candidate tree %q", files)
	}
	// The source clone is untouched: still shallow, no new refs.
	if gitTest(t, clone, "rev-parse", "--is-shallow-repository") != "true" {
		t.Fatal("source repository changed")
	}
}

func TestShallowCloneMissingHistoryIsExplained(t *testing.T) {
	clone := shallowClone(t, shallowSource(t), "1")
	c, e := BuildCandidate(context.Background(), clone, "origin/main", []string{"origin/old"})
	defer c.Close()
	var shallow *gitrepo.ShallowHistoryError
	if !errors.As(e, &shallow) {
		t.Fatalf("expected a shallow history error, got %v", e)
	}
	if !strings.Contains(e.Error(), "git fetch --unshallow") || !strings.Contains(e.Error(), "fetch-depth: 0") {
		t.Fatalf("error does not say how to fix it: %v", e)
	}
}

func TestCompleteRepositoryHasNoShallowBoundary(t *testing.T) {
	root := shallowSource(t)
	boundary, e := gitrepo.ShallowBoundary(context.Background(), root)
	if e != nil || boundary != nil {
		t.Fatalf("complete repository boundary %v %v", boundary, e)
	}
	plain := errors.New("plain")
	if gitrepo.ExplainShallow(context.Background(), root, plain) != plain {
		t.Fatal("complete repository error rewritten")
	}
}
