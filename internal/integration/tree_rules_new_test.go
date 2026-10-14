package integration

import (
	"context"
	"slices"
	"testing"

	"github.com/Jake-Network/radar/internal/gate"
	"github.com/Jake-Network/radar/internal/lockfiles"
	"github.com/Jake-Network/radar/internal/model"
	"github.com/Jake-Network/radar/internal/uniqueness"
)

func TestDeclaredUniqueKeysFailOnlyWhenBranchesCombine(t *testing.T) {
	root := t.TempDir()
	gitTest(t, root, "init", "-q", "-b", "main")
	put(t, root, ".radar/unique.json", `{"version":1,"rules":[{"name":"error code","files":["errors/*.txt"],"pattern":"E[0-9]+"}]}`)
	put(t, root, "README.md", "fixture\n")
	gitTest(t, root, "add", ".")
	gitTest(t, root, "commit", "-qm", "base")
	for _, branch := range []string{"a", "b"} {
		gitTest(t, root, "checkout", "-q", "-b", branch, "main")
		put(t, root, "errors/"+branch+".txt", "E1001\n")
		gitTest(t, root, "add", ".")
		gitTest(t, root, "commit", "-qm", branch)
	}
	gitTest(t, root, "checkout", "-q", "main")
	for _, branches := range [][]string{{"a"}, {"b"}, {"a", "b"}} {
		r, e := Preview(context.Background(), root, Options{Base: "main", Branches: branches})
		if e != nil {
			t.Fatal(e)
		}
		if !slices.Contains(r.Gate.Policy.Require, uniqueness.CheckID) {
			t.Fatalf("check not required: %+v", r.Gate)
		}
		for _, c := range r.Checks {
			if c.ID == uniqueness.CheckID {
				want := model.StatusPassed
				if len(branches) == 2 {
					want = model.StatusFailed
				}
				if c.Status != want {
					t.Fatalf("%v status %s want %s", branches, c.Status, want)
				}
			}
		}
		if len(branches) == 2 && r.Gate.Verdict != gate.Fail {
			t.Fatalf("combined verdict %s", r.Gate.Verdict)
		}
	}
}
func TestLockfileMissingDependencyIsRequiredStaticFailure(t *testing.T) {
	root := t.TempDir()
	gitTest(t, root, "init", "-q", "-b", "main")
	put(t, root, "package.json", `{"name":"fixture","dependencies":{}}`)
	put(t, root, "package-lock.json", `{"lockfileVersion":3,"packages":{"":{"dependencies":{}}}}`)
	gitTest(t, root, "add", ".")
	gitTest(t, root, "commit", "-qm", "base")
	gitTest(t, root, "checkout", "-q", "-b", "dependency")
	put(t, root, "package.json", `{"name":"fixture","dependencies":{"missing":"1"}}`)
	gitTest(t, root, "commit", "-qam", "dependency")
	gitTest(t, root, "checkout", "-q", "main")
	r, e := Preview(context.Background(), root, Options{Base: "main", Branches: []string{"dependency"}})
	if e != nil {
		t.Fatal(e)
	}
	if r.Gate.Verdict != gate.Fail || !slices.Contains(r.Gate.Policy.Require, lockfiles.CheckID) {
		t.Fatalf("%+v", r.Gate)
	}
	found := false
	for _, f := range r.Findings {
		found = found || f.Code == "lockfile_dependency_missing" && f.Severity == model.SeverityError
	}
	if !found {
		t.Fatalf("missing finding: %+v", r.Findings)
	}
}
