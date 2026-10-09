package evidence

import (
	"context"
	"errors"
	gitrepo "github.com/radar-engine/radar/internal/git"
	"os"
	"path/filepath"
	"strings"
)

const maxSnapshot = 64 << 20

func snapshot(ctx context.Context, root, revision, dest string) error {
	entries, err := gitrepo.Entries(ctx, root, revision)
	if err != nil {
		return err
	}
	if len(entries) > 10000 {
		return errors.New("snapshot exceeds 10000 files")
	}
	total := 0
	for _, entry := range entries {
		if entry.Mode != "100644" && entry.Mode != "100755" {
			continue
		}
		path := entry.Path
		clean := filepath.Clean(path)
		if filepath.IsAbs(path) || strings.ContainsAny(path, "\\:\x00") || clean == ".." || strings.HasPrefix(clean, "../") || clean == ".git" || strings.HasPrefix(clean, ".git/") {
			return errors.New("unsafe checkpoint path")
		}
		content, err := gitrepo.ReadBlob(ctx, root, entry.OID)
		if err != nil {
			return err
		}
		total += len(content)
		if total > maxSnapshot {
			return errors.New("snapshot exceeds 64 MiB")
		}
		full := filepath.Join(dest, clean)
		if err = os.MkdirAll(filepath.Dir(full), 0700); err != nil {
			return err
		}
		mode := os.FileMode(0600)
		if entry.Mode == "100755" {
			mode = 0700
		}
		if err = os.WriteFile(full, content, mode); err != nil {
			return err
		}
	}
	return nil
}
