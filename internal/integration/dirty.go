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
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
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
	stable     bool
}
type dirtyFile struct {
	data    []byte
	mode    string
	deleted bool
}

// IndexHidesWorktree reports skip-worktree or assume-unchanged entries, whose
// saved content Git status and diff do not compare against the index.
// With unmerged set, conflicted entries count as well: Git diff can report no
// change for a conflicted path whose saved file equals the compared tree.
func IndexHidesWorktree(ctx context.Context, root string, unmerged bool) (bool, error) {
	flags, err := dirtyGit(ctx, root, nil, "ls-files", "-v", "-z")
	if err != nil {
		return false, err
	}
	for _, f := range strings.Split(string(flags), "\x00") {
		if len(f) > 1 && (f[0] == 'S' || f[0] >= 'a' && f[0] <= 'z' || unmerged && f[0] == 'M') {
			return true, nil
		}
	}
	return false, nil
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
	b.stable = true
	return b, nil
}

func dirtyGit(ctx context.Context, root string, stdin io.Reader, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := gitrepo.PrivateCommand(ctx, root, args...)
	// Attribute inspection is data-only and must honor repository-local
	// core.attributesFile as well as .gitattributes and .git/info/attributes.
	// Suppressing it could conceal normalization used by source status.
	if len(args) > 0 && args[0] == "check-attr" {
		for i := 1; i < len(cmd.Args); i++ {
			if cmd.Args[i-1] == "-c" && cmd.Args[i] == "core.attributesFile="+gitrepo.NullPath {
				cmd.Args = append(cmd.Args[:i-1], cmd.Args[i+1:]...)
				break
			}
		}
	}
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
	if err := rejectDirtyNormalizationConfig(ctx, root); err != nil {
		return s, err
	}
	w, err := gitrepo.InspectWorktree(ctx, root)
	if err != nil {
		return s, err
	}
	s.Head = w.Commit
	s.Branch = w.Branch
	s.Staged = w.Staged
	s.Unstaged = w.Unstaged
	s.Untracked = w.Untracked
	entries, err := gitrepo.Entries(ctx, root, s.Head)
	if err != nil {
		return s, err
	}
	if err := validateTreeEntries(entries); err != nil {
		return s, err
	}
	index, err := dirtyGit(ctx, root, nil, "ls-files", "--stage", "-z")
	if err != nil {
		return s, err
	}
	tracked := map[string]bool{}
	indexModes := map[string]string{}
	hidden := map[string]bool{}
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
		_, info, err := safeDirtyPathInfo(root, p)
		if err != nil {
			return s, err
		}
		if info == nil {
			hidden[p] = true
		} else {
			if !info.Mode().IsRegular() {
				return s, &UnsupportedEntryError{Path: p}
			}
			mode := "100644"
			if info.Mode()&0111 != 0 {
				mode = "100755"
			}
			if runtime.GOOS != "windows" && mode != f[0] {
				hidden[p] = true
			}
		}
		tracked[p] = true
		indexModes[p] = f[0]
	}
	// Unmerged entries were already rejected from the staged listing above.
	if flagged, err := IndexHidesWorktree(ctx, root, false); err != nil {
		return s, err
	} else if flagged {
		return s, errors.New("sparse or assume-unchanged index entries cannot be previewed")
	}
	for p := range hidden {
		found := false
		for _, v := range s.Unstaged {
			if v == p {
				found = true
				break
			}
		}
		if !found {
			s.Unstaged = append(s.Unstaged, p)
		}
	}
	sort.Strings(s.Unstaged)
	attrPaths := make([]string, 0, len(tracked)+len(s.Untracked))
	for p := range tracked {
		attrPaths = append(attrPaths, p)
	}
	for _, p := range s.Untracked {
		if _, err := safeDirtyPath(root, p); err != nil {
			return s, err
		}
		attrPaths = append(attrPaths, p)
	}
	sort.Strings(attrPaths)
	attrs, err := dirtyGit(ctx, root, strings.NewReader(strings.Join(attrPaths, "\x00")+"\x00"), "check-attr", "-z", "--stdin", "text", "eol", "working-tree-encoding", "ident")
	if err != nil {
		return s, err
	}
	fields := strings.Split(strings.TrimSuffix(string(attrs), "\x00"), "\x00")
	if len(attrPaths) == 0 {
		fields = nil
	}
	if len(fields)%3 != 0 {
		return s, errors.New("dirty normalization attributes could not be inspected")
	}
	for i := 0; i < len(fields); i += 3 {
		if fields[i+2] != "unspecified" && fields[i+2] != "unset" {
			return s, fmt.Errorf("dirty preview does not support Git %s normalization for %s", fields[i+1], fields[i])
		}
	}
	paths := map[string]bool{}
	for _, ps := range [][]string{s.Staged, s.Unstaged, s.Untracked} {
		for _, p := range ps {
			paths[p] = true
		}
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
	h.Write(attrs)
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
				if runtime.GOOS == "windows" {
					if mode := indexModes[p]; mode != "" {
						f.mode = mode
					}
				} else if info.Mode()&0111 != 0 {
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

// Status can conceal normalized raw-byte differences. Until raw checkout
// normalization is reproduced, fail closed rather than verify a different file.
func rejectDirtyNormalizationConfig(ctx context.Context, root string) error {
	value, err := dirtyGit(ctx, root, nil, "config", "--get", "core.autocrlf")
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 1 {
			return nil
		}
		return err
	}
	switch strings.ToLower(strings.TrimSpace(string(value))) {
	case "false", "no", "off", "0":
		return nil
	}
	return errors.New("dirty preview does not support core.autocrlf normalization")
}

func safeDirtyPath(root, p string) (string, error) {
	path, _, err := safeDirtyPathInfo(root, p)
	return path, err
}
func safeDirtyPathInfo(root, p string) (string, os.FileInfo, error) {
	clean, err := pathutil.RepoRelative(p)
	if err != nil {
		return "", nil, &UnsupportedEntryError{Path: p, Unsafe: err}
	}
	for _, part := range strings.Split(clean, "/") {
		if strings.EqualFold(part, ".git") {
			return "", nil, errors.New("dirty snapshot refuses Git metadata paths")
		}
	}
	abs, info, err := pathutil.StatePathInfo(root, clean)
	if err != nil {
		return "", nil, err
	}
	return abs, info, nil
}

// ReobserveDirty validates an already double-captured snapshot with one fresh
// full capture. The prior stable digest is the comparison observation; a new
// unanchored snapshot must always enter through ObserveDirty instead.
func ReobserveDirty(ctx context.Context, prior DirtySnapshot) (DirtySnapshot, error) {
	if prior.Digest == "" || prior.files == nil || !prior.stable {
		return DirtySnapshot{}, errors.New("dirty snapshot has no captured contents")
	}
	fresh, err := captureDirty(ctx, prior.Worktree)
	if err != nil {
		return fresh, err
	}
	if fresh.Head != prior.Head || fresh.Digest != prior.Digest {
		return fresh, errors.New("dirty snapshot superseded before candidate construction")
	}
	fresh.stable = true
	return fresh, nil
}

func revalidateDirtySnapshots(ctx context.Context, snapshots []DirtySnapshot) error {
	return revalidateDirtySnapshotsWith(ctx, snapshots, func(ctx context.Context, snapshot DirtySnapshot) error {
		_, err := ReobserveDirty(ctx, snapshot)
		return err
	})
}

func revalidateDirtySnapshotsWith(ctx context.Context, snapshots []DirtySnapshot, validate func(context.Context, DirtySnapshot) error) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	jobs := make(chan int)
	var firstErr error
	var firstFailure sync.Once
	var workers sync.WaitGroup
	for range min(2, len(snapshots)) {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for i := range jobs {
				err := validate(ctx, snapshots[i])
				if err != nil {
					firstFailure.Do(func() {
						firstErr = err
						cancel()
					})
				}
			}
		}()
	}
