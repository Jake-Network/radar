// Package contracts checks explicitly declared contracts with bounded schema support.
package contracts

import (
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/radar-engine/radar/internal/jsonptr"
	"gopkg.in/yaml.v3"
)

// DecodeDocument decodes a JSON contract document.
func DecodeDocument(data []byte) (map[string]any, error) {
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("invalid JSON contract: %w", err)
	}
	if doc == nil {
		return nil, fmt.Errorf("contract must be an object")
	}
	return doc, nil
}

// DecodeDocumentAt decodes JSON, or YAML when the path ends in .yaml/.yml.
// YAML is normalized through JSON so both formats compare identically.
func DecodeDocumentAt(p string, data []byte) (map[string]any, error) {
	switch strings.ToLower(path.Ext(p)) {
	case ".yaml", ".yml":
		var value any
		if err := yaml.Unmarshal(data, &value); err != nil {
			return nil, fmt.Errorf("invalid YAML contract: %w", err)
		}
		normalized, err := json.Marshal(value)
		if err != nil {
			return nil, fmt.Errorf("YAML contract is not JSON-compatible (non-string keys?): %w", err)
		}
		return DecodeDocument(normalized)
	}
	return DecodeDocument(data)
}

// Pointer resolves an object inside a contract document.
func Pointer(doc map[string]any, pointer string) (map[string]any, error) {
	value, found, err := jsonptr.Lookup(doc, pointer)
	if err != nil {
		return nil, fmt.Errorf("pointer %q: %w", pointer, err)
	}
	m, ok := value.(map[string]any)
	if !found || !ok {
		return nil, fmt.Errorf("pointer %q missing or not object", pointer)
	}
	return m, nil
}

// Schema is the normalized subset of JSON Schema/OpenAPI Radar compares.
// Annotations are dropped, validation keywords are summarized, and constructs
// Radar cannot reason about are kept as opaque subtrees.
type Schema struct {
	Types       []string // sorted; nil means unconstrained
	Properties  map[string]*Schema
	Required    []string
	Items       *Schema
	Enum        []string // canonical JSON values; nil means no enum
	Constraints string   // canonical JSON of validation keywords
	Opaque      string   // reason this subtree is not analyzed
	Raw         string   // canonical expanded form of an opaque subtree
	Ignored     []string // unrecognized keywords
}

var (
	annotationKeys = set("title", "description", "$comment", "default", "examples", "example", "deprecated", "$schema", "$id", "$anchor", "$defs", "definitions", "readOnly", "writeOnly", "externalDocs", "xml", "discriminator", "contentMediaType", "contentEncoding", "contentSchema")
	constraintKeys = set("format", "pattern", "minLength", "maxLength", "minimum", "maximum", "exclusiveMinimum", "exclusiveMaximum", "multipleOf", "minItems", "maxItems", "uniqueItems", "minProperties", "maxProperties", "additionalProperties", "patternProperties", "propertyNames", "minContains", "maxContains", "dependentRequired")
	opaqueKeys     = set("not", "if", "then", "else", "dependentSchemas", "prefixItems", "unevaluatedProperties", "unevaluatedItems", "contains", "additionalItems")
	typeNames      = set("object", "array", "string", "number", "integer", "boolean", "null")
)

func set(values ...string) map[string]bool {
	out := map[string]bool{}
	for _, v := range values {
		out[v] = true
	}
	return out
}

