// Package contracts checks explicitly declared contracts with bounded schema support.
package contracts

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

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
func Pointer(doc map[string]any, pointer string) (map[string]any, error) {
	if pointer == "" {
		return doc, nil
	}
	if !strings.HasPrefix(pointer, "/") {
		return nil, fmt.Errorf("expected JSON pointer, got %q", pointer)
	}
	var cur any = doc
	for _, p := range strings.Split(pointer[1:], "/") {
		p = strings.ReplaceAll(strings.ReplaceAll(p, "~1", "/"), "~0", "~")
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("pointer %q does not resolve to object", pointer)
		}
		cur = m[p]
	}
	m, ok := cur.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("pointer %q missing or not object", pointer)
	}
	return m, nil
}
func resolve(doc, schema map[string]any, depth int) (map[string]any, error) {
	if depth > 32 {
		return nil, fmt.Errorf("schema nesting/reference exceeds limit")
	}
	if raw, exists := schema["$ref"]; exists {
		ref, ok := raw.(string)
		if !ok {
			return nil, fmt.Errorf("$ref must be a string")
		}
		for key := range schema {
			if key != "$ref" && key != "description" && key != "summary" && key != "$comment" {
				return nil, fmt.Errorf("$ref sibling %s unsupported", key)
			}
		}
		if !strings.HasPrefix(ref, "#") {
			return nil, fmt.Errorf("external schema references unsupported: %s", ref)
		}
		next, err := Pointer(doc, strings.TrimPrefix(ref, "#"))
		if err != nil {
			return nil, err
		}
		return resolve(doc, next, depth+1)
	}
	supported := map[string]bool{"type": true, "properties": true, "required": true, "items": true, "$schema": true, "$id": true, "$defs": true, "definitions": true, "title": true, "description": true, "$comment": true, "default": true, "examples": true, "example": true, "deprecated": true}
	for key := range schema {
		if !supported[key] {
			return nil, fmt.Errorf("schema keyword %s is not supported", key)
		}
	}
	if typ, ok := schema["type"]; ok {
		str, ok := typ.(string)
		if !ok || !strings.Contains("|object|array|string|number|integer|boolean|null|", "|"+str+"|") {
			return nil, fmt.Errorf("invalid or unsupported schema type")
		}
	}
	if props, ok := schema["properties"]; ok {
		if _, ok := props.(map[string]any); !ok {
			return nil, fmt.Errorf("properties must be an object")
		}
	}
	if required, ok := schema["required"]; ok {
		values, ok := required.([]any)
		if !ok {
			return nil, fmt.Errorf("required must be an array")
		}
		for _, v := range values {
			if _, ok := v.(string); !ok {
				return nil, fmt.Errorf("required entries must be strings")
			}
		}
	}
	return schema, nil
}

// Compare checks property removal, changed types and newly required fields.
// Other JSON Schema compatibility semantics are not asserted.
func Compare(before, after map[string]any, pointer string) ([]Change, error) {
	a, err := Pointer(before, pointer)
	if err != nil {
		return nil, err
	}
	b, err := Pointer(after, pointer)
	if err != nil {
		return nil, err
	}
	if err := validateSchema(before, a, 0); err != nil {
		return nil, err
	}
	if err := validateSchema(after, b, 0); err != nil {
		return nil, err
	}
	return compare(before, after, a, b, "", 0)
}
func validateSchema(doc, schema map[string]any, depth int) error {
	s, err := resolve(doc, schema, depth)
	if err != nil {
		return err
	}
	if properties, ok := s["properties"].(map[string]any); ok {
		for key, value := range properties {
			child, ok := value.(map[string]any)
			if !ok {
				return fmt.Errorf("property %s is not a schema object", key)
			}
			if err := validateSchema(doc, child, depth+1); err != nil {
				return err
			}
		}
	}
	if items, exists := s["items"]; exists {
		child, ok := items.(map[string]any)
		if !ok {
			return fmt.Errorf("items schema unsupported")
		}
		return validateSchema(doc, child, depth+1)
	}
	return nil
}
func compare(adoc, bdoc, a, b map[string]any, path string, depth int) ([]Change, error) {
	a, err := resolve(adoc, a, depth)
	if err != nil {
		return nil, err
	}
	b, err = resolve(bdoc, b, depth)
	if err != nil {
		return nil, err
	}
	var changes []Change
	if fmt.Sprint(a["type"]) != fmt.Sprint(b["type"]) {
		changes = append(changes, Change{Field: path, Kind: "type_changed", Explanation: fmt.Sprintf("%s type changed from %v to %v", path, a["type"], b["type"]), BeforeType: schemaType(a), AfterType: schemaType(b)})
	}
	ap, _ := a["properties"].(map[string]any)
	bp, _ := b["properties"].(map[string]any)
	keys := make([]string, 0, len(ap))
	for k := range ap {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		field := k
		if path != "" {
			field = path + "." + k
		}
		av, ok := ap[k].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("property %s is not a schema object", field)
		}
		bv, exists := bp[k]
		if !exists {
			changes = append(changes, Change{Field: field, Kind: "field_removed", Explanation: fmt.Sprintf("property %s was removed", field)})
			continue
		}
		bm, ok := bv.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("property %s is not a schema object", field)
		}
		nested, err := compare(adoc, bdoc, av, bm, field, depth+1)
		if err != nil {
			return nil, err
		}
		changes = append(changes, nested...)
	}
	required := func(s map[string]any) map[string]bool {
		r := map[string]bool{}
		vals, _ := s["required"].([]any)
		for _, v := range vals {
			if str, ok := v.(string); ok {
				r[str] = true
			}
		}
		return r
	}
	ar, br := required(a), required(b)
	keys = nil
	for k := range br {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if !ar[k] {
			field := k
			if path != "" {
				field = path + "." + k
			}
			changes = append(changes, Change{Field: field, Kind: "required_added", Explanation: fmt.Sprintf("property %s is newly required", field)})
		}
	}
	keys = nil
	for k := range ar {
		if !br[k] {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		field := k
		if path != "" {
			field = path + "." + k
		}
		changes = append(changes, Change{Field: field, Kind: "required_removed", Explanation: fmt.Sprintf("property %s is no longer guaranteed required", field)})
	}
	if ai, ok := a["items"].(map[string]any); ok {
		if bi, ok := b["items"].(map[string]any); ok {
			nested, err := compare(adoc, bdoc, ai, bi, path+"[]", depth+1)
			if err != nil {
				return nil, err
			}
			changes = append(changes, nested...)
		}
	}
	return changes, nil
}
