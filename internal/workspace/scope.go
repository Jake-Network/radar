package workspace

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"sort"
	"strings"
)

// Scope sources: where the set of repositories came from.
const (
	SourceRegistry = "registry"
	SourceWith     = "--with"
	SourceReplay   = "replay"
	SourceTeamFile = "team file"
)

// Error is an invocation error that ends with one next action.
type Error struct {
	Message string
	Next    string
}

func (e *Error) Error() string {
	if e.Next == "" {
		return e.Message
	}
	return e.Message + "\nNext: " + e.Next
}

func errorf(next, format string, args ...any) *Error {
	return &Error{Message: fmt.Sprintf(format, args...), Next: next}
}

// ScopeRepo is one repository in a gate run. Missing explains why its
// registered location cannot be used; such a repo is reported, not checked.
type ScopeRepo struct {
	ID        string `json:"id"`
	Path      string `json:"path"`
	CommonDir string `json:"common_dir"`
	Origin    string `json:"origin"`
	Missing   string `json:"missing,omitempty"`
	TeamBase  string `json:"team_base,omitempty"`
}

// Scope is the resolved set of repositories one gate run checks.
type Scope struct {
	// Workspace is the registered workspace name; empty for a one-off scope
	// built only from --with.
	Workspace string
	// Key names the run-record directory: the workspace name, or a key
	// derived from the current repository for an unregistered one.
	Key    string
	Source string
	// OneOff is set when --with added repositories for this run only.
	OneOff bool
	// Only lists the --only repo IDs; All is every repo ID before --only.
	Only []string
	All  []string
	// Current is the ID of the repository radar runs in.
	Current  string
	Repos    []ScopeRepo
	Excluded []ScopeRepo
}

// Partial reports whether --only excluded repositories.
func (s Scope) Partial() bool { return len(s.Repos) < len(s.All) }

// Has reports whether id is checked in this run.
func (s Scope) Has(id string) bool {
	for _, r := range s.Repos {
		if r.ID == id {
			return true
		}
	}
	return false
}

// ScopeInput is everything scope resolution reads; it performs no I/O.
type ScopeInput struct {
	Registry Registry
	Current  Location
	// With are the --with locations, in argument order, with the raw
	// arguments for messages.
	With     []Location
	WithArgs []string
	Only     []string
	// Usable reports why a registered location cannot be used now, or "".
	Usable func(Repo) string
}

// AdhocKey names the run records of a one-off scope started from a
// repository that is not in a workspace. Every worktree of the repository
// derives the same key.
func AdhocKey(current Location) string {
	sum := sha256.Sum256([]byte(current.CommonDir))
	return "_with-" + current.DefaultRepoID() + "-" + hex.EncodeToString(sum[:4])
}

// ResolveScope applies the scope rules: the registered workspace of the
// current repository, else a one-off scope when --with is given, else nil for
// the unchanged single-repository gate. --with adds repositories and --only
// restricts the run to the named repo IDs.
func ResolveScope(in ScopeInput) (*Scope, error) {
	wi, _, registered := in.Registry.Find(in.Current.CommonDir)
	if !registered && len(in.With) == 0 {
		if len(in.Only) > 0 {
			return nil, errorf("radar workspace add <PATH>", "--only limits a workspace run, but this repository is not in a workspace")
		}
		return nil, nil
	}
	s := &Scope{Source: SourceRegistry}
	if registered {
		w := in.Registry.Workspaces[wi]
		s.Workspace, s.Key = w.Name, w.Name
		for _, repo := range w.Repos {
			sr := ScopeRepo{ID: repo.ID, Path: repo.Path, CommonDir: repo.CommonDir, Origin: SourceRegistry}
			if repo.CommonDir == in.Current.CommonDir {
				s.Current = repo.ID
			}
			if why := in.Usable(repo); why != "" {
				if repo.CommonDir == in.Current.CommonDir {
					// This checkout is the repository; its registered main
					// path is unusable, but the repository itself is here.
					sr.Path = in.Current.Root
				} else {
					sr.Missing = why
				}
			}
			s.Repos = append(s.Repos, sr)
		}
	} else {
		s.Source, s.OneOff = SourceWith, true
		s.Key = AdhocKey(in.Current)
		s.Current = in.Current.DefaultRepoID()
		s.Repos = append(s.Repos, ScopeRepo{ID: s.Current, Path: in.Current.Root, CommonDir: in.Current.CommonDir, Origin: SourceWith})
	}
	for i, loc := range in.With {
		if slices.ContainsFunc(s.Repos, func(r ScopeRepo) bool { return r.CommonDir == loc.CommonDir }) {
			continue
		}
		id := loc.DefaultRepoID()
		if wj, rj, ok := in.Registry.Find(loc.CommonDir); ok {
			id = in.Registry.Workspaces[wj].Repos[rj].ID
		}
		if j := slices.IndexFunc(s.Repos, func(r ScopeRepo) bool { return r.ID == id }); j >= 0 {
			return nil, errorf(fmt.Sprintf("radar workspace add %s --id %s_2", ShellPath(in.WithArgs[i]), id), "--with %s: repo ID %q is already used by %s", in.WithArgs[i], id, s.Repos[j].Path)
		}
		s.OneOff = true
		s.Repos = append(s.Repos, ScopeRepo{ID: id, Path: loc.Root, CommonDir: loc.CommonDir, Origin: SourceWith})
	}
	sort.Slice(s.Repos, func(a, b int) bool { return s.Repos[a].ID < s.Repos[b].ID })
	for _, r := range s.Repos {
		s.All = append(s.All, r.ID)
	}
	if len(in.Only) > 0 {
		keep := map[string]bool{}
		for _, id := range in.Only {
			if !slices.Contains(s.All, id) {
				return nil, s.unknown(id, "--only")
			}
			if !keep[id] {
				keep[id] = true
				s.Only = append(s.Only, id)
			}
		}
		filtered := []ScopeRepo{}
		for _, r := range s.Repos {
			if keep[r.ID] {
				filtered = append(filtered, r)
			}
		}
		s.Repos = filtered
	}
	return s, nil
}

