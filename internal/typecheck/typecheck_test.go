package typecheck

import (
	"context"
	"reflect"
	"testing"

	"github.com/Jake-Network/radar/internal/indexer"
)

// tree maps committed paths to their bytes.
type tree map[string]string

func (f tree) Method() string { return "test" }
func (f tree) Entries(context.Context) ([]indexer.Entry, error) {
	out := make([]indexer.Entry, 0, len(f))
	for p := range f {
		out = append(out, indexer.Entry{Path: p})
	}
	return out, nil
}
func (f tree) Read(_ context.Context, e indexer.Entry, _ int64) ([]byte, error) {
	return []byte(f[e.Path]), nil
}

const pkg = "package x\n"

func roots(p Plan) []string {
	var out []string
	for _, t := range p.Targets {
		out = append(out, t.Root)
	}
	return out
}

func TestSelectGoTargets(t *testing.T) {
	base := tree{
		"go.mod": "module example.com/m\n\ngo 1.21\n", "a/a.go": pkg, "b/b_test.go": pkg,
		"tools/go.mod": "module example.com/tools\n", "tools/gen/main.go": pkg,
		"internal/build/b.go": pkg, "dist/d.go": pkg,
		"vendor/x/x.go": pkg, "a/testdata/fixture.go": pkg, "_scratch/s.go": pkg,
		"a/static/page.html": "", "docs/readme.md": "", "fixtures/testdata/go.mod": "module fixture\n",
	}
	loose := tree{"loose/go_only.go": pkg}
	cases := []struct {
		name    string
		tree    tree
		changed []string
		roots   []string
		gaps    []string
	}{
		{name: "root module", tree: base, changed: []string{"a/a.go", "b/b_test.go"}, roots: []string{"."}},
		{name: "nested module is its own target", tree: base, changed: []string{"a/a.go", "tools/gen/main.go"}, roots: []string{".", "tools"}},
		{name: "go.sum change selects its module", tree: base, changed: []string{"tools/go.sum"}, roots: []string{"tools"}},
		{name: "deleted go file still selects the module", tree: base, changed: []string{"a/removed.go"}, roots: []string{"."}},
		{name: "build and dist are ordinary Go packages", tree: base, changed: []string{"internal/build/b.go", "dist/d.go"}, roots: []string{"."}},
		{name: "testdata and files away from Go source select nothing", tree: base, changed: []string{"a/testdata/fixture.go", "docs/readme.md", "a/static/page.html"}},
		{name: "file beside Go source may be embedded", tree: base, changed: []string{"a/data.txt"}, roots: []string{"."}},
		{name: "C and assembly inputs select their module", tree: base, changed: []string{"tools/gen/x.c", "tools/gen/asm_amd64.s"}, roots: []string{"tools"}},
		{name: "vendored file is a gap", tree: base, changed: []string{"vendor/x/x.go"}, gaps: []string{"vendor/x/x.go"}},
		{name: "underscore directory selects the module", tree: base, changed: []string{"_scratch/s.go"}, roots: []string{"."}},
		{name: "file outside every module is a gap", tree: loose, changed: []string{"loose/go_only.go"}, gaps: []string{"loose/go_only.go"}},
		{name: "deleted file outside every module is not a gap", tree: tree{"x.txt": ""}, changed: []string{"gone.go"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p, err := Select(context.Background(), c.tree, c.changed)
			if err != nil {
				t.Fatal(err)
			}
			var gaps []string
			for _, g := range p.Gaps {
				gaps = append(gaps, g.Path)
			}
			for _, target := range p.Targets {
				if target.Tool != ToolGo || target.CWD != target.Root || !reflect.DeepEqual(target.Command, []string{"go", "vet", "-buildtag", "./..."}) || target.ID == "" {
					t.Fatalf("unexpected target %+v", target)
				}
			}
			if !reflect.DeepEqual(roots(p), c.roots) || !reflect.DeepEqual(gaps, c.gaps) {
				t.Fatalf("roots=%v gaps=%+v", roots(p), p.Gaps)
			}
		})
	}
}

