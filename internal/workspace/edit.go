package workspace

import (
	"fmt"
	"strings"
)

// PathChange records a registered repository whose location was updated.
type PathChange struct {
	ID  string `json:"id"`
	Old string `json:"old"`
	New string `json:"new"`
}

// AddResult describes what Add changed.
type AddResult struct {
	Workspace string      `json:"workspace"`
	Created   bool        `json:"created"`
	Added     []Repo      `json:"added"`
	Updated   *PathChange `json:"updated,omitempty"`
	Unchanged bool        `json:"unchanged"`
	Repo      Repo        `json:"repo"`
}

// Add registers target in the workspace of current, creating the workspace
// (current plus target) when current is in none. id overrides the default
// repo ID; an existing ID given explicitly moves that repo to target. name
// names a new workspace. usable reports why a registered location is gone.
func (r *Registry) Add(current, target Location, id, name string, usable func(Repo) string) (AddResult, error) {
	if id != "" {
		if err := ValidID(id); err != nil {
			return AddResult{}, errorf("radar workspace add "+ShellPath(target.Root)+" --id "+DefaultID(id), "%s", err)
		}
	}
	if name != "" {
		if err := ValidID(name); err != nil {
			return AddResult{}, errorf("radar workspace add "+ShellPath(target.Root)+" --name "+DefaultID(name), "workspace name: %s", err)
		}
	}
	// A repository belongs to one workspace, so every member finds the same scope.
	elsewhere := func(loc Location, mine int) error {
		if wj, rj, ok := r.Find(loc.CommonDir); ok && wj != mine {
			other := r.Workspaces[wj]
			repo := other.Repos[rj]
			return errorf(fmt.Sprintf("radar --root %s workspace remove %s", ShellPath(repo.Path), repo.ID), "%s is already repo %q of workspace %q; a repository belongs to one workspace", loc.Root, repo.ID, other.Name)
		}
		return nil
	}
	wi, _, registered := r.Find(current.CommonDir)
	if !registered {
		if err := elsewhere(target, -1); err != nil {
			return AddResult{}, err
		}
		self := Repo{ID: current.DefaultRepoID(), Path: current.MainPath, CommonDir: current.CommonDir}
		repos := []Repo{self}
		added := Repo{}
		if target.CommonDir == current.CommonDir {
			if id != "" {
				repos[0].ID = id
			}
			added = repos[0]
		} else {
			added = Repo{ID: target.DefaultRepoID(), Path: target.MainPath, CommonDir: target.CommonDir}
			if id != "" {
				added.ID = id
			}
			if added.ID == self.ID {
				return AddResult{}, errorf(fmt.Sprintf("radar workspace add %s --id %s_2", ShellPath(target.Root), added.ID), "repo ID %q is already used by this repository (%s)", added.ID, self.Path)
			}
			repos = append(repos, added)
		}
		if name == "" {
			name = repos[0].ID
		}
		if r.Named(name) >= 0 {
			return AddResult{}, errorf(fmt.Sprintf("radar workspace add %s --name %s_2", ShellPath(target.Root), name), "a workspace named %q already exists", name)
		}
		r.Workspaces = append(r.Workspaces, Workspace{Name: name, Repos: repos})
		return AddResult{Workspace: name, Created: true, Added: repos, Repo: added}, nil
	}
	w := &r.Workspaces[wi]
	if name != "" && name != w.Name {
		return AddResult{}, errorf("radar workspace add "+ShellPath(target.Root), "this repository is already in workspace %q; --name only names a new workspace", w.Name)
	}
	if err := elsewhere(target, wi); err != nil {
		return AddResult{}, err
	}
	for _, repo := range w.Repos {
		if repo.CommonDir == target.CommonDir {
			if id != "" && id != repo.ID {
				return AddResult{}, errorf("radar workspace remove "+repo.ID, "%s is already registered as %q in workspace %q", target.Root, repo.ID, w.Name)
			}
			return AddResult{Workspace: w.Name, Unchanged: true, Added: []Repo{}, Repo: repo}, nil
		}
	}
	added := Repo{ID: target.DefaultRepoID(), Path: target.MainPath, CommonDir: target.CommonDir}
	if id != "" {
		added.ID = id
	}
	for i, repo := range w.Repos {
		if repo.ID != added.ID {
			continue
		}
		// An explicit ID, or a default ID whose registered location is gone,
		// means the repository moved: update its location.
		if id == "" && usable(repo) == "" {
			return AddResult{}, errorf(fmt.Sprintf("radar workspace add %s --id %s_2", ShellPath(target.Root), added.ID), "repo ID %q is already used by %s in workspace %q", added.ID, repo.Path, w.Name)
		}
		w.Repos[i] = added
		return AddResult{Workspace: w.Name, Added: []Repo{}, Updated: &PathChange{ID: added.ID, Old: repo.Path, New: added.Path}, Repo: added}, nil
	}
	w.Repos = append(w.Repos, added)
	return AddResult{Workspace: w.Name, Added: []Repo{added}, Repo: added}, nil
}

// Remove unregisters repo id from the workspace of current. A workspace left
// without repositories is deleted.
func (r *Registry) Remove(current Location, id string) (string, Repo, error) {
	wi, _, ok := r.Find(current.CommonDir)
	if !ok {
		return "", Repo{}, errorf("radar workspace add <PATH>", "this repository is not in a workspace")
	}
	w := &r.Workspaces[wi]
	for i, repo := range w.Repos {
		if repo.ID == id {
			if w.Home == id {
				w.Home = ""
			}
			w.Repos = append(w.Repos[:i], w.Repos[i+1:]...)
			name := w.Name
			if len(w.Repos) == 0 {
				r.Workspaces = append(r.Workspaces[:wi], r.Workspaces[wi+1:]...)
			}
			return name, repo, nil
		}
	}
	ids := w.IDs()
	return "", Repo{}, errorf("use one of the registered repos: "+strings.Join(ids, ", "), "unknown repo %q in workspace %q; registered repos: %s", id, w.Name, strings.Join(ids, ", "))
}
