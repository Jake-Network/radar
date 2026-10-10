package evidence

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Jake-Network/radar/internal/model"
)

// goBuildFailedOutput is `go test -json .` for a package that uses a function
// another branch renamed (captured from Go 1.26).
const goBuildFailedOutput = `{"ImportPath":"example.com/m [example.com/m.test]","Action":"build-output","Output":"# example.com/m [example.com/m.test]\n"}
{"ImportPath":"example.com/m [example.com/m.test]","Action":"build-output","Output":"./args.go:149:6: undefined: commandNameMatches\n"}
{"ImportPath":"example.com/m [example.com/m.test]","Action":"build-fail"}
{"Action":"start","Package":"example.com/m"}
{"Action":"output","Package":"example.com/m","Output":"FAIL\texample.com/m [build failed]\n"}
{"Action":"fail","Package":"example.com/m","Elapsed":0,"FailedBuild":"example.com/m [example.com/m.test]"}
`

func TestGoBuildFailureIsSourceFailure(t *testing.T) {
	argv := []string{"go", "test", "-json", "."}
	r := harnessCounts(argv, []byte(goBuildFailedOutput))
	if len(r.BuildErrors) != 1 || r.BuildErrors[0] != (sourceBuildError{Path: "args.go", Line: 149, Symbol: "commandNameMatches"}) {
		t.Fatalf("build errors %+v", r.BuildErrors)
	}
	if s := outcome(false, 1, r, false, argv, []byte(goBuildFailedOutput)); s != model.StatusFailed {
		t.Fatalf("source build failure classified %s", s)
	}
	// A run that never reports the build failure keeps the old semantics.
	if s := outcome(false, 0, r, false, argv, nil); s == model.StatusFailed {
		t.Fatal("successful exit downgraded to failure")
	}
}

func TestGoBuildFailureEnvironmentNegatives(t *testing.T) {
	cases := map[string]string{
		"missing module": `{"ImportPath":"x","Action":"build-output","Output":"a.go:3:8: no required module provides package github.com/nope/nothere; to add it:\n"}
{"ImportPath":"x","Action":"build-fail"}
{"Action":"output","Package":"ex","Output":"FAIL\tex [setup failed]\n"}
`,
		"missing checksum": `{"ImportPath":"x","Action":"build-output","Output":"a.go:3:8: missing go.sum entry for module providing package github.com/a/b\n"}
{"ImportPath":"x","Action":"build-fail"}
`,
		"cgo compiler": `{"ImportPath":"runtime/cgo","Action":"build-output","Output":"# runtime/cgo\n"}
{"ImportPath":"runtime/cgo","Action":"build-output","Output":"cgo: C compiler \"gcc\" not found: exec: \"gcc\": executable file not found in $PATH\n"}
{"ImportPath":"runtime/cgo","Action":"build-fail"}
`,
		"module cache source": `{"ImportPath":"x","Action":"build-output","Output":"/home/u/go/pkg/mod/github.com/a/b@v1.0.0/b.go:10:2: undefined: c\n"}
{"ImportPath":"x","Action":"build-fail"}
`,
		"parent directory": `{"ImportPath":"x","Action":"build-output","Output":"../outside/b.go:10:2: undefined: c\n"}
{"ImportPath":"x","Action":"build-fail"}
`,
		"no build failure": `{"ImportPath":"x","Action":"build-output","Output":"./a.go:1:1: undefined: c\n"}
`,
	}
	argv := []string{"go", "test", "-json", "./..."}
	for name, output := range cases {
		r := harnessCounts(argv, []byte(output))
		if len(r.BuildErrors) != 0 {
			t.Errorf("%s: attributed to source: %+v", name, r.BuildErrors)
		}
		if s := outcome(false, 1, r, false, argv, []byte(output)); s != model.StatusError {
			t.Errorf("%s: classified %s", name, s)
		}
	}
	// Without -json the output is not structured evidence.
	if r := harnessCounts([]string{"go", "test", "."}, []byte(goBuildFailedOutput)); len(r.BuildErrors) != 0 {
		t.Fatal("non-JSON go test recognized")
	}
}

