// Package git provides bounded, read-only repository inspection.
package git

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const MaxFileBytes = 4 << 20

type Info struct {
	Root      string   `json:"root"`
	Head      string   `json:"head"`
	Branch    string   `json:"branch"`
	Dirty     bool     `json:"dirty"`
	Worktrees []string `json:"worktrees"`
}
type boundedBuffer struct {
	bytes.Buffer
	limit int
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	if len(p) > b.limit-b.Len() {
		return 0, errors.New("git output exceeds analysis limit")
	}
	return b.Buffer.Write(p)
}
func run(ctx context.Context, root string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", root, "--no-pager", "-c", "core.fsmonitor=false"}, args...)...)
	// Bind every operation to the explicit root and object database. Caller Git
	// overrides could otherwise redirect reads, inject config, or replace objects.
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if !strings.HasPrefix(strings.ToUpper(key), "GIT_") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	cmd.Env = append(cmd.Env, "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0", "GIT_NO_REPLACE_OBJECTS=1", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull)
	out := &boundedBuffer{limit: MaxFileBytes}
	errout := &boundedBuffer{limit: 8192}
	cmd.Stdout = out
	cmd.Stderr = errout
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(errout.String()))
	}
	return out.Bytes(), nil
}
func Resolve(ctx context.Context, root, ref string) (string, error) {
	if ref == "" || strings.HasPrefix(ref, "-") || strings.ContainsAny(ref, "\x00\r\n") {
		return "", errors.New("invalid Git reference")
	}
	b, err := run(ctx, root, "rev-parse", "--verify", "--end-of-options", ref+"^{commit}")
	return strings.TrimSpace(string(b)), err
}
func Inspect(ctx context.Context, root string) (Info, error) {
	var info Info
	b, err := run(ctx, root, "rev-parse", "--show-toplevel")
	if err != nil {
		return info, err
	}
	info.Root = strings.TrimSpace(string(b))
	info.Head, err = Resolve(ctx, root, "HEAD")
	if err != nil {
		return info, err
	}
	b, err = run(ctx, root, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err == nil {
		info.Branch = strings.TrimSpace(string(b))
	}
	b, err = run(ctx, root, "status", "--porcelain", "--untracked-files=normal")
	if err != nil {
		return info, err
	}
	info.Dirty = len(b) > 0
	b, err = run(ctx, root, "worktree", "list", "--porcelain")
	if err != nil {
		return info, err
	}
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(line, "worktree ") {
			info.Worktrees = append(info.Worktrees, strings.TrimPrefix(line, "worktree "))
		}
	}
	return info, nil
}
func SafePath(root, path string) (string, error) {
	if path == "" || filepath.IsAbs(path) || strings.Contains(path, "\\") {
		return "", errors.New("path must be repository relative")
	}
	clean := filepath.Clean(path)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", errors.New("path escapes repository")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	full := filepath.Join(abs, clean)
	resolved, err := filepath.EvalSymlinks(full)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(abs, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("symlink escapes repository")
	}
	return resolved, nil
}
func ReadFile(ctx context.Context, root, ref, path string) ([]byte, error) {
	if path == "" || filepath.IsAbs(path) || strings.Contains(path, "\\") || strings.ContainsAny(path, "\x00\r\n") || filepath.Clean(path) == ".." || strings.HasPrefix(filepath.Clean(path), "../") {
		return nil, errors.New("invalid repository path")
	}
	if ref == "WORKTREE" {
		full, err := SafePath(root, path)
		if err != nil {
			return nil, err
		}
		f, err := os.Open(full)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		b, err := io.ReadAll(io.LimitReader(f, MaxFileBytes+1))
		if len(b) > MaxFileBytes {
			return nil, errors.New("file exceeds analysis limit")
		}
		return b, err
	}
	sha, err := Resolve(ctx, root, ref)
	if err != nil {
		return nil, err
	}
	// Inspect the exact tree entry before reading; symlink blobs are not source
	// files or JSON contracts, and submodules refer to external repositories.
	b, err := run(ctx, root, "ls-tree", "-z", sha, "--", filepath.ToSlash(path))
	if err != nil {
		return nil, err
	}
	records := strings.Split(strings.TrimSuffix(string(b), "\x00"), "\x00")
	for _, record := range records {
		parts := strings.SplitN(record, "\t", 2)
		if len(parts) != 2 || parts[1] != filepath.ToSlash(path) {
			continue
		}
		fields := strings.Fields(parts[0])
		if len(fields) != 3 {
			return nil, errors.New("malformed Git tree entry")
		}
		if fields[1] != "blob" || (fields[0] != "100644" && fields[0] != "100755") {
			return nil, errors.New("committed file is not regular; symlink or submodule refused")
		}
		return ReadBlob(ctx, root, fields[2])
	}
	return nil, fmt.Errorf("committed file %q: %w", path, os.ErrNotExist)
}
func Files(ctx context.Context, root, ref string) ([]string, error) {
	sha, err := Resolve(ctx, root, ref)
	if err != nil {
		return nil, err
	}
	b, err := run(ctx, root, "ls-tree", "-r", "--name-only", "-z", sha)
	if err != nil {
		return nil, err
	}
	if len(b) == 0 {
		return []string{}, nil
	}
	return strings.Split(strings.TrimSuffix(string(b), "\x00"), "\x00"), nil
}

// TreeEntry records Git object metadata without following symlinks or submodules.
type TreeEntry struct {
	Path string
	Mode string
	Type string
	OID  string
	Size int64
}

func Entries(ctx context.Context, root, ref string) ([]TreeEntry, error) {
	sha, err := Resolve(ctx, root, ref)
	if err != nil {
		return nil, err
	}
	b, err := run(ctx, root, "ls-tree", "-r", "-l", "-z", sha)
	if err != nil {
		return nil, err
	}
	entries := []TreeEntry{}
	for _, record := range strings.Split(string(b), "\x00") {
		if record == "" {
			continue
		}
		parts := strings.SplitN(record, "\t", 2)
		if len(parts) != 2 {
			return nil, errors.New("malformed Git tree entry")
		}
		fields := strings.Fields(parts[0])
		if len(fields) != 4 {
			return nil, errors.New("malformed Git tree metadata")
		}
		size := int64(-1)
		if fields[3] != "-" {
			size, err = strconv.ParseInt(fields[3], 10, 64)
			if err != nil || size < 0 {
				return nil, errors.New("invalid Git blob size")
			}
		}
		entries = append(entries, TreeEntry{Path: parts[1], Mode: fields[0], Type: fields[1], OID: fields[2], Size: size})
	}
	return entries, nil
}

// ReadBlob reads an immutable object, avoiding branch movement during analysis.
func ReadBlob(ctx context.Context, root, oid string) ([]byte, error) {
	if len(oid) != 40 && len(oid) != 64 {
		return nil, errors.New("invalid Git object ID")
	}
	if _, err := hex.DecodeString(oid); err != nil {
		return nil, errors.New("invalid Git object ID")
	}
	return run(ctx, root, "cat-file", "blob", oid)
}
