package testselection

import (
	"sort"

	"github.com/Jake-Network/radar/internal/model"
	"github.com/Jake-Network/radar/internal/pathutil"
)

// impactHop follows only recorded imports and verified declared contract links.
// It is a candidate-selection relationship, never runtime compatibility proof.
type impactHop struct {
	target     string
	provenance model.Provenance
	contract   bool
}

func dependencyImpact(snapshot model.Snapshot, changed map[string]bool) (map[string]string, map[string][]model.Provenance, map[string]bool, map[string][]string, bool) {
	edges := append([]model.Edge(nil), snapshot.Edges...)
	sort.Slice(edges, func(i, j int) bool {
		a, b := edges[i], edges[j]
		if a.ID != b.ID {
			return a.ID < b.ID
		}
		return a.From+"\x00"+a.To < b.From+"\x00"+b.To
	})
	contracts := map[string]bool{}
	for _, edge := range edges {
		if (edge.Kind == "EXPOSES" || edge.Kind == "CONSUMES") && edge.Provenance.Evidence == model.VerifiedStatic {
			contracts[edge.To] = true
		}
	}
	// Include fields defined by an authoritative contract, preserving identity:
	// sharing a schema file does not connect unrelated producer declarations.
	for _, edge := range edges {
		if edge.Kind == "DEFINES" && contracts[edge.From] && edge.Provenance.Evidence == model.VerifiedStatic {
			contracts[edge.To] = true
		}
	}
	adjacency := map[string][]impactHop{}
	add := func(from, to string, p model.Provenance, contract bool) {
		if from != "" && to != "" {
			adjacency[from] = append(adjacency[from], impactHop{to, p, contract})
		}
	}
	for _, edge := range edges {
		switch edge.Kind {
		case "DEPENDS_ON":
			add(edge.To, edge.From, edge.Provenance, false)
		case "EXPOSES":
			if edge.Provenance.Evidence == model.VerifiedStatic {
				file := model.PathFromID(edge.From)
				if file != "" {
					add(model.FileID(file), edge.To, edge.Provenance, true)
				}
			}
		case "CONSUMES":
			if edge.Provenance.Evidence == model.VerifiedStatic {
				file := model.PathFromID(edge.From)
				if file != "" {
					add(edge.To, model.FileID(file), edge.Provenance, true)
				}
			}
		case "DEFINES":
			if contracts[edge.To] && edge.Provenance.Evidence == model.VerifiedStatic {
				add(edge.From, edge.To, edge.Provenance, true)
			}
		}
	}
	// A schema provenance path supports schema-file impact even if an index
	// contains the contract node but omits the redundant file->contract edge.
	for _, node := range snapshot.Nodes {
		if contracts[node.ID] && node.Provenance.Evidence == model.VerifiedStatic {
			if p, e := pathutil.RepoRelative(node.Provenance.Path); e == nil {
				add(model.FileID(p), node.ID, node.Provenance, true)
			}
		}
	}
	for id := range adjacency {
		sort.Slice(adjacency[id], func(i, j int) bool {
			a, b := adjacency[id][i], adjacency[id][j]
			if a.target != b.target {
				return a.target < b.target
			}
			if a.provenance.Path != b.provenance.Path {
				return a.provenance.Path < b.provenance.Path
			}
			return a.provenance.Line < b.provenance.Line
		})
	}
	origins := map[string]string{}
	routes := map[string][]model.Provenance{}
	crossed := map[string]bool{}
	queue := []string{}
	files := make([]string, 0, len(changed))
	for file := range changed {
		files = append(files, file)
	}
	sort.Strings(files)
	for _, file := range files {
		id := model.FileID(file)
		origins[id] = file
		queue = append(queue, id)
	}
	truncated := false
	for i := 0; i < len(queue); i++ {
		if i >= 50000 {
			truncated = true
			break
		}
		current := queue[i]
		for _, hop := range adjacency[current] {
			if _, seen := origins[hop.target]; seen {
				continue
			}
			if len(origins) >= 50000 {
				truncated = true
				continue
			}
			origins[hop.target] = origins[current]
			route := append([]model.Provenance(nil), routes[current]...)
			if len(route) < 16 {
				route = append(route, hop.provenance)
			} else {
				truncated = true
			}
			routes[hop.target] = route
			crossed[hop.target] = crossed[current] || hop.contract
			queue = append(queue, hop.target)
		}
	}
	reached, exhausted := reachingChanges(adjacency, files)
	affected := map[string]string{}
	fileRoutes := map[string][]model.Provenance{}
	contractImpact := map[string]bool{}
	for _, id := range queue {
		if file := model.PathFromID(id); file != "" && id == model.FileID(file) {
			affected[file] = origins[id]
			fileRoutes[file] = routes[id]
			contractImpact[file] = crossed[id]
		}
	}
	return affected, fileRoutes, contractImpact, reached, truncated || exhausted
}

// reachingChanges lists, for each file, every changed file with a dependency
// path to it, not only the first one the shared search met: a test that
// imports a changed module also relates the changed files that module
// imports, and a changed test still relates the changed sources it imports.
// The search is bounded in total; reaching the bound truncates the result.
func reachingChanges(adjacency map[string][]impactHop, changed []string) (map[string][]string, bool) {
	reached := map[string][]string{}
	budget := 1_000_000
	for _, file := range changed {
		start := model.FileID(file)
		seen := map[string]bool{start: true}
		frontier := []string{start}
		for i := 0; i < len(frontier); i++ {
			if budget--; budget < 0 {
				return reached, true
			}
			for _, hop := range adjacency[frontier[i]] {
				if !seen[hop.target] {
					seen[hop.target] = true
					frontier = append(frontier, hop.target)
				}
			}
		}
		for _, id := range frontier[1:] {
			if f := model.PathFromID(id); f != "" && id == model.FileID(f) {
				reached[f] = append(reached[f], file)
			}
		}
	}
	return reached, false
}
