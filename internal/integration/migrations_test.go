package integration

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/Jake-Network/radar/internal/gate"
	"github.com/Jake-Network/radar/internal/migrations"
	"github.com/Jake-Network/radar/internal/model"
)

func alembicRevision(id, down string) string {
	return fmt.Sprintf("revision = %q\ndown_revision = %q\n", id, down)
}

// migrationRepo has an Alembic history with one head and two branches that
// each add one revision on that head.
func migrationRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	gitTest(t, root, "init", "-q", "-b", "main")
	put(t, root, "db/env.py", "print('env')\n")
	put(t, root, "db/versions/r0.py", "revision = 'r0'\ndown_revision = None\n")
	put(t, root, "app.py", "x = 1\n")
	gitTest(t, root, "add", ".")
	gitTest(t, root, "commit", "-qm", "base")
	for _, b := range []string{"a", "b"} {
		gitTest(t, root, "checkout", "-q", "-b", b, "main")
		put(t, root, "db/versions/"+b+"1.py", alembicRevision(b+"1", "r0"))
		gitTest(t, root, "add", ".")
		gitTest(t, root, "commit", "-qm", b)
	}
	gitTest(t, root, "checkout", "-q", "main")
	return root
}

func required(r Report) []string { return r.Gate.Policy.Require }

