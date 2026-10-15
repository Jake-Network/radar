package composition

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"

	gitrepo "github.com/Jake-Network/radar/internal/git"
	"github.com/Jake-Network/radar/internal/integration"
	"github.com/Jake-Network/radar/internal/model"
)

// PairInput pins one repository's branch and optional saved changes. Objects
// from different repositories never enter the same private Git candidate.
type PairInput struct {
	Repo        string `json:"repo"`
	Base        string `json:"base"`
	Ref         string `json:"ref"`
	Commit      string `json:"commit"`
	Tree        string `json:"candidate_tree,omitempty"`
	DirtyDigest string `json:"dirty_digest,omitempty"`
}
type BranchPair struct {
	Inputs []PairInput  `json:"inputs"`
	Status model.Status `json:"status"`
	Links  []Link       `json:"links"`
	Reason string       `json:"reason"`
}
type PairReport struct {
	Version      int          `json:"version"`
	Bases        []PairInput  `json:"bases"`
	Status       model.Status `json:"status"`
	Complete     bool         `json:"complete"`
	IncludeDirty bool         `json:"include_dirty"`
	Pairs        []BranchPair `json:"pairs"`
	Omitted      []BranchPair `json:"omitted"`
	Limitations  []string     `json:"limitations"`
}
type pairCandidate struct {
	repo     int
	input    PairInput
	result   Result
	snapshot *integration.DirtySnapshot
}

