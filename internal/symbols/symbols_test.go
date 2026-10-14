package symbols

import (
	"context"
	"testing"

	"github.com/Jake-Network/radar/internal/languages"
	"github.com/Jake-Network/radar/internal/model"
)

func parsed(t *testing.T, files map[string]string) model.Snapshot {
	t.Helper()
	s := model.Snapshot{}
	for p, text := range files {
		a, ok := languages.ForPath(p)
		if !ok {
			t.Fatal(p)
		}
		got, e := a.Parse(context.Background(), languages.Source{Path: p, Content: []byte(text)})
		if e != nil {
			t.Fatal(e)
		}
		s.Nodes = append(s.Nodes, got.Nodes...)
		s.Diagnostics = append(s.Diagnostics, got.Diagnostics...)
	}
	return s
}

func TestDuplicateGoPackageDefinitions(t *testing.T) {
	b := Tree{Snapshot: parsed(t, map[string]string{"svc/base.go": "package svc\nfunc Base() {}"})}
	c := Tree{Snapshot: parsed(t, map[string]string{"svc/base.go": "package svc\nfunc Base() {}", "svc/a.go": "package svc\nfunc Added() {}", "svc/b.go": "package svc\nfunc Added() {}"})}
	r := Evaluate(b, c, []string{"svc/a.go", "svc/b.go"})
	if r.Status != model.StatusFailed || len(r.Findings) != 1 {
		t.Fatalf("%+v", r)
	}
	// Existing duplicates and a different package directory do not fail.
	if r := Evaluate(c, c, []string{"svc/a.go"}); r.Status != model.StatusPassed {
		t.Fatalf("%+v", r)
	}
	c.Snapshot = parsed(t, map[string]string{"a/f.go": "package svc\nfunc Added() {}", "b/f.go": "package svc\nfunc Added() {}"})
	if r := Evaluate(b, c, []string{"a/f.go", "b/f.go"}); r.Status != model.StatusPassed {
		t.Fatalf("%+v", r)
	}
}

func TestDanglingNamedImportAndUnknown(t *testing.T) {
	b := Tree{Snapshot: parsed(t, map[string]string{"lib.py": "def foo():\n    pass\n"}), References: []Reference{{From: "old.py", Module: "lib", Target: "lib.py", Name: "foo", Line: 1}}}
	c := Tree{Snapshot: parsed(t, map[string]string{"lib.py": "def bar():\n    pass\n"}), References: []Reference{{From: "new.py", Module: "lib", Name: "foo", Line: 1}}}
	r := Evaluate(b, c, []string{"lib.py", "new.py"})
	if r.Status != model.StatusFailed || len(r.Findings) != 1 {
		t.Fatalf("%+v", r)
	}
	c.References[0].Module = "external"
	if r := Evaluate(b, c, []string{"new.py"}); r.Status != model.StatusUnknown || len(r.Findings) != 0 {
		t.Fatalf("%+v", r)
	}
	c.References = b.References
	if r := Evaluate(b, c, []string{"old.py", "lib.py"}); r.Status != model.StatusPassed {
		t.Fatalf("unchanged reference: %+v", r)
	}
}

func TestNamedImportExtraction(t *testing.T) {
	cases := []struct{ language, source, module, name string }{
		{"typescript", `import { foo as alias } from "./lib";`, "./lib", "foo"},
		{"javascript", `export { foo } from "./lib";`, "./lib", "foo"},
		{"java", `import svc.Foo;`, "svc.Foo", "Foo"},
		{"rust", `use crate::lib::foo;`, "crate::lib::foo", "foo"},
		{"go", "package main\nimport \"example/lib\"\nfunc run() { lib.Foo() }", "example/lib", "Foo"},
	}
	for _, c := range cases {
		t.Run(c.language, func(t *testing.T) {
			names := importedNames([]byte(c.source), languages.Import{Language: c.language, Module: c.module, Line: 1})
			if len(names) != 1 || names[0] != c.name {
				t.Fatalf("%v", names)
			}
		})
	}
	names := importedNames([]byte("package main\nimport \"example/lib\"\nfunc run() { lib := struct{ Foo int }{}; _ = lib.Foo }"), languages.Import{Language: "go", Module: "example/lib", Line: 2})
	if len(names) != 0 {
		t.Fatalf("shadowed import selector: %v", names)
	}
}

func TestValidRedefinitionsAndOverloadsDoNotFail(t *testing.T) {
	for p, text := range map[string]string{
		"x.py":  "def foo():\n pass\ndef foo():\n pass\n",
		"x.cpp": "void foo(int) {}\nvoid foo(double) {}\n",
		"x.go":  "package x\nfunc init() {}\nfunc init() {}\n",
	} {
		c := Tree{Snapshot: parsed(t, map[string]string{p: text})}
		if r := Evaluate(Tree{}, c, []string{p}); r.Status != model.StatusPassed {
			t.Fatalf("%s: %+v", p, r)
		}
	}
}

