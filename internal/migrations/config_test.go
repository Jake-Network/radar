package migrations

import (
	"context"
	"github.com/Jake-Network/radar/internal/model"
	"testing"
)

func TestConfiguredAlembicLocation(t *testing.T) {
	config := `{"version":1,"locations":[{"path":"history","tool":"alembic"}]}`
	base := map[string]string{".radar/migrations.json": config, "history/r0.py": rev("r0", "")}
	a := with(base, map[string]string{"history/a.py": rev("a", "r0")})
	b := with(base, map[string]string{"history/b.py": rev("b", "r0")})
	combined := with(a, map[string]string{"history/b.py": rev("b", "r0")})
	s := store{}
	trees := []Tree{s.tree("base", base), s.tree("a", a), s.tree("b", b), s.tree("combined", combined)}
	r, e := Evaluate(context.Background(), trees, diff(base, combined), s.read)
	if e != nil || r.Status != model.StatusFailed || len(r.Findings) != 1 {
		t.Fatalf("%+v %v", r, e)
	}
}
func TestMigrationConfigNegative(t *testing.T) {
	for _, config := range []string{`{"version":1,"locations":[{"tool":"unknown","path":"m"}]}`, `{"version":1,"locations":[{"tool":"alembic","path":"../m"}]}`, `{`} {
		s := store{}
		trees := []Tree{s.tree("base", map[string]string{}), s.tree("combined", map[string]string{".radar/migrations.json": config})}
		r, e := Evaluate(context.Background(), trees, []string{".radar/migrations.json"}, s.read)
		if e != nil || r.Status != model.StatusUnknown {
			t.Fatalf("%+v %v", r, e)
		}
	}
}
func TestPrismaOutOfOrderAndDieselDuplicate(t *testing.T) {
	for _, c := range []struct {
		tool, file string
		want       model.Status
	}{{"prisma", "migration.sql", model.StatusPassed}, {"diesel", "up.sql", model.StatusFailed}} {
		t.Run(c.tool, func(t *testing.T) {
			base := map[string]string{"migrations/20260101_base/" + c.file: "SELECT 1;"}
			after := with(base, map[string]string{"migrations/20251231_new/" + c.file: "SELECT 2;"})
			if c.tool == "diesel" {
				after = with(base, map[string]string{"migrations/20260101_new/up.sql": "SELECT 2;", "migrations/20260101_new/down.sql": "SELECT 3;"})
			}
			s := store{}
			r, e := Evaluate(context.Background(), []Tree{s.tree("base", base), s.tree("combined", after)}, diff(base, after), s.read)
			if e != nil || r.Status != c.want || len(r.Findings) != 1 {
				t.Fatalf("%+v %v", r, e)
			}
		})
	}
}
func TestLiquibaseSameFileDuplicateAndCrossFileIdentity(t *testing.T) {
	config := `{"version":1,"locations":[{"path":"changes","tool":"liquibase"}]}`
	base := map[string]string{".radar/migrations.json": config, "changes/a.xml": `<databaseChangeLog><changeSet id="1" author="a"/></databaseChangeLog>`}
	after := with(base, map[string]string{"changes/a.xml": `<databaseChangeLog><changeSet id="1" author="a"/><changeSet id="1" author="a"/></databaseChangeLog>`})
	s := store{}
	r, e := Evaluate(context.Background(), []Tree{s.tree("base", base), s.tree("combined", after)}, diff(base, after), s.read)
	if e != nil || r.Status != model.StatusFailed {
		t.Fatalf("%+v %v", r, e)
	}
	after = with(base, map[string]string{"changes/b.xml": base["changes/a.xml"]})
	r, e = Evaluate(context.Background(), []Tree{s.tree("base", base), s.tree("combined", after)}, diff(base, after), s.read)
	if e != nil || r.Status != model.StatusUnknown {
		t.Fatalf("cross-file scope: %+v %v", r, e)
	}
}
func TestSQLXDuplicateVersions(t *testing.T) {
	config := `{"version":1,"locations":[{"path":"db","tool":"sqlx"}]}`
	base := map[string]string{".radar/migrations.json": config, "db/1_base.sql": "select 1"}
	after := with(base, map[string]string{"db/1_added.sql": "select 2"})
	s := store{}
	r, e := Evaluate(context.Background(), []Tree{s.tree("base", base), s.tree("combined", after)}, diff(base, after), s.read)
	if e != nil || r.Status != model.StatusFailed {
		t.Fatalf("%+v %v", r, e)
	}
}
func TestBaseDuplicateCannotBeReusedForExtraIdenticalMigration(t *testing.T) {
	base := map[string]string{"db/migration/V1__a.sql": "SELECT 1;", "db/migration/V1__b.sql": "SELECT 2;"}
	after := with(base, map[string]string{"db/migration/V1__c.sql": "SELECT 1;"})
	s := store{}
	r, e := Evaluate(context.Background(), []Tree{s.tree("base", base), s.tree("combined", after)}, diff(base, after), s.read)
	if e != nil || r.Status != model.StatusFailed {
		t.Fatalf("extra identical migration: %+v %v", r, e)
	}
}
func TestConfiguredToolChangeUnknown(t *testing.T) {
	base := map[string]string{".radar/migrations.json": `{"version":1,"locations":[{"path":"db","tool":"sqlx"}]}`, "db/1_a.sql": "SELECT 1;"}
	after := with(base, map[string]string{".radar/migrations.json": `{"version":1,"locations":[{"path":"db","tool":"flyway"}]}`})
	s := store{}
	r, e := Evaluate(context.Background(), []Tree{s.tree("base", base), s.tree("combined", after)}, diff(base, after), s.read)
	if e != nil || r.Status != model.StatusUnknown {
		t.Fatalf("%+v %v", r, e)
	}
}
func TestConfigTrailingDataAndRootLocation(t *testing.T) {
	s := store{}
	config := `{"version":1,"locations":[{"path":".","tool":"sqlx"}]}`
	base := map[string]string{".radar/migrations.json": config, "1_a.sql": "SELECT 1;"}
	after := with(base, map[string]string{"1_b.sql": "SELECT 2;"})
	r, e := Evaluate(context.Background(), []Tree{s.tree("base", base), s.tree("combined", after)}, diff(base, after), s.read)
	if e != nil || r.Status != model.StatusFailed {
		t.Fatalf("root %+v %v", r, e)
	}
	after = with(base, map[string]string{".radar/migrations.json": config + ` {}`})
	r, e = Evaluate(context.Background(), []Tree{s.tree("base", base), s.tree("combined", after)}, diff(base, after), s.read)
	if e != nil || r.Status != model.StatusUnknown {
		t.Fatalf("trailing %+v %v", r, e)
	}
}
func TestLiquibaseIdentityDoesNotCollideOnCommas(t *testing.T) {
	config := `{"version":1,"locations":[{"path":"changes","tool":"liquibase"}]}`
	base := map[string]string{".radar/migrations.json": config, "changes/a.xml": `<databaseChangeLog><changeSet id="a,b" author="c"/></databaseChangeLog>`}
	after := with(base, map[string]string{"changes/a.xml": `<databaseChangeLog><changeSet id="a,b" author="c"/><changeSet id="a" author="b,c"/></databaseChangeLog>`})
	s := store{}
	r, e := Evaluate(context.Background(), []Tree{s.tree("base", base), s.tree("combined", after)}, diff(base, after), s.read)
	if e != nil || r.Status != model.StatusPassed {
		t.Fatalf("%+v %v", r, e)
	}
}
func TestPrismaSameTimestampDifferentNamesNotConflict(t *testing.T) {
	base := map[string]string{"prisma/migrations/20260101_a/migration.sql": "SELECT 1;"}
	after := with(base, map[string]string{"prisma/migrations/20260101_b/migration.sql": "SELECT 2;"})
	s := store{}
	r, e := Evaluate(context.Background(), []Tree{s.tree("base", base), s.tree("combined", after)}, diff(base, after), s.read)
	if e != nil || r.Status != model.StatusPassed || len(r.Findings) != 0 {
		t.Fatalf("%+v %v", r, e)
	}
}
