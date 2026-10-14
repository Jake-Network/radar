package migrations

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"

	gitrepo "github.com/Jake-Network/radar/internal/git"
	"github.com/Jake-Network/radar/internal/model"
)

// store is an in-memory blob store; files maps path to content.
type store map[string][]byte

func (s store) tree(label string, files map[string]string) Tree {
	t := Tree{Label: label, Revision: label}
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		sum := sha1.Sum([]byte(files[p]))
		oid := hex.EncodeToString(sum[:])
		s[oid] = []byte(files[p])
		t.Files = append(t.Files, File{Path: p, OID: oid, Size: int64(len(files[p]))})
	}
	return t
}

func (s store) read(oid string) ([]byte, error) {
	b, ok := s[oid]
	if !ok {
		return nil, os.ErrNotExist
	}
	if len(b) > maxFileBytes {
		return nil, gitrepo.ErrBlobTooLarge
	}
	return b, nil
}

// with returns base plus extra files; an empty value deletes a path.
func with(base map[string]string, extra map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range base {
		out[k] = v
	}
	for k, v := range extra {
		if v == "" {
			delete(out, k)
			continue
		}
		out[k] = v
	}
	return out
}

func rev(id, down string) string {
	d := "None"
	if down != "" {
		d = fmt.Sprintf("%q", down)
	}
	return fmt.Sprintf("\"\"\"doc\"\"\"\nfrom alembic import op\n\nrevision = %q\ndown_revision = %s\nbranch_labels = None\n\n\ndef upgrade():\n    op.add_column('t', 'c')\n", id, d)
}

func djm(deps ...string) string {
	return "from django.db import migrations\n\n\nclass Migration(migrations.Migration):\n    dependencies = [" + strings.Join(deps, ", ") + "]\n    operations = []\n"
}

