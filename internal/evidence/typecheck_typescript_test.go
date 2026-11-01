package evidence

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Jake-Network/radar/internal/model"
)

func tsPut(t *testing.T, root, file, content string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(file))
	if err := os.MkdirAll(filepath.Dir(full), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestParseTypeScriptTypecheck(t *testing.T) {
	root := t.TempDir()
	tsPut(t, root, "tsconfig.json", `{"compilerOptions":{"baseUrl":".","paths":{"@app/*":["src/*"],"@external/*":["../outside/*"]}}}`)
	tsPut(t, root, "src/a.ts", "")
	tsPut(t, root, "pkg/p.ts", "")
	cases := []struct {
		name, output, cwd string
		paths             []string
		env               bool
		noInputs          bool
	}{
		{name: "property error", output: "src/a.ts(3,5): error TS2339: Property 'name' does not exist on type 'User'.\n", paths: []string{"src/a.ts"}},
		{name: "relative missing source", output: "src/a.ts(1,5): error TS2307: Cannot find module './gone' or its corresponding type declarations.\n", paths: []string{"src/a.ts"}},
		{name: "alias missing source", output: "src/a.ts(1,5): error TS2307: Cannot find module '@app/gone' or its corresponding type declarations.\n", paths: []string{"src/a.ts"}},
		{name: "bare package missing", output: "src/a.ts(3,5): error TS2339: Property 'name' does not exist on type 'User'.\nsrc/a.ts(1,5): error TS2307: Cannot find module 'redis' or its corresponding type declarations.\n", env: true},
		{name: "external alias missing", output: "src/a.ts(1,5): error TS2307: Cannot find module '@external/gone' or its corresponding type declarations.\n", env: true},
		{name: "missing types", output: "error TS2688: Cannot find type definition file for 'node'.\n", env: true},
		{name: "outside source", output: "/external/cache/broken.d.ts(1,1): error TS2304: Cannot find name 'X'.\n", env: true},
		{name: "empty project", output: "error TS18003: No inputs were found in config file 'tsconfig.json'.\n", noInputs: true},
		{name: "sibling inside repository", output: "../src/a.ts(3,5): error TS2339: Property 'name' does not exist on type 'User'.\n", cwd: "pkg", paths: []string{"src/a.ts"}},
		{name: "windows separators", output: "src\\a.ts(3,5): error TS2339: Property 'name' does not exist on type 'User'.\r\n", paths: []string{"src/a.ts"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := root
			if tc.cwd != "" {
				dir = filepath.Join(root, tc.cwd)
			}
			parsed := parseTypecheck("tsc", []byte(tc.output), root, dir, tc.cwd, TypecheckScope{})
			var paths []string
			for _, diag := range parsed.diagnostics {
				paths = append(paths, diag.Path)
			}
			if !reflect.DeepEqual(paths, tc.paths) || (parsed.environment != nil) != tc.env || parsed.noInputs != tc.noInputs {
				t.Fatalf("parse=%+v paths=%v", parsed, paths)
			}
		})
	}
}

func TestTypeScriptFingerprintKeepsCodeAndMessage(t *testing.T) {
	root := t.TempDir()
	tsPut(t, root, "a.ts", "")
	parse := func(line string) TypecheckDiagnostic {
		p := parseTypecheck("tsc", []byte(line), root, root, ".", TypecheckScope{})
		if len(p.diagnostics) != 1 {
			t.Fatalf("parse: %+v", p)
		}
		return p.diagnostics[0]
	}
	one := parse("a.ts(1,2): error TS2339: Property 'old' does not exist on type 'User'.")
	shifted := parse("a.ts(21,9): error TS2339: Property 'old' does not exist on type 'User'.")
	changed := parse("a.ts(1,2): error TS2339: Property 'other' does not exist on type 'User'.")
	code := parse("a.ts(1,2): error TS2551: Property 'old' does not exist on type 'User'.")
	if one.Fingerprint != shifted.Fingerprint || one.Fingerprint == changed.Fingerprint || one.Fingerprint == code.Fingerprint {
		t.Fatal("fingerprint loses message/code or tracks position")
	}
	x := parse("a.ts(1,2): error TS2322: Type 'A' is not assignable to type 'B'.\n  Types of property 'x' are incompatible.\n")
	y := parse("a.ts(1,2): error TS2322: Type 'A' is not assignable to type 'B'.\n  Types of property 'y' are incompatible.\n")
	if x.Fingerprint == y.Fingerprint {
		t.Fatal("multiline error detail was lost from fingerprint")
	}
}

func TestTypeScriptImportsUseCommandConfiguration(t *testing.T) {
	root := t.TempDir()
	tsPut(t, root, "src/a.ts", "")
	tsPut(t, root, "tsconfig.json", `{"compilerOptions":{"paths":{"alias":["../external"]}}}`)
	tsPut(t, root, "tsconfig.check.json", `{"compilerOptions":{"paths":{"alias":["src/deleted"]}}}`)
	output := []byte("src/a.ts(1,1): error TS2307: Cannot find module 'alias' or its corresponding type declarations.\n")
	parsed := parseTypecheck("tsc", output, root, root, ".", TypecheckScope{TypeScriptConfig: "tsconfig.check.json"})
	if parsed.environment != nil || len(parsed.diagnostics) != 1 {
		t.Fatalf("actual project ignored: %+v", parsed)
	}
	project := typeScriptProject(root, ".", []string{"node", "node_modules/typescript/bin/tsc", "--project", "tsconfig.check.json"})
	if project != "tsconfig.check.json" {
		t.Fatalf("project=%q", project)
	}
}

func TestResolveTypeScriptCommandUsesOnlyCandidatePackages(t *testing.T) {
	root := t.TempDir()
	tsPut(t, root, "pkg/tsconfig.json", "{}")
	tsPut(t, root, "node_modules/typescript/bin/tsc", "")
	argv := []string{"node", "node_modules/typescript/bin/tsc", "--noEmit", "-p", "tsconfig.json"}
	resolved, err := ResolveTypeScriptCommand(root, "pkg", argv)
	if err != nil || resolved[1] != "../node_modules/typescript/bin/tsc" {
		t.Fatalf("resolved=%v err=%v", resolved, err)
	}
	if argv[1] != "node_modules/typescript/bin/tsc" {
		t.Fatal("mutated input command")
	}
	outside := t.TempDir()
	tsPut(t, outside, "typescript/bin/tsc", "")
	if err := os.Symlink(filepath.Join(outside, "typescript"), filepath.Join(root, "pkg", "node_modules")); err == nil { // A symlink at a candidate dependency path cannot escape it.
		if _, err := ResolveTypeScriptCommand(root, "pkg", argv); err == nil {
			t.Fatal("external dependency path accepted")
		}
	}
}

func TestTypeScriptPathsUsePhysicalCandidateRoots(t *testing.T) {
	root := t.TempDir()
	tsPut(t, root, "pkg/tsconfig.json", "{}")
	tsPut(t, root, "pkg/a.ts", "")
	tsPut(t, root, "node_modules/typescript/bin/tsc", "")
	alias := filepath.Join(t.TempDir(), "candidate")
	if err := os.Symlink(root, alias); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	command, err := ResolveTypeScriptCommand(alias, "pkg", []string{"node", "node_modules/typescript/bin/tsc", "-p", "tsconfig.json"})
	if err != nil || command[1] != "../node_modules/typescript/bin/tsc" {
		t.Fatalf("physical command=%v err=%v", command, err)
	}
	if file, ok := typeScriptSourcePath(alias, filepath.Join(alias, "pkg"), filepath.Join(root, "pkg", "a.ts")); !ok || file != "pkg/a.ts" {
		t.Fatalf("physical path=%q inside=%t", file, ok)
	}
}

func realTypeScript(t *testing.T, root string) {
	t.Helper()
	if _, err := exec.LookPath("node"); err != nil {
		if os.Getenv("RADAR_TEST_TYPESCRIPT_DIR") != "" {
			t.Fatal("configured TypeScript tests require node")
		}
		t.Skip("node unavailable")
	}
	source := os.Getenv("RADAR_TEST_TYPESCRIPT_DIR")
	if source == "" {
		source = "/tmp/radar-typescript-toolchain/node_modules/typescript"
	}
	if _, err := os.Stat(filepath.Join(source, "lib", "typescript.js")); err != nil {
		if os.Getenv("RADAR_TEST_TYPESCRIPT_DIR") != "" {
			t.Fatalf("configured TypeScript compiler unavailable: %v", err)
		}
		t.Skip("real TypeScript not installed; set RADAR_TEST_TYPESCRIPT_DIR")
	}
	err := filepath.WalkDir(source, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(source, p)
		if err != nil {
			return err
		}
		dest := filepath.Join(root, "node_modules", "typescript", rel)
		if d.IsDir() {
			return os.MkdirAll(dest, 0700)
		}
		content, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(dest, content, 0600)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestTypeScriptRealInventoryAndDiagnostics(t *testing.T) {
	root := t.TempDir()
	realTypeScript(t, root)
	tsPut(t, root, "tsconfig.json", `{"compilerOptions":{"strict":true,"types":[]},"include":["src/**/*"]}`)
	tsPut(t, root, "src/model.ts", "export interface User { fullName: string }\n")
	tsPut(t, root, "src/caller.ts", "import { User } from './model'; export const name = (user: User) => user.name;\n")
	tsPut(t, root, "excluded.ts", "const broken: number = 'x';\n")
	argv := []string{"node", "node_modules/typescript/bin/tsc", "--noEmit", "--pretty", "false", "-p", "tsconfig.json"}
	sources, err := TypeScriptSources(context.Background(), root, ".", argv, time.Minute)
	if err != nil || !sources["src/caller.ts"] || !sources["src/model.ts"] || sources["excluded.ts"] {
		t.Fatalf("sources=%v err=%v", sources, err)
	}
	observed, err := ObserveTypecheck(context.Background(), root, ".", "tsc", TypecheckScope{}, argv, time.Minute)
	if err != nil || observed.Status != model.StatusFailed || observed.DiagnosticCount != 1 || observed.Diagnostics[0].Path != "src/caller.ts" {
		t.Fatalf("observation=%+v err=%v", observed, err)
	}
	for _, flag := range []string{"--showConfig", "--listFilesOnly", "--version", "--help", "--watch"} {
		if _, err := TypeScriptSources(context.Background(), root, ".", append(append([]string(nil), argv...), flag), time.Minute); err == nil {
			t.Fatalf("non-checking compiler mode %s counted as coverage", flag)
		}
	}
	tsPut(t, root, "src/unchecked.ts", "// @ts-nocheck\nconst unchecked: number = 'x';\n")
	tsPut(t, root, "src/declaration.d.ts", "declare const unknown: Unavailable;\n")
	tsPut(t, root, "tsconfig.json", `{"compilerOptions":{"skipLibCheck":true,"types":[]},"include":["src/**/*"]}`)
	sources, err = TypeScriptSources(context.Background(), root, ".", argv, time.Minute)
	if err != nil || sources["src/unchecked.ts"] || sources["src/declaration.d.ts"] || !sources["src/model.ts"] {
		t.Fatalf("suppressed source counted as checked: %v %v", sources, err)
	}
	tsPut(t, root, "tsconfig.json", `{"compilerOptions":{"noCheck":true,"types":[]},"include":["src/**/*"]}`)
	if _, err := TypeScriptSources(context.Background(), root, ".", argv, time.Minute); err == nil {
		t.Fatal("disabled type checking counted as coverage")
	}
	tsPut(t, root, "tsconfig.json", `{"compilerOptions":{"composite":true,"types":[]},"include":["src/**/*"]}`)
	if _, err := TypeScriptSources(context.Background(), root, ".", argv, time.Minute); err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Fatalf("composite inventory: %v", err)
	}
}