func TestGoArgumentCountChange(t *testing.T) {
	base := Tree{Snapshot: parsed(t, map[string]string{"lib/f.go": "package lib\nfunc Foo(x int) {}"}), Signatures: map[string]Arity{"lib/f.go\x00Foo": {1, 1}}}
	combined := Tree{Snapshot: parsed(t, map[string]string{"lib/f.go": "package lib\nfunc Foo(x,y int) {}"}), Signatures: map[string]Arity{"lib/f.go\x00Foo": {2, 2}}, Calls: []Call{{Reference: Reference{From: "new.go", Module: "sample/lib", Target: "lib/f.go", Name: "Foo", Line: 3}, Arguments: 1}}}
	if r := Evaluate(base, combined, []string{"lib/f.go", "new.go"}); r.Status != model.StatusFailed || len(r.Findings) != 1 {
		t.Fatalf("%+v", r)
	}
	base.Calls = combined.Calls
	if r := Evaluate(base, combined, []string{"lib/f.go", "new.go"}); r.Status != model.StatusPassed {
		t.Fatalf("inherited call: %+v", r)
	}
	base.Calls = nil
	combined.Signatures["lib/f.go\x00Foo"] = Arity{1, -1}
	if r := Evaluate(base, combined, []string{"new.go"}); r.Status != model.StatusPassed {
		t.Fatalf("variadic: %+v", r)
	}
}

func TestJavaPackagesAndModuleBoundaries(t *testing.T) {
	c := Tree{Snapshot: parsed(t, map[string]string{"a/Foo.java": "package svc; class Foo {}", "b/Foo.java": "package svc; class Foo {}"})}
	if r := Evaluate(Tree{}, c, []string{"a/Foo.java", "b/Foo.java"}); r.Status != model.StatusFailed {
		t.Fatalf("%+v", r)
	}
	for i := range c.Snapshot.Nodes {
		if c.Snapshot.Nodes[i].Properties == nil {
			c.Snapshot.Nodes[i].Properties = map[string]string{}
		}
		c.Snapshot.Nodes[i].Properties["symbol_module_root"] = c.Snapshot.Nodes[i].Provenance.Path
	}
	if r := Evaluate(Tree{}, c, []string{"a/Foo.java", "b/Foo.java"}); r.Status != model.StatusPassed {
		t.Fatalf("distinct modules: %+v", r)
	}
}

func TestHeaderCallMacrosAndMembersAreExcluded(t *testing.T) {
	if names := headerCalls([]byte("#define foo() 0\nvoid run() { foo(); obj.foo(); }\n")); len(names) != 0 {
		t.Fatalf("%v", names)
	}
	names := headerCalls([]byte("void run() { foo(); }\n"))
	if len(names) != 1 || names[0] != "foo" {
		t.Fatalf("%v", names)
	}
}

func TestNewDynamicCallsAreUnknown(t *testing.T) {
	cases := map[string]string{"x.py": "obj.foo()\n", "x.ts": "obj.foo();\n", "x.js": "obj.foo();\n", "X.java": "class X { void run(Object obj) { obj.foo(); } }", "x.rs": "fn run(obj: X) { obj.foo(); }", "x.cpp": "void run(X obj) { obj.foo(); }", "x.go": "package x\nfunc run(obj X) { obj.foo() }"}
	for p, source := range cases {
		refs := dynamicReferences(p, []byte(source))
		if len(refs) != 1 {
			t.Fatalf("%s: %v", p, refs)
		}
		c := Tree{References: refs}
		if r := Evaluate(Tree{}, c, []string{p}); r.Status != model.StatusUnknown || len(r.Diagnostics) != 1 || len(r.Findings) != 0 {
			t.Fatalf("%s: %+v", p, r)
		}
		if r := Evaluate(Tree{}, c, []string{p}); r.Applicable || r.UnknownCount != 1 || r.SupportedStatus != model.StatusPassed {
			t.Fatalf("resolution-only call should be optional: %+v", r)
		}
		if r := Evaluate(c, c, []string{p}); r.Status != model.StatusPassed {
			t.Fatalf("existing unsupported call: %+v", r)
		}
	}
	if refs := dynamicReferences("x.py", []byte("# obj.foo()\ntext = 'obj.foo()'\n")); len(refs) != 0 {
		t.Fatalf("comments/string: %v", refs)
	}
}

func TestIncompleteIndexBlocksSupportedScope(t *testing.T) {
	c := Tree{Snapshot: parsed(t, map[string]string{"x.go": "package x\nfunc Foo() {}"})}
	c.Snapshot.Diagnostics = append(c.Snapshot.Diagnostics, model.Diagnostic{Message: "source budget exceeded"})
	if r := Evaluate(Tree{}, c, []string{"x.go"}); !r.Applicable || r.SupportedStatus != model.StatusUnknown {
		t.Fatalf("%+v", r)
	}
}

func TestDefaultsAndOverloadsHaveNoArityVerdict(t *testing.T) {
	for p, source := range map[string]string{"x.py": "def foo(x,y=0):\n pass\n", "x.ts": "export function foo(x: number, y=0) {}", "x.cpp": "void foo(int);\nvoid foo(double);\n"} {
		a, _ := languages.ForPath(p)
		parsed, e := a.Parse(context.Background(), languages.Source{Path: p, Content: []byte(source)})
		if e != nil {
			t.Fatal(e)
		}
		_, _, unknown, _ := literalCalls(p, []byte(source), parsed, nil)
		if !unknown[p+"\x00foo"] {
			t.Fatalf("%s: %v", p, unknown)
		}
	}
}
