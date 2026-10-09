package cli

import (
	"encoding/json"
	"fmt"
	"github.com/Jake-Network/radar/internal/planning"
	"os"
	"path/filepath"
)

func (a *app) loadPlan(path string) (planning.Plan, error) {
	if path == "" {
		return planning.Plan{}, fmt.Errorf("--plan is required")
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(a.root, path)
	}
	return planning.Load(path)
}
func writeNewJSON(path string, value any) error {
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return e
	}
	b, e := json.MarshalIndent(value, "", "  ")
	if e != nil {
		return e
	}
	f, e := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return fmt.Errorf("refusing to overwrite an existing artifact; choose --output: %w", e)
	}
	_, writeErr := f.Write(append(b, '\n'))
	closeErr := f.Close()
	if writeErr != nil {
		return writeErr
	}
	return closeErr
}