// Pairwise checks each cross-repository branch pair with all other repositories
// at their exact own bases. It returns full-selection results separately: pair
// compatibility is not evidence that an N-way candidate is compatible.
func Pairwise(ctx context.Context, repos []Repo, team *Team, maxPairs int, includeDirty bool) (PairReport, []Result, error) {
	out := PairReport{Version: 1, Bases: []PairInput{}, Status: model.StatusUnknown, Complete: true, IncludeDirty: includeDirty, Pairs: []BranchPair{}, Omitted: []BranchPair{}, Limitations: []string{"Declared supported cross-repository links only; no runtime interoperability was executed.", "Pair results do not prove N-way compatibility. Other participating repositories remain at their pinned bases."}}
	if maxPairs < 0 || maxPairs > 2016 {
		return out, nil, errors.New("cross-repository pair budget must be between 0 and 2016")
	}
	if maxPairs == 0 {
		maxPairs = 256
	}
	seen := map[string]bool{}
	branchCount := 0
	for _, r := range repos {
		if r.ID == "" || seen[r.ID] {
			return out, nil, errors.New("repositories need distinct registered identities")
		}
		seen[r.ID] = true
		branchCount += len(r.Branches)
		if branchCount > 64 {
			return out, nil, errors.New("cross-repository pair inspection supports at most 64 branches")
		}
		if r.Error != "" || r.Missing != "" || r.Base == "" {
			return out, nil, fmt.Errorf("%s has unavailable pinned inputs", r.ID)
		}
	}
	if team != nil {
		for _, r := range repos {
			if r.ID == team.Home && r.Base != team.Base {
				return out, nil, errors.New("team declaration base differs from pinned home base")
			}
		}
	}
	bases := make([]Result, len(repos))
	originals := make([]Result, len(repos))
	candidates := []pairCandidate{}
	snapshots := make([][]integration.DirtySnapshot, len(repos))
	worktreePins := make([][]gitrepo.WorktreeStatus, len(repos))
	unavailable := map[int]bool{}
	for i, r := range repos {
		br := r
		br.Branches = nil
		bases[i] = Result{Repo: br}
		bases[i].Report = build(ctx, &bases[i], integration.Options{}, team)
		pin := PairInput{Repo: r.ID, Base: r.Base, Ref: r.BaseRef, Commit: r.Base}
		if bases[i].Report != nil {
			pin.Tree = bases[i].Report.CandidateTree
		}
		out.Bases = append(out.Bases, pin)
		if includeDirty {
			var err error
			snapshots[i], worktreePins[i], err = observePairDirty(ctx, r)
			if err != nil {
				if ctx.Err() != nil {
					return out, nil, ctx.Err()
				}
				if errors.Is(err, ErrMoving) {
					return out, nil, err
				}
				unavailable[i] = true
				out.Complete = false
				out.Limitations = append(out.Limitations, fmt.Sprintf("Cross-repository pair inspection incomplete: %s saved changes unavailable: %s", r.ID, err))
				originals[i] = Result{Repo: r}
				originals[i].Error = "saved changes unavailable: " + err.Error()
				for _, b := range r.Branches {
					failed := Result{Repo: r}
					failed.Error = originals[i].Error
					candidates = append(candidates, pairCandidate{repo: i, input: PairInput{Repo: r.ID, Base: r.Base, Ref: b.Ref, Commit: b.Commit}, result: failed})
				}
				continue
			}
		}
		selected := append([]Branch(nil), r.Branches...)
		for _, s := range snapshots[i] {
			found := false
			for _, b := range selected {
				if strings.TrimPrefix(b.Ref, "refs/heads/") == s.Branch && b.Commit == s.Head {
					found = true
				}
			}
			if !found {
				selected = append(selected, Branch{Ref: s.Branch, Commit: s.Head, Source: "saved worktree"})
			}
		}
		sort.SliceStable(selected, func(a, b int) bool { return selected[a].Ref < selected[b].Ref })
		for _, b := range selected {
			c := pairCandidate{repo: i, input: PairInput{Repo: r.ID, Base: r.Base, Ref: b.Ref, Commit: b.Commit}}
			for j := range snapshots[i] {
				s := &snapshots[i][j]
				if s.Branch == strings.TrimPrefix(b.Ref, "refs/heads/") && s.Head == b.Commit {
					if c.snapshot != nil {
						return out, nil, errors.New("multiple dirty worktrees target one branch")
					}
					c.snapshot = s
					c.input.DirtyDigest = s.Digest
				}
			}
			cr := r
			cr.Branches = []Branch{b}
			c.result = Result{Repo: cr}
			if c.snapshot == nil {
				c.result.Report = build(ctx, &c.result, integration.Options{}, team)
			} else {
				ss := *c.snapshot
				ss.InputIndex = 0
				if err := buildPairDirty(ctx, &c.result, []integration.DirtySnapshot{ss}, team); err != nil {
					return out, nil, err
				}
			}
			if c.result.Report != nil {
				c.input.Tree = c.result.Report.CandidateTree
			}
			candidates = append(candidates, c)
			if len(candidates) > 64 {
				return out, nil, errors.New("cross-repository pair inspection supports at most 64 branches")
			}
		}
		originals[i] = Result{Repo: r}
		if len(snapshots[i]) == 0 {
			originals[i].Report = build(ctx, &originals[i], integration.Options{}, team)
		} else {
			if err := buildPairDirty(ctx, &originals[i], snapshots[i], team); err != nil {
				return out, nil, err
			}
		}
	}
	for i, a := range candidates {
		for _, b := range candidates[i+1:] {
			if a.repo == b.repo {
				continue
			}
			pair := BranchPair{Inputs: []PairInput{a.input, b.input}, Status: model.StatusUnknown, Links: []Link{}}
			if len(out.Pairs) >= maxPairs {
				pair.Reason = "pair budget exhausted; not checked"
				out.Omitted = append(out.Omitted, pair)
				out.Complete = false
				continue
			}
			selected := append([]Result(nil), bases...)
			selected[a.repo] = a.result
			selected[b.repo] = b.result
			pair.Links = CheckLinks(selected, team)
			pair.Status = pairLinkStatus(pair.Links)
			pair.Reason = "declared static links checked with other repositories at their own bases"
			for _, r := range selected {
				if r.Error != "" || r.Report == nil || len(r.Report.Conflicts) > 0 {
					if pair.Status != model.StatusFailed {
						pair.Status = model.StatusIncomplete
					}
					pair.Reason = "a repository candidate is unavailable or has textual conflicts"
					break
				}
			}
			if len(pair.Links) == 0 {
				pair.Reason = "no declared cross-repository links; compatibility remains unknown"
			}
			out.Pairs = append(out.Pairs, pair)
		}
	}
	out.Status = model.StatusPassed
	if len(out.Pairs) == 0 || !out.Complete {
		out.Status = model.StatusIncomplete
	}
	for _, p := range out.Pairs {
		if p.Status == model.StatusFailed {
			out.Status = model.StatusFailed
			break
		}
		if p.Status != model.StatusPassed {
			out.Status = model.StatusIncomplete
		}
	}
	// Publishing saved input evidence requires the entire inspection to have
	// observed the same refs and dirty bytes, including after candidate creation.
	for i, r := range repos {
		if unavailable[i] {
			continue
		}
		if r.BaseRef != "" {
			sha, err := gitrepo.Resolve(ctx, r.Path, r.BaseRef)
			if err != nil || sha != r.Base {
				return out, nil, ErrMoving
			}
		}
		for _, b := range r.Branches {
			sha, err := gitrepo.Resolve(ctx, r.Path, b.Ref)
			if err != nil || sha != b.Commit {
				return out, nil, ErrMoving
			}
		}
		if includeDirty {
			fresh, err := gitrepo.InspectWorktrees(ctx, r.Path)
			if err != nil || !reflect.DeepEqual(fresh, worktreePins[i]) {
				return out, nil, errors.New("worktree inputs changed during cross-repository inspection")
			}
		}
		for _, s := range snapshots[i] {
			fresh, err := integration.ObserveDirty(ctx, gitrepo.WorktreeStatus{Path: s.Worktree, Commit: s.Head})
			if err != nil {
				return out, nil, err
			}
			if fresh.Digest != s.Digest {
				return out, nil, errors.New("saved worktree changed during cross-repository inspection")
			}
		}
	}
	return out, originals, nil
}

