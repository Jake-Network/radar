package cli

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/Jake-Network/radar/internal/contracts"
	gitrepo "github.com/Jake-Network/radar/internal/git"
	"github.com/Jake-Network/radar/internal/pathutil"
	"github.com/Jake-Network/radar/internal/workspace"
)

// connect serializes read/modify/write of both declarations with the registry
// lock. Git refs, HEAD and index are never changed.
func (a *app) workspaceConnect(current workspace.Location, producer, consumer string, o options) int {
	p, err := workspace.ParseProducer(producer)
	if err != nil {
		return a.fail(err)
	}
	if o.direction != "request" && o.direction != "response" {
		return a.fail(wsError("radar help --all", "connect requires --direction request|response"))
	}
	fields := []string{}
	for _, f := range strings.Split(o.fields, ",") {
		f = strings.TrimSpace(f)
		if f != "" && !slices.Contains(fields, f) {
			fields = append(fields, f)
		}
	}
	if len(fields) == 0 {
		return a.fail(wsError("radar help --all", "connect requires --fields a,b"))
	}
	source := o.source
	if source != "" {
		source, err = pathutil.RepoRelative(source)
		if err != nil {
			return a.fail(wsError("use --source with a repository-relative path", "%v", err))
		}
	}
	id := o.id
	if id == "" {
		prefix := p.Repo + "-" + consumer + "-" + o.direction
		if len(prefix) > 55 {
			prefix = prefix[:55]
		}
		// Separate documents/pointers of the same repo pair must not replace
		// one another when users accept multiple discovery proposals.
		sum := sha256.Sum256([]byte(producer + "\x00" + consumer + "\x00" + o.direction))
		id = fmt.Sprintf("%s-%x", prefix, sum[:4])
	}
	if err = workspace.ValidID(id); err != nil {
		return a.fail(wsError("radar help --all", "%v", err))
	}
	dir, err := workspace.ConfigDir()
	if err != nil {
		return a.fail(err)
	}
	var homePath, consumerPath string
	var oldLink *workspace.Link
	var oldConsume *workspace.Consume
	var newLink workspace.Link
	var newConsume workspace.Consume
	wroteDeclarations := false
	err = workspace.Update(dir, func(reg *workspace.Registry) error {
		wi, ri, ok := reg.Find(current.CommonDir)
		if !ok {
			return wsError("radar workspace add <PATH>", "this repository is not in a workspace")
		}
		w := &reg.Workspaces[wi]
		paths := map[string]string{}
		for _, r := range w.Repos {
			paths[r.ID] = r.Path
		}
		for _, arg := range o.into {
			rid, path, ok := strings.Cut(arg, ":")
			if !ok {
				return wsError("use --into repo:PATH", "invalid --into %q", arg)
			}
			r, ok := w.Repo(rid)
			if !ok {
				return wsError("radar workspace show", "unknown repo %q in --into", rid)
			}
			if !filepath.IsAbs(path) {
				path = filepath.Join(a.root, path)
			}
			loc, e := workspace.Locate(a.ctx, path)
			if e != nil || loc.CommonDir != r.CommonDir {
				return wsError("use --into with a worktree of "+rid, "--into %s does not locate repo %s", arg, rid)
			}
			if slices.ContainsFunc(o.into, func(other string) bool { return other != arg && strings.HasPrefix(other, rid+":") }) {
				return wsError("use one --into per repo", "duplicate --into for %s", rid)
			}
			paths[rid] = loc.Root
		}
		if _, ok := w.Repo(p.Repo); !ok {
			return wsError("radar workspace add <PATH> --id "+p.Repo, "unknown producer repo %s", p.Repo)
		}
		if _, ok := w.Repo(consumer); !ok {
			return wsError("radar workspace add <PATH> --id "+consumer, "unknown consumer repo %s", consumer)
		}
		// Use the committed team file for unique-home discovery, never uncommitted
		// declarations from an arbitrary checkout.
		home := w.Home
		if home == "" {
			for _, r := range w.Repos {
				base := gitrepo.DefaultBranch(a.ctx, r.Path)
				if base == "" {
					continue
				}
				if _, e := gitrepo.ReadFile(a.ctx, r.Path, base, workspace.TeamPath); e == nil {
					if home != "" {
						return wsError("resolve the multiple team files before connecting", "multiple repos have a team file; home is ambiguous")
					}
					home = r.ID
				}
			}
			if home == "" {
				home = w.Repos[ri].ID
			}
		}
		if _, ok := w.Repo(home); !ok {
			return wsError("radar workspace show", "home repo %s is not registered", home)
		}
		usable := func(id string) error {
			r, ok := w.Repo(id)
			if !ok {
				return wsError("radar workspace add <PATH> --id "+id, "team repo %s is not registered", id)
			}
			if why := a.usable(workspace.Repo{ID: id, Path: paths[id], CommonDir: r.CommonDir}); why != "" {
				return wsError("radar workspace add <PATH> --id "+id, "%s", why)
			}
			return nil
		}
		for _, id := range []string{home, p.Repo, consumer} {
			if e := usable(id); e != nil {
				return e
			}
		}
		docPath, e := pathutil.ResolveInside(paths[p.Repo], p.Path)
		if e != nil {
			return wsError("restore the producer document, then retry connect", "%v", e)
		}
		data, e := os.ReadFile(docPath)
		if e != nil {
			return e
		}
		doc, e := contracts.DecodeDocumentAt(p.Path, data)
		if e != nil {
			return wsError("fix the producer document, then retry connect", "%v", e)
		}
		schema, e := contracts.Analyzable(doc, p.Pointer)
		if e != nil {
			return wsError("use an analyzable producer pointer", "%v", e)
		}
		for _, f := range fields {
			field, ok := schema.Field(f)
			if !ok {
				return wsError("use fields present at the producer pointer", "producer field %q is missing", f)
			}
			if field.Opaque != "" || len(field.Notes("")) > 0 {
				return wsError("use analyzable consumer fields", "producer field %q is not analyzable", f)
			}
		}
		homePath, e = pathutil.StatePath(paths[home], workspace.TeamPath)
		if e != nil {
			return wsError("remove the unsafe team file path", "%v", e)
		}
		consumerPath, e = pathutil.StatePath(paths[consumer], workspace.ConsumesPath)
		if e != nil {
			return wsError("remove the unsafe consumes file path", "%v", e)
		}
		homeBytes, e := os.ReadFile(homePath)
		if e != nil && !errors.Is(e, os.ErrNotExist) {
			return e
		}
		var team *workspace.TeamFile
		if e == nil {
			team, e = workspace.ParseTeam(homeBytes)
			if e != nil {
				return e
			}
		} else {
			team = &workspace.TeamFile{Version: 1, Repos: []workspace.TeamRepo{}, Links: []workspace.Link{}}
			for _, r := range w.Repos {
				if e := usable(r.ID); e != nil {
					return e
				}
				identity := gitrepo.Identity(a.ctx, paths[r.ID])
				if identity == "" || !strings.HasPrefix(identity, "git:") {
					return wsError("ensure each repo has a committed history", "cannot identify repo %s", r.ID)
				}
				team.Repos = append(team.Repos, workspace.TeamRepo{ID: r.ID, Identity: identity})
			}
		}
		members := map[string]bool{}
		for _, r := range team.Repos {
			if e := usable(r.ID); e != nil {
				return e
			}
			members[r.ID] = true
			path, ok := paths[r.ID]
			if !ok {
				return wsError("radar workspace add <PATH> --id "+r.ID, "team repo %s is not registered", r.ID)
			}
			if gitrepo.Identity(a.ctx, path) != r.Identity {
				return wsError("radar workspace add <PATH> --id "+r.ID, "repo %s identity differs from team file", r.ID)
			}
		}
		if !members[p.Repo] || !members[consumer] {
			return wsError("add both repos to the team file before connecting", "producer and consumer must belong to the team file")
		}
		consumesBytes, e := os.ReadFile(consumerPath)
		if e != nil && !errors.Is(e, os.ErrNotExist) {
			return e
		}
		consumes := &workspace.ConsumesFile{Version: 1, Consumes: []workspace.Consume{}}
		if e == nil {
			consumes, e = workspace.ParseConsumes(consumesBytes)
			if e != nil {
				return e
			}
		}
		newLink = workspace.Link{ID: id, Direction: o.direction, Producer: producer, Consumer: consumer}
		idx := slices.IndexFunc(team.Links, func(l workspace.Link) bool { return l.ID == id })
		if idx >= 0 {
			v := team.Links[idx]
			oldLink = &v
			team.Links[idx] = newLink
		} else {
			team.Links = append(team.Links, newLink)
		}
		newConsume = workspace.Consume{Contract: id, Fields: fields, Source: source}
		idx = slices.IndexFunc(consumes.Consumes, func(c workspace.Consume) bool { return c.Contract == id })
		if idx >= 0 {
			v := consumes.Consumes[idx]
			oldConsume = &v
			consumes.Consumes[idx] = newConsume
		} else {
			consumes.Consumes = append(consumes.Consumes, newConsume)
		}
		tb, e := json.MarshalIndent(team, "", "  ")
		if e != nil {
			return e
		}
		cb, e := json.MarshalIndent(consumes, "", "  ")
		if e != nil {
			return e
		}
		if _, e = workspace.ParseTeam(tb); e != nil {
			return e
		}
		if _, e = workspace.ParseConsumes(cb); e != nil {
			return e
		}
		// Validate all inputs before creating directories or replacing a file.
		for _, path := range []string{homePath, consumerPath} {
			if e = os.MkdirAll(filepath.Dir(path), 0o700); e != nil {
				return e
			}
		}
		if e = workspace.WriteAtomic(homePath, append(tb, '\n')); e != nil {
			return e
		}
		if e = workspace.WriteAtomic(consumerPath, append(cb, '\n')); e != nil {
			var rollback error
			if homeBytes != nil {
				rollback = workspace.WriteAtomic(homePath, homeBytes)
			} else {
				rollback = os.Remove(homePath)
			}
			return fmt.Errorf("write consumes file: %w (team file rollback: %v)", e, rollback)
		}
		w.Home = home
		wroteDeclarations = true
		return nil
	})
	if err != nil {
		if wroteDeclarations {
			return a.fail(wsError("inspect the two written declarations and repair the registry, then retry connect", "declarations were written to %s and %s, but registry save failed: %v", homePath, consumerPath, err))
		}
		var we *workspace.Error
		if errors.As(err, &we) {
			return a.fail(err)
		}
		return a.fail(wsError("resolve the write error, then retry connect", "%v", err))
	}
	next := "commit both declarations, then: radar gate --again"
	a.report(map[string]any{"id": id, "team_file": homePath, "consumes_file": consumerPath, "previous_link": oldLink, "link": newLink, "previous_consume": oldConsume, "consume": newConsume, "next": next}, func(w io.Writer) {
		fmt.Fprintf(w, "Declared link %s: %s → %s (%s)\n", id, producer, consumer, o.direction)
		if oldLink != nil {
			before, _ := json.Marshal(oldLink)
			after, _ := json.Marshal(newLink)
			fmt.Fprintf(w, "  link: %s → %s\n", before, after)
		}
		if oldConsume != nil {
			before, _ := json.Marshal(oldConsume)
			after, _ := json.Marshal(newConsume)
			fmt.Fprintf(w, "  consumes: %s → %s\n", before, after)
		}
		fmt.Fprintf(w, "  wrote %s\n  wrote %s\n", homePath, consumerPath)
		for _, entry := range []struct{ path, rel string }{{homePath, workspace.TeamPath}, {consumerPath, workspace.ConsumesPath}} {
			root := filepath.Dir(filepath.Dir(entry.path))
			fmt.Fprintf(w, "  git -C %s add %s && git -C %s commit -m %s\n", shellArg(root), entry.rel, shellArg(root), shellArg("Declare link "+id))
		}
		fmt.Fprintf(w, "Commit both files before gate reads the declarations.\n\nNext: %s\n", next)
	})
	return 0
}
