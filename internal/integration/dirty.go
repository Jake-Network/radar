package integration

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	gitrepo "github.com/Jake-Network/radar/internal/git"
	"github.com/Jake-Network/radar/internal/pathutil"
)

const maxDirtyBytes = 32 << 20
const maxDirtyPaths = 4096

// DirtySnapshot pins the final saved worktree state and its separate index
// provenance. Contents are private to the builder and never serialized.
type DirtySnapshot struct {
	Worktree string `json:"worktree"`
	Branch   string `json:"branch,omitempty"`
	// InputIndex associates a snapshot with a pinned input; -1 requests unique matching.
	InputIndex int      `json:"input_index"`
	Head       string   `json:"head"`
	Digest     string   `json:"digest"`
	Staged     []string `json:"staged"`
	Unstaged   []string `json:"unstaged"`
	Untracked  []string `json:"untracked"`
	files      map[string]dirtyFile
}
type dirtyFile struct {
	data    []byte
	mode    string
	deleted bool
}

// ObserveDirty captures twice, including index blob identities and saved file
// bytes. A changing, unreadable or unsupported snapshot is never verified.
func ObserveDirty(ctx context.Context, w gitrepo.WorktreeStatus) (DirtySnapshot, error) {
	return observeDirty(ctx, w, nil)
}

func observeDirty(ctx context.Context, w gitrepo.WorktreeStatus, between func()) (DirtySnapshot, error) {
	a, err := captureDirty(ctx, w.Path)
	if err != nil {
		return a, err
	}
	if between != nil {
		between()
	}
	b, err := captureDirty(ctx, w.Path)
	if err != nil {
		return b, err
	}
	if a.Digest != b.Digest || (w.Commit != "" && a.Head != w.Commit) {
		return b, errors.New("dirty snapshot changed during capture; retry after edits settle")
	}
	return b, nil
}

func dirtyGit(ctx context.Context, root string, stdin io.Reader, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := gitrepo.PrivateCommand(ctx, root, args...)
	cmd.Env = append(cmd.Env, "GIT_OPTIONAL_LOCKS=0")
	cmd.Stdin = stdin
	var out bytes.Buffer
	cmd.Stdout = &dirtyBoundedWriter{out: &out, limit: maxDirtyBytes}
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("snapshot git %s unavailable: %w", args[0], err)
	}
	return out.Bytes(), nil
}

type dirtyBoundedWriter struct {
	out   *bytes.Buffer
	limit int
}

func (w *dirtyBoundedWriter) Write(p []byte) (int, error) {
	if w.out.Len()+len(p) > w.limit {
		return 0, errors.New("snapshot exceeds size limit")
	}
	return w.out.Write(p)
}