func pairLinkStatus(links []Link) model.Status {
	if len(links) == 0 {
		return model.StatusUnknown
	}
	s := model.StatusPassed
	for _, l := range links {
		if l.Status == model.StatusFailed {
			return model.StatusFailed
		}
		if l.Status != model.StatusPassed {
			s = model.StatusIncomplete
		}
	}
	return s
}
func buildPairDirty(ctx context.Context, res *Result, snapshots []integration.DirtySnapshot, team *Team) error {
	// Commit SHAs alone cannot distinguish two named worktrees at the same
	// HEAD. Bind each captured worktree to its exact named selection before
	// passing immutable commits to the candidate builder.
	inputs := commits(res.Branches)
	bound := append([]integration.DirtySnapshot(nil), snapshots...)
	assigned := map[int]bool{}
	for i := range bound {
		snapshot := &bound[i]
		match := -1
		for j, branch := range res.Branches {
			if strings.TrimPrefix(branch.Ref, "refs/heads/") == snapshot.Branch {
				if branch.Commit != snapshot.Head {
					return ErrMoving
				}
				if match >= 0 {
					return errors.New("one dirty worktree has multiple selected branch aliases")
				}
				match = j
			}
		}
		if match < 0 {
			// Auto selection can include saved changes on the base worktree.
			// Appending a distinct slot preserves that worktree's identity even
			// when other named inputs share the same head.
			match = len(inputs)
			inputs = append(inputs, snapshot.Head)
		}
		if assigned[match] {
			return errors.New("multiple dirty worktrees target one named input")
		}
		assigned[match] = true
		snapshot.InputIndex = match
	}
	c, err := integration.BuildDirtyCandidate(ctx, res.Path, res.Base, inputs, bound)
	if c != nil {
		defer c.Close()
	}
	if err != nil {
		return err
	}
	cacheFiles(ctx, res, c, team)
	report, err := integration.Analyze(ctx, c, integration.Options{})
	if err != nil {
		return err
	}
	res.Report = &report
	return nil
}

// An unsupported worktree makes only its repository unavailable; callers keep
// unrelated pair evidence and explicitly mark the omitted scope incomplete.
func observePairDirty(ctx context.Context, r Repo) ([]integration.DirtySnapshot, []gitrepo.WorktreeStatus, error) {
	snapshots := []integration.DirtySnapshot{}
	worktrees, err := gitrepo.InspectWorktrees(ctx, r.Path)
	if err != nil {
		return nil, worktrees, err
	}

	for _, wt := range worktrees {
		if wt.InspectionError != "" {
			return nil, worktrees, fmt.Errorf("%s dirty inspection unavailable", r.ID)
		}
		if len(wt.Staged)+len(wt.Unstaged)+len(wt.Untracked) == 0 {
			continue
		}
		if r.Selection == SelectionNamed {
			selected := false
			for _, branch := range r.Branches {
				if strings.TrimPrefix(branch.Ref, "refs/heads/") == wt.Branch {
					if branch.Commit != wt.Commit {
						return nil, worktrees, ErrMoving
					}
					selected = true
				}
			}
			if !selected {
				continue
			}
		}
		if wt.Detached {
			return nil, worktrees, errors.New("dirty detached worktrees require a named branch for cross-repository inspection")
		}
		s, err := integration.ObserveDirty(ctx, wt)
		if err != nil {
			return nil, worktrees, err
		}
		s.InputIndex = -1
		snapshots = append(snapshots, s)
	}
	return snapshots, worktrees, nil
}
