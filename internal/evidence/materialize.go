package evidence

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	gitrepo "github.com/radar-engine/radar/internal/git"
	"github.com/radar-engine/radar/internal/pathutil"
)

const (
	maxSnapshot      = 64 << 20
	maxSnapshotFiles = 10000
)

// snapshot writes the regular files of a commit into dest through one batch
// object reader. Symlinks and submodules are not materialized.
func snapshot(ctx context.Context, root, revision, dest string) error {
	entries, err := gitrepo.Entries(ctx, root, revision)
	if err != nil {
		return err
	}
	if len(entries) > maxSnapshotFiles {
		return errors.New("snapshot exceeds 10000 files")
	}
	reader, err := gitrepo.OpenBlobReader(ctx, root)
	if err != nil {
		return err
	}
	defer reader.Close()
	var total int64
	for _, entry := range entries {
		if entry.Mode != "100644" && entry.Mode != "100755" {
			continue
		}
		clean, err := pathutil.RepoRelative(entry.Path)
		if err != nil || clean == ".git" || strings.HasPrefix(clean, ".git/") || strings.ContainsRune(clean, ':') {
			return errors.New("unsafe checkpoint path")
		}
		content, err := reader.Read(entry.OID, maxSnapshot-total)
		if errors.Is(err, gitrepo.ErrBlobTooLarge) {
			return errors.New("snapshot exceeds 64 MiB")
		}
		if err != nil {
			return err
		}
		total += int64(len(content))
		full := filepath.Join(dest, filepath.FromSlash(clean))
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