dispatch:
	for i := range snapshots {
		select {
		case jobs <- i:
		case <-ctx.Done():
			break dispatch
		}
	}
	close(jobs)
	workers.Wait()
	if firstErr != nil {
		return firstErr
	}
	return ctx.Err()
}

// BuildDirtyCandidate overlays faithful snapshots on their pinned heads in
// one private object store, then reuses the ordinary candidate merge engine.
// Every dirty input is a deterministic synthetic commit bound to its digest.
func BuildDirtyCandidate(ctx context.Context, root, base string, branches []string, snapshots []DirtySnapshot) (*Candidate, error) {
	if len(snapshots) == 0 {
		return BuildCandidate(ctx, root, base, branches)
	}
	// Include every snapshot head in the initial object copy. This also handles
	// the pack-stream fallback, where heads outside the selected branches would
	// otherwise be absent from the private store.
	heads := append([]string(nil), branches...)
	for _, snapshot := range snapshots {
		heads = append(heads, snapshot.Head)
	}
	// Immutable object preparation and source revalidation are independent.
	// Bound the source readers and join them before writing synthetic commits
	// or removing the private repository, including every cancellation path.
	prepareCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	type validationResult struct {
		err         error
		failedFirst bool
	}
	validated := make(chan validationResult, 1)
	go func() {
		err := revalidateDirtySnapshots(prepareCtx, snapshots)
		failedFirst := err != nil && prepareCtx.Err() == nil
		if err != nil {
			cancel()
		}
		validated <- validationResult{err, failedFirst}
	}()
	seed, err := prepareCandidate(prepareCtx, root, base, heads)
	if err != nil {
		cancel()
	}
	validation := <-validated
	validationErr := validation.err
	if err != nil {
		if validation.failedFirst {
			return seed, validationErr
		}
		return seed, err
	}
	if validationErr != nil {
		seed.Close()
		return seed, validationErr
	}
	keep := false
	defer func() {
		if !keep {
			seed.Close()
		}
	}()
	pinned := append([]string(nil), seed.Inputs[:len(branches)]...)
	inputs := append([]string(nil), pinned...)
	assigned := map[int]bool{}
	for _, s := range snapshots {
		if s.Digest == "" || s.files == nil || !s.stable {
			return nil, errors.New("dirty snapshot has no captured contents")
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
				for i, head := range pinned {
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
			if pinned[match] != s.Head {
				return nil, errors.New("dirty snapshot does not match selected branch head")
			}
			if assigned[match] {
				return nil, errors.New("multiple dirty snapshots target one branch")
			}
			assigned[match] = true
		}
		// writeDirtyCommit uses only the index and captured bytes. Keep the private
		// worktree at base while independently resetting the index for each head.
		if _, err := dirtyGit(ctx, seed.Dir, nil, "read-tree", s.Head); err != nil {
			return nil, err
		}
		sha, err := writeDirtyCommit(ctx, seed.Dir, s)
		if err != nil {
			return nil, err
		}
		if match >= 0 {
			inputs[match] = sha
		} else {
			inputs = append(inputs, sha)
		}
	}
	if _, err := dirtyGit(ctx, seed.Dir, nil, "read-tree", seed.Base); err != nil {
		return nil, err
	}
	seed.Inputs = inputs
	candidate, err := mergeCandidate(ctx, seed)
	if err != nil {
		return candidate, err
	}
	keep = true
	return candidate, nil
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