func TestMigrationOrderFailsTheCombinationOnly(t *testing.T) {
	root := migrationRepo(t)
	for _, branches := range [][]string{{"a"}, {"b"}} {
		r, e := Preview(context.Background(), root, Options{Base: "main", Branches: branches})
		if e != nil {
			t.Fatal(e)
		}
		if r.Gate.Verdict != gate.Pass || !slices.Contains(required(r), migrations.CheckID) {
			t.Fatalf("%v alone: verdict %s, require %v, checks %+v", branches, r.Gate.Verdict, required(r), r.Checks)
		}
	}
	r, e := Preview(context.Background(), root, Options{Base: "main", Branches: []string{"a", "b"}})
	if e != nil {
		t.Fatal(e)
	}
	if r.Gate.Verdict != gate.Fail || r.Status != model.StatusFailed {
		t.Fatalf("combined verdict %s status %s", r.Gate.Verdict, r.Status)
	}
	found := false
	for _, f := range r.Findings {
		if f.Code == "migration_multiple_heads" && f.Severity == model.SeverityError && f.Evidence == model.VerifiedStatic && len(f.Locations) == 2 && f.Remediation != "" {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing migration finding: %+v", r.Findings)
	}
	covered := false
	for _, c := range r.Coverage {
		covered = covered || (c.Analyzer == migrations.CheckID && len(c.Excluded) > 0)
	}
	if !covered {
		t.Fatalf("coverage omits migration scope: %+v", r.Coverage)
	}
}

func TestMigrationOrderAbsentWhenNoMigrationChanged(t *testing.T) {
	root := migrationRepo(t)
	gitTest(t, root, "checkout", "-q", "-b", "c", "main")
	put(t, root, "app.py", "x = 2\n")
	gitTest(t, root, "commit", "-qam", "c")
	r, e := Preview(context.Background(), root, Options{Base: "main", Branches: []string{"c"}})
	if e != nil {
		t.Fatal(e)
	}
	for _, c := range r.Checks {
		if c.ID == migrations.CheckID {
			t.Fatalf("unexpected migration check %+v", c)
		}
	}
	if slices.Contains(required(r), migrations.CheckID) {
		t.Fatalf("migration_order required without a migration change: %v", required(r))
	}
}

func TestMigrationOrderUnknownBlocks(t *testing.T) {
	root := migrationRepo(t)
	gitTest(t, root, "checkout", "-q", "-b", "computed", "main")
	put(t, root, "db/versions/c1.py", "PREV = 'r0'\nrevision = 'c1'\ndown_revision = PREV\n")
	gitTest(t, root, "add", ".")
	gitTest(t, root, "commit", "-qm", "computed")
	r, e := Preview(context.Background(), root, Options{Base: "main", Branches: []string{"computed"}})
	if e != nil {
		t.Fatal(e)
	}
	if r.Gate.Verdict != gate.Blocked {
		t.Fatalf("verdict %s, want blocked: %+v", r.Gate.Verdict, r.Checks)
	}
}

func TestMigrationOrderNotRunOnTextualConflict(t *testing.T) {
	root := migrationRepo(t)
	for _, b := range []string{"x", "y"} {
		gitTest(t, root, "checkout", "-q", "-b", b, "main")
		put(t, root, "db/versions/same.py", alembicRevision("same-"+b, "r0"))
		gitTest(t, root, "add", ".")
		gitTest(t, root, "commit", "-qm", b)
	}
	r, e := Preview(context.Background(), root, Options{Base: "main", Branches: []string{"x", "y"}})
	if e != nil {
		t.Fatal(e)
	}
	if r.Gate.Verdict != gate.Fail || len(r.Conflicts) == 0 {
		t.Fatalf("verdict %s conflicts %v", r.Gate.Verdict, r.Conflicts)
	}
	for _, c := range r.Checks {
		if c.ID == migrations.CheckID {
			t.Fatalf("migration check ran without a combined tree: %+v", c)
		}
	}
}

func migrationFinding(t *testing.T, r Report) model.Finding {
	t.Helper()
	for _, f := range r.Findings {
		if f.Code == "migration_multiple_heads" {
			return f
		}
	}
	t.Fatalf("no migration finding: %+v %+v", r.Checks, r.Findings)
	return model.Finding{}
}

// A branch behind the base that changed no migration must not be blamed for
// a split the base already joined; the branch that adds the new line is.
func TestMigrationOrderDoesNotBlameStaleBranch(t *testing.T) {
	root := t.TempDir()
	gitTest(t, root, "init", "-q", "-b", "main")
	put(t, root, "db/env.py", "print('env')\n")
	put(t, root, "db/versions/r0.py", "revision = 'r0'\ndown_revision = None\n")
	put(t, root, "db/versions/x.py", alembicRevision("x", "r0"))
	put(t, root, "db/versions/y.py", alembicRevision("y", "r0"))
	gitTest(t, root, "add", ".")
	gitTest(t, root, "commit", "-qm", "two heads")
	gitTest(t, root, "checkout", "-q", "-b", "stale")
	put(t, root, "a.txt", "a\n")
	gitTest(t, root, "add", ".")
	gitTest(t, root, "commit", "-qm", "unrelated")
	gitTest(t, root, "checkout", "-q", "main")
	put(t, root, "db/versions/m.py", "revision = 'm'\ndown_revision = ('x', 'y')\n")
	gitTest(t, root, "add", ".")
	gitTest(t, root, "commit", "-qm", "merge heads")
	gitTest(t, root, "checkout", "-q", "-b", "adds")
	put(t, root, "db/versions/n.py", alembicRevision("n", "r0"))
	gitTest(t, root, "add", ".")
	gitTest(t, root, "commit", "-qm", "n")
	gitTest(t, root, "checkout", "-q", "main")
	r, e := Preview(context.Background(), root, Options{Base: "main", Branches: []string{"stale", "adds"}})
	if e != nil {
		t.Fatal(e)
	}
	f := migrationFinding(t, r)
	if r.Gate.Verdict != gate.Fail || !strings.Contains(f.Explanation, "Branch 2 (") || strings.Contains(f.Explanation, "Branch 1 (") {
		t.Fatalf("verdict %s, explanation %s", r.Gate.Verdict, f.Explanation)
	}
	if !strings.Contains(f.Remediation, `down_revision = "m"`) {
		t.Fatalf("remediation %s", f.Remediation)
	}
}

// A branch that forked before the base gained a revision conflicts with the
// base, not with another branch.
func TestMigrationOrderNamesBranchThatConflictsWithBase(t *testing.T) {
	root := migrationRepo(t)
	gitTest(t, root, "checkout", "-q", "-b", "late", "main~0")
	gitTest(t, root, "checkout", "-q", "main")
	put(t, root, "db/versions/r1.py", alembicRevision("r1", "r0"))
	gitTest(t, root, "add", ".")
	gitTest(t, root, "commit", "-qm", "r1 on main")
	r, e := Preview(context.Background(), root, Options{Base: "main", Branches: []string{"a", "late"}})
	if e != nil {
		t.Fatal(e)
	}
	f := migrationFinding(t, r)
	if !strings.Contains(f.Explanation, "Branch 1 (") || strings.Contains(f.Explanation, "only when the branches are combined") {
		t.Fatalf("explanation %s", f.Explanation)
	}
}
