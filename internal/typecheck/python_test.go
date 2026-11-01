package typecheck

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestSelectPythonTargets(t *testing.T) {
	files := tree{
		"pyproject.toml":  "[project]\nname = \"svc\"\n\n[tool.mypy]\nstrict = true\n\n[tool.ruff.lint]\nselect = [\"E\"]\n",
		"app/__init__.py": "", "app/models.py": "", "app/report.py": "", "src/lib/x.py": "", "stubs/m.pyi": "",
		"tools/setup.cfg": "[metadata]\nname = tools\n\n[mypy]\nfiles = gen\n", "tools/gen/g.py": "",
		"lint/ruff.toml": "line-length = 100\n", "lint/l.py": "",
		"other/setup.cfg": "[flake8]\n", "other/o.py": "",
		"docs/pyproject.toml": "[tool.black]\n", "docs/conf.py": "",
		"site-packages/dep/d.py": "",
	}
	cases := []struct {
		name         string
		changed      []string
		targets      []string // tool:root:files
		gaps         []string
		unconfigured []string
	}{
		{name: "pyproject configures mypy and ruff; mypy checks the whole project", changed: []string{"app/report.py", "app/models.py"}, targets: []string{"mypy:.:.", "ruff:.:app/models.py app/report.py"}},
		{name: "nearest configuration owns the file; declared files run as configured", changed: []string{"tools/gen/g.py"}, targets: []string{"ruff:.:tools/gen/g.py", "mypy:tools:"}},
		{name: "ruff.toml configures only ruff below it", changed: []string{"lint/l.py"}, targets: []string{"mypy:.:.", "ruff:lint:l.py"}},
		{name: "stubs are checked", changed: []string{"stubs/m.pyi"}, targets: []string{"mypy:.:.", "ruff:.:stubs/m.pyi"}},
		{name: "a deletion is checked by mypy, not ruff", changed: []string{"app/gone.py"}, targets: []string{"mypy:.:."}},
		{name: "a deleted top-level package is checked by mypy", changed: []string{"gone/__init__.py"}, targets: []string{"mypy:.:."}},
		{name: "installed dependencies are gaps", changed: []string{"site-packages/dep/d.py"}, gaps: []string{"site-packages/dep/d.py"}},
		{name: "non-Python files select nothing", changed: []string{"app/data.json", "pyproject.toml"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p, err := Select(context.Background(), files, c.changed)
			if err != nil {
				t.Fatal(err)
			}
			var got, gaps []string
			for _, target := range p.Targets {
				files := ""
				for i, f := range target.Files {
					if i > 0 {
						files += " "
					}
					files += f
				}
				got = append(got, target.Tool+":"+target.Root+":"+files)
				prefix := pythonCommand(target.Tool)
				if target.Declared {
					prefix = prefix[:len(prefix)-1]
				}
				if target.CWD != target.Root || !reflect.DeepEqual(target.Command, append(prefix, target.Files...)) || target.ID == "" {
					t.Fatalf("unexpected target %+v", target)
				}
			}
			for _, g := range p.Gaps {
				gaps = append(gaps, g.Path)
			}
			if !reflect.DeepEqual(got, c.targets) || !reflect.DeepEqual(gaps, c.gaps) || !reflect.DeepEqual(p.Unconfigured, c.unconfigured) {
				t.Fatalf("targets=%v gaps=%v unconfigured=%v", got, gaps, p.Unconfigured)
			}
		})
	}
}

func TestSelectPythonWithoutConfigurationIsUnconfigured(t *testing.T) {
	p, err := Select(context.Background(), tree{"other/setup.cfg": "[flake8]\n", "other/o.py": "", "docs/pyproject.toml": "[tool.black]\n", "docs/conf.py": ""}, []string{"other/o.py", "docs/conf.py"})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Targets) != 0 || len(p.Gaps) != 0 || !reflect.DeepEqual(p.Unconfigured, []string{"docs/conf.py", "other/o.py"}) {
		t.Fatalf("%+v", p)
	}
}

func TestSelectPythonPackagesIncludeDeletedAndSrcLayout(t *testing.T) {
	files := tree{"svc/mypy.ini": "[mypy]\n", "svc/app/a.py": "", "svc/src/lib/l.py": "", "svc/main.py": "", "svc/9bad/x.py": "", "elsewhere/e.py": ""}
	p, err := Select(context.Background(), files, []string{"svc/app/a.py", "svc/gone/g.py"})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Targets) != 1 || !reflect.DeepEqual(p.Targets[0].Packages, []string{"app", "gone", "lib", "main", "src"}) {
		t.Fatalf("%+v", p.Targets)
	}
}

