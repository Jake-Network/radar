package verification

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/radar-engine/radar/internal/contracts"
	gitrepo "github.com/radar-engine/radar/internal/git"
	"github.com/radar-engine/radar/internal/jsonptr"
	"github.com/radar-engine/radar/internal/model"
	"github.com/radar-engine/radar/internal/pathutil"
	"github.com/radar-engine/radar/internal/planning"
)

// VerifyContracts compares explicitly bound contract objects, including additive
// changes. It does not infer runtime producer/consumer relationships.
func VerifyContracts(ctx context.Context, root string, p planning.Plan, s model.Snapshot) []planning.Check {
	var checks []planning.Check
	add := func(id string, status model.Status, message, path string) {
		c := planning.Check{ID: id, Status: status, Explanation: message, Evidence: model.VerifiedStatic}
		if status == model.StatusUnknown {
			c.Evidence = model.Unknown
		}
		if path != "" {
			c.Location = &model.Provenance{Repository: s.Repository, Revision: s.Revision, Path: path, Method: "approved_contract_projection", Evidence: c.Evidence}
		}
		checks = append(checks, c)
	}
	base, be := gitrepo.Resolve(ctx, root, p.BaseRevision)
	head, he := gitrepo.Resolve(ctx, root, s.Revision)
	if be != nil || he != nil || base != p.BaseRevision || head != s.Revision || !planning.Approved(p) {
		if len(p.ContractDeltas) > 0 {
			add("contract_checkpoint", "unknown", "Contract projection requires a digest-bound approved plan and immutable baseline and implementation commits.", "")
		}
		return checks
	}
	before, err := contracts.LoadManifest(ctx, root, base)
	if err != nil {
		add("contract_scope", "unknown", "Baseline contract bindings unavailable: "+err.Error(), ".radar/contracts.json")
		return checks
	}
	after, err := contracts.LoadManifest(ctx, root, head)
	if err != nil {
		add("contract_scope", "unknown", "Implementation contract bindings unavailable: "+err.Error(), ".radar/contracts.json")
		return checks
	}
	bm, am := map[string]contracts.Binding{}, map[string]contracts.Binding{}
	for _, b := range before.Bindings {
		bm[b.ID] = b
	}
	for _, b := range after.Bindings {
		am[b.ID] = b
	}
	deltas := map[string]planning.ContractDelta{}
	for _, d := range p.ContractDeltas {
		deltas[d.Contract] = d
	}
	ids := map[string]bool{}
	for id := range bm {
		ids[id] = true
	}
	for id := range am {
		ids[id] = true
	}
	for id := range deltas {
		ids[id] = true
	}
	order := make([]string, 0, len(ids))
	for id := range ids {
		order = append(order, id)
	}
	sort.Strings(order)
	for _, id := range order {
		b, bok := bm[id]
		a, aok := am[id]
		d, planned := deltas[id]
		cid := "contract:" + id
		if !planned {
			if !bok || !aok || !reflect.DeepEqual(b, a) {
				add(cid, "failed", "Unexpected contract binding addition, removal, or modification; no approved delta declares this change.", ".radar/contracts.json")
				continue
			}
			bv, err := contractObject(ctx, root, base, b.Schema, b.Pointer)
			if err != nil {
				add(cid, "unknown", "Cannot analyze baseline contract: "+err.Error(), b.Schema)
				continue
			}
			av, err := contractObject(ctx, root, head, a.Schema, a.Pointer)
			if err != nil {
				present, absenceErr := objectPresent(ctx, root, head, a.Schema, a.Pointer)
				if absenceErr == nil && !present {
					add(cid, "failed", "Unexpected removal of the bound contract schema object.", a.Schema)
				} else {
					add(cid, "unknown", "Cannot analyze implementation contract: "+err.Error(), a.Schema)
				}
				continue
			}
			if reflect.DeepEqual(bv, av) {
				add(cid, "passed", "Explicitly bound supported contract unchanged.", a.Schema)
			} else {
				add(cid, "failed", "Unexpected contract drift, including additions; implementation differs from approved baseline.", a.Schema)
			}
			continue
		}
		if d.Schema == "" || (d.Operation != "remove" && len(d.Expected) == 0) {
			add(cid, "unknown", "Contract delta lacks an explicit schema path and complete projected object; prose alone cannot prove conformance.", d.Schema)
			continue
		}
		switch d.Operation {
		case "remove":
			if !bok || b.Schema != d.Schema || b.Pointer != d.Pointer {
				add(cid, "unknown", "Removal does not identify the baseline binding.", d.Schema)
				continue
			}
			if aok {
				add(cid, "failed", "Approved contract removal is incomplete: binding remains.", ".radar/contracts.json")
				continue
			}
			present, err := objectPresent(ctx, root, head, d.Schema, d.Pointer)
			if err != nil {
				add(cid, "unknown", err.Error(), d.Schema)
			} else if present {
				add(cid, "failed", "Approved contract removal is incomplete: schema object remains.", d.Schema)
			} else {
				add(cid, "passed", "Approved binding and schema object are absent at implementation commit.", d.Schema)
			}
		case "add", "modify":
			if (d.Operation == "add" && bok) || (d.Operation == "modify" && !bok) {
				add(cid, "failed", "Delta operation does not match baseline contract existence.", d.Schema)
				continue
			}
			if !aok || a.Schema != d.Schema || a.Pointer != d.Pointer {
				add(cid, "failed", "Implementation binding is missing or does not match approved schema location.", ".radar/contracts.json")
				continue
			}
			// Changes to declared producer, consumer, fields or direction cannot be
			// authorized by a schema-only delta.
			if bok {
				projected := b
				projected.Schema = d.Schema
				projected.Pointer = d.Pointer
				if !reflect.DeepEqual(projected, a) {
					add(cid+":binding", "failed", "Unexpected producer/consumer binding change outside approved schema projection.", ".radar/contracts.json")
				}
			}
			expected, err := contracts.DecodeDocument(d.Expected)
			if err != nil {
				add(cid, "unknown", "Invalid projected object: "+err.Error(), d.Schema)
				continue
			}
			if _, err = contracts.Compare(expected, expected, ""); err != nil {
				add(cid, "unknown", "Unsupported projected schema: "+err.Error(), d.Schema)
				continue
			}
			ev, err := expandSchema(expected, expected, 0)
			if err != nil {
				add(cid, "unknown", err.Error(), d.Schema)
				continue
			}
			av, err := contractObject(ctx, root, head, d.Schema, d.Pointer)
			if err != nil {
				present, absenceErr := objectPresent(ctx, root, head, d.Schema, d.Pointer)
				if absenceErr == nil && !present {
					add(cid, "failed", "Approved projected contract object is absent.", d.Schema)
				} else {
					add(cid, "unknown", "Cannot analyze implementation contract: "+err.Error(), d.Schema)
				}
				continue
			}
			if reflect.DeepEqual(ev, av) {
				add(cid, "passed", "Implementation equals the complete approved contract projection.", d.Schema)
			} else {
				add(cid, "failed", "Implementation contract differs from complete approved projection.", d.Schema)
			}
		default:
			add(cid, "unknown", "Unsupported contract delta operation.", d.Schema)
		}
	}
	return checks
}

