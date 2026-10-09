package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/Jake-Network/radar/internal/composition"
	"github.com/Jake-Network/radar/internal/gate"
	"github.com/Jake-Network/radar/internal/workspace"
)

func workspaceCommand() command {
	return command{name: "workspace", usage: "radar workspace add PATH [--id ID] [--name NAME] | radar workspace show",
		summary: "Group repositories so radar gate checks them together from any of them: add PATH, show.",
		flags: func(fs *flag.FlagSet, o *options) {
			fs.StringVar(&o.id, "id", "", "repo `ID` for add (default: the main worktree's folder name)")
			fs.StringVar(&o.name, "name", "", "workspace `NAME` when add creates one (default: this repository's ID)")
		}, run: (*app).workspace}
}

func (a *app) workspace(o options) int {
	sub, rest := "", []string{}
	if len(o.args) > 0 {
		sub, rest = o.args[0], o.args[1:]
	}
	if sub != "add" && (o.id != "" || o.name != "") {
		return a.fail(wsError("radar workspace add PATH --id ID", "--id and --name apply only to workspace add"))
	}
	if e := a.repository(); e != nil {
		return a.fail(e)
	}
	current, err := workspace.Locate(a.ctx, a.root)
	if err != nil {
		return a.fail(err)
	}
	switch {
	case sub == "add" && len(rest) == 1:
		return a.workspaceAdd(current, rest[0], o)
	case sub == "remove" && len(rest) == 1:
		return a.workspaceRemove(current, rest[0])
	case sub == "show" && len(rest) == 0:
		return a.workspaceShow(current)
	}
	return a.fail(wsError("radar workspace add PATH, or radar workspace show", "usage: radar workspace add PATH [--id ID] [--name NAME] | radar workspace show"))
}

// unsupportedWarning reports submodules or symlinks before a gate run meets them.
func (a *app) unsupportedWarning(id, root, ref string) string {
	paths, err := composition.UnsupportedEntries(a.ctx, root, ref, 3)
	if err != nil || len(paths) == 0 {
		return ""
	}
	return fmt.Sprintf("%s: %s (%s)", id, composition.UnsupportedTree, strings.Join(paths, ", "))
}

func (a *app) workspaceAdd(current workspace.Location, path string, o options) int {
	if !filepath.IsAbs(path) {
		path = filepath.Join(a.root, path)
	}
	target, err := workspace.Locate(a.ctx, path)
	if err != nil {
		return a.fail(wsError("radar workspace add <PATH of a Git checkout>", "%v", err))
	}
	dir, err := workspace.ConfigDir()
	if err != nil {
		return a.fail(err)
	}
	var result workspace.AddResult
	err = workspace.Update(dir, func(r *workspace.Registry) error {
		var e error
		result, e = r.Add(current, target, o.id, o.name, a.usable)
		return e
	})
	if err != nil {
		return a.fail(err)
	}
	warnings := []string{}
	check := []workspace.Repo{result.Repo}
	if result.Created {
		check = result.Added
	}
	for _, repo := range check {
		if w := a.unsupportedWarning(repo.ID, repo.Path, "HEAD"); w != "" {
			warnings = append(warnings, w)
		}
	}
	out := map[string]any{"result": result, "warnings": warnings, "next": "radar gate"}
	a.report(out, func(w io.Writer) {
		switch {
		case result.Created:
			names := []string{}
			for _, repo := range result.Added {
				names = append(names, fmt.Sprintf("%s (%s)", repo.ID, repo.Path))
			}
			fmt.Fprintf(w, "Created workspace %q: %s\n", result.Workspace, strings.Join(names, ", "))
		case result.Updated != nil:
			fmt.Fprintf(w, "Updated %s in workspace %q: %s → %s\n", result.Updated.ID, result.Workspace, result.Updated.Old, result.Updated.New)
		case result.Unchanged:
			fmt.Fprintf(w, "%s (%s) is already in workspace %q; nothing changed.\n", result.Repo.ID, result.Repo.Path, result.Workspace)
		default:
			fmt.Fprintf(w, "Added %s (%s) to workspace %q.\n", result.Repo.ID, result.Repo.Path, result.Workspace)
		}
		for _, warning := range warnings {
			fmt.Fprintf(w, "  ! %s\n", warning)
		}
		fmt.Fprintln(w, "Radar recorded paths only: no remote access, no installs, no tests.")
		fmt.Fprintln(w, "\nNext: radar gate")
	})
	return 0
}

func (a *app) workspaceRemove(current workspace.Location, id string) int {
	dir, err := workspace.ConfigDir()
	if err != nil {
		return a.fail(err)
	}
	var name string
	var removed workspace.Repo
	deleted := false
	err = workspace.Update(dir, func(r *workspace.Registry) error {
		var e error
		name, removed, e = r.Remove(current, id)
		deleted = e == nil && r.Named(name) < 0
		return e
	})
	if err != nil {
		return a.fail(err)
	}
	next := "radar workspace show"
	if deleted {
		next = "radar workspace add <PATH>"
	}
	a.report(map[string]any{"workspace": name, "removed": removed, "workspace_deleted": deleted, "next": next}, func(w io.Writer) {
		fmt.Fprintf(w, "Removed %s (%s) from workspace %q.\n", removed.ID, removed.Path, name)
		if deleted {
			fmt.Fprintf(w, "Workspace %q has no repositories left and was removed.\n", name)
		}
		fmt.Fprintf(w, "\nNext: %s\n", next)
	})
	return 0
}

