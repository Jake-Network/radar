package evidence

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Jake-Network/radar/internal/model"
)

func TestParseGoTypecheck(t *testing.T) {
	root := t.TempDir()
	for _, f := range []string{"b/b.go", "b/b_test.go", "svc/x/x.go", "a/a.go", "c/o.go", "c/c.go", "svc/y/y.go", "root_o.go"} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, f)), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, f), []byte("package x\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cases := []struct {
		name     string
		cwd      string
		output   string
		paths    []string
		symbols  []string
		env      string
		noInputs bool
	}{
		{name: "vet type error", output: "# example.com/m/b\n# [example.com/m/b]\nvet: b/b.go:11:11: undefined: a.Old\n", paths: []string{"b/b.go"}, symbols: []string{"a.Old"}},
		{name: "analyzer finding format", output: "b/b.go:6:14: fmt.Printf format %d has arg \"x\" of wrong type string\n", paths: []string{"b/b.go"}, symbols: []string{""}},
		{name: "several packages", output: "# p\nvet: b/b_test.go:5:32: undefined: nope\n# q\nvet: b/b.go:3:24: cannot use \"s\" as int value\n", paths: []string{"b/b_test.go", "b/b.go"}, symbols: []string{"nope", ""}},
		{name: "cwd joins the module directory", cwd: "svc", output: "vet: x/x.go:2:1: undefined: Gone\n", paths: []string{"svc/x/x.go"}, symbols: []string{"Gone"}},
		{name: "absolute path inside the candidate", output: "vet: " + filepath.Join(root, "b", "b.go") + ":4:2: undefined: Z\n", paths: []string{"b/b.go"}, symbols: []string{"Z"}},
		{name: "toolchain path is not repository source", output: "vet: /usr/local/go/src/fmt/print.go:1:1: broken\n"},
		{name: "parent traversal is not repository source", output: "vet: ../other/o.go:1:1: undefined: X\n"},
		{name: "missing module is the environment", output: "c/c.go:3:8: no required module provides package github.com/nope/x; to add it:\n\tgo get github.com/nope/x\n", env: "github.com/nope/x"},
		{name: "missing checksum is the environment", output: "vet: b/b.go:1:1: undefined: X\nverifying module: missing go.sum entry for module providing package z\n", env: "go"},
		{name: "no packages", output: "go: warning: \"./...\" matched no packages\n", noInputs: true},
		{name: "deleted repository package is the source's", output: "b/b.go:3:8: no required module provides package example.com/m/gone; to add it:\n\tgo get example.com/m/gone\n", paths: []string{"b/b.go"}, symbols: []string{""}},
		{name: "deleted package of a dotless module is the source's", output: "b/b.go:3:8: package m/gone is not in std (/usr/lib/go/src/m/gone)\n", paths: []string{"b/b.go"}, symbols: []string{""}},
		{name: "module path prefix alone is not local", output: "b/b.go:3:8: no required module provides package example.com/mother/x; to add it:\n", env: "example.com/mother/x"},
		{name: "missing external package beside a local one is the environment", output: "b/b.go:3:8: no required module provides package example.com/m/gone; to add it:\nb/b.go:4:8: no required module provides package github.com/ext/x; to add it:\n", env: "github.com/ext/x"},
		{name: "import cycle is located at each importing file of the cycle", output: "package example.com/m/a\n\timports example.com/m/b from a.go\n\timports example.com/m/a from b.go: import cycle not allowed\n", paths: []string{"a/a.go", "b/b.go"}, symbols: []string{"", ""}},
		{name: "import cycle leaves out the importer that is not in the cycle", output: "package example.com/m/a\n\timports example.com/m/b from a.go\n\timports example.com/m/c from b.go\n\timports example.com/m/b from c.go: import cycle not allowed\n", paths: []string{"b/b.go", "c/c.go"}, symbols: []string{"", ""}},
		{name: "import cycle in a nested module maps through the longest module path", cwd: "svc", output: "package example.com/m/svc/x\n\timports example.com/m/svc/y from x.go\n\timports example.com/m/svc/x from y.go: import cycle not allowed\n", paths: []string{"svc/x/x.go", "svc/y/y.go"}, symbols: []string{"", ""}},
		{name: "import cycle outside repository modules is not located", output: "package other.org/p\n\timports other.org/q from p.go: import cycle not allowed\n"},
		{name: "two package names in one directory", output: "found packages c (c.go) and other (o.go) in " + filepath.Join(root, "c") + "\n", paths: []string{"c/o.go"}, symbols: []string{""}},
		{name: "two package names in the module root", output: "found packages m (m.go) and other (root_o.go) in " + root + "\n", paths: []string{"root_o.go"}, symbols: []string{""}},
		{name: "missing cgo header is the environment", output: "# example.com/m/c\nc/c.go:3:11: fatal error: notinstalled_lib.h: No such file or directory\n    3 | // #include <notinstalled_lib.h>\ncompilation terminated.\n", env: "notinstalled_lib.h"},
		{name: "missing C compiler is the environment", output: "# runtime/cgo\ncgo: C compiler \"gcc\" not found: exec: \"gcc\": executable file not found in $PATH\n", env: "cgo"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := root
			if c.cwd != "" {
				dir = filepath.Join(root, c.cwd)
			}
			p := parseTypecheck("go", []byte(c.output), root, dir, c.cwd, TypecheckScope{GoModules: []GoModule{{Path: "example.com/m", Dir: "."}, {Path: "m", Dir: "."}, {Path: "example.com/m/svc", Dir: "svc"}}})
			if c.env != "" {
				if p.environment == nil || p.environment.Kind != "module_missing" || p.environment.Name != c.env || len(p.diagnostics) != 0 {
					t.Fatalf("environment=%+v diagnostics=%+v", p.environment, p.diagnostics)
				}
				return
			}
			if p.environment != nil || p.noInputs != c.noInputs || len(p.diagnostics) != len(c.paths) {
				t.Fatalf("parse=%+v", p)
			}
			for i, d := range p.diagnostics {
				if d.Path != c.paths[i] || d.Symbol != c.symbols[i] || d.Line < 0 || len(d.Fingerprint) != 24 {
					t.Fatalf("diagnostic %d = %+v", i, d)
				}
			}
		})
	}
}