func TestSelectLocalModulesSkipFixtures(t *testing.T) {
	p, err := Select(context.Background(), tree{"go.mod": "module example.com/m // main\n", "lib/go.mod": "module \"example.com/lib\"\n", "x/testdata/go.mod": "module fixture\n"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(p.LocalModules, []Module{{Path: "example.com/m", Dir: "."}, {Path: "example.com/lib", Dir: "lib"}}) {
		t.Fatalf("local modules = %v", p.LocalModules)
	}
}

func TestSelectDependentModules(t *testing.T) {
	cases := []struct {
		name    string
		tree    tree
		changed []string
		roots   []string
		via     map[string][]string
	}{
		{
			name:    "relative replace selects the dependent module",
			tree:    tree{"a/go.mod": "module example.com/a\n", "a/a.go": pkg, "b/go.mod": "module example.com/b\n\nrequire example.com/a v0.0.0\n\nreplace example.com/a => ../a\n", "b/b.go": pkg, "c/go.mod": "module example.com/c\n\nreplace (\n\texample.com/a => example.com/a v1.0.0\n)\n"},
			changed: []string{"a/a.go"},
			roots:   []string{"a", "b"},
			via:     map[string][]string{"b": {"a"}},
		},
		{
			name:    "go.work selects every module it uses",
			tree:    tree{"go.work": "go 1.21\n\nuse (\n\t./lib\n\t./app // the app\n)\n", "lib/go.mod": "module example.com/lib\n", "lib/l.go": pkg, "app/go.mod": "module example.com/app\n", "app/a.go": pkg, "other/go.mod": "module example.com/other\n"},
			changed: []string{"lib/l.go"},
			roots:   []string{"app", "lib"},
			via:     map[string][]string{"app": {"lib"}},
		},
		{
			name:    "go.work change selects its modules",
			tree:    tree{"go.work": "go 1.21\nuse ./lib\nuse ./app\n", "lib/go.mod": "module example.com/lib\n", "app/go.mod": "module example.com/app\n"},
			changed: []string{"go.work"},
			roots:   []string{"app", "lib"},
		},
		{
			name:    "deleted module selects the modules that replaced it",
			tree:    tree{"go.mod": "module root\n", "main.go": pkg, "app/go.mod": "module app\nreplace example.com/lib => ../lib\n"},
			changed: []string{"lib/go.mod", "lib/l.go", "main.go"},
			roots:   []string{".", "app"},
			via:     map[string][]string{"app": {"lib"}},
		},
		{
			name:    "deleted go.work checks every module below it",
			tree:    tree{"lib/go.mod": "module lib\n", "app/go.mod": "module app\n"},
			changed: []string{"go.work"},
			roots:   []string{"app", "lib"},
		},
		{
			name:    "windows-style relative replace",
			tree:    tree{"a/go.mod": "module a\n", "a/a.go": pkg, "b/go.mod": "module b\nreplace a => ..\\a\n"},
			changed: []string{"a/a.go"},
			roots:   []string{"a", "b"},
			via:     map[string][]string{"b": {"a"}},
		},
		{
			name:    "replace chains are followed",
			tree:    tree{"a/go.mod": "module a\n", "a/a.go": pkg, "b/go.mod": "module b\nreplace a => ../a\n", "c/go.mod": "module c\nreplace b => ../b\n"},
			changed: []string{"a/a.go"},
			roots:   []string{"a", "b", "c"},
			via:     map[string][]string{"b": {"a"}, "c": {"b"}},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p, err := Select(context.Background(), c.tree, c.changed)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(roots(p), c.roots) {
				t.Fatalf("roots=%v", roots(p))
			}
			for _, target := range p.Targets {
				if want := c.via[target.Root]; !reflect.DeepEqual(target.Via, want) {
					t.Fatalf("%s via=%v want %v", target.Root, target.Via, want)
				}
			}
		})
	}
}

func TestSelectIsDeterministic(t *testing.T) {
	files := tree{"go.mod": "module m\n", "z/go.mod": "module z\n", "a/a.go": pkg, "z/z.go": pkg}
	first, _ := Select(context.Background(), files, []string{"z/z.go", "a/a.go"})
	second, _ := Select(context.Background(), files, []string{"a/a.go", "z/z.go"})
	if !reflect.DeepEqual(first, second) || first.Targets[0].Root != "." {
		t.Fatalf("%+v %+v", first, second)
	}
}
