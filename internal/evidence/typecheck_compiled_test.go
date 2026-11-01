package evidence

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/Jake-Network/radar/internal/model"
)

func TestCompiledTypecheckDiagnosticBoundaries(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"Main.java", "src/lib.rs", "src/helper.rs"} {
		full := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(full), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "main.cpp"), []byte(""), 0600); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		tool, output string
		count        int
		env, partial bool
	}{
		{"cpp", "main.cpp:3:2: error: missing_name was not declared", 1, false, true},
		{"cpp", "main.cpp:3:2: fatal error: library.h: No such file or directory", 0, true, false},
		{"cpp", "/outside/main.cpp:3:2: error: broken", 0, true, false},
		{"java", "[ERROR] Main.java:[3,4] cannot find symbol", 1, false, true},
		{"java", "Main.java:3: warning: unused", 0, false, false},
		{"java", "Could not resolve dependency", 0, true, false},
		{"cargo", `{"reason":"compiler-message","message":{"message":"wrong type","level":"error","code":{"code":"E0308"},"spans":[{"file_name":"src/lib.rs","line_start":4,"is_primary":true}]}}`, 1, false, true},
		{"cargo", "error: failed to download dependency", 0, true, false},
	} {
		t.Run(c.tool+c.output, func(t *testing.T) {
			p := parseTypecheck(c.tool, []byte(c.output), root, root, ".", TypecheckScope{})
			if len(p.diagnostics) != c.count || (p.environment != nil) != c.env || p.partial != c.partial {
				t.Fatalf("%+v", p)
			}
		})
	}
}

func TestRealCPPSyntaxTypecheck(t *testing.T) {
	if _, err := exec.LookPath("g++"); err != nil {
		t.Skip("g++ unavailable")
	}
	root := t.TempDir()
	for _, name := range []string{"Main.java", "src/lib.rs", "src/helper.rs"} {
		full := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(full), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	file := filepath.Join(root, "main.cpp")
	if err := os.WriteFile(file, []byte("int value(){ return missing_name; }\n"), 0600); err != nil {
		t.Fatal(err)
	}
	obs, err := ObserveTypecheck(context.Background(), root, ".", "cpp", TypecheckScope{}, []string{"g++", "-fsyntax-only", "main.cpp"}, time.Minute)
	if err != nil || obs.Status != model.StatusFailed || obs.DiagnosticCount != 1 {
		t.Fatalf("observation=%+v err=%v", obs, err)
	}
	if err := os.WriteFile(file, []byte("int value(){ return 3; }\n"), 0600); err != nil {
		t.Fatal(err)
	}
	obs, err = ObserveTypecheck(context.Background(), root, ".", "cpp", TypecheckScope{}, []string{"g++", "-fsyntax-only", "main.cpp"}, time.Minute)
	if err != nil || obs.Status != model.StatusPassed {
		t.Fatalf("observation=%+v err=%v", obs, err)
	}
}

func TestCompilerInputInventoryDoesNotInventCoverage(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"Main.java", "src/lib.rs", "src/helper.rs"} {
		full := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(full), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := CompiledTypecheckFiles(root, "cargo"); err == nil {
		t.Fatal("empty build covered source")
	}
	dir := filepath.Join(root, "target", "debug", "deps")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "lib.d"), []byte("target/debug/deps/lib.rmeta: src/lib.rs src/helper.rs\n"), 0600); err != nil {
		t.Fatal(err)
	}
	inventory, err := CompiledTypecheckFiles(root, "cargo")
	if err != nil || !inventory["src/lib.rs"] || inventory["src/unused.rs"] {
		t.Fatalf("%v %v", inventory, err)
	}
}

func TestCPPInputProbeUsesActualHeaders(t *testing.T) {
	if _, err := exec.LookPath("g++"); err != nil {
		t.Skip("g++ unavailable")
	}
	root := t.TempDir()
	for name, src := range map[string]string{"main.cpp": "#include \"api header.hpp\"\nint value(){return answer;}\n", "api header.hpp": "constexpr int answer = 42;\n", "unused.hpp": ""} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(src), 0600); err != nil {
			t.Fatal(err)
		}
	}
	files, err := CPPCompiledFiles(context.Background(), root, ".", []string{"g++", "-fsyntax-only", "main.cpp"}, time.Minute)
	if err != nil || !files["main.cpp"] || !files["api header.hpp"] || files["unused.hpp"] {
		t.Fatalf("files=%v err=%v", files, err)
	}
}

