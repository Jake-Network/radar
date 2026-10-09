package cli

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/Jake-Network/radar/internal/graph"
	"github.com/Jake-Network/radar/internal/model"
	"github.com/Jake-Network/radar/internal/planning"
)

func (a *app) graph(o options) int {
	format := o.format
	if format == "" {
		format = "text"
		if a.machine {
			format = "json"
		}
	}
	if format != "json" && format != "mermaid" && format != "text" {
		return a.fail(errors.New("--format must be text, json or mermaid"))
	}
	if o.projected && o.plan == "" {
		return a.fail(errors.New("--projected requires --plan"))
	}
	var s model.Snapshot
	var e error
	if o.ref != "" {
		s, e = a.snapshot(o.ref)
	} else {
		s, e = a.store.Snapshot(a.ctx, a.root, "")
	}
	if e != nil {
		return a.fail(e)
	}
	if o.plan != "" {
		p, err := a.loadPlan(o.plan)
		if err != nil {
			return a.fail(err)
		}
		if o.ref == "" && p.BaseRevision != s.Revision {
			if s, e = a.snapshot(p.BaseRevision); e != nil {
				return a.fail(e)
			}
		}
		if s, e = planning.IntentGraph(p, s, o.projected); e != nil {
			return a.fail(e)
		}
	}
	g, e := graph.New(s)
	if e != nil {
		return a.fail(e)
	}
	nodes := g.Select(o.kind, o.name)
	if o.from != "" {
		start, ok := g.Nodes[o.from]
		if !ok {
			return a.fail(fmt.Errorf("unknown graph entity %s; find IDs with `radar resolve`", o.from))
		}
		nodes = []model.Node{start}
		for _, hop := range g.Distances(o.from, o.edge, o.reverse, o.depth) {
			n := g.Nodes[hop.ID]
			if (o.kind == "" || n.Kind == o.kind) && (o.name == "" || strings.Contains(n.Name, o.name)) {
				nodes = append(nodes, n)
			}
		}
	}
	selected := map[string]bool{}
	for _, n := range nodes {
		selected[n.ID] = true
	}
	edges := []model.Edge{}
	for _, e := range s.Edges {
		if selected[e.From] && selected[e.To] && (o.edge == "" || e.Kind == o.edge) {
			edges = append(edges, e)
		}
	}
	s.Nodes = nodes
	s.Edges = edges
	switch format {
	case "mermaid":
		view, _ := graph.New(s)
		fmt.Fprint(a.out, view.Mermaid())
	case "json":
		a.emit(s)
	default:
		renderGraph(a.out, s)
	}
	return 0
}

func (a *app) resolve(o options) int {
	query := strings.TrimSpace(strings.Join(o.args, " "))
	if query == "" {
		return a.fail(errors.New("resolve requires a query: a name, a path, or path#Qualified.Name"))
	}
	s, e := a.storedOrFresh(o.ref)
	if e != nil {
		return a.fail(e)
	}
	g, e := graph.New(s)
	if e != nil {
		return a.fail(e)
	}
	matches := g.Resolve(query, o.kind)
	total := len(matches)
	if o.limit > 0 && len(matches) > o.limit {
		matches = matches[:o.limit]
	}
	type match struct {
		ID       string `json:"id"`
		Kind     string `json:"kind"`
		Name     string `json:"name"`
		Path     string `json:"path,omitempty"`
		Line     int    `json:"line,omitempty"`
		Language string `json:"language,omitempty"`
	}
	out := []match{}
	for _, n := range matches {
		out = append(out, match{n.ID, n.Kind, n.Name, n.Provenance.Path, n.Provenance.Line, n.Language})
	}
	result := map[string]any{"query": query, "revision": s.Revision, "total": total, "matches": out}
	a.report(result, func(w io.Writer) {
		if total == 0 {
			fmt.Fprintf(w, "No entity matches %q at %s.\n", query, s.Revision)
			return
		}
		for _, m := range out {
			location := m.Path
			if m.Line > 0 {
				location = fmt.Sprintf("%s:%d", m.Path, m.Line)
			}
			fmt.Fprintf(w, "%-48s %-10s %s\n", m.ID, m.Kind, location)
		}
		if total > len(out) {
			fmt.Fprintf(w, "... %d more (raise --limit)\n", total-len(out))
		}
	})
	return 0
}
