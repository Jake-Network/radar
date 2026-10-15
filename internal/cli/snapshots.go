package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/Jake-Network/radar/internal/contractgraph"
	"github.com/Jake-Network/radar/internal/indexer"
	"github.com/Jake-Network/radar/internal/model"
	"github.com/Jake-Network/radar/internal/project"
	"github.com/Jake-Network/radar/internal/scip"
)

func (a *app) index(o options) int {
	if (o.scip == "") != (o.scipBinding == "") {
		return a.fail(fmt.Errorf("--scip and --scip-binding must be supplied together"))
	}
	s, e := a.snapshot(o.ref)
	if e != nil {
		return a.fail(e)
	}
	if o.scip != "" {
		data, err := a.readIndexInput(o.scip, scip.MaxBytes)
		if err != nil {
			return a.fail(err)
		}
		bound, err := a.readIndexInput(o.scipBinding, 8<<20)
		if err != nil {
			return a.fail(err)
		}
		var binding scip.Binding
		decoder := json.NewDecoder(bytes.NewReader(bound))
		decoder.DisallowUnknownFields()
		var extra any
		if err = decoder.Decode(&binding); err != nil || decoder.Decode(&extra) != io.EOF {
			a.report(map[string]any{"status": model.StatusUnknown, "error": "invalid SCIP source binding"}, func(w io.Writer) { fmt.Fprintln(w, "? SCIP input unverified: invalid source binding") })
			return 2
		}
		if err = scip.Augment(a.ctx, &s, data, binding); err != nil {
			a.report(map[string]any{"status": model.StatusUnknown, "error": err.Error(), "diagnostics": s.Diagnostics}, func(w io.Writer) { fmt.Fprintln(w, "? SCIP input unverified:", err) })
			return 2
		}
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

func (a *app) readIndexInput(name string, limit int64) ([]byte, error) {
	var filename string
	var err error
	if filepath.IsAbs(name) {
		parent, e := filepath.EvalSymlinks(filepath.Dir(name))
		if e != nil {
			return nil, e
		}
		filename, err = project.SafePath(parent, filepath.Base(name))
	} else {
		filename, err = project.SafePath(a.root, name)
	}
	if err != nil {
		return nil, err
	}
	f, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() || st.Size() > limit {
		return nil, fmt.Errorf("index input must be a bounded regular file")
	}
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("index input exceeds size limit")
	}
	return b, nil
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
