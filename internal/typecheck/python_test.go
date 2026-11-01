package typecheck

import (
	"context"
	"reflect"
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

func TestPythonModulesCoverPackageBases(t *testing.T) {
	got := pythonModules([]string{"services/api/app/helpers.py", "services/api/app/__init__.py", "top.py", "node_modules/x/y.py"})
	for _, want := range []string{"app.helpers", "api.app", "services.api.app.helpers"} {
		if !contains(got, want) {
			t.Fatalf("%s missing from %v", want, got)
		}
	}
	if contains(got, "top") || contains(got, "app") || contains(got, "x.y") {
		t.Fatalf("single names and dependencies are not modules: %v", got)
	}
}
