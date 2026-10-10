// Package composition combines the selected changes of every repository in a
// workspace scope. Each repository is pinned, combined and analyzed on its
// own with the integration engine; Git objects of different repositories are
// never mixed. It also derives the composition digest and keeps run records.
package composition

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"sort"
	"strings"

	"github.com/Jake-Network/radar/internal/discovery"
	gitrepo "github.com/Jake-Network/radar/internal/git"
	"github.com/Jake-Network/radar/internal/integration"
	"github.com/Jake-Network/radar/internal/workspace"
)

// Algorithm versions the combination model. It is part of the digest, so a
// change to how candidates are built changes every digest.
const Algorithm = "radar-composition-v1"

// Selection and base sources shown next to every repository.
const (
	SelectionAuto  = "auto: worktree"
	SelectionNamed = "named"
	SelectionBase  = "base only"
	BaseAuto       = "auto"
	BaseNamed      = "named"
	BaseTeam       = "team file"
)

// UnsupportedTree is the guidance for repositories whose trees hold
// submodules or symlinks.
const UnsupportedTree = "trees with submodules or symlinks cannot be combined; register repos bundled as submodules separately with radar workspace add"

// Branch is one pinned input of a repository.
type Branch struct {
	Ref     string   `json:"ref"`
	Commit  string   `json:"commit"`
	Source  string   `json:"source"`
	Changed []string `json:"changed"`
}

// Dirty is a worktree with uncommitted changes. Uncommitted changes never
// enter a candidate; Selected marks worktrees of selected branches.
type Dirty struct {
	Path            string   `json:"path"`
	Branch          string   `json:"branch,omitempty"`
	Selected        bool     `json:"selected"`
	Staged          []string `json:"staged"`
	Unstaged        []string `json:"unstaged"`
	Untracked       []string `json:"untracked"`
	InspectionError string   `json:"inspection_error,omitempty"`
}

// Count is the number of distinct uncommitted paths.
func (d Dirty) Count() int {
	seen := map[string]bool{}
	for _, list := range [][]string{d.Staged, d.Unstaged, d.Untracked} {
		for _, p := range list {
			seen[p] = true
		}
	}
	return len(seen)
}

// Detached is a detached worktree whose commit was not selected.
type Detached struct {
	Path   string `json:"path"`
	Commit string `json:"commit"`
}

// Repo is the pinned selection of one repository. Error, with its next
// action, marks a repository that cannot be checked; other repositories
// are still checked.
type Repo struct {
	workspace.ScopeRepo
	BaseRef                 string     `json:"base_ref"`
	Base                    string     `json:"base"`
	BaseSource              string     `json:"base_source"`
	Selection               string     `json:"selection_source"`
	Branches                []Branch   `json:"branches"`
	Skipped                 []string   `json:"skipped_branches,omitempty"`
	Detached                []Detached `json:"detached,omitempty"`
	Dirty                   []Dirty    `json:"dirty"`
	WorktreeInspectionError string     `json:"worktree_inspection_error,omitempty"`
	Error                   string     `json:"error,omitempty"`
	Next                    string     `json:"next,omitempty"`
	// expectTree is the recorded candidate tree a replay must reproduce.
	expectTree      string
	expectConflicts []string
}

// Request selects what Collect pins. Named maps repo IDs to refs in merge
// order; nil Named selects worktree branches automatically. Replay pins the
// exact commits of a recorded run instead of resolving refs.
type Request struct {
	Scope  workspace.Scope
	Named  map[string][]string
	Bases  map[string]string
	Replay *Record
}

// collected is called after each collection round; tests use it to move
// refs while Radar collects them.
var collected = func(round int) {}

// ErrMoving reports refs that moved during both collection rounds.
var ErrMoving = errors.New("refs kept moving while Radar pinned them; rerun when no agent is committing")

