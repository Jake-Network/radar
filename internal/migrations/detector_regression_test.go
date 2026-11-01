package migrations

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Jake-Network/radar/internal/model"
)

func TestSQLXPathsSurviveAnalyzePrefilter(t *testing.T) {
	if !plausible([]string{"migrations/1_initial.sql"}) {
		t.Fatal("Analyze would skip a recognized SQLX migration")
	}
	if plausible([]string{"src/query.sql"}) {
		t.Fatal("ordinary query was selected")
	}
}

func TestHistoryToolNames(t *testing.T) {
	for tool, name := range map[string]string{toolPrisma: "Prisma", toolLiquibase: "Liquibase", toolSQLX: "SQLx", toolDiesel: "Diesel", "goose": "Goose"} {
		if got := (location{tool, "db"}).String(); got != name+" migrations db" {
			t.Errorf("%s: %s", tool, got)
		}
	}
}

func TestGooseDuplicateVersions(t *testing.T) {
	config := `{"version":1,"locations":[{"path":"db","tool":"goose"}]}`
	base := map[string]string{".radar/migrations.json": config, "db/1_a.sql": "-- +goose Up\nSELECT 1;"}
	after := with(base, map[string]string{"db/1_b.sql": "-- +goose Up\nSELECT 2;"})
	s := store{}
	r, e := Evaluate(context.Background(), []Tree{s.tree("base", base), s.tree("combined", after)}, diff(base, after), s.read)
	if e != nil || r.Status != model.StatusFailed {
		t.Fatalf("%+v %v", r, e)
	}
}

func TestAlembicDependencyValidation(t *testing.T) {
	for _, tc := range []struct {
		name, dep string
		want      model.Status
	}{
		{"valid independent dependency", "a", model.StatusPassed},
		{"missing dependency", "missing", model.StatusUnknown},
		{"self dependency", "b", model.StatusUnknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := map[string]string{"alembic/env.py": "# env", "alembic/versions/a.py": rev("a", "")}
			after := with(base, map[string]string{"alembic/versions/b.py": rev("b", "a") + "\ndepends_on = '" + tc.dep + "'\n"})
			s := store{}
			r, e := Evaluate(context.Background(), []Tree{s.tree("base", base), s.tree("combined", after)}, diff(base, after), s.read)
			if e != nil || r.Status != tc.want {
				t.Fatalf("%+v %v", r, e)
			}
		})
	}
}

func TestAnalyzeSQLXZeroConfig(t *testing.T) {
	root := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1", "GIT_AUTHOR_NAME=Radar", "GIT_AUTHOR_EMAIL=radar@example.test", "GIT_COMMITTER_NAME=Radar", "GIT_COMMITTER_EMAIL=radar@example.test")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %s %v", args, out, err)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q")
	if err := os.MkdirAll(filepath.Join(root, "migrations"), 0700); err != nil {
		t.Fatal(err)
	}
	put := func(name string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, "migrations", name), []byte("SELECT 1;"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	put("1_a.sql")
	git("add", ".")
	git("commit", "-qm", "base")
	base := git("rev-parse", "HEAD")
	put("1_b.sql")
	git("add", ".")
	git("commit", "-qm", "duplicate")
	head := git("rev-parse", "HEAD")
	r, err := Analyze(context.Background(), root, base, []string{head}, head, []string{"migrations/1_b.sql"})
	if err != nil || r.Status != model.StatusFailed {
		t.Fatalf("%+v %v", r, err)
	}
}

func TestAlembicDependencyDoesNotMergeHeads(t *testing.T) {
	base := map[string]string{"alembic/env.py": "# env", "alembic/versions/r.py": rev("r", "")}
	after := with(base, map[string]string{"alembic/versions/a.py": rev("a", "r"), "alembic/versions/b.py": rev("b", "r") + "\ndepends_on = 'a'\n"})
	s := store{}
	r, e := Evaluate(context.Background(), []Tree{s.tree("base", base), s.tree("combined", after)}, diff(base, after), s.read)
	if e != nil || r.Status != model.StatusFailed {
		t.Fatalf("dependency must not merge branches: %+v %v", r, e)
	}
}

func TestGooseGoRegistrationIsUnknown(t *testing.T) {
	config := `{"version":1,"locations":[{"path":"db","tool":"goose"}]}`
	base := map[string]string{".radar/migrations.json": config, "db/1_a.sql": "SELECT 1;"}
	after := with(base, map[string]string{"db/2_b.go": "package migrations"})
	s := store{}
	r, e := Evaluate(context.Background(), []Tree{s.tree("base", base), s.tree("combined", after)}, diff(base, after), s.read)
	if e != nil || r.Status != model.StatusUnknown {
		t.Fatalf("%+v %v", r, e)
	}
}