func TestTypecheckFingerprintIgnoresRunLocationAndLines(t *testing.T) {
	candidate, base := t.TempDir(), t.TempDir()
	for _, root := range []string{candidate, base} {
		if err := os.MkdirAll(filepath.Join(root, "b"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "b", "b.go"), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	fingerprint := func(root, output string) string {
		t.Helper()
		p := parseTypecheck("go", []byte(output), root, root, ".", TypecheckScope{})
		if len(p.diagnostics) != 1 {
			t.Fatalf("%q: %+v", output, p)
		}
		return p.diagnostics[0].Fingerprint
	}
	same := fingerprint(candidate, "vet: b/b.go:10:2: x redeclared in this block (other declaration at "+filepath.Join(candidate, "b", "b.go")+":3:6)\n")
	if moved := fingerprint(base, "vet: b/b.go:40:2: x redeclared in this block (other declaration at "+filepath.Join(base, "b", "b.go")+":9:6)\n"); moved != same {
		t.Fatal("line moves or candidate directories changed the fingerprint")
	}
	if other := fingerprint(candidate, "vet: b/b.go:10:2: y redeclared in this block\n"); other == same {
		t.Fatal("a different message produced the same fingerprint")
	}
	if f := fingerprint(candidate, "vet: b/b.go:10:2: undefined: f(1,2)\n"); f == fingerprint(candidate, "vet: b/b.go:10:2: undefined: f(3,4)\n") {
		t.Fatal("literal message text was dropped from the fingerprint")
	}
}

// fakeTool writes an executable shell script into root that prints output
// and exits with code, so each status branch is observed deterministically.
func fakeTool(t *testing.T, root, output string, code int) []string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell scripts are not executable on Windows")
	}
	if err := os.WriteFile(filepath.Join(root, "out.txt"), []byte(output), 0o600); err != nil {
		t.Fatal(err)
	}
	script := fmt.Sprintf("#!/bin/sh\ncat \"$(dirname \"$0\")/out.txt\"\nexit %d\n", code)
	if err := os.WriteFile(filepath.Join(root, "tc.sh"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return []string{"./tc.sh"}
}

func TestObserveTypecheckStatuses(t *testing.T) {
	many := strings.Repeat("vet: b/b.go:1:1: undefined: X\n", maxTypecheckDiagnostics+1)
	cases := []struct {
		name      string
		output    string
		code      int
		status    model.Status
		diagnosis string
		count     int
	}{
		{name: "clean run passes", status: model.StatusPassed},
		{name: "no packages is unknown", output: "go: warning: \"./...\" matched no packages\n", status: model.StatusUnknown},
		{name: "located error fails", output: "vet: b/b.go:3:2: undefined: Old\n", code: 1, status: model.StatusFailed, diagnosis: "typecheck_failed", count: 1},
		{name: "nonzero exit without a located error is an error", output: "go: errors parsing go.mod\n", code: 1, status: model.StatusError},
		{name: "errors with a successful exit are an error", output: "vet: b/b.go:3:2: undefined: Old\n", status: model.StatusError, count: 1},
		{name: "environment is never a source failure", output: "vet: b/b.go:3:2: undefined: Old\nb/b.go:1:8: no required module provides package example.com/dep; to add it:\n", code: 1, status: model.StatusError, diagnosis: "module_missing"},
		{name: "too many errors to compare is incomplete", output: many, code: 1, status: model.StatusIncomplete, count: maxTypecheckDiagnostics + 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.MkdirAll(filepath.Join(root, "b"), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "b", "b.go"), []byte("package b\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			argv := fakeTool(t, root, c.output, c.code)
			r, err := ObserveTypecheck(context.Background(), root, ".", "go", TypecheckScope{}, argv, 10*time.Second)
			if err != nil {
				t.Fatal(err)
			}
			kept := c.count
			if c.count > maxTypecheckDiagnostics {
				kept = 0
			}
			if r.Status != c.status || r.DiagnosticCount != c.count || len(r.All()) != kept || r.OutputDigest == "" {
				t.Fatalf("observation=%+v", r)
			}
			if got := ""; r.Diagnosis != nil {
				got = r.Diagnosis.Kind
				if got != c.diagnosis {
					t.Fatalf("diagnosis=%+v want %s", r.Diagnosis, c.diagnosis)
				}
			} else if c.diagnosis != "" {
				t.Fatalf("missing diagnosis %s", c.diagnosis)
			}
		})
	}
}

func TestObserveTypecheckBoundsOutputAndTime(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell scripts are not executable on Windows")
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "flood.sh"), []byte("#!/bin/sh\nhead -c 2000000 /dev/zero | tr '\\0' 'x'\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	r, err := ObserveTypecheck(context.Background(), root, ".", "go", TypecheckScope{}, []string{"./flood.sh"}, 10*time.Second)
	if err != nil || r.Status != model.StatusIncomplete {
		t.Fatalf("overflow: %+v %v", r, err)
	}
	if err := os.WriteFile(filepath.Join(root, "slow.sh"), []byte("#!/bin/sh\nsleep 5\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	r, err = ObserveTypecheck(context.Background(), root, ".", "go", TypecheckScope{}, []string{"./slow.sh"}, 200*time.Millisecond)
	if err != nil || r.Status != model.StatusTimeout {
		t.Fatalf("timeout: %+v %v", r, err)
	}
	if _, err := ObserveTypecheck(context.Background(), root, ".", "nope", TypecheckScope{}, []string{"./slow.sh"}, time.Second); err == nil {
		t.Fatal("unsupported tool accepted")
	}
	if _, err := ObserveTypecheck(context.Background(), root, "../outside", "go", TypecheckScope{}, []string{"./slow.sh"}, time.Second); err == nil {
		t.Fatal("working directory outside the candidate accepted")
	}
}

func TestObserveTypecheckRealGoVet(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go unavailable")
	}
	root := t.TempDir()
	write := func(p, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, p)), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, p), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module example.com/m\n\ngo 1.21\n")
	write("a/a.go", "package a\n\nfunc New() int { return 1 }\n")
	write("b/b.go", "package b\n\nimport \"example.com/m/a\"\n\nfunc F() int { return a.New() }\n")
	argv := []string{"go", "vet", "-buildtag", "./..."}
	r, err := ObserveTypecheck(context.Background(), root, ".", "go", TypecheckScope{}, argv, 2*time.Minute)
	if err != nil || r.Status != model.StatusPassed {
		t.Fatalf("clean module: %+v %v", r, err)
	}
	write("b/b.go", "package b\n\nimport \"example.com/m/a\"\n\nfunc F() int { return a.Old() }\n")
	r, err = ObserveTypecheck(context.Background(), root, ".", "go", TypecheckScope{}, argv, 2*time.Minute)
	if err != nil || r.Status != model.StatusFailed || len(r.Diagnostics) != 1 || r.Diagnostics[0].Path != "b/b.go" || r.Diagnostics[0].Symbol != "a.Old" {
		t.Fatalf("renamed function: %+v %v", r, err)
	}
}

func TestGoCompiledFiles(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go unavailable")
	}
	root := t.TempDir()
	write := func(p, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, p)), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, p), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	other := "windows"
	if runtime.GOOS == "windows" {
		other = "linux"
	}
	write("go.mod", "module example.com/m\n\ngo 1.21\n")
	write("main.go", "package main\n\nfunc main() {}\n")
	write("a/a.go", "package a\n")
	write("a/a_test.go", "package a\n")
	write("a/x_test.go", "package a_test\n")
	write("a/only_"+other+".go", "package a\n")
	write("a/tagged.go", "//go:build integration\n\npackage a\n")
	write("_skip/s.go", "package s\n")
	write("vendor/v/v.go", "package v\n")
	write("build/b.go", "package b\n")
	files, err := GoCompiledFiles(context.Background(), root, ".", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"main.go", "a/a.go", "a/a_test.go", "a/x_test.go", "build/b.go"} {
		if !files[want] {
			t.Fatalf("%s not compiled: %v", want, files)
		}
	}
	for _, absent := range []string{"a/only_" + other + ".go", "a/tagged.go", "_skip/s.go", "vendor/v/v.go"} {
		if files[absent] {
			t.Fatalf("%s reported as compiled", absent)
		}
	}
	nested, err := GoCompiledFiles(context.Background(), root, "a", time.Minute)
	if err != nil || !nested["a/a.go"] || nested["main.go"] {
		t.Fatalf("cwd paths: %v %v", nested, err)
	}
}

func TestTypecheckUnknownLineIsNotShownAsZero(t *testing.T) {
	d := typecheckDiagnosis("go vet", []TypecheckDiagnostic{{Path: "a/a.go", Line: 0, Fingerprint: "f"}})
	if strings.Contains(d.Message, "a/a.go:0") || !strings.Contains(d.Message, "the first is at a/a.go") {
		t.Fatal(d.Message)
	}
	if got := (TypecheckDiagnostic{Path: "b/b.go", Line: 7}).Location(); got != "b/b.go:7" {
		t.Fatal(got)
	}
}
