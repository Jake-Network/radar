// Package git provides bounded, read-only repository inspection.
package git

import (
	"bufio"
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Jake-Network/radar/internal/pathutil"
)

// MaxFileBytes bounds a single file or small command output.
const MaxFileBytes = 4 << 20

// maxListingBytes bounds whole-tree listings, which grow with repository size.
const maxListingBytes = 256 << 20

const (
	shortTimeout   = 15 * time.Second
	listingTimeout = 2 * time.Minute
)

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

// command binds every operation to the explicit root and object database.
// Caller Git overrides could otherwise redirect reads, inject config, or
// replace objects.
func command(ctx context.Context, root string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", root, "--no-pager", "-c", "core.fsmonitor=false", "-c", "protocol.allow=never"}, args...)...)
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if !strings.HasPrefix(strings.ToUpper(key), "GIT_") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	cmd.Env = append(cmd.Env, "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0", "GIT_NO_REPLACE_OBJECTS=1", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_NO_LAZY_FETCH=1")
	return cmd
}

func runLimit(ctx context.Context, root string, limit int, timeout time.Duration, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := command(ctx, root, args...)
	out := &boundedBuffer{limit: limit}
	errout := &boundedBuffer{limit: 8192}
	cmd.Stdout = out
	cmd.Stderr = errout
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(errout.String()))
	}
	return out.Bytes(), nil
}
func run(ctx context.Context, root string, args ...string) ([]byte, error) {
	return runLimit(ctx, root, MaxFileBytes, shortTimeout, args...)
}
func runListing(ctx context.Context, root string, args ...string) ([]byte, error) {
	return runLimit(ctx, root, maxListingBytes, listingTimeout, args...)
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
	b, err = runListing(ctx, root, "status", "--porcelain", "--untracked-files=normal")
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

// WorktreeBranches lists local branches checked out in any worktree of the
// repository, in `git worktree list` order. Detached worktrees are omitted.
func WorktreeBranches(ctx context.Context, root string) ([]string, error) {
	b, err := run(ctx, root, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	out := []string{}
	for _, line := range strings.Split(string(b), "\n") {
		if ref, ok := strings.CutPrefix(strings.TrimSpace(line), "branch refs/heads/"); ok && ref != "" {
			out = append(out, ref)
		}
	}
	return out, nil
}

// DefaultBranch guesses the integration base: the local counterpart of
// origin/HEAD, then main, master or trunk. It returns "" when none exists.
func DefaultBranch(ctx context.Context, root string) string {
	candidates := []string{}
	if b, err := run(ctx, root, "symbolic-ref", "--quiet", "--short", "refs/remotes/origin/HEAD"); err == nil {
		remote := strings.TrimSpace(string(b))
		if local, ok := strings.CutPrefix(remote, "origin/"); ok {
			candidates = append(candidates, local)
		}
		candidates = append(candidates, "main", "master", "trunk", remote)
	} else {
		candidates = append(candidates, "main", "master", "trunk")
	}
	for _, c := range candidates {
		if _, err := run(ctx, root, "show-ref", "--verify", "--quiet", "refs/heads/"+c); err == nil {
			return c
		}
		if strings.Contains(c, "/") {
			if _, err := Resolve(ctx, root, c); err == nil {
				return c
			}
		}
	}
	return ""
}

// MergeBase returns the best common ancestor of two commits.
func MergeBase(ctx context.Context, root, a, b string) (string, error) {
	for _, ref := range []string{a, b} {
		if ref == "" || strings.HasPrefix(ref, "-") {
			return "", errors.New("invalid Git reference")
		}
	}
	out, err := run(ctx, root, "merge-base", "--end-of-options", a, b)
	return strings.TrimSpace(string(out)), err
}

// Identity returns a location-independent repository identity derived from
// its root commits, shared by every clone and worktree. It returns "" when the
// directory is not a Git repository with history.
func Identity(ctx context.Context, root string) string {
	b, err := run(ctx, root, "rev-list", "--max-parents=0", "HEAD")
	if err != nil {
		return ""
	}
	roots := strings.Fields(string(b))
	if len(roots) == 0 {
		return ""
	}
	sort.Strings(roots)
	return "git:" + strings.Join(roots, "+")
}

// CommonDir returns the absolute Git common directory shared by all worktrees.
func CommonDir(ctx context.Context, root string) (string, error) {
	b, err := run(ctx, root, "rev-parse", "--git-common-dir")
	if err != nil {
		return "", err
	}
	dir := strings.TrimSpace(string(b))
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(root, dir)
	}
	return filepath.Clean(dir), nil
}

// WorkingFiles lists tracked and untracked, non-ignored files below root,
// relative to root. It respects .gitignore and Git exclude settings.
func WorkingFiles(ctx context.Context, root string) ([]string, error) {
	b, err := runListing(ctx, root, "ls-files", "-z", "--cached", "--others", "--exclude-standard", "--deduplicate")
	if err != nil {
		return nil, err
	}
	return splitNUL(b), nil
}

// ChangedFiles lists paths that differ between base and head. head may be
// WORKTREE, which includes staged, unstaged and untracked non-ignored files.
// Renames are reported as both the old and the new path.
func ChangedFiles(ctx context.Context, root, base, head string) ([]string, error) {
	baseSHA, err := Resolve(ctx, root, base)
	if err != nil {
		return nil, err
	}
	var b []byte
	if head == "WORKTREE" {
		b, err = runListing(ctx, root, "diff", "--name-only", "--no-renames", "-z", baseSHA, "--")
		if err != nil {
			return nil, err
		}
		untracked, err := runListing(ctx, root, "ls-files", "-z", "--others", "--exclude-standard")
		if err != nil {
			return nil, err
		}
		b = append(b, untracked...)
	} else {
		headSHA, err := Resolve(ctx, root, head)
		if err != nil {
			return nil, err
		}
		b, err = runListing(ctx, root, "diff", "--name-only", "--no-renames", "-z", baseSHA, headSHA, "--")
		if err != nil {
			return nil, err
		}
	}
	seen := map[string]bool{}
	out := []string{}
	for _, p := range splitNUL(b) {
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out, nil
}

func splitNUL(b []byte) []string {
	out := []string{}
	for _, p := range strings.Split(string(b), "\x00") {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func ReadFile(ctx context.Context, root, ref, path string) ([]byte, error) {
	clean, err := pathutil.RepoRelative(path)
	if err != nil {
		return nil, errors.New("invalid repository path")
	}
	if ref == "WORKTREE" {
		full, err := pathutil.ResolveInside(root, clean)
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
	b, err := run(ctx, root, "ls-tree", "-z", sha, "--", clean)
	if err != nil {
		return nil, err
	}
	for _, record := range splitNUL(b) {
		parts := strings.SplitN(record, "\t", 2)
		if len(parts) != 2 || parts[1] != clean {
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
	return nil, fmt.Errorf("committed file %q: %w", clean, os.ErrNotExist)
}
func Files(ctx context.Context, root, ref string) ([]string, error) {
	sha, err := Resolve(ctx, root, ref)
	if err != nil {
		return nil, err
	}
	b, err := runListing(ctx, root, "ls-tree", "-r", "--name-only", "-z", sha)
	if err != nil {
		return nil, err
	}
	return splitNUL(b), nil
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
	b, err := runListing(ctx, root, "ls-tree", "-r", "-l", "-z", sha)
	if err != nil {
		return nil, err
	}
	entries := []TreeEntry{}
	for _, record := range splitNUL(b) {
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

func validOID(oid string) error {
	if len(oid) != 40 && len(oid) != 64 {
		return errors.New("invalid Git object ID")
	}
	if _, err := hex.DecodeString(oid); err != nil {
		return errors.New("invalid Git object ID")
	}
	return nil
}

// ReadBlob reads an immutable object, avoiding branch movement during analysis.
func ReadBlob(ctx context.Context, root, oid string) ([]byte, error) {
	if err := validOID(oid); err != nil {
		return nil, err
	}
	return run(ctx, root, "cat-file", "blob", oid)
}

// ErrBlobTooLarge reports a blob above the caller's read limit.
var ErrBlobTooLarge = errors.New("Git blob exceeds read limit")

// BlobReader streams many objects through one `git cat-file --batch` process
// instead of spawning a subprocess per file.
type BlobReader struct {
	mu     sync.Mutex
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Reader
	errout *boundedBuffer
}

// OpenBlobReader starts a batch reader bound to ctx; Close releases it.
func OpenBlobReader(ctx context.Context, root string) (*BlobReader, error) {
	cmd := command(ctx, root, "cat-file", "--batch")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	errout := &boundedBuffer{limit: 8192}
	cmd.Stderr = errout
	if err = cmd.Start(); err != nil {
		return nil, err
	}
	return &BlobReader{cmd: cmd, stdin: stdin, stdout: bufio.NewReaderSize(stdout, 64<<10), errout: errout}, nil
}

// Read returns a blob's content. Blobs above limit are skipped and reported
// with ErrBlobTooLarge without loading them into memory.
func (r *BlobReader) Read(oid string, limit int64) ([]byte, error) {
	if err := validOID(oid); err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, err := io.WriteString(r.stdin, oid+"\n"); err != nil {
		return nil, fmt.Errorf("git cat-file: %w: %s", err, strings.TrimSpace(r.errout.String()))
	}
	header, err := r.stdout.ReadString('\n')
	if err != nil {
		return nil, fmt.Errorf("git cat-file: %w", err)
	}
	fields := strings.Fields(header)
	if len(fields) == 2 && fields[1] == "missing" {
		return nil, fmt.Errorf("Git object %s: %w", oid, os.ErrNotExist)
	}
	if len(fields) != 3 {
		return nil, errors.New("malformed git cat-file header")
	}
	size, err := strconv.ParseInt(fields[2], 10, 64)
	if err != nil || size < 0 {
		return nil, errors.New("malformed git cat-file size")
	}
	if fields[1] != "blob" {
		if _, err = r.stdout.Discard(int(size) + 1); err != nil {
			return nil, err
		}
		return nil, errors.New("Git object is not a blob")
	}
	if size > limit {
		if _, err = r.stdout.Discard(int(size) + 1); err != nil {
			return nil, err
		}
		return nil, ErrBlobTooLarge
	}
	content := make([]byte, size+1)
	if _, err = io.ReadFull(r.stdout, content); err != nil {
		return nil, err
	}
	return content[:size], nil
}

// Close stops the batch process.
func (r *BlobReader) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stdin.Close()
	err := r.cmd.Wait()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return nil
	}
	return err
}