func contractObject(ctx context.Context, root, rev, path, pointer string) (map[string]any, error) {
	doc, err := contracts.ReadSchema(ctx, root, rev, path)
	if err != nil {
		return nil, err
	}
	if _, err = contracts.Compare(doc, doc, pointer); err != nil {
		return nil, err
	}
	obj, err := contracts.Pointer(doc, pointer)
	if err != nil {
		return nil, err
	}
	return expandSchema(doc, obj, 0)
}

// Expand local refs so changes in referenced definitions are visible. Other
// supported annotations remain part of the exact approved object comparison.
func expandSchema(doc, obj map[string]any, depth int) (map[string]any, error) {
	if depth > 32 {
		return nil, fmt.Errorf("schema reference/nesting exceeds limit")
	}
	if ref, ok := obj["$ref"].(string); ok {
		if !strings.HasPrefix(ref, "#") {
			return nil, fmt.Errorf("external refs unsupported")
		}
		next, err := contracts.Pointer(doc, strings.TrimPrefix(ref, "#"))
		if err != nil {
			return nil, err
		}
		return expandSchema(doc, next, depth+1)
	}
	out := map[string]any{}
	for k, v := range obj {
		switch k {
		case "properties", "$defs", "definitions":
			m, ok := v.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("%s must be an object", k)
			}
			nested := map[string]any{}
			for key, value := range m {
				child, ok := value.(map[string]any)
				if !ok {
					return nil, fmt.Errorf("schema %s must be an object", key)
				}
				expanded, err := expandSchema(doc, child, depth+1)
				if err != nil {
					return nil, err
				}
				nested[key] = expanded
			}
			out[k] = nested
		case "required":
			values, ok := v.([]any)
			if !ok {
				return nil, fmt.Errorf("required must be an array")
			}
			names := make([]string, 0, len(values))
			for _, value := range values {
				name, ok := value.(string)
				if !ok {
					return nil, fmt.Errorf("required entries must be strings")
				}
				names = append(names, name)
			}
			sort.Strings(names)
			normalized := make([]any, 0, len(names))
			for _, name := range names {
				if len(normalized) == 0 || normalized[len(normalized)-1] != name {
					normalized = append(normalized, name)
				}
			}
			out[k] = normalized
		case "items":
			child, ok := v.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("items must be object")
			}
			expanded, err := expandSchema(doc, child, depth+1)
			if err != nil {
				return nil, err
			}
			out[k] = expanded
		default:
			out[k] = v
		}
	}
	return out, nil
}

// objectPresent proves absence from the committed file inventory or a valid
// JSON object pointer. Read/parse errors never stand in for deletion evidence.
func objectPresent(ctx context.Context, root, rev, path, pointer string) (bool, error) {
	if _, err := pathutil.RepoRelative(path); err != nil {
		return false, fmt.Errorf("invalid repository path")
	}
	files, err := gitrepo.Files(ctx, root, rev)
	if err != nil {
		return false, err
	}
	found := false
	for _, f := range files {
		if f == path {
			found = true
			break
		}
	}
	if !found {
		return false, nil
	}
	doc, err := contracts.ReadSchema(ctx, root, rev, path)
	if err != nil {
		return false, err
	}
	value, present, err := jsonptr.Lookup(doc, pointer)
	if err != nil {
		return false, err
	}
	if !present {
		return false, nil
	}
	if _, ok := value.(map[string]any); !ok {
		return false, fmt.Errorf("pointer target is not an object")
	}
	return true, nil
}
