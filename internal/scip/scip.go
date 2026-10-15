// Package scip imports externally produced SCIP protobuf occurrences on explicit
// opt-in. An index is a tool assertion, not proof that a compiler was run: SCIP
// producers may themselves be heuristic. Structural graph evidence is retained.
package scip

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"path"
	"strings"
	"unicode/utf8"

	"github.com/Jake-Network/radar/internal/model"
)

const MaxBytes = 32 << 20
const maxOccurrences = 200000

// Binding is a local freshness assertion alongside standard index.scip bytes.
// Revision is exact, IndexSHA256 covers protobuf bytes, Sources covers every
// indexed document. Hashes detect staleness/tampering, not producer identity.
type Binding struct {
	Version     int               `json:"version"`
	Revision    string            `json:"revision"`
	IndexSHA256 string            `json:"index_sha256"`
	Sources     map[string]string `json:"sources"`
}
type field struct {
	num   int
	wire  int
	value uint64
	bytes []byte
}

// fields decodes bounded protobuf wire types. Groups are deliberately unsupported.
func fields(data []byte) ([]field, error) {
	out := []field{}
	for len(data) > 0 {
		tag, n := binary.Uvarint(data)
		if n <= 0 || tag>>3 == 0 || tag>>3 > 536870911 {
			return nil, fmt.Errorf("invalid protobuf tag")
		}
		data = data[n:]
		f := field{num: int(tag >> 3), wire: int(tag & 7)}
		switch f.wire {
		case 0:
			v, n := binary.Uvarint(data)
			if n <= 0 {
				return nil, fmt.Errorf("truncated protobuf varint")
			}
			f.value = v
			data = data[n:]
		case 1:
			if len(data) < 8 {
				return nil, fmt.Errorf("truncated protobuf fixed64")
			}
			data = data[8:]
		case 2:
			size, n := binary.Uvarint(data)
			if n <= 0 || size > uint64(len(data)-n) {
				return nil, fmt.Errorf("truncated protobuf bytes")
			}
			data = data[n:]
			f.bytes = data[:int(size)]
			data = data[int(size):]
		case 5:
			if len(data) < 4 {
				return nil, fmt.Errorf("truncated protobuf fixed32")
			}
			data = data[4:]
		default:
			return nil, fmt.Errorf("unsupported protobuf wire type")
		}
		out = append(out, f)
		if len(out) > maxOccurrences {
			return nil, fmt.Errorf("protobuf field budget exceeded")
		}
	}
	return out, nil
}
func text(f field) (string, error) {
	if f.wire != 2 || !utf8.Valid(f.bytes) {
		return "", fmt.Errorf("invalid protobuf string")
	}
	return string(f.bytes), nil
}
func relative(p string) bool {
	return p != "" && p != "." && path.Clean(p) == p && !strings.HasPrefix(p, "/") && !strings.ContainsAny(p, "\\:\x00") && p != ".." && !strings.HasPrefix(p, "../")
}

type occurrence struct {
	symbol    string
	role      uint64
	line, end int
}
type document struct {
	path, language string
	occurrences    []occurrence
}

func parseOccurrence(data []byte) (occurrence, error) {
	o := occurrence{}
	fs, err := fields(data)
	if err != nil {
		return o, err
	}
	var r []uint64
	var typed []uint64
	for _, f := range fs {
		switch f.num {
		case 1:
			if f.wire == 0 {
				r = append(r, f.value)
			} else if f.wire == 2 {
				b := f.bytes
				for len(b) > 0 {
					v, n := binary.Uvarint(b)
					if n <= 0 {
						return o, fmt.Errorf("invalid occurrence range")
					}
					r = append(r, v)
					b = b[n:]
					if len(r) > 4 {
						return o, fmt.Errorf("invalid occurrence range")
					}
				}
			} else {
				return o, fmt.Errorf("invalid occurrence range")
			}
		case 2:
			o.symbol, err = text(f)
			if err != nil {
				return o, err
			}
		case 3:
			if f.wire != 0 {
				return o, fmt.Errorf("invalid symbol role")
			}
			o.role = f.value
		case 8, 9:
			if f.wire != 2 {
				return o, fmt.Errorf("invalid typed range")
			}
			parts, e := fields(f.bytes)
			if e != nil {
				return o, e
			}
			size := 3
			if f.num == 9 {
				size = 4
			}
			typed = make([]uint64, size)
			for _, v := range parts {
				if v.num < 1 || v.num > size || v.wire != 0 {
					return o, fmt.Errorf("invalid typed range")
				}
				typed[v.num-1] = v.value
			}
		}
	}
	if len(typed) > 0 {
		r = typed
	}
	if len(r) != 3 && len(r) != 4 {
		return o, fmt.Errorf("missing or unsupported occurrence range")
	}
	for _, v := range r {
		if v > 2147483646 {
			return o, fmt.Errorf("occurrence range out of bounds")
		}
	}
	endLine, endCol := r[0], r[2]
	if len(r) == 4 {
		endLine, endCol = r[2], r[3]
	}
	if endLine < r[0] || (endLine == r[0] && endCol < r[1]) {
		return o, fmt.Errorf("reversed occurrence range")
	}
	o.line, o.end = int(r[0])+1, int(endLine)+1
	return o, nil
}
func parseDocument(data []byte) (document, error) {
	d := document{}
	fs, err := fields(data)
	if err != nil {
		return d, err
	}
	for _, f := range fs {
		switch f.num {
		case 1:
			d.path, err = text(f)
		case 4:
			d.language, err = text(f)
		case 6:
			if f.wire != 0 || f.value > 3 {
				return d, fmt.Errorf("unsupported position encoding")
			}
		case 2:
			if f.wire != 2 {
				return d, fmt.Errorf("invalid occurrence")
			}
			o, e := parseOccurrence(f.bytes)
			if e != nil {
				return d, e
			}
			d.occurrences = append(d.occurrences, o)
		}
		if err != nil {
			return d, err
		}
	}
	if !relative(d.path) {
		return d, fmt.Errorf("noncanonical SCIP document path")
	}
	return d, nil
}