func TestDrizzleJournalIntegrity(t *testing.T) {
	base := map[string]string{"drizzle/meta/_journal.json": `{"version":"7","entries":[{"idx":0,"tag":"0000_base"}]}`, "drizzle/0000_base.sql": "SELECT 1;"}
	for _, tc := range []struct {
		name, journal string
		want          model.Status
	}{
		{"valid", `{"version":"7","entries":[{"idx":0,"tag":"0000_base"}]}`, model.StatusPassed},
		{"duplicate", `{"version":"7","entries":[{"idx":0,"tag":"0000_base"},{"idx":0,"tag":"0000_base"}]}`, model.StatusFailed},
		{"missing sql", `{"version":"7","entries":[{"idx":0,"tag":"missing"}]}`, model.StatusFailed},
		{"computed", `{"entries":[{"tag":"../escape"}]}`, model.StatusUnknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			after := with(base, map[string]string{"drizzle/meta/_journal.json": tc.journal})
			s := store{}
			r, e := Evaluate(context.Background(), []Tree{s.tree("base", base), s.tree("combined", after)}, []string{"drizzle/meta/_journal.json"}, s.read)
			if e != nil || r.Status != tc.want {
				t.Fatalf("%+v %v", r, e)
			}
		})
	}
}

func TestAtlasDirectoryChecksums(t *testing.T) {
	// Atlas hashes names and bytes cumulatively, then names and encoded
	// cumulative hashes for the integrity file's first line.
	content := "SELECT 1;"
	name := "20260101000000_base.sql"
	cumulative := sha256.Sum256([]byte(name + content))
	hash := base64.StdEncoding.EncodeToString(cumulative[:])
	aggregate := sha256.Sum256([]byte(name + hash))
	sum := "h1:" + base64.StdEncoding.EncodeToString(aggregate[:]) + "\n" + name + " h1:" + hash + "\n"
	base := map[string]string{"db/atlas.sum": sum, "db/" + name: content}
	for _, tc := range []struct {
		name  string
		extra map[string]string
		want  model.Status
	}{
		{"valid", map[string]string{}, model.StatusPassed},
		{"modified", map[string]string{"db/" + name: "SELECT 2;"}, model.StatusFailed},
		{"missing", map[string]string{"db/" + name: ""}, model.StatusFailed},
		{"malformed", map[string]string{"db/atlas.sum": "bad"}, model.StatusUnknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			after := with(base, tc.extra)
			s := store{}
			r, e := Evaluate(context.Background(), []Tree{s.tree("base", base), s.tree("combined", after)}, []string{"db/atlas.sum"}, s.read)
			if e != nil || r.Status != tc.want {
				t.Fatalf("%+v %v", r, e)
			}
		})
	}
}

func TestAlembicINICombinesVersionLocations(t *testing.T) {
	base := map[string]string{"alembic.ini": "[alembic]\nscript_location = alembic\nversion_locations = %(here)s/alembic/core;%(here)s/alembic/ext\npath_separator = ;\n", "alembic/env.py": "# env", "alembic/core/r.py": rev("r", "")}
	after := with(base, map[string]string{"alembic/core/a.py": rev("a", "r"), "alembic/ext/b.py": rev("b", "r")})
	s := store{}
	r, e := Evaluate(context.Background(), []Tree{s.tree("base", base), s.tree("combined", after)}, diff(base, after), s.read)
	if e != nil || r.Status != model.StatusFailed {
		t.Fatalf("%+v %v", r, e)
	}
}
func TestAlembicINIUnsafeLocationUnknown(t *testing.T) {
	base := map[string]string{"alembic.ini": "[alembic]\nscript_location = ../outside\n"}
	s := store{}
	r, e := Evaluate(context.Background(), []Tree{s.tree("base", map[string]string{}), s.tree("combined", base)}, []string{"alembic.ini"}, s.read)
	if e != nil || r.Status != model.StatusUnknown {
		t.Fatalf("%+v %v", r, e)
	}
}

