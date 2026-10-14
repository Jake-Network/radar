package symbols

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/Jake-Network/radar/internal/model"
)

func TestRealMergedNamedReferences(t *testing.T) {
	cases := []struct {
		name                                   string
		files                                  map[string]string
		producer, renamed, consumer, reference string
	}{
		{"python", map[string]string{"lib.py": "def foo():\n    pass\n"}, "lib.py", "def bar():\n    pass\n", "new.py", "from lib import foo\nfoo()\n"},
		{"typescript", map[string]string{"lib.ts": "export function foo() {}\n"}, "lib.ts", "export function bar() {}\n", "new.ts", "import { foo } from './lib';\nfoo();\n"},
		{"typescript-barrel", map[string]string{"lib.ts": "export function foo() {}\n", "barrel.ts": "export { foo as exported } from './lib';\n"}, "lib.ts", "export function bar() {}\n", "new.ts", "import { exported } from './barrel';\nexported();\n"},
		{"javascript", map[string]string{"lib.js": "export function foo() {}\n"}, "lib.js", "export function bar() {}\n", "new.js", "import { foo } from './lib';\nfoo();\n"},
		{"go", map[string]string{"go.mod": "module example\n\ngo 1.23\n", "lib/lib.go": "package lib\nfunc Foo() {}\n"}, "lib/lib.go", "package lib\nfunc Bar() {}\n", "main.go", "package main\nimport \"example/lib\"\nfunc main() { lib.Foo() }\n"},
		{"java", map[string]string{"svc/Foo.java": "package svc;\npublic class Foo {}\n"}, "svc/Foo.java", "package svc;\nclass Bar {}\n", "Main.java", "import svc.Foo;\nclass Main { Foo f; }\n"},
		{"rust", map[string]string{"src/lib.rs": "pub mod helper;\n", "src/helper.rs": "pub fn foo() {}\n", "Cargo.toml": "[package]\nname = \"sample\"\nversion = \"0.1.0\"\n"}, "src/helper.rs", "pub fn bar() {}\n", "src/new.rs", "use crate::helper::foo;\nfn run() { foo(); }\n"},
		{"cpp", map[string]string{"lib.h": "void foo();\n"}, "lib.h", "void bar();\n", "new.cpp", "#include \"lib.h\"\nvoid run() { foo(); }\n"},
		{"python-arity", map[string]string{"lib.py": "def foo(x):\n    pass\n"}, "lib.py", "def foo(x,y):\n    pass\n", "new.py", "from lib import foo\nfoo(1)\n"},
		{"typescript-arity", map[string]string{"lib.ts": "export function foo(x: number) {}\n"}, "lib.ts", "export function foo(x: number,y: number) {}\n", "new.ts", "import {\n foo\n} from './lib';\nfoo(1);\n"},
		{"javascript-arity", map[string]string{"lib.js": "export function foo(x) {}\n"}, "lib.js", "export function foo(x,y) {}\n", "new.js", "import { foo } from './lib';\nfoo(1);\n"},
		{"go-arity", map[string]string{"go.mod": "module example\n\ngo 1.23\n", "lib/lib.go": "package lib\nfunc Foo(x int) {}\n"}, "lib/lib.go", "package lib\nfunc Foo(x,y int) {}\n", "main.go", "package main\nimport \"example/lib\"\nfunc main() { lib.Foo(1) }\n"},
		{"go-package-alias", map[string]string{"go.mod": "module example\n\ngo 1.23\n", "sdk/v2/lib.go": "package sdk\nfunc Foo() {}\n"}, "sdk/v2/lib.go", "package sdk\nfunc Bar() {}\n", "main.go", "package main\nimport \"example/sdk/v2\"\nfunc main() { sdk.Foo() }\n"},
		{"go-deleted-file", map[string]string{"go.mod": "module example\n\ngo 1.23\n", "lib/foo.go": "package lib\nfunc Foo() {}\n", "lib/other.go": "package lib\nfunc Bar() {}\n"}, "lib/foo.go", "", "main.go", "package main\nimport \"example/lib\"\nfunc main() { lib.Foo() }\n"},
		{"java-arity", map[string]string{"svc/Foo.java": "package svc; public class Foo { public static void run(int x) {} }\n"}, "svc/Foo.java", "package svc; public class Foo { public static void run(int x,int y) {} }\n", "Main.java", "import svc.Foo;\nclass Main { void run() { Foo.run(1); } }\n"},
		{"rust-arity", map[string]string{"src/lib.rs": "pub mod helper;\n", "src/helper.rs": "pub fn foo(x: i32) {}\n", "Cargo.toml": "[package]\nname = \"sample\"\nversion = \"0.1.0\"\n"}, "src/helper.rs", "pub fn foo(x: i32,y: i32) {}\n", "src/new.rs", "use crate::helper::foo;\nfn run() { foo(1); }\n"},
		{"rust-include-duplicate", map[string]string{"src/lib.rs": "include!(\"a.rs\");\ninclude!(\"b.rs\");\n", "src/a.rs": "fn a() {}\n", "src/b.rs": "fn b() {}\n", "Cargo.toml": "[package]\nname = \"sample\"\nversion = \"0.1.0\"\n"}, "src/a.rs", "fn a() {}\nfn foo() {}\n", "src/b.rs", "fn b() {}\nfn foo() {}\n"},
		{"go-duplicate", map[string]string{"go.mod": "module example\n\ngo 1.23\n", "lib/base.go": "package lib\nfunc Base() {}\n"}, "lib/a.go", "package lib\nfunc Foo() {}\n", "lib/b.go", "package lib\nfunc Foo() {}\n"},
		{"java-duplicate", map[string]string{"svc/Base.java": "package svc; class Base {}\n"}, "svc/a.java", "package svc; class Foo {}\n", "svc/b.java", "package svc; class Foo {}\n"},
		{"cpp-arity", map[string]string{"lib.h": "void foo(int x);\n"}, "lib.h", "void foo(int x,int y);\n", "new.cpp", "#include \"lib.h\"\nvoid run() { foo(1); }\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			git := func(args ...string) string {
				t.Helper()
				cmd := exec.Command("git", append([]string{"-c", "user.name=fixture", "-c", "user.email=fixture@example.test", "-c", "core.hooksPath=/dev/null"}, args...)...)
				cmd.Dir = root
				cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
				out, e := cmd.CombinedOutput()
				if e != nil {
					t.Fatalf("git %v: %s: %v", args, out, e)
				}
				return string(out)
			}
			write := func(p, text string) {
				t.Helper()
				target := filepath.Join(root, p)
				if e := os.MkdirAll(filepath.Dir(target), 0755); e != nil {
					t.Fatal(e)
				}
				if e := os.WriteFile(target, []byte(text), 0644); e != nil {
					t.Fatal(e)
				}
			}
			git("init", "-q", "-b", "main")
			for p, text := range c.files {
				write(p, text)
			}
			git("add", ".")
			git("commit", "-qm", "base")
			git("branch", "base")
			git("checkout", "-qb", "rename")
			if c.renamed == "" {
				if e := os.Remove(filepath.Join(root, c.producer)); e != nil {
					t.Fatal(e)
				}
			} else {
				write(c.producer, c.renamed)
			}
			git("add", ".")
			git("commit", "-qm", "rename")
			git("checkout", "-qb", "consumer", "base")
			write(c.consumer, c.reference)
			git("add", ".")
			git("commit", "-qm", "consumer")
			git("merge", "--no-ff", "--no-edit", "rename")
			r, e := Analyze(context.Background(), root, "base", nil, "HEAD", []string{c.producer, c.consumer})
			if e != nil {
				t.Fatal(e)
			}
			if r.Status != model.StatusFailed {
				t.Fatalf("expected dangling name failure: %+v", r)
			}
		})
	}
}