func TestSelectPythonBoundsFiles(t *testing.T) {
	files := tree{"ruff.toml": ""}
	var changed []string
	for i := 0; i < maxPythonFiles+2; i++ {
		f := "p/" + string(rune('a'+i/26/26%26)) + string(rune('a'+i/26%26)) + string(rune('a'+i%26)) + ".py"
		files[f] = ""
		changed = append(changed, f)
	}
	p, err := Select(context.Background(), files, changed)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Targets) != 1 || len(p.Targets[0].Files) != maxPythonFiles || len(p.Gaps) != 2 {
		t.Fatalf("targets=%d files=%d gaps=%v", len(p.Targets), len(p.Targets[0].Files), p.Gaps)
	}
}

func TestSelectPythonBaseOnlyPaths(t *testing.T) {
	files := tree{"ruff.toml": "", "mypy.ini": "[mypy]\n", "new.py": "", "app/a.py": ""}
	p, err := Select(context.Background(), files, []string{"old.py", "new.py", "gone/x.py"})
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range p.Targets {
		switch target.Tool {
		case ToolMypy:
			if target.BaseOnly != nil || !reflect.DeepEqual(target.Files, []string{"."}) {
				t.Fatalf("%+v", target)
			}
		case ToolRuff:
			if !reflect.DeepEqual(target.BaseOnly, []string{"gone/x.py", "old.py"}) || !reflect.DeepEqual(target.Files, []string{"new.py"}) {
				t.Fatalf("%+v", target)
			}
		}
	}
}

func TestMypyDeclaresTargets(t *testing.T) {
	for _, c := range []struct {
		files tree
		want  bool
	}{
		{tree{"mypy.ini": "[mypy]\nfiles = src, tests\n"}, true},
		{tree{"mypy.ini": "[mypy]\nstrict = True\n[mypy-foo.*]\nfiles = x\n"}, false},
		{tree{"pyproject.toml": "[tool.mypy]\npackages = [\"app\"]\n"}, true},
		{tree{"pyproject.toml": "[tool.mypy]\nstrict = true\n\n[[tool.mypy.overrides]]\nmodule = \"x\"\n"}, false},
		{tree{"setup.cfg": "[mypy]\nmodules = a\n"}, true},
		{tree{"mypy.ini": "[mypy]\n", "pyproject.toml": "[tool.mypy]\nfiles = [\"a\"]\n"}, false},
	} {
		read := func(name string) ([]byte, bool) { b, ok := c.files[name]; return []byte(b), ok }
		if got := mypyDeclaresTargets(".", read); got != c.want {
			t.Fatalf("%v: got %v", c.files, got)
		}
	}
}

func TestModulesUnderOnlyNamesModulesBelowSearchBases(t *testing.T) {
	paths := []string{"services/api/app/helpers.py", "services/api/app/__init__.py", "pkg/integrations/redis/client.py", "top.py", "node_modules/x/y.py"}
	got := ModulesUnder([]string{".", "services/api"}, paths)
	for _, want := range []string{"app", "app.helpers", "services.api.app.helpers", "pkg.integrations.redis.client", "top"} {
		if !contains(got, want) {
			t.Fatalf("%s missing from %v", want, got)
		}
	}
	// A tail of a repository path is not a module below a base.
	for _, wrong := range []string{"redis.client", "redis", "api.app", "x.y", "y"} {
		if contains(got, wrong) {
			t.Fatalf("%s is not importable from a base: %v", wrong, got)
		}
	}
}

func TestMypyPathsReadConfiguredSearchDirectories(t *testing.T) {
	for _, c := range []struct {
		dir, file, content string
		want               []string
	}{
		{".", "mypy.ini", "[mypy]\nmypy_path = lib, shared:more\n", []string{"lib", "shared", "more"}},
		{"api", "api/pyproject.toml", "[tool.mypy]\nmypy_path = \"../core\"\n", []string{"core"}},
		{"api", "api/pyproject.toml", "[tool.mypy]\nmypy_path = [\n  \"$MYPY_CONFIG_FILE_DIR/stubs\",\n  \"../../outside\",\n]\n", []string{"api/stubs"}},
		{".", "setup.cfg", "[mypy]\nmypy_path = /abs/elsewhere\n[mypy-x.*]\nmypy_path = no\n", nil},
		{".", "pyproject.toml", "[tool.mypy]\nstrict = true\n\n[[tool.mypy.overrides]]\nmypy_path = \"no\"\n", nil},
	} {
		read := func(name string) ([]byte, bool) {
			if name == c.file {
				return []byte(c.content), true
			}
			return nil, false
		}
		got := mypyPaths(c.dir, read)
		if strings.Join(got, ",") != strings.Join(c.want, ",") {
			t.Fatalf("%s: got %v want %v", c.content, got, c.want)
		}
	}
}

