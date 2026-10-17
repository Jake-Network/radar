package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	gitrepo "github.com/Jake-Network/radar/internal/git"
	"github.com/Jake-Network/radar/internal/integration"
	"github.com/Jake-Network/radar/internal/watch"
)

type hookObservation struct {
	input watchInput
	pins  []integration.DirtySnapshot
}

// A self observation must not read or hash unselected peer files. In
// particular, a busy or unavailable peer cannot starve the saved-change check.
func (a *app) observeHookSelf(ctx context.Context, o options, prior ...hookObservation) (watch.State, error) {
	var pins []integration.DirtySnapshot
	base := o.base
	if base == "" {
		base = gitrepo.DefaultBranch(ctx, a.root)
	}
	if base == "" {
		return watch.State{}, errors.New("no default base; pass --base REF")
	}
	sha, err := gitrepo.Resolve(ctx, a.root, base)
	if err != nil {
		return watch.State{}, err
	}
	in := watchInput{Base: sha, Refs: append([]string(nil), o.args...)}
	for _, ref := range in.Refs {
		head, err := gitrepo.Resolve(ctx, a.root, ref)
		if err != nil {
			return watch.State{}, err
		}
		in.SHAs = append(in.SHAs, head)
	}
	worktrees, err := hookWorktrees(ctx, a.root)
	if err != nil {
		return watch.State{}, err
	}
	if len(worktrees) > 32 || len(in.SHAs) > 32 {
		return watch.State{}, errors.New("hook supports at most 32 worktrees and selected branches")
	}
	local, err := gitrepo.LocalBranches(ctx, a.root)
	if err != nil {
		return watch.State{}, err
	}
	for _, w := range worktrees {
		idx := -1
		for i, ref := range in.Refs {
			named := strings.TrimPrefix(ref, "refs/heads/")
			if named == w.Branch && !w.Detached || ref == "HEAD" && hookSamePath(w.Path, a.root) {
				idx = i
				break
			}
			if ref == "HEAD" || in.SHAs[i] != w.Commit {
				continue
			}
			known := false
			for _, branch := range local {
				if named == branch {
					known = true
					break
				}
			}
			if known {
				continue
			}
			matches := 0
			for _, other := range worktrees {
				if other.Commit == w.Commit {
					matches++
				}
			}
			if matches > 1 {
				return watch.State{}, fmt.Errorf("dirty selection %q matches multiple worktrees; select branch names explicitly", ref)
			}
			idx = i
			break
		}
		if idx < 0 {
			continue
		}
		var snap integration.DirtySnapshot
		pinned := false
		if len(prior) > 0 {
			for _, previous := range prior[0].pins {
				if hookSamePath(previous.Worktree, w.Path) && previous.Head == w.Commit {
					snap, err = integration.ReobserveDirty(ctx, previous)
					pinned = true
					break
				}
			}
			if !pinned {
				return watch.State{}, errors.New("selected worktree input changed during analysis")
			}
		} else {
			snap, err = integration.ObserveDirty(ctx, w)
		}
		if err != nil {
			return watch.State{}, err
		}
		pins = append(pins, snap)
		if len(snap.Staged)+len(snap.Unstaged)+len(snap.Untracked) > 0 {
			if len(in.Snapshots) >= 8 {
				return watch.State{}, errors.New("dirty hook supports at most 8 selected worktree snapshots")
			}
			snap.InputIndex = idx
			in.Snapshots = append(in.Snapshots, snap)
		}
		in.Worktrees = append(in.Worktrees, w)
	}
	b := *a
	b.ctx = ctx
	in.config, err = b.captureConfiguration(o)
	if err != nil {
		return watch.State{}, err
	}
	in.Configuration = in.config.Inputs
	raw, _ := json.Marshal(in)
	return watch.State{Key: configurationDigest(raw), Value: hookObservation{input: in, pins: pins}}, nil
}

func hookSamePath(a, b string) bool {
	x, ex := os.Stat(a)
	y, ey := os.Stat(b)
	if ex == nil && ey == nil {
		return os.SameFile(x, y)
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

// Listing metadata is cheap and does not run status or attribute inspection in
// every sibling worktree. Peers are inspected individually after self preview.
func hookWorktrees(ctx context.Context, root string) ([]gitrepo.WorktreeStatus, error) {
	raw, err := gitrepo.RunPrivate(ctx, root, "worktree", "list", "--porcelain", "-z")
	if err != nil {
		return nil, err
	}
	out := []gitrepo.WorktreeStatus{}
	for _, record := range strings.Split(raw, "\x00\x00") {
		w := gitrepo.WorktreeStatus{}
		bare := false
		for _, field := range strings.Split(record, "\x00") {
			switch {
			case strings.HasPrefix(field, "worktree "):
				w.Path = strings.TrimPrefix(field, "worktree ")
			case strings.HasPrefix(field, "HEAD "):
				w.Commit = strings.TrimPrefix(field, "HEAD ")
			case strings.HasPrefix(field, "branch refs/heads/"):
				w.Branch = strings.TrimPrefix(field, "branch refs/heads/")
			case field == "bare":
				bare = true
			case field == "detached":
				w.Detached = true
			}
		}
		if w.Path != "" && !bare {
			out = append(out, w)
		}
	}
	return out, nil
}
