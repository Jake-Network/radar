package verification

import (
	"github.com/radar-engine/radar/internal/model"
	"github.com/radar-engine/radar/internal/planning"
	"reflect"
)

// incompleteFor ignores informational capability notices. Warning and error
// diagnostics indicate source excluded from the structural observations.
func incompleteFor(s model.Snapshot, path string) bool {
	for _, d := range s.Diagnostics {
		if (d.Severity == "warning" || d.Severity == "error") && (path == "" || d.Path == "" || d.Path == path) {
			return true
		}
	}
	return false
}
func verified(e model.Evidence) bool { return e == model.VerifiedStatic || e == model.VerifiedTool }
func evaluateDelta(d planning.Delta, s model.Snapshot) planning.Check {
	c := planning.Check{ID: "delta", Status: "unknown", Explanation: "Graph delta is malformed or unsupported.", Evidence: model.Unknown}
	if (d.Node == nil) == (d.Edge == nil) || (d.Operation != "add" && d.Operation != "modify" && d.Operation != "remove") {
		return c
	}
	id, path := "", ""
	found, equal := false, false
	var observed model.Provenance
	if d.Node != nil {
		id, path = d.Node.ID, d.Node.Provenance.Path
		if d.Operation != "remove" && (d.Node.Kind == "" || d.Node.Name == "") {
			return c
		}
		for _, n := range s.Nodes {
			if n.ID == id {
				found = true
				observed = n.Provenance
				equal = n.Kind == d.Node.Kind && n.Name == d.Node.Name && reflect.DeepEqual(n.Properties, d.Node.Properties)
			}
		}
	} else {
		id, path = d.Edge.ID, d.Edge.Provenance.Path
		if d.Operation != "remove" && (d.Edge.Kind == "" || d.Edge.From == "" || d.Edge.To == "") {
			return c
		}
		for _, e := range s.Edges {
			if e.ID == id {
				found = true
				observed = e.Provenance
				equal = e.Kind == d.Edge.Kind && e.From == d.Edge.From && e.To == d.Edge.To
			}
		}
	}
	c.ID = "delta:" + id
	if id == "" {
		return c
	}
	if found && !verified(observed.Evidence) {
		c.Location = &observed
		c.Explanation = "Observed graph entity lacks verified indexing provenance."
		return c
	}
	if !found && incompleteFor(s, path) {
		c.Explanation = "Incomplete indexing prevents proving graph entity absence."
		return c
	}
	c.Status = "failed"
	c.Evidence = model.VerifiedStatic
	c.Explanation = "Expected graph change is absent or differs from observed structure."
	if found {
		c.Location = &observed
		c.Evidence = observed.Evidence
	}
	if (d.Operation == "remove" && !found) || (d.Operation != "remove" && found && equal) {
		c.Status = "passed"
		c.Explanation = "Expected graph structure is observed; runtime behavior is not implied."
	}
	return c
}