// Collect pins every repository's base and inputs to commit SHAs. When a ref
// moves while refs are being pinned, it pins again once; refs that move
// again are an error. Only then does it inspect worktrees and changes.
func Collect(ctx context.Context, req Request) ([]Repo, error) {
	var repos []Repo
	if req.Replay != nil {
		repos = replayed(ctx, req)
	} else {
		repos = pin(ctx, req)
		collected(0)
		if check := pin(ctx, req); !reflect.DeepEqual(pins(check), pins(repos)) {
			// A ref moved while pinning: pin once more and confirm.
			repos = check
			collected(1)
			if confirm := pin(ctx, req); !reflect.DeepEqual(pins(confirm), pins(repos)) {
				return nil, ErrMoving
			}
		}
	}
	for i := range repos {
		describe(ctx, &repos[i])
	}
	return repos, nil
}

// pins is the comparable part of a pinned selection.
func pins(repos []Repo) []string {
	out := []string{}
	for _, r := range repos {
		line := r.ID + "\x00" + r.Base + "\x00" + r.Error
		for _, b := range r.Branches {
			line += "\x00" + b.Ref + "=" + b.Commit
		}
		out = append(out, line)
	}
	return out
}

func failed(r *Repo, next, format string, args ...any) {
	r.Error = fmt.Sprintf(format, args...)
	r.Next = next
}

func pin(ctx context.Context, req Request) []Repo {
	repos := []Repo{}
	for _, sr := range req.Scope.Repos {
		r := Repo{ScopeRepo: sr, Branches: []Branch{}, Dirty: []Dirty{}}
		repos = append(repos, r)
		p := &repos[len(repos)-1]
		if sr.Missing != "" {
			failed(p, "radar workspace add <NEW PATH> --id "+sr.ID, "%s: %s", sr.ID, sr.Missing)
			continue
		}
		p.BaseRef, p.BaseSource = req.Bases[sr.ID], BaseNamed
		if p.BaseRef == "" && sr.TeamBase != "" {
			p.BaseRef, p.BaseSource = sr.TeamBase, BaseTeam
		}
		if p.BaseRef == "" {
			p.BaseSource = BaseAuto
			if p.BaseRef = gitrepo.DefaultBranch(ctx, sr.Path); p.BaseRef == "" {
				failed(p, "radar gate --base "+sr.ID+":<REF>", "%s: no main, master or trunk branch found", sr.ID)
				continue
			}
		}
		var err error
		if p.Base, err = gitrepo.Resolve(ctx, sr.Path, p.BaseRef); err != nil {
			failed(p, "radar gate --base "+sr.ID+":<REF>", "%s: base %q is not a commit", sr.ID, p.BaseRef)
			continue
		}
		if req.Named != nil {
			for _, ref := range req.Named[sr.ID] {
				sha, err := gitrepo.Resolve(ctx, sr.Path, ref)
				if err != nil {
					failed(p, "radar workspace show", "%s:%s is not a commit in %s", sr.ID, ref, sr.Path)
					break
				}
				p.Branches = append(p.Branches, Branch{Ref: ref, Commit: sha, Source: SelectionNamed, Changed: []string{}})
			}
			p.Selection = SelectionNamed
		} else {
			auto(ctx, p)
			p.Selection = SelectionAuto
		}
		if len(p.Branches) == 0 && p.Error == "" {
			p.Selection = SelectionBase
		}
	}
	return repos
}

// auto applies the single-repository rule to one repository: every worktree
// branch with commits beyond the base, here in ref-name order.
func auto(ctx context.Context, r *Repo) {
	worktrees, err := gitrepo.WorktreeBranches(ctx, r.Path)
	if err != nil {
		failed(r, "radar workspace show", "%s: worktrees cannot be listed: %v", r.ID, err)
		return
	}
	sort.Strings(worktrees)
	for _, ref := range worktrees {
		sha, e := gitrepo.Resolve(ctx, r.Path, ref)
		if e != nil || ref == r.BaseRef || strings.TrimPrefix(r.BaseRef, "origin/") == ref {
			continue
		}
		if mb, e := gitrepo.MergeBase(ctx, r.Path, r.Base, sha); e == nil && mb == sha {
			r.Skipped = append(r.Skipped, ref+" (no commits beyond "+r.BaseRef+")")
			continue
		}
		r.Branches = append(r.Branches, Branch{Ref: ref, Commit: sha, Source: SelectionAuto, Changed: []string{}})
	}
}