func canonical(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

const maxDepth = 64

// Normalize resolves local references and reduces a schema object.
func Normalize(doc, schema map[string]any) (*Schema, error) {
	return normalize(doc, schema, 0, map[string]bool{})
}

func normalize(doc, s map[string]any, depth int, stack map[string]bool) (*Schema, error) {
	if depth > maxDepth {
		return &Schema{Opaque: "schema nesting exceeds limit", Raw: canonical(s)}, nil
	}
	if raw, ok := s["$ref"]; ok {
		ref, isString := raw.(string)
		if !isString {
			return nil, fmt.Errorf("$ref must be a string")
		}
		if !strings.HasPrefix(ref, "#") {
			return &Schema{Opaque: "external reference " + ref, Raw: canonical(s)}, nil
		}
		if stack[ref] {
			return &Schema{Opaque: "recursive reference " + ref, Raw: canonical(map[string]any{"$ref": ref})}, nil
		}
		target, err := Pointer(doc, strings.TrimPrefix(ref, "#"))
		if err != nil {
			return nil, err
		}
		stack[ref] = true
		resolved, err := normalize(doc, target, depth+1, stack)
		delete(stack, ref)
		if err != nil {
			return nil, err
		}
		// OpenAPI 3.1 permits $ref siblings; they further constrain the target.
		siblings := map[string]any{}
		for k, v := range s {
			if k != "$ref" && !annotationKeys[k] {
				siblings[k] = v
			}
		}
		if len(siblings) == 0 {
			return resolved, nil
		}
		rest, err := normalize(doc, siblings, depth+1, stack)
		if err != nil {
			return nil, err
		}
		merge(resolved, rest)
		return resolved, nil
	}
	out := &Schema{}
	constraints := map[string]any{}
	keys := make([]string, 0, len(s))
	for k := range s {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		value := s[key]
		switch {
		case annotationKeys[key] || strings.HasPrefix(key, "x-"):
		case constraintKeys[key]:
			constraints[key] = value
		case opaqueKeys[key]:
			return opaque(doc, s, "unsupported keyword "+key, stack)
		case key == "type":
			types, err := parseTypes(value)
			if err != nil {
				return nil, err
			}
			out.Types = union(out.Types, types)
		case key == "nullable":
			// Applied after the type is known.
		case key == "properties":
			props, ok := value.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("properties must be an object")
			}
			out.Properties = map[string]*Schema{}
			for name, raw := range props {
				child, ok := raw.(map[string]any)
				if !ok {
					return nil, fmt.Errorf("property %s is not a schema object", name)
				}
				normalized, err := normalize(doc, child, depth+1, stack)
				if err != nil {
					return nil, fmt.Errorf("property %s: %w", name, err)
				}
				out.Properties[name] = normalized
			}
		case key == "required":
			values, ok := value.([]any)
			if !ok {
				return nil, fmt.Errorf("required must be an array")
			}
			for _, v := range values {
				name, ok := v.(string)
				if !ok {
					return nil, fmt.Errorf("required entries must be strings")
				}
				out.Required = union(out.Required, []string{name})
			}
		case key == "items":
			switch items := value.(type) {
			case map[string]any:
				child, err := normalize(doc, items, depth+1, stack)
				if err != nil {
					return nil, fmt.Errorf("items: %w", err)
				}
				out.Items = child
			case []any:
				return opaque(doc, s, "tuple items", stack)
			case bool:
				constraints[key] = value
			default:
				return nil, fmt.Errorf("items schema unsupported")
			}
		case key == "enum":
			values, ok := value.([]any)
			if !ok {
				return nil, fmt.Errorf("enum must be an array")
			}
			out.Enum = []string{}
			for _, v := range values {
				out.Enum = union(out.Enum, []string{canonical(v)})
			}
		case key == "const":
			out.Enum = []string{canonical(value)}
		case key == "allOf":
			branches, err := normalizeBranches(doc, value, depth, stack)
			if err != nil {
				return nil, err
			}
			for _, b := range branches {
				merge(out, b)
			}
		case key == "anyOf" || key == "oneOf":
			branches, err := normalizeBranches(doc, value, depth, stack)
			if err != nil {
				return nil, err
			}
			combined := combineAlternatives(branches)
			if combined.Opaque != "" {
				return opaque(doc, s, key+" with multiple structured alternatives", stack)
			}
			merge(out, combined)
		default:
			out.Ignored = append(out.Ignored, key)
		}
	}
	if nullable, _ := s["nullable"].(bool); nullable && out.Types != nil {
		out.Types = union(out.Types, []string{"null"})
	}
	if len(constraints) > 0 {
		out.Constraints = canonical(constraints)
	}
	return out, nil
}

