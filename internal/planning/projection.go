// Package planning validates declared intent against evidence, without inventing a design.
package planning

import (
	"fmt"
	"github.com/Jake-Network/radar/internal/model"
	"sort"
)

// Project creates a separate proposed graph. Existing verified provenance is retained.
func Project(p Plan, s model.Snapshot) (model.Snapshot, error) {
	out := s
	out.Nodes = append([]model.Node{}, s.Nodes...)
	out.Edges = append([]model.Edge{}, s.Edges...)
	nodes := map[string]model.Node{}
	edges := map[string]model.Edge{}
	for _, n := range out.Nodes {
		nodes[n.ID] = n
	}
	for _, e := range out.Edges {
		edges[e.ID] = e
	}
	for _, d := range p.GraphDeltas {
		if (d.Node == nil) == (d.Edge == nil) {
			return out, fmt.Errorf("graph delta requires exactly one node or edge")
		}
		if d.Operation != "add" && d.Operation != "remove" && d.Operation != "modify" {
			return out, fmt.Errorf("unsupported graph delta operation %q", d.Operation)
		}
		id := ""
		exists := false
		if d.Node != nil {
			id = d.Node.ID
			_, exists = nodes[id]
		} else {
			id = d.Edge.ID
			_, exists = edges[id]
		}
		if id == "" {
			return out, fmt.Errorf("graph delta needs entity ID")
		}
		if d.Operation == "add" && exists {
			return out, fmt.Errorf("graph add entity already exists: %s", id)
		}
		if d.Operation != "add" && !exists {
			return out, fmt.Errorf("graph delta entity unresolved: %s", id)
		}
		if d.Node != nil {
			n := *d.Node
			if d.Operation != "remove" && (n.Kind == "" || n.Name == "") {
				return out, fmt.Errorf("proposed node needs kind and name")
			}
			n.Provenance.Repository = s.Repository
			n.Provenance.Revision = p.BaseRevision
			n.Provenance.Evidence = model.Proposed
			n.Provenance.Method = "plan_graph_delta"
			if d.Operation == "remove" {
				delete(nodes, id)
			} else {
				nodes[id] = n
			}
		} else {
			e := *d.Edge
			if d.Operation != "remove" && e.Kind == "" {
				return out, fmt.Errorf("proposed edge needs kind")
			}
			e.Provenance.Repository = s.Repository
			e.Provenance.Revision = p.BaseRevision
			e.Provenance.Evidence = model.Proposed
			e.Provenance.Method = "plan_graph_delta"
			if d.Operation == "remove" {
				delete(edges, id)
			} else {
				edges[id] = e
			}
		}
	}
	out.Nodes = []model.Node{}
	out.Edges = []model.Edge{}
	for _, n := range nodes {
		out.Nodes = append(out.Nodes, n)
	}
	for _, e := range edges {
		if _, ok := nodes[e.From]; !ok {
			return out, fmt.Errorf("projected edge %s has unresolved source %s", e.ID, e.From)
		}
		if _, ok := nodes[e.To]; !ok {
			return out, fmt.Errorf("projected edge %s has unresolved target %s", e.ID, e.To)
		}
		out.Edges = append(out.Edges, e)
	}
	sort.Slice(out.Nodes, func(i, j int) bool { return out.Nodes[i].ID < out.Nodes[j].ID })
	sort.Slice(out.Edges, func(i, j int) bool { return out.Edges[i].ID < out.Edges[j].ID })
	return out, nil
}
