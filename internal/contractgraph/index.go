// Package contractgraph adds explicit schema and declared dependency evidence to source snapshots.
package contractgraph

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/radar-engine/radar/internal/contracts"
	"github.com/radar-engine/radar/internal/model"
	"strings"
)

func Augment(ctx context.Context, s *model.Snapshot) error { return AugmentAt(ctx, s, "WORKTREE") }

// AugmentAt reads explicit contract evidence from the same immutable revision as source.
func AugmentAt(ctx context.Context, s *model.Snapshot, ref string) error {
	m, e := contracts.LoadManifest(ctx, s.Repository, ref)
	if e != nil {
		s.Diagnostics = append(s.Diagnostics, model.Diagnostic{Severity: "info", Message: "Explicit contract graph unavailable: " + e.Error()})
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
	for _, b := range m.Bindings {
		if e := ctx.Err(); e != nil {
			return e
		}
		doc, e := contracts.ReadSchema(ctx, s.Repository, ref, b.Schema)
		if e != nil {
			s.Diagnostics = append(s.Diagnostics, model.Diagnostic{Path: b.Schema, Severity: "warning", Message: e.Error()})
			continue
		}
		schema, e := contracts.Pointer(doc, b.Pointer)
		if e != nil {
			s.Diagnostics = append(s.Diagnostics, model.Diagnostic{Path: b.Schema, Severity: "warning", Message: e.Error()})
			continue
		}
		// Graph extraction intentionally requires direct properties; compatibility may follow local refs.
		if _, ref := schema["$ref"]; ref {
			s.Diagnostics = append(s.Diagnostics, model.Diagnostic{Path: b.Schema, Severity: "warning", Message: "contract graph field extraction requires direct properties; $ref field extraction unavailable"})
			continue
		}
		raw, _ := json.Marshal(doc)
		digest := sha256.Sum256(raw)
		p := model.Provenance{Repository: s.Repository, Revision: s.Revision, Path: b.Schema, Method: "explicit_json_schema", Evidence: model.VerifiedStatic}
		fid := model.StableID(s.Repository, "file", b.Schema)
		addNode(model.Node{ID: fid, Kind: "file", Name: b.Schema, Properties: map[string]string{"content_sha256": hex.EncodeToString(digest[:])}, Provenance: p})
		cid := model.StableID(s.Repository, "contract", b.Schema, b.Pointer)
		addNode(model.Node{ID: cid, Kind: schemaKind(b.Direction), Name: b.ID, Properties: map[string]string{"json_pointer": b.Pointer, "binding_id": b.ID, "schema": b.Schema}, Provenance: p})
		addEdge(fid, cid, "DEFINES", p)
		declared := model.Provenance{Repository: s.Repository, Revision: s.Revision, Path: ".radar/contracts.json", Method: "explicit_declared_dependency (runtime usage unproven)", Evidence: model.VerifiedStatic}
		for _, link := range []struct{ path, kind string }{{b.Producer, "EXPOSES"}, {b.Consumer, "CONSUMES"}} {
			if link.path == "" {
				continue
			}
			id := model.StableID(s.Repository, "file", link.path)
			if nodes[id] {
				addEdge(id, cid, link.kind, declared)
			} else {
				s.Diagnostics = append(s.Diagnostics, model.Diagnostic{Path: link.path, Severity: "warning", Message: "declared contract endpoint file was not structurally indexed"})
			}
		}
		props, _ := schema["properties"].(map[string]any)
		for field := range props {
			pointer := b.Pointer + "/properties/" + strings.ReplaceAll(strings.ReplaceAll(field, "~", "~0"), "/", "~1")
			id := model.StableID(s.Repository, "schema_field", b.Schema, pointer)
			addNode(model.Node{ID: id, Kind: "schema_field", Name: field, Properties: map[string]string{"json_pointer": pointer, "contract": b.ID}, Provenance: p})
			addEdge(cid, id, "DEFINES", p)
			for _, used := range b.Fields {
				if used == field {
					consumer := model.StableID(s.Repository, "file", b.Consumer)
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
