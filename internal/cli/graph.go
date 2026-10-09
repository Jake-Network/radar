package cli

import (
	"errors"
	"fmt"
	"github.com/radar-engine/radar/internal/graph"
	"github.com/radar-engine/radar/internal/model"
	"github.com/radar-engine/radar/internal/planning"
	"strings"
)

func (a *app) graph(o options) int {
	s, e := a.store.Snapshot(a.ctx, a.root, "")
	if o.ref != "" {
		s, e = a.snapshot(o.ref)
	}
	if e == nil && o.plan != "" {
		p, err := a.loadPlan(o.plan)
		if err != nil {
			return a.fail(err)
		}
		if o.ref == "" && p.BaseRevision != s.Revision {
			s, e = a.snapshot(p.BaseRevision)
		}
		if e == nil {
			s, e = planning.IntentGraph(p, s, o.projected)
		}
	}
	if o.projected && o.plan == "" {
		return a.fail(errors.New("--projected requires --plan"))
	}
	if e != nil {
		return a.fail(e)
	}
	g, e := graph.New(s)
	if e != nil {
		return a.fail(e)
	}
	if o.format != "json" && o.format != "mermaid" {
		return a.fail(errors.New("--format must be json or mermaid"))
	}
	nodes := g.Select(o.kind, o.name)
	if o.from != "" {
		start, ok := g.Nodes[o.from]
		if !ok {
			return a.fail(fmt.Errorf("unknown graph entity %s", o.from))
		}
		nodes = append([]model.Node{start}, g.Reachable(o.from, o.edge, o.reverse)...)
		filtered := []model.Node{}
		for _, n := range nodes {
			if (o.kind == "" || n.Kind == o.kind) && (o.name == "" || strings.Contains(n.Name, o.name)) {
				filtered = append(filtered, n)
			}
		}
		nodes = filtered
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
	if o.format == "mermaid" {
		view, _ := graph.New(s)
		fmt.Fprint(a.out, view.Mermaid())
	} else {
		a.emit(s)
	}
	return 0
}