func parseTypes(value any) ([]string, error) {
	switch t := value.(type) {
	case string:
		if !typeNames[t] {
			return nil, fmt.Errorf("invalid or unsupported schema type %q", t)
		}
		return []string{t}, nil
	case []any:
		out := []string{}
		for _, v := range t {
			name, ok := v.(string)
			if !ok || !typeNames[name] {
				return nil, fmt.Errorf("invalid or unsupported schema type")
			}
			out = union(out, []string{name})
		}
		return out, nil
	}
	return nil, fmt.Errorf("invalid or unsupported schema type")
}

func normalizeBranches(doc map[string]any, value any, depth int, stack map[string]bool) ([]*Schema, error) {
	list, ok := value.([]any)
	if !ok || len(list) == 0 {
		return nil, fmt.Errorf("composition keyword requires a non-empty array")
	}
	out := []*Schema{}
	for _, raw := range list {
		branch, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("composition branch is not a schema object")
		}
		s, err := normalize(doc, branch, depth+1, stack)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, nil
}

// combineAlternatives supports the common nullable form (X | null) and unions
// of scalar types. Several structured alternatives remain opaque.
func combineAlternatives(branches []*Schema) *Schema {
	nullable := false
	rest := []*Schema{}
	for _, b := range branches {
		if b.Opaque != "" {
			return &Schema{Opaque: b.Opaque}
		}
		if len(b.Types) == 1 && b.Types[0] == "null" && b.Properties == nil && b.Items == nil && b.Enum == nil {
			nullable = true
			continue
		}
		rest = append(rest, b)
	}
	var out *Schema
	switch {
	case len(rest) == 0:
		out = &Schema{Types: []string{"null"}}
	case len(rest) == 1:
		copied := *rest[0]
		out = &copied
	default:
		out = &Schema{}
		for _, b := range rest {
			if b.Properties != nil || b.Items != nil || len(b.Required) > 0 || b.Types == nil {
				return &Schema{Opaque: "structured alternatives"}
			}
			out.Types = union(out.Types, b.Types)
			if b.Enum != nil {
				out.Enum = union(out.Enum, b.Enum)
			}
		}
	}
	if nullable && out.Types != nil {
		out.Types = union(out.Types, []string{"null"})
	}
	return out
}

// merge applies src as an additional constraint on dst (allOf semantics).
func merge(dst, src *Schema) {
	if src.Opaque != "" && dst.Opaque == "" {
		dst.Opaque, dst.Raw = src.Opaque, src.Raw
	}
	switch {
	case dst.Types == nil:
		dst.Types = src.Types
	case src.Types != nil:
		dst.Types = intersect(dst.Types, src.Types)
	}
	if src.Properties != nil {
		if dst.Properties == nil {
			dst.Properties = map[string]*Schema{}
		}
		for name, p := range src.Properties {
			if existing, ok := dst.Properties[name]; ok && canonical(existing) != canonical(p) {
				merge(existing, p)
				continue
			}
			dst.Properties[name] = p
		}
	}
	dst.Required = union(dst.Required, src.Required)
	if src.Items != nil {
		if dst.Items == nil {
			dst.Items = src.Items
		} else {
			merge(dst.Items, src.Items)
		}
	}
	if src.Enum != nil {
		if dst.Enum == nil {
			dst.Enum = src.Enum
		} else {
			dst.Enum = intersect(dst.Enum, src.Enum)
		}
	}
	if src.Constraints != "" {
		dst.Constraints = strings.Trim(dst.Constraints+"&"+src.Constraints, "&")
	}
	dst.Ignored = append(dst.Ignored, src.Ignored...)
}

