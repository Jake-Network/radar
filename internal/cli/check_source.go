package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"sort"

	"github.com/Jake-Network/radar/internal/contracts"
	"github.com/Jake-Network/radar/internal/indexer"
	"github.com/Jake-Network/radar/internal/pathutil"
)

// checkSourceState supplements the indexed graph digest with bounded repository
// content observations. Runner configuration and declarations are inputs too,
// even when they do not produce graph nodes. This is an observation boundary,
// not a filesystem lock or a claim to notice transient writes that are reverted.
func checkSourceState(ctx context.Context, root, ref string) (string, error) {
	if ref != "WORKTREE" {
		return ref, nil
	} // check pins immutable commits first.
	p, err := indexer.NewWorktree(root)
	if err != nil {
		return "", err
	}
	entries, err := p.Entries(ctx)
	if err != nil {
		return "", err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	h := sha256.New()
	var total int64
	count := 0
	observe := func(entry indexer.Entry) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if _, err := pathutil.RepoRelative(entry.Path); err != nil {
			return err
		}
		fmt.Fprintf(h, "%d:%s:%d:%d\n", len(entry.Path), entry.Path, entry.Mode, entry.Size)
		if entry.Mode != indexer.ModeRegular {
			return nil
		} // readers skip these entries.
		count++
		if count > indexer.MaxSourceFiles || entry.Size > indexer.MaxTotalSourceBytes || total+entry.Size > indexer.MaxTotalSourceBytes {
			return fmt.Errorf("source stability observation exceeds 10000 files or 64 MiB at %s", entry.Path)
		}
		if _, err := pathutil.StatePath(root, entry.Path); err != nil {
			return err
		}
		content, err := p.Read(ctx, entry, indexer.MaxTotalSourceBytes-total)
		if err != nil {
			return fmt.Errorf("source stability read %s: %w", entry.Path, err)
		}
		total += int64(len(content))
		if total > indexer.MaxTotalSourceBytes {
			return fmt.Errorf("source stability observation exceeds 64 MiB")
		}
		digest := sha256.Sum256(content)
		h.Write(digest[:])
		return nil
	}
	for _, entry := range entries {
		if indexer.ExcludedPath(entry.Path) {
			continue
		}
		if err := observe(entry); err != nil {
			return "", err
		}
	}
	// State directories are normally excluded, but the approved declarations are
	// authoritative source inputs. Read them once through the same safe provider.
	filename, err := pathutil.StatePath(root, contracts.ManifestPath)
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(filename)
	if errors.Is(err, os.ErrNotExist) {
		fmt.Fprintln(h, "contract-manifest:absent")
	} else if err != nil {
		return "", err
	} else {
		if !info.Mode().IsRegular() {
			return "", fmt.Errorf("source stability declaration is not a regular file")
		}
		if err := observe(indexer.Entry{Path: contracts.ManifestPath, Size: info.Size(), Mode: indexer.ModeRegular}); err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