type showRepo struct {
	ID         string              `json:"id"`
	Path       string              `json:"path"`
	CommonDir  string              `json:"common_dir"`
	BaseRef    string              `json:"base_ref,omitempty"`
	Base       string              `json:"base,omitempty"`
	BaseSource string              `json:"base_source,omitempty"`
	WouldPick  []string            `json:"would_select"`
	Dirty      []composition.Dirty `json:"dirty"`
	Warnings   []string            `json:"warnings"`
	Next       string              `json:"next,omitempty"`
}

type showRun struct {
	RunID     string       `json:"run_id"`
	CreatedAt string       `json:"created_at"`
	Verdict   gate.Verdict `json:"verdict"`
	Digest    string       `json:"digest"`
}

func (a *app) workspaceShow(current workspace.Location) int {
	registry, err := a.registry()
	if err != nil {
		return a.fail(wsError("fix or remove the registry file, then rerun", "%v", err))
	}
	scope, err := workspace.ResolveScope(workspace.ScopeInput{Registry: registry, Current: current, Usable: a.usable})
	if err != nil {
		return a.fail(err)
	}
	if scope == nil {
		next := "radar workspace add <PATH>"
		a.report(map[string]any{"workspace": nil, "next": next}, func(w io.Writer) {
			fmt.Fprintln(w, "This repository is not in a workspace; radar gate checks it alone.")
			fmt.Fprintf(w, "\nNext: %s\n", next)
		})
		return 0
	}
	repos, err := composition.Collect(a.ctx, composition.Request{Scope: *scope})
	if err != nil && !errors.Is(err, composition.ErrMoving) {
		return a.fail(err)
	}
	out := []showRepo{}
	for _, r := range repos {
		s := showRepo{ID: r.ID, Path: r.Path, CommonDir: r.CommonDir, BaseRef: r.BaseRef, Base: r.Base, BaseSource: r.BaseSource, WouldPick: []string{}, Dirty: r.Dirty, Warnings: []string{}, Next: r.Next}
		for _, b := range r.Branches {
			s.WouldPick = append(s.WouldPick, b.Ref)
		}
		if r.Error != "" {
			s.Warnings = append(s.Warnings, r.Error)
		} else if w := a.unsupportedWarning(r.ID, r.Path, r.Base); w != "" {
			s.Warnings = append(s.Warnings, w)
		}
		out = append(out, s)
	}
	runs := []showRun{}
	if store, err := composition.OpenStore(scope.Key); err == nil {
		for _, rec := range store.Recent(5) {
			runs = append(runs, showRun{RunID: rec.RunID, CreatedAt: rec.CreatedAt, Verdict: gate.Verdict(rec.Verdict), Digest: rec.Digest})
		}
	}
	report := map[string]any{"workspace": scope.Workspace, "scope_source": scope.Source, "current": scope.Current, "repos": out, "recent_runs": runs, "next": "radar gate"}
	a.report(report, func(w io.Writer) {
		fmt.Fprintf(w, "Workspace %q · %s · scope from the local registry (this machine only)\n", scope.Workspace, plural(len(out), "repo", "repos"))
		for _, r := range out {
			marker := ""
			if r.ID == scope.Current {
				marker = "  (current)"
			}
			fmt.Fprintf(w, "\n  %s%s\n    path:        %s\n    common dir:  %s\n", r.ID, marker, r.Path, r.CommonDir)
			if r.Base != "" {
				fmt.Fprintf(w, "    base:        %s@%s (%s)\n", r.BaseRef, short(r.Base), r.BaseSource)
				picks := "nothing beyond the base"
				if len(r.WouldPick) > 0 {
					picks = strings.Join(r.WouldPick, ", ")
				}
				fmt.Fprintf(w, "    radar gate:  %s\n", picks)
			}
			for _, d := range r.Dirty {
				label := d.Branch
				if label == "" {
					label = "detached"
				}
				fmt.Fprintf(w, "    dirty:       %s (%s): %d uncommitted, excluded from candidates\n", d.Path, label, d.Count())
			}
			for _, warning := range r.Warnings {
				fmt.Fprintf(w, "    ! %s\n", warning)
			}
			if r.Next != "" {
				fmt.Fprintf(w, "      next: %s\n", r.Next)
			}
		}
		if len(runs) > 0 {
			fmt.Fprintln(w, "\nRecent runs:")
			for _, r := range runs {
				fmt.Fprintf(w, "  %s  %s  %s\n", r.RunID, r.CreatedAt, verdictMark[r.Verdict])
			}
		}
		fmt.Fprintln(w, "\nNext: radar gate")
	})
	return 0
}
