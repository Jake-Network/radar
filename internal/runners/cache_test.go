package runners

import (
	"context"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCacheInterpreterUsesSelectedExecutableIdentity(t *testing.T) {
	toolcache := filepath.Join(t.TempDir(), "hostedtoolcache", "Python", "3.12.9", "x64", "bin")
	if err := os.MkdirAll(toolcache, 0700); err != nil {
		t.Fatal(err)
	}
	name := "python3"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	python := filepath.Join(toolcache, name)
	if err := os.WriteFile(python, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", toolcache)
	p := Profile{Root: ".", Prefix: []string{".venv/bin/python"}, Lockfiles: []string{"uv.lock"}, Artifacts: []string{".venv"}}
	key, err := CacheKey(p, "repo", map[string][]byte{"uv.lock": []byte("lock")})
	if err != nil {
		t.Fatal(err)
	}
	cache := Cache{Dir: t.TempDir(), MaxBytes: 1 << 20}
	root := t.TempDir()
	bin := filepath.Join(root, ".venv", "bin")
	if err := os.MkdirAll(bin, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(python, filepath.Join(bin, "python")); err != nil {
		t.Fatal(err)
	}
	if err := cache.Store(context.Background(), key, root, p); err != nil {
		t.Fatal("selected hosted interpreter refused", err)
	}
	restored := t.TempDir()
	if hit, err := cache.Restore(context.Background(), key, restored, p); err != nil || !hit {
		t.Fatal(hit, err)
	}
	link, err := os.Readlink(filepath.Join(restored, ".venv", "bin", "python"))
	if err != nil || link != python {
		t.Fatal(link, err)
	}
	// A similarly named executable outside the selected installation does not
	// become trusted because it has a Python-shaped name or executable mode.
	other := filepath.Join(t.TempDir(), "python3")
	if err := os.WriteFile(other, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if currentPythonInterpreter(other) {
		t.Fatal("unselected interpreter trusted")
	}
	if err := os.Remove(filepath.Join(bin, "python")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(other, filepath.Join(bin, "python")); err != nil {
		t.Fatal(err)
	}
	if err := cache.Store(context.Background(), key, root, p); err == nil {
		t.Fatal("unselected external executable cached")
	}
}

func TestRelocateDirectURLComparesPathsNotText(t *testing.T) {
	old, next := filepath.Join(t.TempDir(), "old root"), filepath.Join(t.TempDir(), "new #root")
	path := filepath.ToSlash(filepath.Join(old, "libs", "a"))
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	source := (&url.URL{Scheme: "file", Path: path}).String()
	stored, err := relocateDirectURL([]byte(`{"url": "`+source+`", "dir_info": {"editable": true}}`), old, true)
	if err != nil || strings.Contains(string(stored), "old") || !strings.Contains(string(stored), `"__RADAR_PROJECT_ROOT__/libs/a"`) || !strings.Contains(string(stored), `"editable":true`) {
		t.Fatalf("store: %s %v", stored, err)
	}
	restored, err := relocateDirectURL(stored, next, false)
	if err != nil {
		t.Fatal(err)
	}
	var record struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(restored, &record); err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(record.URL)
	if err != nil {
		t.Fatal(err)
	}
	decoded := u.Path
	if runtime.GOOS == "windows" {
		decoded = strings.TrimPrefix(decoded, "/")
	}
	if err != nil || u.Scheme != "file" || filepath.FromSlash(decoded) != filepath.Join(next, "libs", "a") {
		t.Fatalf("restore: %s %v", restored, err)
	}
	// URLs outside the root and non-file URLs are kept as they are.
	for _, data := range []string{`{"url": "file:///elsewhere/pkg", "dir_info": {}}`, `{"url": "git+https://example.com/repo", "vcs_info": {"vcs": "git"}}`} {
		if got, err := relocateDirectURL([]byte(data), old, true); err != nil || string(got) != data {
			t.Fatalf("%s: %s %v", data, got, err)
		}
	}
	if _, err := relocateDirectURL([]byte("not json"), old, true); err == nil {
		t.Fatal("malformed metadata cached")
	}
}
