package indexer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	gitrepo "github.com/Jake-Network/radar/internal/git"
)

// EntryMode classifies a repository entry without following it.
type EntryMode int

const (
	ModeRegular EntryMode = iota
	ModeSymlink
	ModeSubmodule
	ModeOther
)

// Entry is one repository path offered by a Provider.
type Entry struct {
	Path string
	Size int64
	Mode EntryMode
	oid  string
}

// Provider supplies repository files from the working tree or a Git commit.
type Provider interface {
	Method() string
	Entries(ctx context.Context) ([]Entry, error)
	Read(ctx context.Context, e Entry, limit int64) ([]byte, error)
}

var errTooLarge = errors.New("file exceeds read limit")

type worktree struct{ root string }

func newWorktree(root string) (*worktree, error) {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	absolute, err = filepath.EvalSymlinks(absolute)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("repository root is not a directory")
	}
	return &worktree{root: absolute}, nil
}

func (w *worktree) Method() string { return "filesystem" }

func (w *worktree) Entries(ctx context.Context) ([]Entry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// Prefer Git's view so ignored build output and private virtualenvs are skipped.
	if paths, err := gitrepo.WorkingFiles(ctx, w.root); err == nil && len(paths) > 0 {
		out := make([]Entry, 0, len(paths))
		for _, p := range paths {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			info, err := os.Lstat(filepath.Join(w.root, filepath.FromSlash(p)))
			if err != nil {
				continue // tracked but deleted in the working tree
			}
			out = append(out, Entry{Path: p, Size: info.Size(), Mode: modeOf(info)})
		}
		return out, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	out := []Entry{}
	err := filepath.WalkDir(w.root, func(full string, entry fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if full != w.root && excluded[entry.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(w.root, full)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		out = append(out, Entry{Path: filepath.ToSlash(rel), Size: info.Size(), Mode: modeOf(info)})
		return nil
	})
	return out, err
}

func modeOf(info fs.FileInfo) EntryMode {
	switch {
	case info.Mode()&os.ModeSymlink != 0:
		return ModeSymlink
	case info.IsDir():
		return ModeSubmodule // a directory listed by Git is a gitlink
	case info.Mode().IsRegular():
		return ModeRegular
	}
	return ModeOther
}

func (w *worktree) Read(_ context.Context, e Entry, limit int64) ([]byte, error) {
	f, err := os.Open(filepath.Join(w.root, filepath.FromSlash(e.Path)))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	content, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(content)) > limit {
		return nil, errTooLarge
	}
	return content, nil
}

// Commit reads an immutable Git commit through a single batch object reader.
type Commit struct {
	root, sha string
	reader    *gitrepo.BlobReader
}

// NewCommit prepares a provider for an already resolved commit SHA.
func NewCommit(root, sha string) *Commit { return &Commit{root: root, sha: sha} }

func (c *Commit) Method() string { return "git_object" }

func (c *Commit) Entries(ctx context.Context) ([]Entry, error) {
	entries, err := gitrepo.Entries(ctx, c.root, c.sha)
	if err != nil {
		return nil, err
	}
	out := make([]Entry, 0, len(entries))
	for _, e := range entries {
		mode := ModeOther
		switch {
		case e.Mode == "160000":
			mode = ModeSubmodule
		case e.Mode == "120000":
			mode = ModeSymlink
		case e.Type == "blob" && (e.Mode == "100644" || e.Mode == "100755"):
			mode = ModeRegular
		}
		out = append(out, Entry{Path: e.Path, Size: e.Size, Mode: mode, oid: e.OID})
	}
	return out, nil
}

func (c *Commit) Read(ctx context.Context, e Entry, limit int64) ([]byte, error) {
	if c.reader == nil {
		r, err := gitrepo.OpenBlobReader(ctx, c.root)
		if err != nil {
			return nil, err
		}
		c.reader = r
	}
	return c.reader.Read(e.oid, limit)
}

// Close releases the batch reader.
func (c *Commit) Close() error {
	if c.reader == nil {
		return nil
	}
	err := c.reader.Close()
	c.reader = nil
	return err
}
