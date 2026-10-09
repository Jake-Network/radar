package cli

import (
	"errors"
	"io"
	"sort"
	"strings"

	"github.com/Jake-Network/radar/internal/checkpoint"
	"github.com/Jake-Network/radar/internal/contracts"
	gitrepo "github.com/Jake-Network/radar/internal/git"
	"github.com/Jake-Network/radar/internal/graph"
	"github.com/Jake-Network/radar/internal/model"
)

type affectedFile struct {
	Path    string `json:"path"`
	Depth   int    `json:"depth"`
	Changed string `json:"changed"`
	Via     string `json:"via,omitempty"`
}
type affectedContract struct {
	ID     string `json:"id"`
	Reason string `json:"reason"`
}
type affectedTask struct {
	ID    string   `json:"id"`
	Paths []string `json:"paths"`
}
type affectedReport struct {
	Base        string             `json:"base"`
	Head        string             `json:"head"`
	Changed     []string           `json:"changed"`
	Affected    []affectedFile     `json:"affected"`
	Contracts   []affectedContract `json:"contracts"`
	Tasks       []affectedTask     `json:"tasks,omitempty"`
	Evidence    model.Evidence     `json:"evidence"`
	Diagnostics []model.Diagnostic `json:"diagnostics"`
}

// affected follows inferred DEPENDS_ON edges backwards from changed files in
// both the base and head graphs, so removed or renamed files still reach the
// files that imported them.
func (a *app) affectedReport(o options) (affectedReport, error) {
	if o.base == "" {
		return affectedReport{}, errors.New("--base is required")
	}
	if e := a.repository(); e != nil {
		return affectedReport{}, e
	}
	changed, e := gitrepo.ChangedFiles(a.ctx, a.root, o.base, o.head)
	if e != nil {
		return affectedReport{}, e
	}
	base, e := checkpoint.Index(a.ctx, a.root, o.base)
	if e != nil {
		return affectedReport{}, e
	}
	head := base
	if o.head == "WORKTREE" {
		head, e = freshSnapshot(a.ctx, a.root)
	} else if o.head != o.base {
		head, e = checkpoint.Index(a.ctx, a.root, o.head)
	}
	if e != nil {
		return affectedReport{}, e
	}
	r := affectedReport{Base: base.Revision, Head: o.head, Changed: changed, Affected: []affectedFile{}, Contracts: []affectedContract{}, Evidence: model.Inferred, Diagnostics: []model.Diagnostic{}}
	if o.head != "WORKTREE" {
		r.Head = head.Revision
	}
	r.Diagnostics = append(r.Diagnostics, base.Diagnostics...)
	r.Diagnostics = append(r.Diagnostics, head.Diagnostics...)
	isChanged := map[string]bool{}
	for _, p := range changed {
		isChanged[p] = true
	}
	best := map[string]affectedFile{}
	for _, s := range []model.Snapshot{base, head} {
		g, e := graph.New(s)
		if e != nil {
			return affectedReport{}, e
		}
		for _, p := range changed {
			if _, ok := g.Nodes[model.FileID(p)]; !ok {
				continue
			}
			for _, hop := range g.Distances(model.FileID(p), "DEPENDS_ON", true, o.depth) {
				path := model.PathFromID(hop.ID)
				if path == "" || isChanged[path] {
					continue
				}
				if current, ok := best[path]; !ok || hop.Depth < current.Depth {
					best[path] = affectedFile{Path: path, Depth: hop.Depth, Changed: p, Via: model.PathFromID(hop.Via)}
				}
			}
		}
	}
	for _, f := range best {
		r.Affected = append(r.Affected, f)
	}
	sort.Slice(r.Affected, func(i, j int) bool {
		if r.Affected[i].Depth != r.Affected[j].Depth {
			return r.Affected[i].Depth < r.Affected[j].Depth
		}
		return r.Affected[i].Path < r.Affected[j].Path
	})
	touched := func(p string) string {
		if isChanged[p] {
			return "changed"
		}
		if _, ok := best[p]; ok {
			return "affected"
		}
		return ""
	}
	manifestRef := r.Head
	if o.head == "WORKTREE" {
		manifestRef = "WORKTREE"
	}
	m, e := contracts.LoadManifest(a.ctx, a.root, manifestRef)
	if e != nil {
		r.Diagnostics = append(r.Diagnostics, model.Diagnostic{Severity: model.SeverityInfo, Message: e.Error()})
	}
	for _, b := range m.Bindings {
		var reasons []string
		for _, link := range []struct{ role, path string }{{"schema", b.Schema}, {"producer", b.Producer}, {"consumer", b.Consumer}} {
			if state := touched(link.path); link.path != "" && state != "" {
				reasons = append(reasons, link.role+" "+link.path+" "+state)
			}
		}
		if len(reasons) > 0 {
			r.Contracts = append(r.Contracts, affectedContract{ID: b.ID, Reason: strings.Join(reasons, "; ")})
		}
	}
	if o.plan != "" {
		p, e := a.loadPlan(o.plan)
		if e != nil {
			return affectedReport{}, e
		}
		bindings := map[string]contracts.Binding{}
		for _, b := range m.Bindings {
			bindings[b.ID] = b
		}
		for _, t := range p.Tasks {
			paths := map[string]bool{}
			for _, c := range t.Components {
				if path := model.PathFromID(c); path != "" && touched(path) != "" {
					paths[path] = true
				}
			}
			for _, c := range t.Contracts {
				b := bindings[c]
				for _, path := range []string{b.Schema, b.Producer, b.Consumer} {
					if path != "" && touched(path) != "" {
						paths[path] = true
					}
				}
			}
			if len(paths) > 0 {
				list := make([]string, 0, len(paths))
				for path := range paths {
					list = append(list, path)
				}
				sort.Strings(list)
				r.Tasks = append(r.Tasks, affectedTask{ID: t.ID, Paths: list})
			}
		}
	}
	return r, nil
}

func (a *app) affected(o options) int {
	r, err := a.affectedReport(o)
	if err != nil {
		return a.fail(err)
	}
	a.report(r, func(w io.Writer) { renderAffected(w, r) })
	return 0
}
