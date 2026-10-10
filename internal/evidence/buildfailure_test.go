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
	r := harnessCounts(argv, []byte(goBuildFailedOutput), "")
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
		r := harnessCounts(argv, []byte(output), "")
		if len(r.BuildErrors) != 0 {
			t.Errorf("%s: attributed to source: %+v", name, r.BuildErrors)
		}
		if s := outcome(false, 1, r, false, argv, []byte(output)); s != model.StatusError {
			t.Errorf("%s: classified %s", name, s)
		}
	}
	// Without -json the output is not structured evidence.
	if r := harnessCounts([]string{"go", "test", "."}, []byte(goBuildFailedOutput), ""); len(r.BuildErrors) != 0 {
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
	r := harnessCounts([]string{"cargo", "test"}, []byte(source), "")
	if len(r.BuildErrors) != 1 || r.BuildErrors[0] != (sourceBuildError{Path: "src/lib.rs", Line: 12, Symbol: "parse_total"}) {
		t.Fatalf("cargo build errors %+v", r.BuildErrors)
	}
	if s := outcome(false, 101, r, false, []string{"cargo", "test"}, []byte(source)); s != model.StatusFailed {
		t.Fatalf("cargo source build failure classified %s", s)
	}
	registry := "error[E0308]: mismatched types\n" +
		"  --> /home/u/.cargo/registry/src/index.crates.io-1/openssl-sys-0.9.1/src/lib.rs:3:5\n" +
		"error: could not compile `openssl-sys` (lib) due to 1 previous error\n"
	if r := harnessCounts([]string{"cargo", "test"}, []byte(registry), ""); len(r.BuildErrors) != 0 {
		t.Fatalf("registry crate failure attributed to source: %+v", r.BuildErrors)
	}
	buildScript := "error: failed to run custom build command for `openssl-sys v0.9.1`\n"
	if r := harnessCounts([]string{"cargo", "test"}, []byte(buildScript), ""); len(r.BuildErrors) != 0 {
		t.Fatal("build script failure attributed to source")
	}
}

