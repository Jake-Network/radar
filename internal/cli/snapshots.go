package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/Jake-Network/radar/internal/contractgraph"
	"github.com/Jake-Network/radar/internal/indexer"
	"github.com/Jake-Network/radar/internal/model"
)

func (a *app) index(o options) int {
	s, e := a.snapshot(o.ref)
	if e != nil {
		return a.fail(e)
	}
	if e = a.store.SaveSnapshot(a.ctx, s); e != nil {
		return a.fail(e)
	}
	nodeKinds, edgeKinds := map[string]int{}, map[string]int{}
	for _, n := range s.Nodes {
		nodeKinds[n.Kind]++
	}
	for _, edge := range s.Edges {
		edgeKinds[edge.Kind]++
	}
	dependencies := edgeKinds["DEPENDS_ON"]
	var value any = s
	if o.summary {
		value = map[string]any{"repository": s.Repository, "revision": s.Revision, "entities": len(s.Nodes), "relationships": len(s.Edges), "entity_kinds": nodeKinds, "relationship_kinds": edgeKinds, "diagnostics": s.Diagnostics}
	}
	a.report(value, func(w io.Writer) {
		fmt.Fprintf(w, "Indexed %d entities and %d relationships (%d resolved file dependencies) at %s\n", len(s.Nodes), len(s.Edges), dependencies, s.Revision)
		for _, d := range s.Diagnostics {
			fmt.Fprintf(w, "%s: %s %s\n", d.Severity, d.Path, d.Message)
		}
	})
	return 0
}

// freshSnapshot indexes the working tree. Its revision is a digest of the
// observed content, independent of where the checkout lives.
func freshSnapshot(ctx context.Context, root string) (model.Snapshot, error) {
	s, e := indexer.Index(ctx, root, "WORKTREE")
	if e != nil {
		return s, e
	}
	if e = contractgraph.Augment(ctx, &s); e != nil {
		return s, e
	}
	model.SortSnapshot(&s)
	portable := s
	portable.Repository = ""
	portable.Nodes = append([]model.Node(nil), s.Nodes...)
	portable.Edges = append([]model.Edge(nil), s.Edges...)
	for i := range portable.Nodes {
		portable.Nodes[i].Provenance.Repository = ""
	}
	for i := range portable.Edges {
		portable.Edges[i].Provenance.Repository = ""
	}
	b, e := json.Marshal(portable)
	if e != nil {
		return s, e
	}
	sum := sha256.Sum256(b)
	s.Revision = "WORKTREE:" + hex.EncodeToString(sum[:16])
	now := time.Now().UTC().Format(time.RFC3339)
	for i := range s.Nodes {
		s.Nodes[i].Provenance.Revision = s.Revision
		s.Nodes[i].Provenance.Timestamp = now
	}
	for i := range s.Edges {
		s.Edges[i].Provenance.Revision = s.Revision
		s.Edges[i].Provenance.Timestamp = now
	}
	return s, nil
}