// diff lists paths whose content differs between two file maps.
func diff(a, b map[string]string) []string {
	out := []string{}
	for p, v := range a {
		if w, ok := b[p]; !ok || w != v {
			out = append(out, p)
		}
	}
	for p := range b {
		if _, ok := a[p]; !ok {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

type scenario struct {
	name     string
	base     map[string]string
	branches []map[string]string
	// combined defaults to base plus every branch's additions.
	combined map[string]string
	status   model.Status
	codes    []string
	contains []string
	fix      string
}

func run(t *testing.T, c scenario) Result {
	t.Helper()
	s := store{}
	trees := []Tree{s.tree("the base", c.base)}
	combined := c.base
	for i, b := range c.branches {
		trees = append(trees, s.tree(fmt.Sprintf("branch %d (b%d) merged alone onto the base", i+1, i+1), b))
		combined = with(combined, b)
	}
	if c.combined != nil {
		combined = c.combined
	}
	trees = append(trees, s.tree("the combined tree", combined))
	res, err := Evaluate(context.Background(), trees, diff(c.base, combined), s.read)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

var alembicBase = map[string]string{
	"db/alembic.ini":           "[alembic]\nscript_location = db\n",
	"db/env.py":                "print('env')\n",
	"db/versions/r0_init.py":   rev("r0", ""),
	"db/versions/__init__.py":  "# package\n",
	"app/models.py":            "x = 1\n",
	"db/versions/README":       "notes\n",
	"db/versions/r0_helper.md": "notes\n",
}

func TestEvaluate(t *testing.T) {
	cases := []scenario{
		{
			name:     "alembic heads diverge only when combined",
			base:     alembicBase,
			branches: []map[string]string{with(alembicBase, map[string]string{"db/versions/a1.py": rev("a1", "r0")}), with(alembicBase, map[string]string{"db/versions/b1.py": rev("b1", "r0")})},
			status:   model.StatusFailed,
			codes:    []string{"migration_multiple_heads"},
			contains: []string{`"a1" (db/versions/a1.py)`, `"b1" (db/versions/b1.py)`, `all revise "r0"`, "appears only when the branches are combined"},
			fix:      `In db/versions/b1.py, set down_revision = "a1"`,
		},
		{
			name:     "fix points the later line at the other line's head",
			base:     alembicBase,
			branches: []map[string]string{with(alembicBase, map[string]string{"db/versions/a1.py": rev("a1", "r0"), "db/versions/a2.py": rev("a2", "a1")}), with(alembicBase, map[string]string{"db/versions/b1.py": rev("b1", "r0")})},
			status:   model.StatusFailed,
			codes:    []string{"migration_multiple_heads"},
			fix:      `In db/versions/b1.py, set down_revision = "a2"`,
		},
		{
			name:     "rebased revision keeps one head",
			base:     alembicBase,
			branches: []map[string]string{with(alembicBase, map[string]string{"db/versions/a1.py": rev("a1", "r0")}), with(alembicBase, map[string]string{"db/versions/a1.py": rev("a1", "r0"), "db/versions/b1.py": rev("b1", "a1")})},
			status:   model.StatusPassed,
		},
		{
			name: "merge revision joins the lines",
			base: alembicBase,
			branches: []map[string]string{
				with(alembicBase, map[string]string{"db/versions/a1.py": rev("a1", "r0")}),
				with(alembicBase, map[string]string{"db/versions/a1.py": rev("a1", "r0"), "db/versions/b1.py": rev("b1", "r0"), "db/versions/m.py": "revision = 'm'\ndown_revision = ('a1', 'b1')\n"}),
			},
			status: model.StatusPassed,
		},
		{
			name: "split already on the base is a warning, not a failure",
			base: with(alembicBase, map[string]string{"db/versions/x1.py": rev("x1", "r0"), "db/versions/x2.py": rev("x2", "r0")}),
			branches: []map[string]string{
				with(alembicBase, map[string]string{"db/versions/x1.py": rev("x1", "r0"), "db/versions/x2.py": rev("x2", "r0"), "db/versions/a1.py": rev("a1", "x1")}),
			},
			status: model.StatusPassed,
			codes:  []string{"migration_multiple_heads_on_base"},
		},
		{
			name: "a new line on a base split is still a new divergence",
			base: with(alembicBase, map[string]string{"db/versions/x1.py": rev("x1", "r0"), "db/versions/x2.py": rev("x2", "r0")}),
			branches: []map[string]string{
				with(alembicBase, map[string]string{"db/versions/x1.py": rev("x1", "r0"), "db/versions/x2.py": rev("x2", "r0"), "db/versions/a1.py": rev("a1", "r0")}),
			},
			status: model.StatusFailed,
			codes:  []string{"migration_multiple_heads"},
		},
		{
			name:     "divergence inside one branch is not blamed on the combination",
			base:     alembicBase,
			branches: []map[string]string{with(alembicBase, map[string]string{"db/versions/a1.py": rev("a1", "r0"), "db/versions/a2.py": rev("a2", "r0")}), with(alembicBase, map[string]string{"app/models.py": "x = 2\n"})},
			status:   model.StatusFailed,
			codes:    []string{"migration_multiple_heads"},
			contains: []string{"Branch 1 (b1) merged alone onto the base already has this"},
		},
		{
			name:     "branch that diverges from a revision added to the base is named",
			base:     with(alembicBase, map[string]string{"db/versions/x.py": rev("x", "r0")}),
			branches: []map[string]string{with(alembicBase, map[string]string{"db/versions/x.py": rev("x", "r0"), "db/versions/y.py": rev("y", "r0")})},
			status:   model.StatusFailed,
			codes:    []string{"migration_multiple_heads"},
			contains: []string{"Branch 1 (b1) merged alone onto the base already has this"},
			fix:      `In db/versions/y.py, set down_revision = "x"`,
		},
		{
			name:     "typed assignments are literals",
			base:     alembicBase,
			branches: []map[string]string{with(alembicBase, map[string]string{"db/versions/a1.py": "from typing import Union\nrevision: str = 'a1'\ndown_revision: Union[str, None] = 'r0'\n"}), with(alembicBase, map[string]string{"db/versions/b1.py": rev("b1", "r0")})},
			status:   model.StatusFailed,
			codes:    []string{"migration_multiple_heads"},
		},
		{
			name:     "repair that follows another branch's revision passes combined",
			base:     alembicBase,
			branches: []map[string]string{with(alembicBase, map[string]string{"db/versions/a1.py": rev("a1", "r0")}), with(alembicBase, map[string]string{"db/versions/b1.py": rev("b1", "a1")})},
			status:   model.StatusPassed,
			contains: []string{`"b1" follows "a1", which is not in this history in branch 2 (b2) merged alone onto the base`},
		},
		{
			name:     "unreadable branch alone leaves a conflict unattributed",
			base:     alembicBase,
			branches: []map[string]string{with(alembicBase, map[string]string{"db/versions/a1.py": rev("a1", "r0"), "db/versions/a2.py": rev("a2", "a1")}), with(alembicBase, map[string]string{"db/versions/b1.py": rev("b1", "a1")})},
			status:   model.StatusFailed,
			codes:    []string{"migration_multiple_heads"},
			contains: []string{"could not be merged or read alone"},
		},
		{
			name: "unreadable base cannot attribute a combined conflict",
			base: with(alembicBase, map[string]string{"db/versions/bad.py": "revision = X\ndown_revision = 'r0'\n"}),
			branches: []map[string]string{
				with(alembicBase, map[string]string{"db/versions/a1.py": rev("a1", "r0")}),
				with(alembicBase, map[string]string{"db/versions/b1.py": rev("b1", "r0")}),
			},
			combined: with(alembicBase, map[string]string{"db/versions/a1.py": rev("a1", "r0"), "db/versions/b1.py": rev("b1", "r0")}),
			status:   model.StatusUnknown,
			contains: []string{"base cannot be read"},
		},
		{
			name: "unreadable base with a clean combined tree passes",
			base: with(alembicBase, map[string]string{"db/versions/bad.py": "revision = X\ndown_revision = 'r0'\n"}),
			branches: []map[string]string{
				with(alembicBase, map[string]string{"db/versions/bad.py": rev("bad", "r0")}),
			},
			status: model.StatusPassed,
		},
		{
			name:     "computed down_revision is unknown",
			base:     alembicBase,
			branches: []map[string]string{with(alembicBase, map[string]string{"db/versions/a1.py": "PREV = 'r0'\nrevision = 'a1'\ndown_revision = PREV\n"})},
			status:   model.StatusUnknown,
			contains: []string{"down_revision is not a string"},
		},
		{
			name:     "f-string revision is unknown",
			base:     alembicBase,
			branches: []map[string]string{with(alembicBase, map[string]string{"db/versions/a1.py": "revision = f'a{1}'\ndown_revision = 'r0'\n"})},
			status:   model.StatusUnknown,
		},
		{
			name:     "parent outside the history is unknown",
			base:     alembicBase,
			branches: []map[string]string{with(alembicBase, map[string]string{"db/versions/a1.py": rev("a1", "elsewhere")})},
			status:   model.StatusUnknown,
			contains: []string{"not in this history"},
		},
		{
			name:     "nested version directory is unknown",
			base:     alembicBase,
			branches: []map[string]string{with(alembicBase, map[string]string{"db/versions/2024/a1.py": rev("a1", "r0")})},
			status:   model.StatusUnknown,
			contains: []string{"nested version directory"},
		},
		{
			name:     "syntax error is unknown",
			base:     alembicBase,
			branches: []map[string]string{with(alembicBase, map[string]string{"db/versions/a1.py": "revision = 'a1'\ndown_revision = ('r0'\n"})},
			status:   model.StatusUnknown,
		},
		{
			name:     "duplicate revision identifier is unknown",
			base:     alembicBase,
			branches: []map[string]string{with(alembicBase, map[string]string{"db/versions/a1.py": rev("r0", "")})},
			status:   model.StatusUnknown,
			contains: []string{"declared by both"},
		},
		{
			name:     "oversized file is unknown",
			base:     alembicBase,
			branches: []map[string]string{with(alembicBase, map[string]string{"db/versions/a1.py": rev("a1", "r0") + "#" + strings.Repeat("x", maxFileBytes)})},
			status:   model.StatusUnknown,
			contains: []string{"analysis limit"},
		},
		{
			name: "confirmed conflict wins over an unknown history",
			base: with(alembicBase, map[string]string{"other/env.py": "x", "other/versions/o.py": rev("o", "")}),
			branches: []map[string]string{
				with(alembicBase, map[string]string{"other/env.py": "x", "other/versions/o.py": rev("o", ""), "db/versions/a1.py": rev("a1", "r0"), "other/versions/p.py": "revision = X\n"}),
				with(alembicBase, map[string]string{"other/env.py": "x", "other/versions/o.py": rev("o", ""), "db/versions/b1.py": rev("b1", "r0")}),
			},
			status: model.StatusFailed,
			codes:  []string{"migration_multiple_heads"},
		},
		{
			name: "flask-migrate migrations directory is alembic, not django",
			base: map[string]string{"migrations/env.py": "x", "migrations/alembic.ini": "x", "migrations/versions/r0.py": rev("r0", "")},
			branches: []map[string]string{
				{"migrations/env.py": "x", "migrations/alembic.ini": "x", "migrations/versions/r0.py": rev("r0", ""), "migrations/versions/a.py": rev("a", "r0")},
				{"migrations/env.py": "x", "migrations/alembic.ini": "x", "migrations/versions/r0.py": rev("r0", ""), "migrations/versions/b.py": rev("b", "r0")},
			},
			status: model.StatusFailed,
			codes:  []string{"migration_multiple_heads"},
		},
	}
	shop := map[string]string{
		"shop/migrations/__init__.py":     "# package\n",
		"shop/migrations/0001_initial.py": djm(`("auth", "0001_initial")`, `migrations.swappable_dependency(settings.AUTH_USER_MODEL)`),
	}
	cases = append(cases,
		scenario{
			name:     "django leaves diverge only when combined",
			base:     shop,
			branches: []map[string]string{with(shop, map[string]string{"shop/migrations/0002_a.py": djm(`("shop", "0001_initial")`)}), with(shop, map[string]string{"shop/migrations/0002_b.py": djm(`('shop', '0001_initial')`, `("auth", "0012_x")`)})},
			status:   model.StatusFailed,
			codes:    []string{"migration_multiple_leaves"},
			contains: []string{"for app shop", "0002_a (shop/migrations/0002_a.py)", "all depend on 0001_initial"},
			fix:      `In shop/migrations/0002_b.py, depend on ("shop", "0002_a") instead of ("shop", "0001_initial"), or run ` + "`python manage.py makemigrations --merge shop`",
		},
		scenario{
			name: "django merge migration joins the leaves",
			base: shop,
			branches: []map[string]string{
				with(shop, map[string]string{"shop/migrations/0002_a.py": djm(`("shop", "0001_initial")`)}),
				with(shop, map[string]string{"shop/migrations/0002_a.py": djm(`("shop", "0001_initial")`), "shop/migrations/0002_b.py": djm(`("shop", "0001_initial")`), "shop/migrations/0003_merge.py": djm(`("shop", "0002_a")`, `("shop", "0002_b")`)}),
			},
			status: model.StatusPassed,
		},
		scenario{
			name: "django app label differs from the directory",
			base: map[string]string{"apps/store/migrations/0001_initial.py": djm(), "apps/store/migrations/0002_x.py": djm(`("shop", "0001_initial")`)},
			branches: []map[string]string{
				{"apps/store/migrations/0001_initial.py": djm(), "apps/store/migrations/0002_x.py": djm(`("shop", "0001_initial")`), "apps/store/migrations/0003_a.py": djm(`("shop", "0002_x")`)},
				{"apps/store/migrations/0001_initial.py": djm(), "apps/store/migrations/0002_x.py": djm(`("shop", "0001_initial")`), "apps/store/migrations/0003_b.py": djm(`("shop", "0002_x")`)},
			},
			status:   model.StatusFailed,
			codes:    []string{"migration_multiple_leaves"},
			contains: []string{"for app shop"},
		},
		scenario{
			name:     "django computed dependencies are unknown",
			base:     shop,
			branches: []map[string]string{with(shop, map[string]string{"shop/migrations/0002_a.py": "from django.db import migrations\nDEPS = []\n\nclass Migration(migrations.Migration):\n    dependencies = DEPS + [('shop', '0001_initial')]\n"})},
			status:   model.StatusUnknown,
			contains: []string{"dependencies is not a literal list"},
		},
		scenario{
			name:     "django squash with replaced migrations present is unknown",
			base:     shop,
			branches: []map[string]string{with(shop, map[string]string{"shop/migrations/0001_squashed.py": "from django.db import migrations\n\nclass Migration(migrations.Migration):\n    replaces = [('shop', '0001_initial')]\n    dependencies = []\n"})},
			status:   model.StatusUnknown,
		},
		scenario{
			name:     "django numbered module without a Migration class is unknown",
			base:     shop,
			branches: []map[string]string{with(shop, map[string]string{"shop/migrations/0002_a.py": "x = 1\n"})},
			status:   model.StatusUnknown,
		},
		scenario{
			name:     "django helper module is ignored",
			base:     shop,
			branches: []map[string]string{with(shop, map[string]string{"shop/migrations/helpers.py": "x = 1\n", "shop/migrations/0002_a.py": djm(`("shop", "0001_initial")`)})},
			status:   model.StatusPassed,
		},
	)
	flyway := map[string]string{"src/main/resources/db/migration/V1__init.sql": "create table t();", "src/main/resources/db/migration/V3__more.sql": "select 1;"}
	sql := map[string]string{"migrations/000001_init.up.sql": "a", "migrations/000001_init.down.sql": "b"}
	cases = append(cases,
		scenario{
			name:     "flyway duplicate version only when combined",
			base:     flyway,
			branches: []map[string]string{with(flyway, map[string]string{"src/main/resources/db/migration/V4__a.sql": "a"}), with(flyway, map[string]string{"src/main/resources/db/migration/V4__b.sql": "b"})},
			status:   model.StatusFailed,
			codes:    []string{"migration_duplicate_version"},
			contains: []string{"use version 4 more than once"},
			fix:      "Renumber src/main/resources/db/migration/V4__b.sql (and its matching files) to a version above 4.",
		},
		scenario{
			name:     "flyway versions compare numerically",
			base:     flyway,
			branches: []map[string]string{with(flyway, map[string]string{"src/main/resources/db/migration/V4_1__a.sql": "a"}), with(flyway, map[string]string{"src/main/resources/db/migration/V04.1.0__b.sql": "b"})},
			status:   model.StatusFailed,
			codes:    []string{"migration_duplicate_version"},
		},
		scenario{
			name:     "flyway version below the base is a warning",
			base:     flyway,
			branches: []map[string]string{with(flyway, map[string]string{"src/main/resources/db/migration/V2__late.sql": "a"})},
			status:   model.StatusPassed,
			codes:    []string{"migration_out_of_order"},
		},
		scenario{
			name:     "sql up and down files are one migration",
			base:     sql,
			branches: []map[string]string{with(sql, map[string]string{"migrations/000002_a.up.sql": "a", "migrations/000002_a.down.sql": "b"})},
			status:   model.StatusPassed,
		},
		scenario{
			name:     "sql duplicate version is one finding",
			base:     sql,
			branches: []map[string]string{with(sql, map[string]string{"migrations/000002_a.up.sql": "a", "migrations/000002_a.down.sql": "b"}), with(sql, map[string]string{"migrations/2_b.up.sql": "c", "migrations/2_b.down.sql": "d"})},
			status:   model.StatusFailed,
			codes:    []string{"migration_duplicate_version"},
		},
	)
	labeled := with(alembicBase, map[string]string{"db/versions/r0_init.py": "revision = 'r0'\ndown_revision = None\nbranch_labels = ('default',)\n"})
	legacy := with(shop, map[string]string{
		"legacy/migrations/0001_initial.py":         djm(),
		"legacy/migrations/0002_a.py":               djm(`("legacy", "0001_initial")`),
		"legacy/migrations/0001_squashed_0002_a.py": "from django.db import migrations\n\nclass Migration(migrations.Migration):\n    replaces = [('legacy', '0001_initial'), ('legacy', '0002_a')]\n    dependencies = []\n",
	})
	cases = append(cases,
		scenario{
			name: "a branch that changed no migration is not blamed",
			base: with(alembicBase, map[string]string{"db/versions/x.py": rev("x", "r0"), "db/versions/y.py": rev("y", "r0"), "db/versions/m.py": "revision = 'm'\ndown_revision = ('x', 'y')\n"}),
			branches: []map[string]string{
				with(alembicBase, map[string]string{"db/versions/x.py": rev("x", "r0"), "db/versions/y.py": rev("y", "r0"), "db/versions/m.py": "revision = 'm'\ndown_revision = ('x', 'y')\n", "app/models.py": "x = 3\n"}),
				with(alembicBase, map[string]string{"db/versions/x.py": rev("x", "r0"), "db/versions/y.py": rev("y", "r0"), "db/versions/m.py": "revision = 'm'\ndown_revision = ('x', 'y')\n", "db/versions/n.py": rev("n", "r0")}),
			},
			status:   model.StatusFailed,
			codes:    []string{"migration_multiple_heads"},
			contains: []string{"Branch 2 (b2) merged alone onto the base already has this"},
			fix:      `In db/versions/n.py, set down_revision = "m"`,
		},
		scenario{
			name:     "a branch's own split plus another branch is not blamed on one branch",
			base:     alembicBase,
			branches: []map[string]string{with(alembicBase, map[string]string{"db/versions/a1.py": rev("a1", "r0"), "db/versions/a2.py": rev("a2", "r0")}), with(alembicBase, map[string]string{"db/versions/b1.py": rev("b1", "r0")})},
			status:   model.StatusFailed,
			codes:    []string{"migration_multiple_heads"},
			contains: []string{"Part of it already appears with branch 1 (b1) merged alone onto the base; combining adds the rest"},
		},
		scenario{
			name:     "a new labeled Alembic base is a named branch, not a conflict",
			base:     labeled,
			branches: []map[string]string{with(labeled, map[string]string{"db/versions/n0.py": "revision = 'n0'\ndown_revision = None\nbranch_labels = ('networking',)\n"})},
			status:   model.StatusPassed,
		},
		scenario{
			name: "two unlabeled heads inside a named branch still conflict",
			base: with(labeled, map[string]string{"db/versions/n0.py": "revision = 'n0'\ndown_revision = None\nbranch_labels = ['networking']\n"}),
			branches: []map[string]string{
				with(labeled, map[string]string{"db/versions/n0.py": "revision = 'n0'\ndown_revision = None\nbranch_labels = ['networking']\n", "db/versions/n1.py": rev("n1", "n0")}),
				with(labeled, map[string]string{"db/versions/n0.py": "revision = 'n0'\ndown_revision = None\nbranch_labels = ['networking']\n", "db/versions/n2.py": rev("n2", "n0")}),
			},
			status: model.StatusFailed,
			codes:  []string{"migration_multiple_heads"},
		},
		scenario{
			name:     "computed branch_labels is unknown",
			base:     alembicBase,
			branches: []map[string]string{with(alembicBase, map[string]string{"db/versions/a1.py": "L = 'x'\nrevision = 'a1'\ndown_revision = 'r0'\nbranch_labels = (L,)\n"})},
			status:   model.StatusUnknown,
		},
		scenario{
			name:     "conditional reassignment of revision is unknown",
			base:     alembicBase,
			branches: []map[string]string{with(alembicBase, map[string]string{"db/versions/a1.py": "import os\nrevision = 'a1'\ndown_revision = 'r0'\nif os.environ.get('X'):\n    down_revision = 'zz'\n"})},
			status:   model.StatusUnknown,
		},
		scenario{
			name:     "django dependencies extended later are unknown",
			base:     shop,
			branches: []map[string]string{with(shop, map[string]string{"shop/migrations/0002_a.py": "from django.db import migrations\n\nclass Migration(migrations.Migration):\n    dependencies = [('shop', '0001_initial')]\n    dependencies += [('shop', '0001_other')]\n"})},
			status:   model.StatusUnknown,
		},
		scenario{
			name:     "django dependencies changed in __init__ are unknown",
			base:     shop,
			branches: []map[string]string{with(shop, map[string]string{"shop/migrations/0002_a.py": "from django.db import migrations\n\nclass Migration(migrations.Migration):\n    dependencies = [('shop', '0001_initial')]\n\n    def __init__(self, *a):\n        super().__init__(*a)\n        self.dependencies.append(('shop', 'x'))\n"})},
			status:   model.StatusUnknown,
		},
		scenario{
			name:     "an unreadable history no branch changed does not block",
			base:     legacy,
			branches: []map[string]string{with(legacy, map[string]string{"shop/migrations/0002_a.py": djm(`("shop", "0001_initial")`)})},
			status:   model.StatusPassed,
		},
		scenario{
			name:     "a migrations directory without Django Migration classes is not judged",
			base:     map[string]string{"migrations/001_initial.py": "def migrate(migrator, database, fake=False, **kwargs):\n    pass\n"},
			branches: []map[string]string{{"migrations/001_initial.py": "def migrate(migrator, database, fake=False, **kwargs):\n    pass\n", "migrations/002_add.py": "def migrate(migrator, database, fake=False, **kwargs):\n    pass\n"}},
			status:   "",
		},
		scenario{
			name: "flyway vendor folders are separate histories",
			base: map[string]string{"src/main/resources/db/migration/mysql/V1__init.sql": "a", "src/main/resources/db/migration/postgresql/V1__init.sql": "b"},
			branches: []map[string]string{{
				"src/main/resources/db/migration/mysql/V1__init.sql": "a", "src/main/resources/db/migration/postgresql/V1__init.sql": "b",
				"src/main/resources/db/migration/mysql/V2__c.sql": "c", "src/main/resources/db/migration/postgresql/V2__c.sql": "d",
			}},
			status: model.StatusPassed,
		},
		scenario{
			name:     "renaming one file of a base duplicate is not blamed on the branch",
			base:     with(flyway, map[string]string{"src/main/resources/db/migration/V2__a.sql": "a", "src/main/resources/db/migration/V2__b.sql": "b", "src/main/resources/db/migration/V2__c.sql": "c"}),
			branches: []map[string]string{with(flyway, map[string]string{"src/main/resources/db/migration/V2__a.sql": "a", "src/main/resources/db/migration/V2__b_renamed.sql": "b", "src/main/resources/db/migration/V2__c.sql": "c"})},
			combined: with(flyway, map[string]string{"src/main/resources/db/migration/V2__a.sql": "a", "src/main/resources/db/migration/V2__b_renamed.sql": "b", "src/main/resources/db/migration/V2__c.sql": "c"}),
			status:   model.StatusPassed,
			codes:    []string{"migration_duplicate_version_on_base"},
		},
		scenario{
			name:     "removing one head of a base split is not blamed on the branch",
			base:     with(alembicBase, map[string]string{"db/versions/a.py": rev("a", "r0"), "db/versions/b.py": rev("b", "r0"), "db/versions/c.py": rev("c", "r0")}),
			branches: []map[string]string{with(alembicBase, map[string]string{"db/versions/a.py": rev("a", "r0"), "db/versions/b.py": rev("b", "r0")})},
			combined: with(alembicBase, map[string]string{"db/versions/a.py": rev("a", "r0"), "db/versions/b.py": rev("b", "r0")}),
			status:   model.StatusPassed,
			codes:    []string{"migration_multiple_heads_on_base"},
		},
		scenario{
			name:     "a new head on a base split says the base already split",
			base:     with(alembicBase, map[string]string{"db/versions/a.py": rev("a", "r0"), "db/versions/b.py": rev("b", "r0")}),
			branches: []map[string]string{with(alembicBase, map[string]string{"db/versions/a.py": rev("a", "r0"), "db/versions/b.py": rev("b", "r0"), "db/versions/d.py": rev("d", "r0")})},
			status:   model.StatusFailed,
			codes:    []string{"migration_multiple_heads"},
			contains: []string{"The base already has part of it"},
		},
		scenario{
			name:     "flyway version repeated in a nested directory is unknown",
			base:     flyway,
			branches: []map[string]string{with(flyway, map[string]string{"src/main/resources/db/migration/V4__a.sql": "a"}), with(flyway, map[string]string{"src/main/resources/db/migration/sub/V4__b.sql": "b"})},
			status:   model.StatusUnknown,
			contains: []string{"version 4"},
		},
		scenario{
			name: "a branch label used twice conflicts",
			base: alembicBase,
			branches: []map[string]string{
				with(alembicBase, map[string]string{"db/versions/x.py": "revision = 'x'\ndown_revision = 'r0'\nbranch_labels = ('reporting',)\n"}),
				with(alembicBase, map[string]string{"db/versions/y.py": "revision = 'y'\ndown_revision = 'r0'\nbranch_labels = 'reporting'\n"}),
			},
			status: model.StatusFailed,
			codes:  []string{"migration_duplicate_branch_label"},
		},
		scenario{
			name:     "django dependencies appended in the class body are unknown",
			base:     shop,
			branches: []map[string]string{with(shop, map[string]string{"shop/migrations/0002_a.py": "from django.db import migrations\n\nclass Migration(migrations.Migration):\n    dependencies = [('shop', '0001_initial')]\n    dependencies.append(('shop', 'x'))\n"})},
			status:   model.StatusUnknown,
		},
		scenario{
			name:     "django dependencies replaced at module level are unknown",
			base:     shop,
			branches: []map[string]string{with(shop, map[string]string{"shop/migrations/0002_a.py": "from django.db import migrations\n\nclass Migration(migrations.Migration):\n    dependencies = [('shop', '0001_initial')]\n\nMigration.dependencies = [('shop', 'x')]\n"})},
			status:   model.StatusUnknown,
		},
		scenario{
			name:     "swapping base duplicates for new files is a new conflict",
			base:     with(flyway, map[string]string{"src/main/resources/db/migration/V2__a.sql": "a", "src/main/resources/db/migration/V2__b.sql": "b"}),
			branches: []map[string]string{with(flyway, map[string]string{"src/main/resources/db/migration/V2__b.sql": "b", "src/main/resources/db/migration/V2__c.sql": "c"}), with(flyway, map[string]string{"src/main/resources/db/migration/V2__a.sql": "a", "src/main/resources/db/migration/V2__d.sql": "d"})},
			combined: with(flyway, map[string]string{"src/main/resources/db/migration/V2__c.sql": "c", "src/main/resources/db/migration/V2__d.sql": "d"}),
			status:   model.StatusFailed,
			codes:    []string{"migration_duplicate_version"},
		},
		scenario{
			name:     "completing an up/down pair of a base duplicate is not blamed",
			base:     map[string]string{"migrations/0002_a.up.sql": "a", "migrations/0002_a.down.sql": "b", "migrations/0002_b.up.sql": "c"},
			branches: []map[string]string{{"migrations/0002_a.up.sql": "a", "migrations/0002_a.down.sql": "b", "migrations/0002_b.up.sql": "c", "migrations/0002_b.down.sql": "d"}},
			status:   model.StatusPassed,
			codes:    []string{"migration_duplicate_version_on_base"},
		},
		scenario{
			name:     "a model field named dependencies in RunPython is not a reassignment",
			base:     shop,
			branches: []map[string]string{with(shop, map[string]string{"shop/migrations/0002_a.py": "from django.db import migrations\n\n\ndef forwards(apps, schema_editor):\n    for t in apps.get_model('shop', 'Task').objects.all():\n        t.dependencies.set([])\n\n\nclass Migration(migrations.Migration):\n    dependencies = [('shop', '0001_initial')]\n    operations = [migrations.RunPython(forwards)]\n"})},
			status:   model.StatusPassed,
		},
		scenario{
			name:     "out-of-order warning is one per version and ignores renames",
			base:     map[string]string{"migrations/000001_init.up.sql": "a", "migrations/000001_init.down.sql": "b", "migrations/000005_x.up.sql": "c", "migrations/000005_x.down.sql": "d"},
			branches: []map[string]string{{"migrations/000001_initial.up.sql": "a", "migrations/000001_initial.down.sql": "b", "migrations/000005_x.up.sql": "c", "migrations/000005_x.down.sql": "d", "migrations/000003_late.up.sql": "e", "migrations/000003_late.down.sql": "f"}},
			combined: map[string]string{"migrations/000001_initial.up.sql": "a", "migrations/000001_initial.down.sql": "b", "migrations/000005_x.up.sql": "c", "migrations/000005_x.down.sql": "d", "migrations/000003_late.up.sql": "e", "migrations/000003_late.down.sql": "f"},
			status:   model.StatusPassed,
			codes:    []string{"migration_out_of_order"},
		},
	)
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res := run(t, c)
			if c.status == "" {
				if res.Applicable {
					t.Fatalf("expected no check, got %+v", res)
				}
				return
			}
			if res.Status != c.status {
				t.Fatalf("status %s, want %s: %s %+v", res.Status, c.status, res.Explanation, res.Findings)
			}
			if (res.Status == model.StatusUnknown) != (res.Evidence == model.Unknown) {
				t.Fatalf("evidence %s with status %s", res.Evidence, res.Status)
			}
			codes := []string{}
			text := res.Explanation
			for _, f := range res.Findings {
				codes = append(codes, f.Code)
				text += "\n" + f.Explanation
				if (f.Severity == model.SeverityError) != (res.Status == model.StatusFailed && !strings.HasSuffix(f.Code, "_on_base") && f.Code != "migration_out_of_order") {
					t.Fatalf("finding %s severity %s with status %s", f.Code, f.Severity, res.Status)
				}
				if len(f.Locations) == 0 {
					t.Fatalf("finding %s has no location", f.Code)
				}
			}
			for _, d := range res.Diagnostics {
				text += "\n" + d.Message
			}
			if strings.Join(codes, ",") != strings.Join(c.codes, ",") {
				t.Fatalf("codes %v, want %v\n%s", codes, c.codes, text)
			}
			for _, want := range c.contains {
				if !strings.Contains(text, want) {
					t.Fatalf("missing %q in\n%s", want, text)
				}
			}
			if c.fix != "" && (len(res.Findings) == 0 || !strings.HasPrefix(res.Findings[0].Remediation, c.fix)) {
				t.Fatalf("fix %q, want prefix %q", res.Findings[0].Remediation, c.fix)
			}
		})
	}
}

func TestApplicability(t *testing.T) {
	if plausible([]string{"app/models.py", "db/env.py", "README.md"}) {
		t.Fatal("paths outside migration names must not list trees")
	}
	for _, p := range []string{"db/versions/a1.py", "shop/migrations/0002_a.py", "x/V2__y.sql", "m/2_x.up.sql"} {
		if !plausible([]string{p}) {
			t.Fatalf("%s should be a plausible migration path", p)
		}
	}
	for _, c := range []scenario{
		{name: "change outside the versions directory", base: alembicBase, branches: []map[string]string{with(alembicBase, map[string]string{"db/env.py": "print(2)\n"})}},
		{name: "flyway outside db/migration", base: map[string]string{"sql/V1__x.sql": "a"}, branches: []map[string]string{{"sql/V1__x.sql": "a", "sql/V2__x.sql": "b"}}},
	} {
		if res := run(t, c); res.Applicable {
			t.Fatalf("%s: unexpected check %+v", c.name, res)
		}
	}
	removed := run(t, scenario{base: alembicBase, branches: []map[string]string{with(alembicBase, map[string]string{"db/versions/a1.py": rev("a1", "r0"), "db/versions/r0_init.py": ""})}, combined: with(alembicBase, map[string]string{"db/versions/a1.py": rev("a1", "r0"), "db/versions/r0_init.py": ""})})
	if !removed.Applicable || removed.Status != model.StatusUnknown {
		t.Fatalf("a deleted parent must be judged and unknown: %+v", removed)
	}
}

func TestDiagnosticsAreNotRepeatedPerTree(t *testing.T) {
	bad := with(alembicBase, map[string]string{"db/versions/bad.py": "revision = X\ndown_revision = 'r0'\n"})
	res := run(t, scenario{base: alembicBase, branches: []map[string]string{bad, bad}})
	if res.Status != model.StatusUnknown || len(res.Diagnostics) != 1 || !strings.Contains(res.Diagnostics[0].Message, "branch 1 (b1) merged alone onto the base, branch 2 (b2) merged alone onto the base, the combined tree") {
		t.Fatalf("diagnostics %+v", res.Diagnostics)
	}
}

func TestUnavailableSingleBranchMergeOnlyWeakensAttribution(t *testing.T) {
	s := store{}
	trees := []Tree{
		s.tree("the base", alembicBase),
		{Label: "branch 1 (b1) merged alone onto the base", Unavailable: "it conflicts with the base"},
		s.tree("branch 2 (b2) merged alone onto the base", with(alembicBase, map[string]string{"db/versions/b1.py": rev("b1", "r0")})),
		s.tree("the combined tree", with(alembicBase, map[string]string{"db/versions/a1.py": rev("a1", "r0"), "db/versions/b1.py": rev("b1", "r0")})),
	}
	res, err := Evaluate(context.Background(), trees, []string{"db/versions/a1.py", "db/versions/b1.py"}, s.read)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != model.StatusFailed || !strings.Contains(res.Findings[0].Explanation, "could not be merged or read alone") {
		t.Fatalf("%+v", res)
	}
}

func TestEvaluateIsDeterministic(t *testing.T) {
	c := scenario{
		base: alembicBase,
		branches: []map[string]string{
			with(alembicBase, map[string]string{"db/versions/a1.py": rev("a1", "r0")}),
			with(alembicBase, map[string]string{"db/versions/b1.py": rev("b1", "r0")}),
			with(alembicBase, map[string]string{"db/versions/c1.py": rev("c1", "r0")}),
		},
	}
	first := fmt.Sprintf("%+v", run(t, c))
	for range 5 {
		if again := fmt.Sprintf("%+v", run(t, c)); again != first {
			t.Fatalf("nondeterministic result:\n%s\n%s", first, again)
		}
	}
}