// Compilers driven by build tools print absolute paths inside the private
// snapshot; only those, not toolchain or cache paths, name repository source.
func TestAbsoluteSnapshotPaths(t *testing.T) {
	dir := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if e := os.Symlink(dir, link); e != nil {
		t.Fatal(e)
	}
	inside := filepath.Join(dir, "src", "lib.rs")
	cases := []struct {
		dir, path, want string
		ok              bool
	}{
		{dir, inside, "src/lib.rs", true},
		{link, inside, "src/lib.rs", true}, // reported through the resolved directory
		{dir, "src/lib.rs", "src/lib.rs", true},
		{"", inside, "", false},
		{dir, dir, "", false},
		{dir, filepath.Join(dir, "..", "outside.rs"), "", false},
		{dir, "/home/u/.cargo/registry/src/x/lib.rs", "", false},
		{dir, "../outside.rs", "", false},
	}
	for _, c := range cases {
		got, ok := sourcePath(c.dir, c.path)
		if got != c.want || ok != c.ok {
			t.Errorf("sourcePath(%q, %q) = %q, %t", c.dir, c.path, got, ok)
		}
	}
	source := "error[E0425]: cannot find function `parse_total` in this scope\n" +
		" --> " + inside + ":12:5\n" +
		"error: could not compile `demo` (lib test) due to 1 previous error\n"
	if r := harnessCounts([]string{"cargo", "test"}, []byte(source), dir); len(r.BuildErrors) != 1 || r.BuildErrors[0].Path != "src/lib.rs" {
		t.Fatalf("absolute snapshot path not attributed: %+v", r.BuildErrors)
	}
	if r := harnessCounts([]string{"cargo", "test"}, []byte(source), t.TempDir()); len(r.BuildErrors) != 0 {
		t.Fatalf("path outside the execution directory attributed: %+v", r.BuildErrors)
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

// mavenCompileOutput is `mvn -o -B test` when a branch renamed a method
// another branch still calls (javac through maven-compiler-plugin).
func mavenCompileOutput(dir string) string {
	use := filepath.Join(dir, "core", "src", "main", "java", "com", "shop", "Use.java")
	return "[INFO] --- compiler:3.13.0:compile (default-compile) @ core ---\n" +
		"[INFO] -------------------------------------------------------------\n" +
		"[ERROR] COMPILATION ERROR : \n" +
		"[INFO] -------------------------------------------------------------\n" +
		"[ERROR] " + use + ":[5,12] cannot find symbol\n" +
		"  symbol:   method reserve(java.lang.String)\n" +
		"  location: variable inventory of type com.shop.Inventory\n" +
		"[INFO] 1 error\n" +
		"[ERROR] Failed to execute goal org.apache.maven.plugins:maven-compiler-plugin:3.13.0:compile (default-compile) on project core: Compilation failure\n" +
		"[ERROR] " + use + ":[5,12] cannot find symbol\n" +
		"[ERROR]   symbol:   method reserve(java.lang.String)\n" +
		"[ERROR]   location: variable inventory of type com.shop.Inventory\n"
}

func gradleCompileOutput(dir string) string {
	use := filepath.Join(dir, "core", "src", "main", "java", "com", "shop", "Use.java")
	return "> Task :core:compileJava FAILED\n" +
		use + ":5: error: cannot find symbol\n" +
		"        inventory.reserve(\"a\");\n" +
		"                 ^\n" +
		"  symbol:   method reserve(String)\n" +
		"  location: variable inventory of type Inventory\n" +
		"1 error\n\nFAILURE: Build failed with an exception.\n\n* What went wrong:\n" +
		"Execution failed for task ':core:compileJava'.\n> Compilation failed; see the compiler error output for details.\n"
}

func TestJavaBuildFailureIsSourceFailure(t *testing.T) {
	dir := t.TempDir()
	use := filepath.Join(dir, "core", "src", "main", "java", "com", "shop", "Use.java")
	if e := os.MkdirAll(filepath.Dir(use), 0o755); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(use, []byte("package com.shop;\n"), 0o644); e != nil {
		t.Fatal(e)
	}
	for _, c := range []struct {
		argv   []string
		output string
	}{
		{[]string{"./mvnw", "-o", "-B", "test"}, mavenCompileOutput(dir)},
		{[]string{"mvn", "-o", "test"}, mavenCompileOutput(dir)},
		{[]string{"./gradlew", "--offline", ":core:test"}, gradleCompileOutput(dir)},
	} {
		r := harnessCounts(c.argv, []byte(c.output), dir)
		want := sourceBuildError{Path: "core/src/main/java/com/shop/Use.java", Line: 5, Symbol: "reserve"}
		if len(r.BuildErrors) != 1 || r.BuildErrors[0] != want {
			t.Fatalf("%v: build errors %+v", c.argv, r.BuildErrors)
		}
		if s := outcome(false, 1, r, false, c.argv, []byte(c.output)); s != model.StatusFailed {
			t.Fatalf("%v: classified %s", c.argv, s)
		}
		obs := CandidateObservation{Status: model.StatusFailed, ExitCode: 1}
		decorateCandidateObservation(&obs, c.argv, []byte(c.output), dir, dir, ".")
		if obs.Diagnosis == nil || obs.Diagnosis.Kind != "build_failed" || obs.Diagnosis.Name != "reserve" || len(obs.Locations) != 1 {
			t.Fatalf("%v: diagnosis %+v locations %+v", c.argv, obs.Diagnosis, obs.Locations)
		}
	}
}

func TestJavaBuildFailureEnvironmentNegatives(t *testing.T) {
	dir := t.TempDir()
	compile := mavenCompileOutput(dir)
	cases := map[string]string{
		"offline dependency":  "[ERROR] Failed to execute goal on project core: Could not resolve dependencies for project com.shop:core:jar:1.0: Cannot access central (https://repo.maven.apache.org/maven2) in offline mode\n" + compile,
		"gradle offline":      "> Could not resolve all files for configuration ':core:compileClasspath'.\n   > No cached version of org.junit:junit-bom:5.10.0 available for offline mode.\n" + gradleCompileOutput(dir),
		"external package":    strings.Replace(compile, "cannot find symbol", "package org.apache.commons.lang3 does not exist", 2),
		"toolchain":           "[ERROR] Failed to execute goal org.apache.maven.plugins:maven-compiler-plugin:3.13.0:compile: Fatal error compiling: error: release version 21 not supported\n[ERROR] COMPILATION ERROR : \n",
		"outside directory":   strings.ReplaceAll(compile, dir, "/home/u/.m2/repository/x"),
		"relative escape":     strings.ReplaceAll(compile, dir, ".."),
		"no compile failure":  "[ERROR] " + filepath.Join(dir, "A.java") + ":[1,1] cannot find symbol\n",
		"unknown runner text": compile,
	}
	for name, output := range cases {
		argv := []string{"mvn", "-o", "test"}
		if name == "gradle offline" {
			argv = []string{"gradle", "--offline", "test"}
		}
		if name == "unknown runner text" {
			argv = []string{"make", "test"}
		}
		r := harnessCounts(argv, []byte(output), dir)
		if len(r.BuildErrors) != 0 {
			t.Errorf("%s: attributed to source: %+v", name, r.BuildErrors)
		}
		if s := outcome(false, 1, r, false, argv, []byte(output)); s != model.StatusError {
			t.Errorf("%s: classified %s", name, s)
		}
	}
}

func TestSurefireSummaryAndMissingJVM(t *testing.T) {
	output := "[INFO] Tests run: 2, Failures: 1, Errors: 0, Skipped: 0, Time elapsed: 0.05 s <<< FAILURE! -- in com.shop.InventoryTest\n" +
		"[INFO] Results:\n[INFO] \n[ERROR] Tests run: 3, Failures: 1, Errors: 1, Skipped: 1\n" +
		"[INFO] Results:\n[INFO] \n[INFO] Tests run: 4, Failures: 0, Errors: 0, Skipped: 0\n"
	r := harnessCounts([]string{"./mvnw", "-o", "test"}, []byte(output), "")
	if r.Harness != "maven-surefire" || r.Run != 6 || r.Failed != 2 || r.Skipped != 1 {
		t.Fatalf("surefire totals %+v", r)
	}
	if r := harnessCounts([]string{"./gradlew", "test"}, []byte("BUILD SUCCESSFUL in 3s\n"), ""); r.Run != 0 || r.Harness != "" {
		t.Fatalf("gradle output counted %+v", r)
	}
	for _, argv := range [][]string{{"./gradlew", "test"}, {"./mvnw", "test"}} {
		d := diagnose(argv, 1, []byte("\nERROR: JAVA_HOME is not set and no 'java' command could be found in your PATH.\n"))
		if d == nil || d.Kind != "runner_missing" || d.Name != "java" || !d.Environment() {
			t.Fatalf("%v: missing JVM diagnosis %+v", argv, d)
		}
	}
	if d := diagnose([]string{"python3", "-m", "pytest"}, 1, []byte("JAVA_HOME is not set and no 'java' command could be found")); d != nil {
		t.Fatalf("JVM diagnosis outside a JVM build %+v", d)
	}
}