func TestCargoBuildFailure(t *testing.T) {
	source := "   Compiling demo v0.1.0 (/tmp/radar-integration-1)\n" +
		"error[E0425]: cannot find function `parse_total` in this scope\n" +
		" --> src/lib.rs:12:5\n" +
		"  |\n" +
		"12 |     parse_total(x)\n" +
		"\n" +
		"error: could not compile `demo` (lib test) due to 1 previous error\n"
	r := harnessCounts([]string{"cargo", "test"}, []byte(source))
	if len(r.BuildErrors) != 1 || r.BuildErrors[0] != (sourceBuildError{Path: "src/lib.rs", Line: 12, Symbol: "parse_total"}) {
		t.Fatalf("cargo build errors %+v", r.BuildErrors)
	}
	if s := outcome(false, 101, r, false, []string{"cargo", "test"}, []byte(source)); s != model.StatusFailed {
		t.Fatalf("cargo source build failure classified %s", s)
	}
	registry := "error[E0308]: mismatched types\n" +
		"  --> /home/u/.cargo/registry/src/index.crates.io-1/openssl-sys-0.9.1/src/lib.rs:3:5\n" +
		"error: could not compile `openssl-sys` (lib) due to 1 previous error\n"
	if r := harnessCounts([]string{"cargo", "test"}, []byte(registry)); len(r.BuildErrors) != 0 {
		t.Fatalf("registry crate failure attributed to source: %+v", r.BuildErrors)
	}
	buildScript := "error: failed to run custom build command for `openssl-sys v0.9.1`\n"
	if r := harnessCounts([]string{"cargo", "test"}, []byte(buildScript)); len(r.BuildErrors) != 0 {
		t.Fatal("build script failure attributed to source")
	}
}

func TestBuildFailureDiagnosisAndLocations(t *testing.T) {
	root := t.TempDir()
	if e := os.MkdirAll(filepath.Join(root, "svc"), 0o755); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(root, "svc", "args.go"), []byte("package svc\n"), 0o644); e != nil {
		t.Fatal(e)
	}
	argv := []string{"go", "test", "-json", "."}
	r := CandidateObservation{Status: model.StatusFailed, ExitCode: 1}
	decorateCandidateObservation(&r, argv, []byte(goBuildFailedOutput), root, filepath.Join(root, "svc"), "svc")
	d := r.Diagnosis
	if d == nil || d.Kind != "build_failed" || d.Name != "commandNameMatches" || d.Environment() {
		t.Fatalf("diagnosis %+v", d)
	}
	if !strings.Contains(d.Message, "svc/args.go:149") || strings.Contains(d.Message, "example.com") {
		t.Fatalf("message %q", d.Message)
	}
	if len(r.Locations) != 1 || r.Locations[0].Path != "svc/args.go" || r.Locations[0].Line != 149 || r.Locations[0].Method != "observed_compiler_error" {
		t.Fatalf("locations %+v", r.Locations)
	}
	generic := buildDiagnosis("go test", []sourceBuildError{{Path: "a.go", Line: 3}})
	if generic.Name != "a.go" || !strings.Contains(generic.Message, "a.go:3") {
		t.Fatalf("generic diagnosis %+v", generic)
	}
}

func TestUnittestRemovedNameIsSourceFailure(t *testing.T) {
	argv := []string{"python3", "-m", "unittest", "discover"}
	removed := "ERROR: test_app (unittest.loader._FailedTest.test_app)\n" +
		"ImportError: Failed to import test module: test_app\n" +
		"Traceback (most recent call last):\n" +
		"ImportError: cannot import name 'parse' from 'app' (/tmp/radar-integration-1/app.py)\n" +
		"Ran 1 test in 0.000s\n\nFAILED (errors=1)\n"
	if environmentFailure(argv, []byte(removed)) {
		t.Fatal("removed repository name downgraded to environment error")
	}
	missing := strings.Replace(removed, "ImportError: cannot import name 'parse' from 'app' (/tmp/radar-integration-1/app.py)", "ModuleNotFoundError: No module named 'yaml'", 1)
	if !environmentFailure(argv, []byte(missing)) {
		t.Fatal("missing dependency reported as a test failure")
	}
}

func TestObserveCandidateGoBuildFailure(t *testing.T) {
	if _, e := exec.LookPath("go"); e != nil {
		t.Skip(e)
	}
	root := fixture(t, map[string]string{
		"go.mod":      "module example.com/fixture\n\ngo 1.23\n",
		"name.go":     "package fixture\n\nfunc namesMatch(a, b string) bool { return a == b }\n",
		"use.go":      "package fixture\n\nfunc Any(n string) bool { return commandNameMatches(n, \"run\") }\n",
		"any_test.go": "package fixture\n\nimport \"testing\"\n\nfunc TestAny(t *testing.T) { _ = Any(\"run\") }\n",
	})
	r, e := ObserveCandidate(context.Background(), root, []string{"go", "test", "-json", "."}, 2*time.Minute)
	if e != nil {
		t.Fatal(e)
	}
	if r.Status != model.StatusFailed || r.Diagnosis == nil || r.Diagnosis.Kind != "build_failed" || r.Diagnosis.Name != "commandNameMatches" {
		t.Fatalf("observation %+v diagnosis %+v", r, r.Diagnosis)
	}
	if len(r.Locations) == 0 || r.Locations[0].Path != "use.go" {
		t.Fatalf("locations %+v", r.Locations)
	}
}
