package planning

import (
	"fmt"
	"github.com/radar-engine/radar/internal/model"
	"sort"
	"strconv"
)

// IntentGraph overlays declared planning entities with proposed provenance.
// It never converts approval or natural-language intent into source evidence.
func IntentGraph(p Plan, s model.Snapshot, projected bool) (model.Snapshot, error) {
	var err error
	if projected {
		s, err = Project(p, s)
		if err != nil {
			return s, err
		}
	} else {
		s.Nodes = append([]model.Node(nil), s.Nodes...)
		s.Edges = append([]model.Edge(nil), s.Edges...)
		s.Diagnostics = append([]model.Diagnostic(nil), s.Diagnostics...)
	}
	provenance := model.Provenance{Repository: s.Repository, Revision: p.BaseRevision, Method: "structured_plan:" + Digest(p), Evidence: model.Proposed}
	refs := map[string]bool{}
	contracts := map[string]string{}
	for _, n := range s.Nodes {
		refs[n.ID] = true
		if id := n.Properties["binding_id"]; id != "" {
			contracts[id] = n.ID
		}
	}
	entity := func(kind, id, name string, props map[string]string) string {
		key := "plan:" + p.FeatureID + ":" + kind + ":" + id
		if !refs[key] {
			refs[key] = true
			s.Nodes = append(s.Nodes, model.Node{ID: key, Kind: kind, Name: name, Properties: props, Provenance: provenance})
		}
		return key
	}
	edge := func(from, to, kind string) {
		if !refs[from] || !refs[to] {
			s.Diagnostics = append(s.Diagnostics, model.Diagnostic{Severity: "warning", Message: fmt.Sprintf("planned %s relationship unresolved: %s -> %s", kind, from, to)})
			return
		}
		s.Edges = append(s.Edges, model.Edge{ID: model.StableID("plan", p.FeatureID, from, kind, to), From: from, To: to, Kind: kind, Provenance: provenance})
	}
	feature := entity("feature", p.FeatureID, p.Intent, map[string]string{"feature_id": p.FeatureID})
	requirements := map[string]string{}
	criteria := map[string]string{}
	tasks := map[string]string{}
	for _, r := range p.Requirements {
		requirements[r.ID] = entity("feature_requirement", r.ID, r.Intent, map[string]string{"requirement_id": r.ID, "security_sensitive": strconv.FormatBool(r.SecuritySensitive)})
		edge(feature, requirements[r.ID], "DEFINES")
	}
	for _, d := range p.Decisions {
		id := entity("architecture_decision", d.ID, d.Intent, nil)
		edge(feature, id, "DEPENDS_ON")
	}
	for _, c := range p.Constraints {
		id := entity("constraint", c.ID, c.Intent, nil)
		edge(feature, id, "DEPENDS_ON")
		if c.Rule != nil {
			rule := entity("verification_rule", "constraint:"+c.ID, c.Rule.Kind, nil)
			edge(id, rule, "VALIDATED_BY")
		}
	}
	for _, c := range p.Acceptance {
		criteria[c.ID] = entity("acceptance_criterion", c.ID, c.Intent, nil)
		edge(requirements[c.Requirement], criteria[c.ID], "VALIDATED_BY")
		if c.Rule != nil {
			rule := entity("verification_rule", "acceptance:"+c.ID, c.Rule.Kind, nil)
			edge(criteria[c.ID], rule, "VALIDATED_BY")
		}
	}
	for _, t := range p.Tasks {
		tasks[t.ID] = entity("implementation_task", t.ID, t.Intent, map[string]string{"task_id": t.ID})
		edge(feature, tasks[t.ID], "DEFINES")
	}
	for _, t := range p.Tasks {
		for _, r := range t.Requirements {
			edge(tasks[t.ID], requirements[r], "IMPLEMENTS")
		}
		for _, c := range t.Acceptance {
			edge(tasks[t.ID], criteria[c], "VALIDATED_BY")
		}
		for _, d := range t.DependsOn {
			edge(tasks[t.ID], tasks[d], "DEPENDS_ON")
		}
		for _, c := range t.Components {
			edge(tasks[t.ID], c, "AFFECTS")
		}
		for _, c := range t.Contracts {
			edge(tasks[t.ID], contracts[c], "AFFECTS")
		}
	}
	sort.Slice(s.Nodes, func(i, j int) bool { return s.Nodes[i].ID < s.Nodes[j].ID })
	sort.Slice(s.Edges, func(i, j int) bool { return s.Edges[i].ID < s.Edges[j].ID })
	// Repeated declarations should not create duplicate graph edge identities.
	unique := []model.Edge{}
	seen := map[string]bool{}
	for _, e := range s.Edges {
		if !seen[e.ID] {
			seen[e.ID] = true
			unique = append(unique, e)
		}
	}
	s.Edges = unique
	return s, nil
}