func replayed(ctx context.Context, req Request) []Repo {
	recorded := map[string]RecordRepo{}
	for _, r := range req.Replay.Repos {
		recorded[r.ID] = r
	}
	repos := []Repo{}
	for _, sr := range req.Scope.Repos {
		rec := recorded[sr.ID]
		r := Repo{ScopeRepo: sr, BaseRef: rec.BaseRef, Base: rec.Base, BaseSource: rec.BaseSource, Selection: rec.Selection, Branches: []Branch{}, Dirty: []Dirty{}, expectTree: rec.CandidateTree, expectConflicts: rec.Conflicts}
		for _, b := range rec.Branches {
			r.Branches = append(r.Branches, Branch{Ref: b.Ref, Commit: b.Commit, Source: b.Source, Changed: []string{}})
		}
		switch {
		case sr.Missing != "":
			failed(&r, "radar workspace add <NEW PATH> --id "+sr.ID, "%s: %s", sr.ID, sr.Missing)
		case rec.Error != "":
			failed(&r, "radar gate --again", "not reproducible: %s had no candidate in run %s (%s)", sr.ID, req.Replay.RunID, rec.Error)
		default:
			for _, sha := range append([]string{r.Base}, commits(r.Branches)...) {
				if _, err := gitrepo.Resolve(ctx, sr.Path, sha); err != nil {
					failed(&r, "radar gate --again", "not reproducible: commit %s is no longer in %s", short(sha), sr.ID)
					break
				}
			}
		}
		repos = append(repos, r)
	}
	return repos
}

// describe adds worktree state and per-branch changes to a pinned repo.
func describe(ctx context.Context, r *Repo) {
	if r.Error != "" || r.Path == "" {
		return
	}
	for i := range r.Branches {
		b := &r.Branches[i]
		if mb, err := gitrepo.MergeBase(ctx, r.Path, r.Base, b.Commit); err == nil {
			if changed, err := gitrepo.ChangedFiles(ctx, r.Path, mb, b.Commit); err == nil {
				b.Changed = changed
			}
		}
	}
	worktrees, err := gitrepo.InspectWorktrees(ctx, r.Path)
	if err != nil {
		r.WorktreeInspectionError = err.Error()
		return
	}
	selected := map[string]bool{}
	for _, b := range r.Branches {
		selected[b.Ref] = true
	}
	inputs := append([]string{r.Base}, commits(r.Branches)...)
	for _, wt := range worktrees {
		if wt.Detached && !slices.Contains(inputs, wt.Commit) {
			r.Detached = append(r.Detached, Detached{Path: wt.Path, Commit: wt.Commit})
		}
		if wt.InspectionError != "" || len(wt.Staged)+len(wt.Unstaged)+len(wt.Untracked) > 0 {
			r.Dirty = append(r.Dirty, Dirty{Path: wt.Path, Branch: wt.Branch, Selected: !wt.Detached && selected[wt.Branch], Staged: wt.Staged, Unstaged: wt.Unstaged, Untracked: wt.Untracked, InspectionError: wt.InspectionError})
		}
	}
}

func commits(branches []Branch) []string {
	out := []string{}
	for _, b := range branches {
		out = append(out, b.Commit)
	}
	return out
}

