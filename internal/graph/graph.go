// Package graph implements deterministic queries over evidence-bearing snapshots.
package graph

import (
	"fmt"
	"github.com/radar-engine/radar/internal/model"
	"sort"
	"strings"
)

type Graph struct {
	Nodes map[string]model.Node
	Edges []model.Edge
}

func New(s model.Snapshot) (*Graph, error) {
	g := &Graph{Nodes: map[string]model.Node{}, Edges: append([]model.Edge(nil), s.Edges...)}
	for _, n := range s.Nodes {
		if n.ID == "" {
			return nil, fmt.Errorf("node has empty identity")
		}
		if _, ok := g.Nodes[n.ID]; ok {
			return nil, fmt.Errorf("duplicate node %s", n.ID)
		}
		g.Nodes[n.ID] = n
	}
	seen := map[string]bool{}
	for _, e := range g.Edges {
		if e.ID == "" || seen[e.ID] {
			return nil, fmt.Errorf("empty or duplicate edge identity %q", e.ID)
		}
		seen[e.ID] = true
		if _, ok := g.Nodes[e.From]; !ok {
			return nil, fmt.Errorf("edge %s missing source %s", e.ID, e.From)
		}
		if _, ok := g.Nodes[e.To]; !ok {
			return nil, fmt.Errorf("edge %s missing target %s", e.ID, e.To)
		}
	}
	return g, nil
}
func (g *Graph) Select(kind, name string) []model.Node {
	out := []model.Node{}
	for _, n := range g.Nodes {
		if (kind == "" || n.Kind == kind) && (name == "" || strings.Contains(n.Name, name)) {
			out = append(out, n)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Reachable follows only recorded edges, never infers runtime reachability.
func (g *Graph) Reachable(start, kind string, reverse bool) []model.Node {
	seen := map[string]bool{start: true}
	q := []string{start}
	out := []model.Node{}
	for len(q) > 0 {
		x := q[0]
		q = q[1:]
		for _, e := range g.Edges {
			if kind != "" && e.Kind != kind {
				continue
			}
			from, to := e.From, e.To
			if reverse {
				from, to = to, from
			}
			if from == x && !seen[to] {
				seen[to] = true
				q = append(q, to)
				out = append(out, g.Nodes[to])
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
func (g *Graph) Mermaid() string {
	var b strings.Builder
	b.WriteString("graph LR\n")
	nodes := g.Select("", "")
	names := map[string]string{}
	for i, n := range nodes {
		id := fmt.Sprintf("n%d", i)
		names[n.ID] = id
		label := strings.NewReplacer("\"", "'", "\n", " ", "[", "(", "]", ")", "<", "(", ">", ")", "&", "and").Replace(n.Kind + ": " + n.Name)
		fmt.Fprintf(&b, "  %s[\"%s\"]\n", id, label)
	}
	edges := append([]model.Edge(nil), g.Edges...)
	sort.Slice(edges, func(i, j int) bool { return edges[i].ID < edges[j].ID })
	for _, e := range edges {
		label := strings.NewReplacer("\"", "'", "\n", " ", "|", "/", "<", "(", ">", ")", "&", "and").Replace(e.Kind + " (" + string(e.Provenance.Evidence) + ")")
		fmt.Fprintf(&b, "  %s -->|\"%s\"| %s\n", names[e.From], label, names[e.To])
	}
	return b.String()
}
