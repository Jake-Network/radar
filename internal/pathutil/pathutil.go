// Package pathutil validates untrusted repository-relative paths in one place.
package pathutil

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// RepoRelative validates a slash-separated repository-relative path and returns
// its cleaned form. Absolute paths, backslashes, control separators and parent
// traversal are rejected.
func RepoRelative(p string) (string, error) {
	if p == "" || strings.HasPrefix(p, "/") || filepath.IsAbs(p) || strings.ContainsAny(p, "\\\x00\r\n") {
		return "", errors.New("path must be repository relative")
	}
	clean := path.Clean(p)
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return "", errors.New("path escapes repository")
	}
	for _, part := range strings.Split(p, "/") {
		if part == ".." {
			return "", errors.New("path escapes repository")
		}
	}
	return clean, nil
}

// ResolveInside resolves a repository-relative path, following symlinks, and
// refuses results outside root. Use it for reading existing working-tree files.
func ResolveInside(root, rel string) (string, error) {
	clean, err := RepoRelative(rel)
	if err != nil {
		return "", err
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	if resolvedRoot, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolvedRoot
	}
	resolved, err := filepath.EvalSymlinks(filepath.Join(abs, filepath.FromSlash(clean)))
	if err != nil {
		return "", err
	}
	if !inside(abs, resolved) {
		return "", errors.New("symlink escapes repository")
	}
	return resolved, nil
}

// StatePath returns a location for Radar-owned state below root. It never
// follows a symlink in any existing component, so state cannot be redirected.
func StatePath(root, rel string) (string, error) {
	if filepath.IsAbs(rel) {
		return "", fmt.Errorf("expected repository-relative path")
	}
	candidate := filepath.Join(root, filepath.Clean(rel))
	if !inside(root, candidate) {
		return "", fmt.Errorf("path escapes repository: %q", rel)
	}
	r, _ := filepath.Rel(root, candidate)
	cur := root
	for _, part := range strings.Split(r, string(filepath.Separator)) {
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

func inside(root, candidate string) bool {
	rel, err := filepath.Rel(root, candidate)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
