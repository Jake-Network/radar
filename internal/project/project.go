// Package project bounds persistent state to an explicitly selected project root.
package project

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	gitrepo "github.com/radar-engine/radar/internal/git"
	"github.com/radar-engine/radar/internal/pathutil"
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

// SafePath returns a Radar state location below root without following symlinks.
func SafePath(root, path string) (string, error) { return pathutil.StatePath(root, path) }

// StateRoot selects the directory holding .radar state. A linked Git worktree
// without its own initialized state shares the main worktree's state, so
// evidence and snapshots recorded by parallel agents are visible everywhere.
func StateRoot(ctx context.Context, root string) string {
	if initialized(root) {
		return root
	}
	common, err := gitrepo.CommonDir(ctx, root)
	if err != nil || filepath.Base(common) != ".git" {
		return root
	}
	main := filepath.Dir(common)
	if main != root && initialized(main) {
		return main
	}
	return root
}

func initialized(root string) bool {
	path, err := SafePath(root, ".radar/config.json")
	if err != nil {
		return false
	}
	_, err = os.Stat(path)
	return err == nil
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
