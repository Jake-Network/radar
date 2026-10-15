package indexer

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Jake-Network/radar/internal/model"
)

func TestWorkspaceManifestImports(t *testing.T) {
	cases := []struct {
		name     string
		files    map[string]string
		from, to string
	}{
		{"uv", map[string]string{"pyproject.toml": "[tool.uv.workspace]\nmembers = [\"apps/*\", \"libs/*\"]\n", "apps/app/pyproject.toml": "[project]\nname='app'\n", "libs/lib/pyproject.toml": "[project]\nname='lib'\n", "apps/app/test_app.py": "from lib.api import run\n", "libs/lib/src/lib/api.py": "def run(): pass\n"}, "apps/app/test_app.py", "libs/lib/src/lib/api.py"},
		{"poetry", map[string]string{"libs/lib/pyproject.toml": "[tool.poetry]\npackages = [{include='lib', from='python'}]\n", "apps/app/test_app.py": "from lib.api import run\n", "libs/lib/python/lib/api.py": "def run(): pass\n"}, "apps/app/test_app.py", "libs/lib/python/lib/api.py"},
		{"npm", map[string]string{"package.json": `{"workspaces":["apps/*","libs/*"]}`, "apps/app/package.json": `{"name":"app"}`, "libs/lib/package.json": `{"name":"lib","source":"src/index.ts"}`, "apps/app/test_app.ts": "import {run} from 'lib';\n", "libs/lib/src/index.ts": "export function run() {}\n"}, "apps/app/test_app.ts", "libs/lib/src/index.ts"},
		{"pnpm", map[string]string{"pnpm-workspace.yaml": "packages:\n  - apps/*\n  - libs/*\n", "apps/app/package.json": `{"name":"app"}`, "libs/lib/package.json": `{"name":"lib","source":"src/index.ts"}`, "apps/app/test_app.ts": "import {run} from 'lib';\n", "libs/lib/src/index.ts": "export function run() {}\n"}, "apps/app/test_app.ts", "libs/lib/src/index.ts"},
		{"references", map[string]string{"tsconfig.json": `{"references":[{"path":"apps/app"},{"path":"libs/lib"}]}`, "apps/app/package.json": `{"name":"app"}`, "libs/lib/package.json": `{"name":"lib","source":"src/index.ts"}`, "apps/app/test_app.ts": "import {run} from 'lib';\n", "libs/lib/src/index.ts": "export function run() {}\n"}, "apps/app/test_app.ts", "libs/lib/src/index.ts"},
		{"cargo", map[string]string{"Cargo.toml": "[workspace]\nmembers=['apps/*','libs/*']\n", "apps/app/Cargo.toml": "[package]\nname='app'\n", "libs/lib/Cargo.toml": "[package]\nname='my-lib'\n[lib]\npath='rust/library.rs'\n", "apps/app/src/main.rs": "use my_lib::run;\nfn main() {}\n", "libs/lib/rust/library.rs": "pub fn run() {}\n"}, "apps/app/src/main.rs", "libs/lib/rust/library.rs"},
		{"go", map[string]string{"go.work": "go 1.23\nuse (\n ./apps/app\n ./libs/lib\n)\n", "apps/app/go.mod": "module example.com/app\n", "libs/lib/go.mod": "module example.com/lib\n", "apps/app/main.go": "package app\nimport \"example.com/lib\"\nfunc run() {lib.Run()}\n", "libs/lib/lib.go": "package lib\nfunc Run() {}\n"}, "apps/app/main.go", "libs/lib/lib.go"},
		{"maven", map[string]string{"pom.xml": "<project><modules><module>apps/app</module><module>libs/lib</module></modules></project>", "apps/app/pom.xml": "<project/>", "libs/lib/pom.xml": "<project/>", "apps/app/App.java": "package app; import lib.Library; public class App {}", "libs/lib/Library.java": "package lib; public class Library {}"}, "apps/app/App.java", "libs/lib/Library.java"},
		{"gradle", map[string]string{"settings.gradle.kts": "include(\":apps:app\", \":libs:lib\")\n", "apps/app/App.java": "package app; import lib.Library; public class App {}", "libs/lib/Library.java": "package lib; public class Library {}"}, "apps/app/App.java", "libs/lib/Library.java"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := indexWorkspaceFixture(t, tc.files)
			for _, e := range s.Edges {
				if e.Kind == "DEPENDS_ON" && model.PathFromID(e.From) == tc.from && model.PathFromID(e.To) == tc.to {
					if !strings.HasPrefix(e.Provenance.Method, "workspace_member:") || e.Provenance.Evidence != model.Inferred {
						t.Fatalf("invalid provenance: %+v", e.Provenance)
					}
					return
				}
			}
			t.Fatalf("missing %s -> %s", tc.from, tc.to)
		})
	}
}

