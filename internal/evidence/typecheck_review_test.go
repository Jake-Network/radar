package evidence

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCompiledTypecheckSourceAndEnvironmentBoundaries(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"src/lib.rs", "src/a.c", "Cargo.lock"} {
		full := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(full), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	located := `{"reason":"compiler-message","message":{"message":"mismatched types","level":"error","code":{"code":"E0308"},"spans":[{"file_name":"src/lib.rs","line_start":4,"is_primary":true}]}}`
	abort := func(msg string) string {
		return `{"reason":"compiler-message","message":{"message":"` + msg + `","level":"error","code":null,"spans":[]}}`
	}
	deleted := []string{"src/util.h"}
	for _, c := range []struct {
		name, tool, output string
		deleted            []string
		count              int
		includeDirs        []string
		env, crashed       bool
	}{
		{"rustc abort summary is not a crash", "cargo", located + "\n" + abort("aborting due to 1 previous error"), nil, 1, nil, false, false},
		{"plural abort summary with warnings", "cargo", located + "\n" + abort("aborting due to 2 previous errors; 1 warning emitted"), nil, 1, nil, false, false},
		{"other unlocated error still crashes", "cargo", located + "\n" + abort("linking with `cc` failed"), nil, 1, nil, false, true},
		{"abort summary with a span is not trusted", "cargo", `{"reason":"compiler-message","message":{"message":"aborting due to 1 previous error","level":"error","spans":[{"file_name":"/rustc/x.rs","line_start":1,"is_primary":true}]}}`, nil, 0, nil, false, true},
		{"stale repository lock file is source", "cargo", "error: the lock file " + filepath.Join(root, "Cargo.lock") + " needs to be updated but --locked was passed to prevent this", nil, 1, nil, false, false},
		{"stale lock outside repository is environment", "cargo", "error: the lock file /elsewhere/Cargo.lock needs to be updated but --locked was passed to prevent this", nil, 0, nil, true, false},
		{"other lock file trouble is environment", "cargo", "error: failed to parse lock file at: " + filepath.Join(root, "Cargo.lock"), nil, 0, nil, true, false},
		{"deleted repository header is source (gcc)", "cpp", "src/a.c:1:10: fatal error: util.h: No such file or directory", deleted, 1, nil, false, false},
		{"deleted repository header is source (clang)", "cpp", "src/a.c:1:10: fatal error: 'util.h' file not found", deleted, 1, nil, false, false},
		{"nested include operand resolves through -I", "cpp", "src/a.c:1:10: fatal error: src/util.h: No such file or directory", deleted, 1, []string{"."}, false, false},
		{"nested include operand without its search directory", "cpp", "src/a.c:1:10: fatal error: src/util.h: No such file or directory", deleted, 0, nil, true, false},
		{"same name deleted elsewhere is environment", "cpp", "src/a.c:1:10: fatal error: config.h: No such file or directory", []string{"legacy/config.h"}, 0, nil, true, false},
		{"same name deleted in a searched directory is source", "cpp", "src/a.c:1:10: fatal error: config.h: No such file or directory", []string{"legacy/config.h"}, 1, []string{"legacy"}, false, false},
		{"system header missing is environment", "cpp", "src/a.c:1:10: fatal error: openssl/ssl.h: No such file or directory", deleted, 0, nil, true, false},
		{"basename prefix is not a match", "cpp", "src/a.c:1:10: fatal error: til.h: No such file or directory", deleted, 0, nil, true, false},
		{"no deletions keeps environment", "cpp", "src/a.c:1:10: fatal error: util.h: No such file or directory", nil, 0, nil, true, false},
		{"escaping operand is environment", "cpp", "src/a.c:1:10: fatal error: ../util.h: No such file or directory", []string{"util.h"}, 0, nil, true, false},
		{"includer outside repository is environment", "cpp", "/usr/include/x.h:1:10: fatal error: util.h: No such file or directory", deleted, 0, nil, true, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			p := parseTypecheck(c.tool, []byte(c.output), root, root, ".", TypecheckScope{Deleted: c.deleted, IncludeDirs: c.includeDirs})
			if len(p.diagnostics) != c.count || (p.environment != nil) != c.env || p.crashed != c.crashed {
				t.Fatalf("%+v", p)
			}
		})
	}
}

func TestCargoStaleLockAdviceIsNotEnvironment(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "Cargo.lock"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	lock := filepath.Join(root, "Cargo.lock")
	for _, out := range []string{
		"error: the lock file " + lock + " needs to be updated but --locked was passed to prevent this\nIf you want to try to generate the lock file without accessing the network, remove the --locked flag and use --offline instead.",
		"error: cannot update the lock file " + lock + " because --locked was passed to prevent this\nhelp: to generate the lock file without accessing the network, remove the --locked flag and use --offline instead.",
	} {
		p := parseTypecheck("cargo", []byte(out), root, root, ".", TypecheckScope{})
		if len(p.diagnostics) != 1 || p.environment != nil {
			t.Fatalf("%q: %+v", out, p)
		}
	}
	// The advice alone, without the located error, is not a source failure.
	p := parseTypecheck("cargo", []byte("If you want to try to generate the lock file without accessing the network, remove the --locked flag"), root, root, ".", TypecheckScope{})
	if len(p.diagnostics) != 0 || p.environment == nil {
		t.Fatalf("%+v", p)
	}
}
