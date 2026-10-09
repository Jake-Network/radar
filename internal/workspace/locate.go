package workspace

import (
	"context"
	"fmt"
	"path/filepath"

	gitrepo "github.com/Jake-Network/radar/internal/git"
)

// Location identifies the repository a checkout belongs to. Root is the
// checkout's top level; MainPath is the main worktree, which names the repo.
type Location struct {
	Root      string `json:"root"`
	CommonDir string `json:"common_dir"`
	MainPath  string `json:"main_path"`
}

// Locate resolves path to its repository. Linked worktrees of one repository
// share CommonDir, so they locate to the same repository.
func Locate(ctx context.Context, path string) (Location, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return Location{}, err
	}
	if resolved, e := filepath.EvalSymlinks(abs); e == nil {
		abs = resolved
	} else {
		return Location{}, fmt.Errorf("%s: %w", path, e)
	}
	root, err := gitrepo.TopLevel(ctx, abs)
	if err != nil {
		return Location{}, fmt.Errorf("%s is not inside a Git working tree", path)
	}
	common, err := gitrepo.CommonDir(ctx, root)
	if err != nil {
		return Location{}, fmt.Errorf("%s: %w", path, err)
	}
	if resolved, e := filepath.EvalSymlinks(common); e == nil {
		common = resolved
	}
	if resolved, e := filepath.EvalSymlinks(root); e == nil {
		root = resolved
	}
	loc := Location{Root: root, CommonDir: common, MainPath: root}
	if filepath.Base(common) == ".git" {
		loc.MainPath = filepath.Dir(common)
	}
	return loc, nil
}

// DefaultRepoID is the ID a repository gets unless one is chosen: its main
// worktree's folder name, normalized.
func (l Location) DefaultRepoID() string { return DefaultID(filepath.Base(l.MainPath)) }

// Usable reports why the registered path of repo cannot be used now, or "".
func Usable(ctx context.Context, repo Repo) string {
	loc, err := Locate(ctx, repo.Path)
	if err != nil {
		return "path " + repo.Path + " is not available"
	}
	if loc.CommonDir != repo.CommonDir {
		return "path " + repo.Path + " now holds a different repository"
	}
	return ""
}