func captureDirty(ctx context.Context, root string) (DirtySnapshot, error) {
	s := DirtySnapshot{Worktree: root, InputIndex: -1, files: map[string]dirtyFile{}}
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		return s, err
	}
	root = canonical
	s.Worktree = root
	w, err := gitrepo.InspectWorktree(ctx, root)
	if err != nil {
		return s, err
	}
	s.Head = w.Commit
	s.Branch = w.Branch
	s.Staged = w.Staged
	s.Unstaged = w.Unstaged
	s.Untracked = w.Untracked
	if err := validateTree(ctx, root, s.Head); err != nil {
		return s, err
	}
	index, err := dirtyGit(ctx, root, nil, "ls-files", "--stage", "-z")
	if err != nil {
		return s, err
	}
	tracked := map[string]bool{}
	for _, v := range strings.Split(string(index), "\x00") {
		if v == "" {
			continue
		}
		meta, p, ok := strings.Cut(v, "\t")
		f := strings.Fields(meta)
		if !ok || len(f) != 3 || f[2] != "0" {
			return s, errors.New("unmerged index cannot be previewed")
		}
		if f[0] != "100644" && f[0] != "100755" {
			return s, &UnsupportedEntryError{Path: p}
		}
		if _, err := safeDirtyPath(root, p); err != nil {
			return s, err
		}
		tracked[p] = true
	}
	flags, err := dirtyGit(ctx, root, nil, "ls-files", "-v", "-z")
	if err != nil {
		return s, err
	}
	for _, f := range strings.Split(string(flags), "\x00") {
		if len(f) > 1 && (f[0] == 'S' || f[0] >= 'a' && f[0] <= 'z') {
			return s, errors.New("sparse or assume-unchanged index entries cannot be previewed")
		}
	}
	paths := map[string]bool{}
	for _, ps := range [][]string{s.Staged, s.Unstaged, s.Untracked} {
		for _, p := range ps {
			paths[p] = true
		}
	}
	entries, err := gitrepo.Entries(ctx, root, s.Head)
	if err != nil {
		return s, err
	}
	for _, e := range entries {
		if !tracked[e.Path] {
			paths[e.Path] = true
		}
	}
	if len(paths) > maxDirtyPaths {
		return s, errors.New("dirty snapshot exceeds path limit")
	}
	untracked := map[string]bool{}
	for _, p := range s.Untracked {
		untracked[p] = true
	}
	ordered := make([]string, 0, len(paths))
	for p := range paths {
		ordered = append(ordered, p)
	}
	sort.Strings(ordered)
	h := sha256.New()
	fmt.Fprintf(h, "dirty-preview-v1\x00%s\x00", s.Head)
	h.Write(index)
	total := 0
	for _, p := range ordered {
		if ctx.Err() != nil {
			return s, ctx.Err()
		}
		abs, err := safeDirtyPath(root, p)
		if err != nil {
			return s, err
		}
		f := dirtyFile{deleted: !tracked[p] && !untracked[p]}
		if !f.deleted {
			info, err := os.Lstat(abs)
			if os.IsNotExist(err) {
				f.deleted = true
			} else if err != nil {
				return s, err
			} else {
				if !info.Mode().IsRegular() {
					return s, &UnsupportedEntryError{Path: p}
				}
				if info.Size() > gitrepo.MaxFileBytes {
					return s, fmt.Errorf("dirty file exceeds size limit: %s", p)
				}
				handle, err := os.Open(abs)
				if err != nil {
					return s, err
				}
				opened, err := handle.Stat()
				if err != nil || !os.SameFile(info, opened) {
					handle.Close()
					return s, errors.New("dirty file changed during open")
				}
				data, readErr := io.ReadAll(io.LimitReader(handle, gitrepo.MaxFileBytes+1))
				after, statErr := handle.Stat()
				handle.Close()
				if readErr != nil {
					return s, readErr
				}
				if statErr != nil || len(data) > gitrepo.MaxFileBytes || !os.SameFile(info, after) || info.Size() != after.Size() || !info.ModTime().Equal(after.ModTime()) {
					return s, errors.New("dirty file changed during read")
				}
				if _, err := safeDirtyPath(root, p); err != nil {
					return s, err
				}
				f.data = data
				f.mode = "100644"
				if info.Mode()&0111 != 0 {
					f.mode = "100755"
				}
				total += len(data)
				if total > maxDirtyBytes {
					return s, errors.New("dirty snapshot exceeds byte limit")
				}
			}
		}
		s.files[p] = f
		fmt.Fprintf(h, "\x00%s\x00%s\x00%t\x00%d\x00", p, f.mode, f.deleted, len(f.data))
		h.Write(f.data)
	}
	for _, ps := range [][]string{s.Staged, s.Unstaged, s.Untracked} {
		fmt.Fprintf(h, "\x00%q", ps)
	}
	s.Digest = hex.EncodeToString(h.Sum(nil))
	return s, nil
}

func safeDirtyPath(root, p string) (string, error) {
	clean, err := pathutil.RepoRelative(p)
	if err != nil {
		return "", &UnsupportedEntryError{Path: p, Unsafe: err}
	}
	for _, part := range strings.Split(clean, "/") {
		if strings.EqualFold(part, ".git") {
			return "", errors.New("dirty snapshot refuses Git metadata paths")
		}
	}
	abs, err := pathutil.StatePath(root, clean)
	if err != nil {
		return "", err
	}
	return abs, nil
}