func TestTypeORMIdentityAndTimestamp(t *testing.T) {
	config := `{"version":1,"locations":[{"path":"db","tool":"typeorm"}]}`
	migration := func(name string) string {
		return "export class " + name + " { async up(): Promise<void> {} async down(): Promise<void> {} }"
	}
	base := map[string]string{".radar/migrations.json": config, "db/a.ts": migration("Create1700000000000")}
	for _, tc := range []struct {
		name, source string
		want         model.Status
	}{
		{"duplicate identity", migration("Create1700000000000"), model.StatusFailed},
		{"timestamp only", migration("Other1700000000000"), model.StatusPassed},
		{"string is not class", "const text = 'class Create1700000000000 {}';", model.StatusUnknown},
		{"computed name", "export class Create1700000000000 { name = getName(); async up() {} async down() {} }", model.StatusUnknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			after := with(base, map[string]string{"db/b.ts": tc.source})
			s := store{}
			r, e := Evaluate(context.Background(), []Tree{s.tree("base", base), s.tree("combined", after)}, diff(base, after), s.read)
			if e != nil || r.Status != tc.want {
				t.Fatalf("%+v %v", r, e)
			}
		})
	}
}
func TestKnexTimestampOnlyIsWarning(t *testing.T) {
	config := `{"version":1,"locations":[{"path":"db","tool":"knex"}]}`
	base := map[string]string{".radar/migrations.json": config, "db/1700000000000_a.js": "exports.up = async () => {}; exports.down = async () => {};"}
	after := with(base, map[string]string{"db/1700000000000_b.js": "exports.up = async () => {}; exports.down = async () => {};"})
	s := store{}
	r, e := Evaluate(context.Background(), []Tree{s.tree("base", base), s.tree("combined", after)}, diff(base, after), s.read)
	if e != nil || r.Status != model.StatusPassed || len(r.Findings) != 1 || r.Findings[0].Evidence != model.Inferred || r.Findings[0].Severity != model.SeverityWarning {
		t.Fatalf("%+v %v", r, e)
	}
}

func TestDjangoRunBeforeOrder(t *testing.T) {
	base := map[string]string{"shop/migrations/0001_a.py": djm()}
	after := with(base, map[string]string{"shop/migrations/0001_a.py": djm() + "    run_before = [('shop', '0002_b')]\n", "shop/migrations/0002_b.py": djm()})
	s := store{}
	r, e := Evaluate(context.Background(), []Tree{s.tree("base", base), s.tree("combined", after)}, diff(base, after), s.read)
	if e != nil || r.Status != model.StatusPassed {
		t.Fatalf("%+v %v", r, e)
	}
}

func TestDjangoCrossAppRunBeforeCycle(t *testing.T) {
	base := map[string]string{"shop/migrations/0001_a.py": djm(), "billing/migrations/0001_a.py": djm()}
	after := with(base, map[string]string{"shop/migrations/0001_a.py": djm("('billing', '0001_a')") + "    run_before = [('billing', '0001_a')]\n"})
	s := store{}
	r, e := Evaluate(context.Background(), []Tree{s.tree("base", base), s.tree("combined", after)}, diff(base, after), s.read)
	if e != nil || r.Status != model.StatusUnknown {
		t.Fatalf("%+v %v", r, e)
	}
}

func TestNewMigrationIdentityChecksFailOnlyCombination(t *testing.T) {
	for _, tool := range []string{"goose", "typeorm"} {
		t.Run(tool, func(t *testing.T) {
			config := `{"version":1,"locations":[{"path":"db","tool":"` + tool + `"}]}`
			base := map[string]string{".radar/migrations.json": config}
			var a, b map[string]string
			if tool == "goose" {
				a = with(base, map[string]string{"db/1_a.sql": "SELECT 1;"})
				b = with(base, map[string]string{"db/1_b.sql": "SELECT 2;"})
			} else {
				source := "export class Create1700000000000 { async up() {} async down() {} }"
				a = with(base, map[string]string{"db/a.ts": source})
				b = with(base, map[string]string{"db/b.ts": source})
			}
			combined := with(a, b)
			for _, alone := range []map[string]string{a, b} {
				s := store{}
				r, e := Evaluate(context.Background(), []Tree{s.tree("base", base), s.tree("alone", alone)}, diff(base, alone), s.read)
				if e != nil || r.Status != model.StatusPassed {
					t.Fatalf("standalone: %+v %v", r, e)
				}
			}
			s := store{}
			r, e := Evaluate(context.Background(), []Tree{s.tree("base", base), s.tree("a", a), s.tree("b", b), s.tree("combined", combined)}, diff(base, combined), s.read)
			if e != nil || r.Status != model.StatusFailed {
				t.Fatalf("combined: %+v %v", r, e)
			}
		})
	}
}
func TestGooseDirectionalNamesAreDistinctMigrations(t *testing.T) {
	config := `{"version":1,"locations":[{"path":"db","tool":"goose"}]}`
	base := map[string]string{".radar/migrations.json": config, "db/1_a.up.sql": "SELECT 1;"}
	after := with(base, map[string]string{"db/1_a.down.sql": "SELECT 2;"})
	s := store{}
	r, e := Evaluate(context.Background(), []Tree{s.tree("base", base), s.tree("combined", after)}, diff(base, after), s.read)
	if e != nil || r.Status != model.StatusFailed {
		t.Fatalf("Goose does not pair directional suffixes: %+v %v", r, e)
	}
}
