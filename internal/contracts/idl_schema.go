package contracts

import (
	"fmt"
	"strings"
)

// normalizedIDL expands known local named types through JSON Schema's bounded
// reference normalizer. Recursive shapes retain its opaque/unknown diagnostics.
func normalizedIDL(doc map[string]any) (map[string]any, error) {
	format := doc[idlFormat].(idlKind)
	pointers := map[string]string{}
	var collect func(map[string]any, string, string)
	collect = func(m map[string]any, scope, pointer string) {
		p, _ := m["properties"].(map[string]any)
		for name, raw := range p {
			v := raw.(map[string]any)
			if v["x-radar-idl-kind"] != nil {
				qualified := child(scope, name)
				ptr := pointer + "/properties/" + name
				pointers[qualified] = ptr
				collect(v, qualified, ptr)
			}
		}
	}
	collect(doc, "", "")
	resolveProto := func(typ, scope string) string {
		pkg, _ := doc["x-radar-package"].(string)
		if pkg != "" && strings.HasPrefix(typ, pkg+".") {
			return strings.TrimPrefix(typ, pkg+".")
		}
		for scope != "" {
			if pointers[child(scope, typ)] != "" {
				return child(scope, typ)
			}
			i := strings.LastIndex(scope, ".")
			if i < 0 {
				break
			}
			scope = scope[:i]
		}
		return typ
	}
	protoScalar := func(typ string) (string, bool) {
		switch typ {
		case "string", "bytes":
			return "string", true
		case "bool":
			return "boolean", true
		case "float", "double":
			return "number", true
		case "int32", "int64", "uint32", "uint64", "sint32", "sint64", "fixed32", "fixed64", "sfixed32", "sfixed64":
			return "integer", true
		}
		return "", false
	}
	var graphqlType func(string) (map[string]any, error)
	graphqlType = func(typ string) (map[string]any, error) {
		nonNull := strings.HasSuffix(typ, "!")
		base := strings.TrimSuffix(typ, "!")
		var schema map[string]any
		if strings.HasPrefix(base, "[") {
			if !strings.HasSuffix(base, "]") {
				return nil, fmt.Errorf("malformed GraphQL list")
			}
			item, e := graphqlType(base[1 : len(base)-1])
			if e != nil {
				return nil, e
			}
			schema = map[string]any{"type": "array", "items": item}
		} else {
			scalar := ""
			switch base {
			case "String", "ID":
				scalar = "string"
			case "Int":
				scalar = "integer"
			case "Float":
				scalar = "number"
			case "Boolean":
				scalar = "boolean"
			}
			if scalar != "" {
				schema = map[string]any{"type": scalar}
			} else {
				ptr := pointers[base]
				if ptr == "" {
					return nil, fmt.Errorf("unresolved GraphQL type %s", base)
				}
				schema = map[string]any{"$ref": "#" + ptr}
			}
		}
		if !nonNull {
			schema = map[string]any{"anyOf": []any{schema, map[string]any{"type": "null"}}}
		}
		return schema, nil
	}
	var normalize func(map[string]any, string) (map[string]any, error)
	normalize = func(m map[string]any, scope string) (map[string]any, error) {
		out := map[string]any{}
		for k, v := range m {
			out[k] = v
		}
		p, _ := m["properties"].(map[string]any)
		if p != nil {
			np := map[string]any{}
			for name, raw := range p {
				v := raw.(map[string]any)
				if v["x-radar-idl-kind"] != nil {
					n, e := normalize(v, child(scope, name))
					if e != nil {
						return nil, e
					}
					np[name] = n
					continue
				}
				if format == "protobuf" {
					typ, _ := v["x-radar-wire-type"].(string)
					scalar, ok := protoScalar(typ)
					var field map[string]any
					if ok {
						field = map[string]any{"type": scalar}
					} else {
						target := resolveProto(typ, scope)
						ptr := pointers[target]
						if ptr == "" {
							return nil, fmt.Errorf("unresolved protobuf type %s", typ)
						}
						field = map[string]any{"$ref": "#" + ptr}
					}
					if v["x-radar-cardinality"] == "repeated" {
						field = map[string]any{"type": "array", "items": field}
					}
					np[name] = field
				} else {
					typ, _ := v["x-radar-graphql-type"].(string)
					n, e := graphqlType(typ)
					if e != nil {
						return nil, e
					}
					np[name] = n
				}
			}
			out["properties"] = np
		}
		return out, nil
	}
	return normalize(doc, "")
}
