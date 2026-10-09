// Package contractgraph adds explicit schema and declared dependency evidence to source snapshots.
package contractgraph

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"

	"github.com/radar-engine/radar/internal/contracts"
	gitrepo "github.com/radar-engine/radar/internal/git"
	"github.com/radar-engine/radar/internal/jsonptr"
	"github.com/radar-engine/radar/internal/model"
)

func Augment(ctx context.Context, s *model.Snapshot) error { return AugmentAt(ctx, s, "WORKTREE") }

// AugmentAt reads explicit contract evidence from the same immutable revision as source.
func AugmentAt(ctx context.Context, s *model.Snapshot, ref string) error {
	m, e := contracts.LoadManifest(ctx, s.Repository, ref)
	if e != nil {
		s.Diagnostics = append(s.Diagnostics, model.Diagnostic{Severity: model.SeverityInfo, Message: "Explicit contract graph unavailable: " + e.Error()})
		return nil
	}
	nodes := map[string]bool{}
	edges := map[string]bool{}
	for _, n := range s.Nodes {
		nodes[n.ID] = true
	}
	for _, e := range s.Edges {
		edges[e.ID] = true
	}
	addNode := func(n model.Node) {
		if !nodes[n.ID] {
			nodes[n.ID] = true
			s.Nodes = append(s.Nodes, n)
		}
	}
	addEdge := func(from, to, kind string, p model.Provenance) {
		id := model.StableID(from, kind, to)
		if !edges[id] {
			edges[id] = true
			s.Edges = append(s.Edges, model.Edge{ID: id, From: from, To: to, Kind: kind, Provenance: p})
		}
	}
	warn := func(path, message string) {
		s.Diagnostics = append(s.Diagnostics, model.Diagnostic{Path: path, Severity: model.SeverityWarning, Message: message})
	}
	for _, b := range m.Bindings {
		if e := ctx.Err(); e != nil {
			return e
		}
		doc, e := contracts.ReadSchema(ctx, s.Repository, ref, b.Schema)
		if e != nil {
			warn(b.Schema, e.Error())
			continue
		}
		schema, e := contracts.Analyzable(doc, b.Pointer)
		if e != nil {
			warn(b.Schema, e.Error())
			continue
		}
		raw, _ := json.Marshal(doc)
		digest := sha256.Sum256(raw)
		p := model.Provenance{Repository: s.Repository, Revision: s.Revision, Path: b.Schema, Method: "explicit_json_schema", Evidence: model.VerifiedStatic}
		fid := model.FileID(b.Schema)
		addNode(model.Node{ID: fid, Kind: "file", Name: b.Schema, Properties: map[string]string{"content_sha256": hex.EncodeToString(digest[:])}, Provenance: p})
		cid := model.ContractID(b.Schema, b.Pointer)
		addNode(model.Node{ID: cid, Kind: schemaKind(b.Direction), Name: b.ID, Properties: map[string]string{"json_pointer": b.Pointer, "binding_id": b.ID, "schema": b.Schema}, Provenance: p})
		addEdge(fid, cid, "DEFINES", p)
		declared := model.Provenance{Repository: s.Repository, Revision: s.Revision, Path: contracts.ManifestPath, Method: "explicit_declared_dependency (runtime usage unproven)", Evidence: model.VerifiedStatic}
		for _, link := range []struct{ path, kind string }{{b.Producer, "EXPOSES"}, {b.Consumer, "CONSUMES"}} {
			if link.path == "" {
				continue
			}
			id := model.FileID(link.path)
			if nodes[id] {
				addEdge(id, cid, link.kind, declared)
			} else {
				warn(link.path, "declared contract endpoint file was not structurally indexed")
			}
		}
		if b.Consumer != "" {
			if content, err := gitrepo.ReadFile(ctx, s.Repository, ref, b.Consumer); err == nil {
				for _, field := range contracts.MissingConsumerFields(content, b.Fields) {
					warn(b.Consumer, "declared consumer field "+field+" of binding "+b.ID+" does not appear in the consumer; the declaration may be stale (lexical check)")
				}
			}
		}
		// Direct properties, including those reached through local $ref and allOf.
		names := make([]string, 0, len(schema.Properties))
		for name := range schema.Properties {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, field := range names {
			pointer := b.Pointer + "/properties/" + jsonptr.Escape(field)
			id := model.SchemaFieldID(b.Schema, pointer)
			addNode(model.Node{ID: id, Kind: "schema_field", Name: field, Properties: map[string]string{"json_pointer": pointer, "contract": b.ID}, Provenance: p})
			addEdge(cid, id, "DEFINES", p)
			for _, used := range b.Fields {
				if used == field {
					consumer := model.FileID(b.Consumer)
					if nodes[consumer] {
						addEdge(consumer, id, "CONSUMES", declared)
					}
				}
			}
		}
	}
	return nil
}

func schemaKind(direction string) string {
	switch direction {
	case "request":
		return "request_schema"
	case "response":
		return "response_schema"
	default:
		return "contract_schema"
	}
}