// BuildDirtyCandidate overlays faithful snapshots on their pinned heads in
// private repositories, then reuses the ordinary candidate merge engine.
// Every dirty input is a deterministic synthetic commit bound to its digest.
func BuildDirtyCandidate(ctx context.Context, root, base string, branches []string, snapshots []DirtySnapshot) (*Candidate, error) {
	seed, err := BuildCandidate(ctx, root, base, nil)
	if err != nil {
		return seed, err
	}
	defer seed.Close()
	inputs := append([]string(nil), branches...)
	for _, s := range snapshots {
		if s.Digest == "" || s.files == nil {
			return nil, errors.New("dirty snapshot has no captured contents")
		}
		fresh, err := ObserveDirty(ctx, gitrepo.WorktreeStatus{Path: s.Worktree, Commit: s.Head})
		if err != nil {
			return nil, err
		}
		if fresh.Digest != s.Digest {
			return nil, errors.New("dirty snapshot superseded before candidate construction")
		}
		branch, err := BuildCandidate(ctx, root, s.Head, nil)
		if err != nil {
			return branch, err
		}
		sha, err := writeDirtyCommit(ctx, branch.Dir, s)
		if err == nil {
			err = copyDirtyObjects(ctx, branch.Dir, seed.Dir, sha)
		}
		branch.Close()
		if err != nil {
			return nil, err
		}
		match := s.InputIndex
		if match >= len(branches) {
			return nil, errors.New("dirty snapshot input index is outside selected branches")
		}
		if match < 0 {
			for i, ref := range branches {
				if s.Branch != "" && (ref == s.Branch || ref == "refs/heads/"+s.Branch) {
					match = i
					break
				}
			}
			if match < 0 {
				for i, ref := range branches {
					head, e := gitrepo.Resolve(ctx, root, ref)
					if e != nil {
						return nil, e
					}
					if head == s.Head {
						if match >= 0 {
							return nil, errors.New("dirty snapshot requires explicit input association for branches sharing a head")
						}
						match = i
					}
				}
			}
		}
		if match >= 0 {
			head, e := gitrepo.Resolve(ctx, root, branches[match])
			if e != nil {
				return nil, e
			}
			if head != s.Head {
				return nil, errors.New("dirty snapshot does not match selected branch head")
			}
			if inputs[match] != branches[match] {
				return nil, errors.New("multiple dirty snapshots target one branch")
			}
			inputs[match] = sha
		} else {
			inputs = append(inputs, sha)
		}
	}
	// Clean heads were not copied into the seed yet.
	for _, ref := range branches {
		sha, err := gitrepo.Resolve(ctx, root, ref)
		if err != nil {
			return nil, err
		}
		if err = copyDirtyObjects(ctx, root, seed.Dir, sha); err != nil {
			return nil, err
		}
	}
	for i, ref := range inputs {
		if i < len(branches) && ref == branches[i] {
			sha, err := gitrepo.Resolve(ctx, root, ref)
			if err != nil {
				return nil, err
			}
			inputs[i] = sha
		}
	}
	c, err := BuildCandidate(ctx, seed.Dir, seed.Base, inputs)
	if c != nil {
		c.Source = root
	}
	return c, err
}
func writeDirtyCommit(ctx context.Context, root string, s DirtySnapshot) (string, error) {
	paths := make([]string, 0, len(s.files))
	for p := range s.files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	var updates bytes.Buffer
	for _, p := range paths {
		f := s.files[p]
		if f.deleted {
			fmt.Fprintf(&updates, "0 %s\t%s\x00", strings.Repeat("0", len(s.Head)), p)
			continue
		}
		blob, err := dirtyGit(ctx, root, bytes.NewReader(f.data), "hash-object", "-w", "--stdin", "--no-filters")
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&updates, "%s %s\t%s\x00", f.mode, strings.TrimSpace(string(blob)), p)
	}
	if _, err := dirtyGit(ctx, root, &updates, "update-index", "-z", "--index-info"); err != nil {
		return "", err
	}
	tree, err := dirtyGit(ctx, root, nil, "write-tree")
	if err != nil {
		return "", err
	}
	commit, err := dirtyGit(ctx, root, strings.NewReader("Radar dirty preview "+s.Digest+"\n"), "commit-tree", strings.TrimSpace(string(tree)), "-p", s.Head)
	return strings.TrimSpace(string(commit)), err
}
func copyDirtyObjects(ctx context.Context, from, to, sha string) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	producer := gitrepo.PrivateCommand(ctx, from, "pack-objects", "--stdout", "--revs")
	producer.Stdin = strings.NewReader(sha + "\n")
	pipe, err := producer.StdoutPipe()
	if err != nil {
		return err
	}
	consumer := gitrepo.PrivateCommand(ctx, to, "index-pack", "--stdin")
	consumer.Stdin = pipe
	consumer.Stdout = io.Discard
	consumer.Stderr = io.Discard
	producer.Stderr = io.Discard
	if err = consumer.Start(); err != nil {
		return err
	}
	if err = producer.Start(); err != nil {
		pipe.Close()
		consumer.Wait()
		return err
	}
	pErr := producer.Wait()
	cErr := consumer.Wait()
	if pErr != nil || cErr != nil {
		return errors.New("unable to copy dirty preview objects")
	}
	return nil
}
