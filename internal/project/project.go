// Package project bounds persistent state to an explicitly selected project root.
package project

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Config struct {
	Version     int  `json:"version"`
	NoTelemetry bool `json:"no_telemetry"`
}

func Root(path string) (string, error) {
	abs, e := filepath.Abs(path)
	if e != nil {
		return "", e
	}
	abs, e = filepath.EvalSymlinks(abs)
	if e != nil {
		return "", e
	}
	st, e := os.Stat(abs)
	if e != nil {
		return "", e
	}
	if !st.IsDir() {
		return "", fmt.Errorf("root must be a directory")
	}
	return abs, nil
}
func SafePath(root, path string) (string, error) {
	if filepath.IsAbs(path) {
		return "", fmt.Errorf("expected repository-relative path")
	}
	candidate := filepath.Join(root, filepath.Clean(path))
	rel, e := filepath.Rel(root, candidate)
	if e != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes repository: %q", path)
	}
	cur := root
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		cur = filepath.Join(cur, part)
		st, e := os.Lstat(cur)
		if os.IsNotExist(e) {
			continue
		}
		if e != nil {
			return "", e
		}
		if st.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("refusing symlink in state path: %q", cur)
		}
	}
	return candidate, nil
}
func Init(root string) error {
	dir, e := SafePath(root, ".radar")
	if e != nil {
		return e
	}
	if e = os.MkdirAll(dir, 0700); e != nil {
		return e
	}
	path, e := SafePath(root, ".radar/config.json")
	if e != nil {
		return e
	}
	b, _ := json.MarshalIndent(Config{1, true}, "", "  ")
	f, e := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if os.IsExist(e) {
		_, e = Read(root)
		return e
	}
	if e != nil {
		return e
	}
	defer f.Close()
	_, e = f.Write(append(b, '\n'))
	return e
}
func Read(root string) (Config, error) {
	path, e := SafePath(root, ".radar/config.json")
	if e != nil {
		return Config{}, e
	}
	b, e := os.ReadFile(path)
	if e != nil {
		return Config{}, fmt.Errorf("run radar init: %w", e)
	}
	var c Config
	if e = json.Unmarshal(b, &c); e != nil {
		return c, e
	}
	if c.Version != 1 {
		return c, fmt.Errorf("unsupported configuration version %d", c.Version)
	}
	if !c.NoTelemetry {
		return c, fmt.Errorf("telemetry is unsupported; no_telemetry must be true")
	}
	return c, nil
}
