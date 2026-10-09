package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/radar-engine/radar/internal/contractgraph"
	"github.com/radar-engine/radar/internal/indexer"
	"github.com/radar-engine/radar/internal/model"
	"sort"
	"time"
)

func (a *app) index(o options) int {
	s, e := a.snapshot(o.ref)
	if e != nil {
		return a.fail(e)
	}
	if e = a.store.SaveSnapshot(a.ctx, s); e != nil {
		return a.fail(e)
	}
	if a.machine {
		a.emit(s)
	} else {
		fmt.Fprintf(a.out, "Indexed %d entities and %d relationships at %s\n", len(s.Nodes), len(s.Edges), s.Revision)
		for _, d := range s.Diagnostics {
			fmt.Fprintf(a.out, "%s: %s %s\n", d.Severity, d.Path, d.Message)
		}
	}
	return 0
}
func freshSnapshot(ctx context.Context, root string) (model.Snapshot, error) {
	s, e := indexer.Index(ctx, root, "WORKTREE")
	if e != nil {
		return s, e
	}
	if e = contractgraph.Augment(ctx, &s); e != nil {
		return s, e
	}
	sort.Slice(s.Nodes, func(i, j int) bool { return s.Nodes[i].ID < s.Nodes[j].ID })
	sort.Slice(s.Edges, func(i, j int) bool { return s.Edges[i].ID < s.Edges[j].ID })
	b, e := json.Marshal(s)
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