func short(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

// Result is a repository's pinned selection and, when it could be built, the
// integration report of its candidate.
type Result struct {
	Repo
	Report    *integration.Report  `json:"report,omitempty"`
	Files     map[string]FileSides `json:"-"`
	Discovery *discovery.Report    `json:"-"`
}

// Build combines and analyzes every repository that has no error, one at a
// time and each in its own private repository. A repository that fails to
// build gets an error; the others are still built. With a replayed selection
// the candidate must reproduce the recorded tree. A repository with no
// selected branch takes part at its base: nothing is combined there, so it
// is analyzed without execution and execution is not required of it.
func Build(ctx context.Context, repos []Repo, o integration.Options, teams ...*Team) []Result {
	var team *Team
	if len(teams) > 0 {
		team = teams[0]
	}
	results := []Result{}
	cacheTeam := team
	ordered := append([]Repo(nil), repos...)
	if team != nil {
		sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].ID == team.Home && ordered[j].ID != team.Home })
	}
	for _, r := range ordered {
		res := Result{Repo: r}
		if r.Error == "" {
			ro := o
			if len(r.Branches) == 0 {
				ro.Verify, ro.AllowExecution, ro.Suite, ro.Command = false, false, "", nil
			}
			res.Report = build(ctx, &res, ro, cacheTeam)
		}
		results = append(results, res)
		if team != nil && res.ID == team.Home {
			if candidate, err := workspace.ParseTeam(res.Files[workspace.TeamPath].Candidate); err == nil {
				copy := *team
				copy.File = unionTeam(team.File, candidate)
				cacheTeam = &copy
			}
		}
	}
	order := map[string]int{}
	for i, r := range repos {
		order[r.ID] = i
	}
	sort.SliceStable(results, func(i, j int) bool { return order[results[i].ID] < order[results[j].ID] })
	return results
}

func build(ctx context.Context, res *Result, o integration.Options, team *Team) *integration.Report {
	r := &res.Repo
	c, err := integration.BuildCandidate(ctx, r.Path, r.Base, commits(r.Branches))
	defer c.Close()
	var unsupported *integration.UnsupportedEntryError
	switch {
	case errors.As(err, &unsupported):
		failed(r, "radar workspace show", "%s: %s (%s)", r.ID, UnsupportedTree, unsupported.Path)
		return nil
	case err != nil:
		failed(r, "radar gate --again", "%s: %v", r.ID, err)
		return nil
	}
	if r.expectTree != "" || len(r.expectConflicts) > 0 {
		if c.Tree != r.expectTree || !slices.Equal(c.Conflicts, r.expectConflicts) {
			failed(r, "radar gate --again", "not reproducible: %s candidate tree %s differs from the recorded %s", r.ID, short(c.Tree), short(r.expectTree))
			return nil
		}
	}
	cacheFiles(ctx, res, c, team)
	report, err := integration.Analyze(ctx, c, o)
	if err != nil {
		failed(r, "radar gate --again", "%s: %v", r.ID, err)
		return nil
	}
	return &report
}

// Digest identifies a composition by what was combined: repo IDs, base SHAs,
// ordered input SHAs, candidate trees (or conflicts) and the algorithm. It
// excludes paths, times and temporary directories, so the same selection
// has the same digest from any checkout, and a moved repository keeps it.
func Digest(results []Result, teams ...*Team) string {
	type entry struct {
		ID        string   `json:"id"`
		Base      string   `json:"base"`
		Inputs    []string `json:"inputs"`
		Tree      string   `json:"tree"`
		Conflicts []string `json:"conflicts"`
		Error     bool     `json:"error"`
	}
	entries := []entry{}
	for _, r := range results {
		e := entry{ID: r.ID, Base: r.Base, Inputs: commits(r.Branches), Conflicts: []string{}, Error: r.Error != ""}
		if r.Report != nil {
			e.Tree, e.Conflicts = r.Report.CandidateTree, r.Report.Conflicts
		}
		entries = append(entries, e)
	}
	sort.Slice(entries, func(a, b int) bool { return entries[a].ID < entries[b].ID })
	input := map[string]any{"algorithm": Algorithm, "repos": entries}
	if len(teams) > 0 && teams[0] != nil {
		input["team"] = teams[0].Digest
	}
	data, _ := json.Marshal(input)
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// UnsupportedEntries lists submodule and symlink paths in the tree at ref,
// at most limit of them, so they can be reported before a gate run.
func UnsupportedEntries(ctx context.Context, root, ref string, limit int) ([]string, error) {
	entries, err := gitrepo.Entries(ctx, root, ref)
	if err != nil {
		return nil, err
	}
	out := []string{}
	for _, e := range entries {
		if e.Mode != "100644" && e.Mode != "100755" {
			if out = append(out, e.Path); len(out) == limit {
				break
			}
		}
	}
	return out, nil
}