func TestGradleInputObserverIsFreshAndBounded(t *testing.T) {
	root := t.TempDir()
	args := []string{"gradle", "--offline", "testClasses"}
	actual, inventory, artifacts, err := PrepareJVMTypecheck(root, ".", args)
	if err != nil || inventory == "" || len(artifacts) != 2 || len(actual) <= len(args) {
		t.Fatalf("%v %q %v %v", actual, inventory, artifacts, err)
	}
	if _, err := JVMTypecheckFiles(root, inventory); err == nil {
		t.Fatal("empty observer invented coverage")
	}
	src := filepath.Join(root, "Main.java")
	if err := os.WriteFile(src, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(inventory, []byte(src+"\n/outside/Other.java\n"), 0600); err != nil {
		t.Fatal(err)
	}
	files, err := JVMTypecheckFiles(root, inventory)
	if err != nil || len(files) != 1 || !files["Main.java"] {
		t.Fatalf("%v %v", files, err)
	}
	_, next, _, err := PrepareJVMTypecheck(root, ".", args)
	if err != nil || next == inventory {
		t.Fatalf("observer reused %q err %v", next, err)
	}
	if _, err := JVMTypecheckFiles(root, next); err == nil {
		t.Fatal("prior command inputs reused")
	}
}

func TestCargoCompilerCacheIsBoundToExactSourceTree(t *testing.T) {
	root := t.TempDir()
	t.Setenv("RADAR_STATE_DIR", t.TempDir())
	git(t, root, "init", "-q")
	if err := os.WriteFile(filepath.Join(root, "Cargo.toml"), []byte("[package]\nname=\"example\"\nversion=\"0.1.0\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	git(t, root, "add", ".")
	git(t, root, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-qm", "base")
	first, err := PrepareCargoTarget(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	same, err := PrepareCargoTarget(context.Background(), root)
	if err != nil || first != same {
		t.Fatalf("%s %s %v", first, same, err)
	}
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	git(t, root, "add", ".")
	git(t, root, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-qm", "next")
	next, err := PrepareCargoTarget(context.Background(), root)
	if err != nil || next == first {
		t.Fatalf("%s %s %v", first, next, err)
	}
	if _, err := (TypecheckOptions{CargoTargetDir: next}).Environment("java"); err == nil {
		t.Fatal("Cargo setting applied to another tool")
	}
	env, err := (TypecheckOptions{CargoTargetDir: next}).Environment("cargo")
	if err != nil || len(env) != 1 {
		t.Fatalf("%v %v", env, err)
	}
}

func TestCargoInventoryIgnoresArtifactsNotReportedByThisRun(t *testing.T) {
	root := t.TempDir()
	cache := t.TempDir()
	for _, name := range []string{"checked.rs", "unchecked.rs"} {
		if err := os.WriteFile(filepath.Join(root, name), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	for name, src := range map[string]string{"checked.d": "checked.rs", "stale.d": "unchecked.rs"} {
		if err := os.WriteFile(filepath.Join(cache, name), []byte("out: "+filepath.Join(root, src)+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	files, err := CargoArtifactFiles(root, cache, []string{filepath.Join(cache, "libchecked.rmeta")})
	if err != nil || !files["checked.rs"] || files["unchecked.rs"] {
		t.Fatalf("files=%v err=%v", files, err)
	}
	if _, err := CargoArtifactFiles(root, cache, nil); err == nil {
		t.Fatal("unreported warm artifacts established coverage")
	}
}

func TestMavenInventoryMustBeProducedByCurrentInvocation(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init", "-q")
	if err := os.WriteFile(filepath.Join(root, "pom.xml"), []byte("<project/>"), 0600); err != nil {
		t.Fatal(err)
	}
	git(t, root, "add", ".")
	git(t, root, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-qm", "base")
	dir := filepath.Join(root, "target", "maven-status", "compiler")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "inputFiles.lst")
	if err := os.WriteFile(file, []byte("stale"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := ClearMavenTypecheckInventory(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatalf("stale list remains: %v", err)
	}
	if err := os.WriteFile(file, []byte("committed"), 0600); err != nil {
		t.Fatal(err)
	}
	git(t, root, "add", ".")
	git(t, root, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-qm", "committed inventory")
	if err := ClearMavenTypecheckInventory(context.Background(), root); err == nil {
		t.Fatal("committed inventory was accepted")
	}
	b, err := os.ReadFile(file)
	if err != nil || string(b) != "committed" {
		t.Fatalf("tracked file changed %q %v", b, err)
	}
}

func TestCPPSystemIncludeCannotShadowCandidateThroughExternalSymlink(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "api.hpp"), []byte("inline int answer(){return 1;}"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.cpp"), []byte("#include <api.hpp>\nint value(){return answer();}"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "deps")); err != nil {
		t.Skip("symlink unavailable")
	}
	if _, err := CPPCompiledFiles(context.Background(), root, ".", []string{"g++", "-isystem", "deps", "main.cpp", "-fsyntax-only"}, time.Minute); err == nil {
		t.Fatal("external system include shadow was accepted")
	}
}
