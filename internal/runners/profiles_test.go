package runners

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
)

func TestProfilesValidateAndBindCache(t *testing.T) {
	p := Profile{Root: "apps/api", Prefix: []string{"uv", "run", "--frozen"}, Setup: [][]string{{"uv", "sync", "--frozen"}}, Lockfiles: []string{"uv.lock"}, Artifacts: []string{".venv"}}
	first, err := CacheKey(p, "repo", map[string][]byte{"uv.lock": []byte("one")})
	if err != nil {
		t.Fatal(err)
	}
	second, _ := CacheKey(p, "repo", map[string][]byte{"uv.lock": []byte("two")})
	if first == second {
		t.Fatal("lockfile did not invalidate cache")
	}
	p.Exclude = []string{"-m", "not integration"}
	third, _ := CacheKey(p, "repo", map[string][]byte{"uv.lock": []byte("one")})
	if first == third {
		t.Fatal("profile did not invalidate cache")
	}
	if _, err := CacheKey(p, "repo", nil); err == nil {
		t.Fatal("missing lockfile accepted")
	}
	for _, bad := range []string{`{"version":2,"profiles":[]}`, `{"version":1,"profiles":[{"root":"../out","prefix":["uv"]}]}`, `{"version":1,"profiles":[{"root":".","prefix":["/bin/sh"]}]}`, `{"version":1,"profiles":[{"root":".","prefix":["uv"],"artifacts":["secrets"],"lockfiles":["uv.lock"]}]}`, `{"version":1,"profiles":[],"unknown":true}`} {
		if _, err := Parse([]byte(bad)); err == nil {
			t.Fatalf("accepted %s", bad)
		}
	}
	argv := p.Rewrite([]string{"python3", "-m", "pytest", "test.py"})
	if !reflect.DeepEqual(argv, []string{"uv", "run", "--frozen", "python3", "-m", "pytest", "test.py", "-m", "not integration"}) {
		t.Fatal(argv)
	}
	pnpm := Profile{Root: ".", Prefix: []string{"pnpm", "exec"}}
	if got := pnpm.Rewrite([]string{"./node_modules/.bin/jest", "file.test.js"}); !reflect.DeepEqual(got, []string{"pnpm", "exec", "jest", "file.test.js"}) {
		t.Fatal(got)
	}
}

func TestCacheRejectsEscapeSecretsAndPrunes(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	cache := Cache{Dir: t.TempDir(), MaxBytes: 8}
	p := Profile{Root: ".", Prefix: []string{"cargo"}, Lockfiles: []string{"Cargo.lock"}, Artifacts: []string{"target"}}
	os.Mkdir(filepath.Join(root, "target"), 0700)
	os.WriteFile(filepath.Join(root, "target", "data"), []byte("123456"), 0600)
	first, _ := CacheKey(p, "repo", map[string][]byte{"Cargo.lock": []byte("1")})
	second, _ := CacheKey(p, "repo", map[string][]byte{"Cargo.lock": []byte("2")})
	if err := cache.Store(ctx, first, root, p); err != nil {
		t.Fatal(err)
	}
	if err := cache.Store(ctx, second, root, p); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(cache.Dir, first)); !os.IsNotExist(err) {
		t.Fatal("LRU entry retained over budget")
	}
	restored := t.TempDir()
	hit, err := cache.Restore(ctx, second, restored, p)
	if err != nil || !hit {
		t.Fatal(hit, err)
	}
	if _, err := cache.Restore(ctx, second, restored, p); err == nil {
		t.Fatal("overwrote existing artifact")
	}
	outside := filepath.Join(t.TempDir(), "secret")
	os.WriteFile(outside, []byte("secret"), 0600)
	os.Symlink(outside, filepath.Join(root, "target", "leak"))
	third, _ := CacheKey(p, "repo", map[string][]byte{"Cargo.lock": []byte("3")})
	if err := cache.Store(ctx, third, root, p); err == nil {
		t.Fatal("cached external symlink")
	}
	os.Remove(filepath.Join(root, "target", "leak"))
	os.WriteFile(filepath.Join(root, "target", ".env"), []byte("KEY=secret"), 0600)
	if err := cache.Store(ctx, third, root, p); err == nil {
		t.Fatal("cached secret file")
	}
}

func TestPythonExecutablePrefixReplacesInterpreter(t *testing.T) {
	for _, tc := range []struct {
		prefix, original string
		replace          bool
	}{
		{".venv/Scripts/python.exe", "python3", true},
		{".venv/Scripts/python.exe", "python.exe", true},
		{".venv/Scripts/PYTHON.EXE", "python3.exe", runtime.GOOS == "windows"},
		{".venv/bin/python", "python3", true},
		{"python-wrapper.exe", "python3", false},
		{"mypython.exe", "python3", false},
		{"uv", "python3", false},
		{"python.exe.config", "python3", false},
		{"python2.exe", "python3", false},
		{"tool.exe", "tool", false},
	} {
		t.Run(tc.prefix+"-"+tc.original, func(t *testing.T) {
			profile := Profile{Prefix: []string{tc.prefix}}
			want := []string{tc.prefix, tc.original, "-m", "unittest"}
			if tc.replace {
				want = []string{tc.prefix, "-m", "unittest"}
			}
			got := profile.Rewrite([]string{tc.original, "-m", "unittest"})
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("rewritten argv = %v, want %v", got, want)
			}
		})
	}
}