func indexWorkspaceFixture(t *testing.T, files map[string]string) model.Snapshot {
	t.Helper()
	root := t.TempDir()
	for p, content := range files {
		full := filepath.Join(root, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	s, err := Index(context.Background(), root, "WORKTREE")
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestWorkspaceRejectsDynamicAndEscapingPaths(t *testing.T) {
	s := indexWorkspaceFixture(t, map[string]string{"settings.gradle": "include(projectNames)\n", "pyproject.toml": "[tool.uv.workspace]\nmembers=['../outside']\n", "app/test_app.py": "import outside\n"})
	if len(s.Diagnostics) != 2 {
		t.Fatalf("expected unknown diagnostics: %+v", s.Diagnostics)
	}
	for _, d := range s.Diagnostics {
		if !strings.Contains(d.Message, "unknown") {
			t.Fatal(d)
		}
	}
	for _, e := range s.Edges {
		if e.Kind == "DEPENDS_ON" {
			t.Fatal("resolved unsupported workspace", e)
		}
	}
}

func TestWorkspaceAmbiguousPythonAndRustStayUnresolved(t *testing.T) {
	s := indexWorkspaceFixture(t, map[string]string{"pyproject.toml": "[tool.uv.workspace]\nmembers=['libs/*']\n", "libs/a/pyproject.toml": "[project]\nname='a'\n", "libs/b/pyproject.toml": "[project]\nname='b'\n", "libs/a/src/common.py": "pass\n", "libs/b/src/common.py": "pass\n", "app/test_app.py": "import common\n", "Cargo.toml": "[workspace]\nmembers=['crates/*']\n", "crates/a/Cargo.toml": "[package]\nname='common'\n", "crates/b/Cargo.toml": "[package]\nname='common'\n", "crates/a/src/lib.rs": "pub fn run() {}\n", "crates/b/src/lib.rs": "pub fn run() {}\n", "app/src/main.rs": "use common::run;\n"})
	for _, e := range s.Edges {
		if e.Kind == "DEPENDS_ON" {
			t.Fatal("ambiguous edge", e)
		}
	}
}

func TestLiteralWorkspaceMemberArrays(t *testing.T) {
	for _, value := range []string{`["apps/*", # "ignored/*"
 "libs/*",]`, `['apps/*', 'libs/*']`} {
		members, ok := literalMembers(value)
		if !ok || len(members) != 2 || members[1] != "libs/*" {
			t.Fatalf("%q: %v %v", value, members, ok)
		}
	}
	for _, value := range []string{`computed("apps/*")`, `["apps/*", fn("libs/*")]`, `["apps/*" "libs/*"]`, `["apps/*"`} {
		if members, ok := literalMembers(value); ok {
			t.Fatalf("accepted %q: %v", value, members)
		}
	}
}

func TestWorkspaceExclusionsDoNotResolvePrivatePackages(t *testing.T) {
	s := indexWorkspaceFixture(t, map[string]string{"pyproject.toml": "[tool.uv.workspace]\nmembers=['libs/*']\nexclude=['libs/private']\n", "libs/private/pyproject.toml": "[project]\nname='private'\n", "libs/private/src/private_pkg.py": "pass\n", "apps/main.py": "import private_pkg\n", "Cargo.toml": "[workspace]\nmembers=['crates/*']\nexclude=['crates/private']\n", "crates/private/Cargo.toml": "[package]\nname='private_crate'\n", "crates/private/src/lib.rs": "pub fn run() {}\n", "apps/main.rs": "use private_crate::run;\n"})
	for _, e := range s.Edges {
		if e.Kind == "DEPENDS_ON" {
			t.Fatal("resolved excluded workspace member", e)
		}
	}
}

func TestPoetryUnsupportedPackageDeclarationsStayUnknown(t *testing.T) {
	for _, value := range []string{`[{include='lib', from=true}]`, `compute([{include='lib', from='src'}])`} {
		s := indexWorkspaceFixture(t, map[string]string{"libs/member/pyproject.toml": "[tool.poetry]\npackages=" + value + "\n", "libs/member/lib.py": "pass\n", "apps/main.py": "import lib\n"})
		if len(s.Diagnostics) == 0 {
			t.Fatal("unsupported Poetry config silently accepted", value)
		}
		for _, e := range s.Edges {
			if e.Kind == "DEPENDS_ON" {
				t.Fatal("unsupported Poetry configuration resolved", e)
			}
		}
	}
}

func TestWorkspaceManifestIngestionBudgetIsExplicit(t *testing.T) {
	files := map[string]string{}
	for _, name := range []string{"a", "b", "c", "d", "e", "f", "g", "h", "i"} {
		files[name+"/package.json"] = "{}" + strings.Repeat(" ", maxManifestBytes-2)
	}
	s := indexWorkspaceFixture(t, files)
	if len(s.Diagnostics) != 1 || !strings.Contains(s.Diagnostics[0].Message, "manifest budget exhausted") {
		t.Fatalf("budget not explicit: %+v", s.Diagnostics)
	}
}

func TestGradleInterpolatedIncludesStayUnknown(t *testing.T) {
	for _, manifest := range []string{"settings.gradle", "settings.gradle.kts"} {
		s := indexWorkspaceFixture(t, map[string]string{manifest: `include("$module")`, "$module/Library.java": "package lib; public class Library {}", "apps/App.java": "package app; import lib.Library; public class App {}"})
		if len(s.Diagnostics) != 1 || !strings.Contains(s.Diagnostics[0].Message, "interpolated Gradle include") {
			t.Fatalf("interpolation not explicit: %+v", s.Diagnostics)
		}
		for _, e := range s.Edges {
			if strings.HasPrefix(e.Provenance.Method, "workspace_member:") {
				t.Fatal("interpolated include claimed workspace membership", e)
			}
		}
	}
}

// An array-of-tables header starts a new table; its keys do not belong to
// the table before it.
func TestTOMLArrayTablesEndTheirPredecessor(t *testing.T) {
	if got := PyProjectName([]byte("[tool.poetry]\npackage-mode = false\n[[tool.poetry.source]]\nname = \"private-mirror\"\n")); got != "" {
		t.Fatalf("source name read as project name: %q", got)
	}
	if got := PyProjectName([]byte("[[tool.poetry.source]]\nname = \"mirror\"\n[tool.poetry]\nname = \"app\"\n")); got != "app" {
		t.Fatalf("project name: %q", got)
	}
	if got := tomlValue("[lib]\npath = \"src/lib.rs\"\n[[bin]]\nname = \"tool\"\n", "lib", "name"); got != "" {
		t.Fatalf("bin name read as lib name: %q", got)
	}
}