func (s Scope) unknown(id, where string) *Error {
	return errorf("use one of the registered repos: "+strings.Join(s.All, ", "), "unknown repo %q in %s; registered repos: %s", id, where, strings.Join(s.All, ", "))
}

// repoFor maps a target or --base value to the repo ID it names.
func (s Scope) repoFor(t Target, where string) (string, error) {
	id := s.Current
	if t.Qualified {
		id = t.Repo
		if !slices.Contains(s.All, id) {
			return "", s.unknown(id, where+" "+t.String())
		}
	}
	if !s.Has(id) {
		return "", errorf("radar gate --only "+id+" …, or drop "+t.String(), "%s %s names repo %s, which --only excludes", where, t.String(), id)
	}
	return id, nil
}

// AssignTargets groups targets by repo ID, keeping argument order, which is
// the merge order within each repository.
func (s Scope) AssignTargets(targets []Target) (map[string][]string, error) {
	out := map[string][]string{}
	for _, t := range targets {
		id, err := s.repoFor(t, "target")
		if err != nil {
			return nil, err
		}
		if t.Ref == "" {
			return nil, errorf("radar gate "+id+":<BRANCH>", "target %q names no ref", t.String())
		}
		out[id] = append(out[id], t.Ref)
	}
	return out, nil
}

// AssignBases maps --base values (REF for the current repo, REPO:REF for a
// workspace repo) to repo IDs. Two bases for one repo are ambiguous.
func (s Scope) AssignBases(values []string) (map[string]string, error) {
	out := map[string]string{}
	for _, v := range values {
		t := ParseTarget(v)
		id, err := s.repoFor(t, "--base")
		if err != nil {
			return nil, err
		}
		if t.Ref == "" {
			return nil, errorf("radar gate --base "+id+":<REF>", "--base %q names no ref", v)
		}
		if prev, ok := out[id]; ok {
			return nil, errorf("radar gate --base "+id+":"+t.Ref, "two --base values for %s: %s and %s", id, prev, t.Ref)
		}
		out[id] = t.Ref
	}
	return out, nil
}

// Qualify returns targets with every repo spelled out, so a repeated run
// resolves them the same way from any repository of the workspace.
func (s Scope) Qualify(targets []Target) []string {
	out := []string{}
	for _, t := range targets {
		if !t.Qualified {
			t = Target{Repo: s.Current, Ref: t.Ref, Qualified: true}
		}
		out = append(out, t.String())
	}
	return out
}

// QualifyBases is Qualify for --base values.
func (s Scope) QualifyBases(values []string) []string {
	targets := []Target{}
	for _, v := range values {
		targets = append(targets, ParseTarget(v))
	}
	return s.Qualify(targets)
}

// ShellPath quotes a value for a copyable POSIX shell command unless every
// character is known to be safe there. Suggested commands are presentation
// only, but must stay one safe command when pasted.
func ShellPath(p string) string {
	safe := p != ""
	for _, r := range p {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("_./:@%+=,-", r)) {
			safe = false
			break
		}
	}
	if safe {
		return p
	}
	return "'" + strings.ReplaceAll(p, "'", `'"'"'`) + "'"
}
