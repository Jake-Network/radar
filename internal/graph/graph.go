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
	out   map[string][]int // edge indexes by source
	in    map[string][]int // edge indexes by target
}

func New(s model.Snapshot) (*Graph, error) {
	g := &Graph{Nodes: map[string]model.Node{}, Edges: append([]model.Edge(nil), s.Edges...), out: map[string][]int{}, in: map[string][]int{}}
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
	for i, e := range g.Edges {
		g.out[e.From] = append(g.out[e.From], i)
		g.in[e.To] = append(g.in[e.To], i)
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
	out := []model.Node{}
	for _, id := range g.Distances(start, kind, reverse, 0) {
		out = append(out, g.Nodes[id.ID])
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Hop is a node reached by traversal with its distance and predecessor.
type Hop struct {
	ID    string `json:"id"`
	Depth int    `json:"depth"`
	Via   string `json:"via,omitempty"`
}

// Distances runs a breadth-first traversal from start, optionally limited to
// maxDepth hops (0 means unlimited). start itself is not included.
func (g *Graph) Distances(start, kind string, reverse bool, maxDepth int) []Hop {
	seen := map[string]bool{start: true}
	frontier := []Hop{{ID: start}}
	out := []Hop{}
	for len(frontier) > 0 {
		x := frontier[0]
		frontier = frontier[1:]
		if maxDepth > 0 && x.Depth >= maxDepth {
			continue
		}
		index := g.out[x.ID]
		if reverse {
			index = g.in[x.ID]
		}
		for _, i := range index {
			e := g.Edges[i]
			if kind != "" && e.Kind != kind {
				continue
			}
			next := e.To
			if reverse {
				next = e.From
			}
			if !seen[next] {
				seen[next] = true
				hop := Hop{ID: next, Depth: x.Depth + 1, Via: x.ID}
				if x.ID == start {
					hop.Via = ""
				}
				frontier = append(frontier, hop)
				out = append(out, hop)
			}
		}
	}
	return out
}

// Resolve finds entities for a human query: an exact ID, a "path#qualified"
// suffix, a repository path, or a case-insensitive name/qualified-name match.
func (g *Graph) Resolve(query, kind string) []model.Node {
	if n, ok := g.Nodes[query]; ok && (kind == "" || n.Kind == kind) {
		return []model.Node{n}
	}
	lower := strings.ToLower(query)
	var exact, partial []model.Node
	for _, n := range g.Nodes {
		if kind != "" && n.Kind != kind {
			continue
		}
		_, suffix, _ := strings.Cut(n.ID, ":")
		qualified := n.Properties["qualified_name"]
		switch {
		case suffix == query, n.Provenance.Path+"#"+qualified == query && qualified != "":
			exact = append(exact, n)
		case n.Kind == "file" && n.Name == query:
			exact = append(exact, n)
		case strings.EqualFold(n.Name, query) || strings.EqualFold(qualified, query):
			exact = append(exact, n)
		case strings.Contains(strings.ToLower(n.Name), lower) || strings.Contains(strings.ToLower(qualified), lower) || strings.Contains(strings.ToLower(n.ID), lower):
			partial = append(partial, n)
		}
	}
	out := exact
	if len(out) == 0 {
		out = partial
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