func TestPythonImports(t *testing.T) {
	src := "import os, app.models as m\nfrom app.helpers import slug, title as t  # note\nfrom . import helpers\nfrom ..core.calc import (\n    total,\n    grand,\n)\nx = 1; import radar_x\n# import commented\nfromage = 3\n"
	got := PythonImports([]byte(src))
	want := []PythonImport{
		{Line: 1, Module: "os"}, {Line: 1, Module: "app.models"},
		{Line: 2, Module: "app.helpers", Names: []string{"slug", "title"}},
		{Line: 3, Level: 1, Names: []string{"helpers"}},
		{Line: 4, Level: 2, Module: "core.calc", Names: []string{"total", "grand"}},
		{Line: 8, Module: "radar_x"},
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
	if m := got[3].Modules("app"); fmt.Sprint(m) != "[app app.helpers]" {
		t.Fatalf("relative modules=%v", m)
	}
	if m := got[4].Modules("svc.api"); fmt.Sprint(m) != "[svc.core.calc svc.core.calc.total svc.core.calc.grand]" {
		t.Fatalf("parent-relative modules=%v", m)
	}
	if m := got[4].Modules("api"); m != nil {
		t.Fatalf("an import above the top package resolved: %v", m)
	}
}

func TestSelectMypyDependents(t *testing.T) {
	files := map[string]string{
		"pyproject.toml":       "[tool.mypy]\nmypy_path = \"lib\"\n",
		"app/main.py":          "from pkg.util import f\n",
		"lib/pyproject.toml":   "[tool.mypy]\n",
		"lib/pkg/util.py":      "def f() -> int: ...\n",
		"core/pyproject.toml":  "[tool.mypy]\n",
		"core/corelib/calc.py": "x = 1\n",
		"api/pyproject.toml":   "[tool.mypy]\nmypy_path = \"../core\"\n",
		"api/handler.py":       "import os\n",
		"other/pyproject.toml": "[tool.mypy]\n",
		"other/use.py":         "from lib.pkg import util\n",
	}
	plan, err := Select(context.Background(), tree(files), []string{"lib/pkg/util.py", "core/corelib/calc.py"})
	if err != nil {
		t.Fatal(err)
	}
	byRoot := map[string]Target{}
	for _, tg := range plan.Targets {
		if tg.Tool == ToolMypy {
			byRoot[tg.Root] = tg
		}
	}
	if tg, ok := byRoot["."]; !ok || fmt.Sprint(tg.Via) != "[lib]" || len(tg.Changed) != 0 || fmt.Sprint(tg.Files) != "[.]" {
		t.Fatalf("root reaches lib through mypy_path and imports pkg: %+v", byRoot)
	}
	if _, ok := byRoot["api"]; ok {
		t.Fatalf("api reaches core but imports none of it: %+v", byRoot["api"])
	}
	if _, ok := byRoot["other"]; ok {
		t.Fatalf("other cannot reach lib (no search path to it): %+v", byRoot["other"])
	}
	if _, ok := byRoot["lib"]; !ok {
		t.Fatalf("lib itself: %+v", byRoot)
	}
}

func TestSelectMypyDependentsReachesTransitiveReexports(t *testing.T) {
	files := tree{
		"core/mypy.ini": "[mypy]\n", "core/corelib/model.py": "x = 1\n",
		"api/mypy.ini": "[mypy]\nmypy_path = ../core\n", "api/apilib/model.py": "from corelib.model import x\n",
		"cli/mypy.ini": "[mypy]\nmypy_path = ../api\n", "cli/use.py": "from apilib.model import x\n",
	}
	p, err := Select(context.Background(), files, []string{"core/corelib/model.py"})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(roots(p), ","); got != "api,cli,core" {
		t.Fatalf("transitive dependents=%s", got)
	}
}

func TestPythonImportsSkipsStringsAndReadsContinuedStatements(t *testing.T) {
	src := []byte("doc = '''\nfrom gone import x\n'''\nfrom app.\\\n helpers import (slug,\n title)\nif True: import app.models\n")
	imports := PythonImports(src)
	if len(imports) != 2 || imports[0].Module != "app.helpers" || imports[0].Line != 4 || imports[1].Module != "app.models" {
		t.Fatalf("imports=%+v", imports)
	}
}
