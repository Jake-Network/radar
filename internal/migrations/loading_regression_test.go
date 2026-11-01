package migrations

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Jake-Network/radar/internal/model"
)

func TestAnalyzeExistingAlembicCustomVersionLocations(t *testing.T) {
	for _, tc := range []struct {
		name, secondParent string
		want               model.Status
	}{
		{"linear", "a", model.StatusPassed},
		{"fork", "r", model.StatusFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			gitIn(t, root, "init", "-q")
			put := func(name, content string) {
				t.Helper()
				full := filepath.Join(root, filepath.FromSlash(name))
				if err := os.MkdirAll(filepath.Dir(full), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(full, []byte(content), 0600); err != nil {
					t.Fatal(err)
				}
			}
			put("alembic.ini", "[alembic]\nscript_location = alembic\nversion_locations = %(here)s/alembic/core;%(here)s/alembic/ext\npath_separator = ;\n")
			put("alembic/env.py", "# configuration is data, never imported\n")
			put("alembic/core/r.py", rev("r", ""))
			gitIn(t, root, "add", ".")
			gitIn(t, root, "commit", "-qm", "base")
			base := gitIn(t, root, "rev-parse", "HEAD")
			put("alembic/core/a.py", rev("a", "r"))
			put("alembic/ext/b.py", rev("b", tc.secondParent))
			gitIn(t, root, "add", ".")
			gitIn(t, root, "commit", "-qm", "revisions")
			head := gitIn(t, root, "rev-parse", "HEAD")
			// The prefilter must use committed declarations even if the
			// corresponding configuration is missing from the worktree.
			if err := os.Remove(filepath.Join(root, "alembic.ini")); err != nil {
				t.Fatal(err)
			}
			r, err := Analyze(context.Background(), root, base, []string{head}, head, []string{"alembic/core/a.py", "alembic/ext/b.py"})
			if err != nil || !r.Applicable || r.Status != tc.want {
				t.Fatalf("Analyze = %+v, error = %v", r, err)
			}
		})
	}
}

func TestTypeORMLoadsOnlyLiteralExportedClasses(t *testing.T) {
	config := `{"version":1,"locations":[{"path":"db","tool":"typeorm"}]}`
	class := func(name string) string {
		return "class " + name + " { async up() {} async down() {} }"
	}
	base := map[string]string{".radar/migrations.json": config, "db/a.ts": "export " + class("Create1700000000000")}
	for _, tc := range []struct {
		name, source string
		want         model.Status
	}{
		{"private helper", class("Create1700000000000") + "\nexport " + class("Other1700000000001"), model.StatusPassed},
		{"nested helper", "function helper() { " + class("Create1700000000000") + " }\nexport " + class("Other1700000000001"), model.StatusPassed},
		{"real exported duplicate", "export " + class("Create1700000000000"), model.StatusFailed},
		{"default exported duplicate", "export default " + class("Create1700000000000"), model.StatusFailed},
		{"export alias needs resolution", class("Create1700000000000") + "\nexport { Create1700000000000 };", model.StatusUnknown},
		{"re-export needs resolution", "export { Create1700000000000 } from './a';", model.StatusUnknown},
		{"computed exports need resolution", "export const migrations = makeMigrations();", model.StatusUnknown},
		{"primitive exports are ignored", "export const version = 1, title = 'migration', enabled = true, absent = null, disabled = false;\nexport const previous = -1;\nexport default 'metadata';\nexport " + class("Other1700000000001"), model.StatusPassed},
		{"computed metadata remains unknown", "export const version = getVersion();\nexport " + class("Other1700000000001"), model.StatusUnknown},
		{"mutable metadata remains unknown", "export let version = 1;\nexport " + class("Other1700000000001"), model.StatusUnknown},
		{"array exports need resolution", "export const migrations = [];\nexport " + class("Other1700000000001"), model.StatusUnknown},
		{"object exports need resolution", "export const metadata = {};\nexport " + class("Other1700000000001"), model.StatusUnknown},
		{"CommonJS replacement remains unknown", class("Create1700000000000") + "\nexport " + class("Other1700000000001") + "\nmodule.exports = { Create1700000000000 };", model.StatusUnknown},
		{"CommonJS member remains unknown", class("Create1700000000000") + "\nexport " + class("Other1700000000001") + "\nexports.Migration = Create1700000000000;", model.StatusUnknown},
		{"CommonJS computed member remains unknown", class("Create1700000000000") + "\nexport " + class("Other1700000000001") + "\nexports['Migration'] = Create1700000000000;", model.StatusUnknown},
		{"CommonJS assign remains unknown", class("Create1700000000000") + "\nexport " + class("Other1700000000001") + "\nObject.assign(exports, { Create1700000000000 });", model.StatusUnknown},
		{"CommonJS property remains unknown", class("Create1700000000000") + "\nexport " + class("Other1700000000001") + "\nObject.defineProperty(exports, 'Migration', { value: Create1700000000000 });", model.StatusUnknown},
		{"exported types do not load helpers", "export interface MigrationOptions {}\n" + class("Create1700000000000") + "\nexport " + class("Other1700000000001"), model.StatusPassed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			after := with(base, map[string]string{"db/b.ts": tc.source})
			s := store{}
			r, err := Evaluate(context.Background(), []Tree{s.tree("base", base), s.tree("combined", after)}, []string{"db/b.ts"}, s.read)
			if err != nil || r.Status != tc.want {
				t.Fatalf("Evaluate = %+v, error = %v", r, err)
			}
		})
	}
}