func opaque(doc, s map[string]any, reason string, stack map[string]bool) (*Schema, error) {
	return &Schema{Opaque: reason, Raw: canonical(expand(doc, s, stack, 0))}, nil
}

// expand inlines local references so a change in a referenced definition
// still changes the canonical form of an opaque subtree.
func expand(doc map[string]any, v any, stack map[string]bool, depth int) any {
	if depth > maxDepth {
		return v
	}
	switch t := v.(type) {
	case map[string]any:
		if ref, ok := t["$ref"].(string); ok && strings.HasPrefix(ref, "#") && !stack[ref] {
			if target, err := Pointer(doc, strings.TrimPrefix(ref, "#")); err == nil {
				stack[ref] = true
				defer delete(stack, ref)
				return expand(doc, target, stack, depth+1)
			}
		}
		out := map[string]any{}
		for k, child := range t {
			if !annotationKeys[k] {
				out[k] = expand(doc, child, stack, depth+1)
			}
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, child := range t {
			out[i] = expand(doc, child, stack, depth+1)
		}
		return out
	}
	return v
}

func union(a, b []string) []string {
	if a == nil && b == nil {
		return nil
	}
	seen := map[string]bool{}
	out := []string{}
	for _, v := range append(append([]string{}, a...), b...) {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	sort.Strings(out)
	return out
}

func intersect(a, b []string) []string {
	in := set(b...)
	out := []string{}
	for _, v := range a {
		if in[v] || (v == "integer" && in["number"]) {
			out = append(out, v)
		} else if v == "number" && in["integer"] {
			out = append(out, "integer")
		}
	}
	return union(out, nil)
}

// Analyzable reports whether the object at pointer can be normalized.
func Analyzable(doc map[string]any, pointer string) (*Schema, error) {
	obj, err := Pointer(doc, pointer)
	if err != nil {
		return nil, err
	}
	return Normalize(doc, obj)
}

// Compare reports supported differences between two schema objects. Unsupported
// constructs that differ are reported as changes of kind "unanalyzed".
func Compare(before, after map[string]any, pointer string) ([]Change, error) {
	a, err := Analyzable(before, pointer)
	if err != nil {
		return nil, err
	}
	b, err := Analyzable(after, pointer)
	if err != nil {
		return nil, err
	}
	return diff(a, b, ""), nil
}

func label(field string) string {
	if field == "" {
		return "schema root"
	}
	return field
}

func child(path, name string) string {
	if path == "" {
		return name
	}
	return path + "." + name
}

func diff(a, b *Schema, field string) []Change {
	if a.Opaque != "" || b.Opaque != "" {
		if a.Opaque == b.Opaque && a.Raw == b.Raw {
			return nil
		}
		reason := a.Opaque
		if reason == "" {
			reason = b.Opaque
		}
		return []Change{{Field: field, Kind: "unanalyzed", Explanation: fmt.Sprintf("%s uses an unsupported construct (%s) that differs; compatibility not analyzed", label(field), reason)}}
	}
	var changes []Change
	if strings.Join(a.Types, "|") != strings.Join(b.Types, "|") {
		changes = append(changes, Change{Field: field, Kind: "type_changed", BeforeType: strings.Join(a.Types, "|"), AfterType: strings.Join(b.Types, "|"), Explanation: fmt.Sprintf("%s type changed from %s to %s", label(field), typeLabel(a.Types), typeLabel(b.Types))})
	}
	names := make([]string, 0, len(a.Properties))
	for name := range a.Properties {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		f := child(field, name)
		bp, ok := b.Properties[name]
		if !ok {
			changes = append(changes, Change{Field: f, Kind: "field_removed", Explanation: fmt.Sprintf("property %s was removed", f)})
			continue
		}
		changes = append(changes, diff(a.Properties[name], bp, f)...)
	}
	ar, br := set(a.Required...), set(b.Required...)
	for _, name := range b.Required {
		if !ar[name] {
			f := child(field, name)
			changes = append(changes, Change{Field: f, Kind: "required_added", Explanation: fmt.Sprintf("property %s is newly required", f)})
		}
	}
	for _, name := range a.Required {
		// A removed property is already reported as field_removed.
		if _, kept := b.Properties[name]; !kept && a.Properties[name] != nil {
			continue
		}
		if !br[name] {
			f := child(field, name)
			changes = append(changes, Change{Field: f, Kind: "required_removed", Explanation: fmt.Sprintf("property %s is no longer guaranteed required", f)})
		}
	}
	switch {
	case a.Items != nil && b.Items != nil:
		changes = append(changes, diff(a.Items, b.Items, field+"[]")...)
	case a.Items != nil || b.Items != nil:
		changes = append(changes, Change{Field: field + "[]", Kind: "items_changed", Explanation: fmt.Sprintf("%s item schema was added or removed", label(field))})
	}
	changes = append(changes, enumChanges(a.Enum, b.Enum, field)...)
	if a.Constraints != b.Constraints {
		changes = append(changes, Change{Field: field, Kind: "constraint_changed", Explanation: fmt.Sprintf("%s validation constraints changed from %s to %s", label(field), orNone(a.Constraints), orNone(b.Constraints))})
	}
	return changes
}

func enumChanges(a, b []string, field string) []Change {
	switch {
	case a == nil && b == nil:
		return nil
	case a == nil:
		return []Change{{Field: field, Kind: "enum_added", Values: b, Explanation: fmt.Sprintf("%s is now restricted to %s", label(field), strings.Join(b, ", "))}}
	case b == nil:
		return []Change{{Field: field, Kind: "enum_removed", Values: a, Explanation: fmt.Sprintf("%s is no longer restricted to an enumeration", label(field))}}
	}
	as, bs := set(a...), set(b...)
	var added, removed []string
	for _, v := range b {
		if !as[v] {
			added = append(added, v)
		}
	}
	for _, v := range a {
		if !bs[v] {
			removed = append(removed, v)
		}
	}
	var out []Change
	if len(removed) > 0 {
		out = append(out, Change{Field: field, Kind: "enum_value_removed", Values: removed, Explanation: fmt.Sprintf("%s enumeration removed %s", label(field), strings.Join(removed, ", "))})
	}
	if len(added) > 0 {
		out = append(out, Change{Field: field, Kind: "enum_value_added", Values: added, Explanation: fmt.Sprintf("%s enumeration added %s", label(field), strings.Join(added, ", "))})
	}
	return out
}

func typeLabel(types []string) string {
	if types == nil {
		return "unconstrained"
	}
	return strings.Join(types, "|")
}

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}

// Notes lists unsupported subtrees and ignored keywords below a schema, for diagnostics.
func (s *Schema) Notes(field string) []string {
	var out []string
	if s.Opaque != "" {
		return []string{label(field) + ": " + s.Opaque + " (changes inside are reported as unanalyzed)"}
	}
	for _, k := range s.Ignored {
		out = append(out, fmt.Sprintf("%s: keyword %s ignored", label(field), k))
	}
	names := make([]string, 0, len(s.Properties))
	for name := range s.Properties {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		out = append(out, s.Properties[name].Notes(child(field, name))...)
	}
	if s.Items != nil {
		out = append(out, s.Items.Notes(field+"[]")...)
	}
	return out
}

// Field resolves a dotted field path ("a.b", "items[].id") in a normalized
// schema, descending into array items when needed.
func (s *Schema) Field(field string) (*Schema, bool) {
	cur := s
	for _, part := range strings.Split(strings.ReplaceAll(field, "[]", ""), ".") {
		if part == "" {
			continue
		}
		for cur.Items != nil && cur.Properties == nil {
			cur = cur.Items
		}
		next, ok := cur.Properties[part]
		if !ok {
			return nil, false
		}
		cur = next
	}
	return cur, true
}