// Augment validates the entire input before adding nodes and edges. Failure adds
// a fixed unknown diagnostic and returns an error; no partial tool graph survives.
// The supported subset imports occurrence definitions/references; rich symbol
// documentation, external declarations and relationship metadata are not analyzed.
func Augment(ctx context.Context, s *model.Snapshot, data []byte, b Binding) (err error) {
	defer func() {
		if err != nil {
			s.Diagnostics = append(s.Diagnostics, model.Diagnostic{Severity: model.SeverityWarning, Message: "SCIP input unknown: malformed, unsupported, or stale index; tool references not imported"})
		}
	}()
	if err = ctx.Err(); err != nil {
		return err
	}
	if len(data) == 0 || len(data) > MaxBytes {
		return fmt.Errorf("SCIP input size limit")
	}
	digest := sha256.Sum256(data)
	if b.Version != 1 || b.Revision == "" || b.Revision != s.Revision || b.IndexSHA256 != hex.EncodeToString(digest[:]) {
		return fmt.Errorf("SCIP binding mismatch")
	}
	if len(b.Sources) == 0 || len(b.Sources) > 10000 {
		return fmt.Errorf("SCIP source binding budget exceeded")
	}
	for p, digest := range b.Sources {
		decoded, e := hex.DecodeString(digest)
		if !relative(p) || e != nil || len(decoded) != 32 || digest != strings.ToLower(digest) {
			return fmt.Errorf("invalid SCIP source binding")
		}
	}
	sources := map[string]string{}
	for _, n := range s.Nodes {
		if n.Kind == "file" {
			sources[n.Provenance.Path] = n.Properties["content_sha256"]
		}
	}
	wireBudget := 1000000
	if err := validateWire(data, "Index", 0, &wireBudget); err != nil {
		return err
	}
	fs, e := fields(data)
	if e != nil {
		return e
	}
	var docs []document
	seen := map[string]bool{}
	metadata := false
	count := 0
	for i, f := range fs {
		if err = ctx.Err(); err != nil {
			return err
		}
		switch f.num {
		case 1:
			if i != 0 || metadata || f.wire != 2 {
				return fmt.Errorf("invalid SCIP metadata")
			}
			metadata = true
			mf, e := fields(f.bytes)
			if e != nil {
				return e
			}
			for _, v := range mf {
				if v.num == 1 && (v.wire != 0 || v.value != 0) {
					return fmt.Errorf("unsupported SCIP protocol")
				}
			}
		case 2:
			if f.wire != 2 {
				return fmt.Errorf("invalid SCIP document")
			}
			d, e := parseDocument(f.bytes)
			if e != nil {
				return e
			}
			if seen[d.path] || sources[d.path] == "" || sources[d.path] != b.Sources[d.path] {
				return fmt.Errorf("stale or duplicate SCIP document")
			}
			seen[d.path] = true
			count += len(d.occurrences)
			if count > maxOccurrences {
				return fmt.Errorf("SCIP occurrence budget exceeded")
			}
			docs = append(docs, d)
		}
	}
	if !metadata || len(docs) == 0 {
		return fmt.Errorf("SCIP metadata or documents missing")
	}
	nodes := map[string]model.Node{}
	edges := map[string]model.Edge{}
	for _, d := range docs {
		for _, o := range d.occurrences {
			if o.symbol == "" {
				continue
			}
			scope := ""
			if strings.HasPrefix(o.symbol, "local ") {
				scope = d.path
			}
			id := "scip_symbol:" + model.StableID(scope, o.symbol)
			prov := model.Provenance{Repository: s.Repository, Revision: s.Revision, Path: d.path, Line: o.line, EndLine: o.end, Method: "scip-import", Evidence: model.VerifiedTool}
			n := model.Node{ID: id, Kind: "scip_symbol", Name: o.symbol, Language: d.language, Provenance: prov}
			if _, ok := nodes[id]; !ok || o.role&1 != 0 {
				nodes[id] = n
			}
			kind := "REFERENCES"
			if o.role&1 != 0 {
				kind = "DEFINES"
			}
			edge := model.Edge{ID: model.StableID(model.FileID(d.path), kind, id, fmt.Sprint(o.line)), From: model.FileID(d.path), To: id, Kind: kind, Provenance: prov}
			edges[edge.ID] = edge
		}
	}
	for _, n := range nodes {
		s.Nodes = append(s.Nodes, n)
	}
	for _, e := range edges {
		s.Edges = append(s.Edges, e)
	}
	model.SortSnapshot(s)
	return nil
}
