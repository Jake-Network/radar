package typecheck

import (
	"context"
	"reflect"
	"testing"
)

func TestCompiledSelectionUsesWholeBuildUnits(t *testing.T) {
	files := tree{"Cargo.toml": "[workspace]\nmembers=[\"core\",\"api\"]", "core/Cargo.toml": "[package]\nname=\"core\"", "core/src/lib.rs": "", "api/Cargo.toml": "[package]\nname=\"api\"", "pom.xml": "<project/>", "mod/pom.xml": "<project/>", "mod/src/main/java/Main.java": ""}
	p, err := Select(context.Background(), files, []string{"core/src/lib.rs", "mod/src/main/java/Main.java"})
	if err != nil {
		t.Fatal(err)
	}
	roots := map[string]string{}
	for _, target := range p.Targets {
		roots[target.Tool] = target.Root
	}
	if !reflect.DeepEqual(roots, map[string]string{"cargo": ".", "java": "."}) {
		t.Fatalf("%+v", p)
	}
}

func TestSyntaxOnlyRejectsUnsupportedCommands(t *testing.T) {
	for _, args := range [][]string{nil, {"sh", "-c", "g++ x.cpp"}, {"clang++", "@flags"}, {"cl.exe", "x.cpp"}, {"clang++", "../x.cpp"}} {
		if _, ok := syntaxOnly(args); ok {
			t.Fatalf("accepted %q", args)
		}
	}
	got, ok := syntaxOnly([]string{"/usr/bin/g++", "-std=c++20", "-c", "main.cpp", "-o", "main.o", "-MMD", "-MF", "main.d"})
	want := []string{"g++", "-std=c++20", "main.cpp", "-fsyntax-only"}
	if !ok || !reflect.DeepEqual(got, want) {
		t.Fatalf("%q %v", got, ok)
	}
}

func TestCCCommandStringDoesNotExecuteOrPass(t *testing.T) {
	p, err := Select(context.Background(), tree{"main.cpp": "", "compile_commands.json": `[{"directory":".","file":"main.cpp","command":"g++ main.cpp; touch surprise"}]`}, []string{"main.cpp"})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Targets) != 0 || len(p.Gaps) != 1 {
		t.Fatalf("%+v", p)
	}
}

func TestGradleLiteralConsumerModulesAreAllCompiled(t *testing.T) {
	files := tree{"settings.gradle.kts": "rootProject.name = \"service\"\ninclude(\"core\", \"api\")\n", "core/build.gradle.kts": "plugins { java }", "api/build.gradle.kts": "plugins { java }", "core/src/main/java/Main.java": ""}
	p, err := Select(context.Background(), files, []string{"core/src/main/java/Main.java"})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Targets) != 1 || !reflect.DeepEqual(p.Targets[0].Command, []string{"gradle", "--offline", "--no-daemon", "--console=plain", ":api:testClasses", ":core:testClasses"}) {
		t.Fatalf("%+v", p)
	}
	files["settings.gradle.kts"] = "include(projectName)"
	p, err = Select(context.Background(), files, []string{"core/src/main/java/Main.java"})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Targets) != 0 || len(p.Gaps) != 1 {
		t.Fatalf("%+v", p)
	}
}

func TestPreprocessorAndQueryModesCannotBecomeSyntaxChecks(t *testing.T) {
	for _, flag := range []string{"-E", "-S", "--help", "--version", "-###", "-print-search-dirs", "-fdirectives-only", "-fpermissive"} {
		if _, ok := CanonicalCPPCommand([]string{"g++", flag, "-c", "main.cpp"}); ok {
			t.Fatalf("accepted non-checking or weakened mode %s", flag)
		}
	}
	p, err := Select(context.Background(), tree{"main.cpp": "int value(){return undefined;}", "compile_commands.json": `[{"directory":".","file":"main.cpp","arguments":["g++","-E","-c","main.cpp"]}]`}, []string{"main.cpp"})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Targets) != 0 || len(p.Gaps) != 1 {
		t.Fatalf("preprocess-only invocation was selected: %+v", p)
	}
}

func TestCPPRejectsUnmodelledSearchPaths(t *testing.T) {
	for _, args := range [][]string{{"g++", "-I=/tmp/stale", "main.cpp"}, {"g++", "-I", "=/tmp/stale", "main.cpp"}, {"g++", "-include-pch", "stale.pch", "main.cpp"}, {"g++", "-idirafter/tmp/stale", "main.cpp"}, {"g++", "-imacros/tmp/stale.h", "main.cpp"}, {"g++", "-Wp,-I/tmp/stale", "main.cpp"}, {"g++", "-Xclang", "-internal-isystem", "/tmp/stale", "main.cpp"}} {
		if _, ok := CanonicalCPPCommand(args); ok {
			t.Fatalf("unmodelled search path accepted: %q", args)
		}
	}
}

func TestCPPClassificationIncludesUppercaseSourcesAndHeaders(t *testing.T) {
	if !CPPSource("src/main.C") || !CPPHeader("include/api.H") {
		t.Fatal("uppercase compiler input omitted")
	}
}
