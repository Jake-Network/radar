package contracts

// The IDL readers deliberately accept a bounded grammar. Anything outside it
// is unavailable evidence, rather than a schema silently stripped of meaning.
import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

type idlKind string

const idlFormat = "x-radar-idl-format"

type idlReader struct {
	tokens []string
	pos    int
}

func lexIDL(data []byte, graphql bool) ([]string, error) {
	s := string(data)
	var out []string
	for i := 0; i < len(s); {
		if unicode.IsSpace(rune(s[i])) || graphql && s[i] == ',' {
			i++
			continue
		}
		if graphql && s[i] == '#' || strings.HasPrefix(s[i:], "//") {
			for i < len(s) && s[i] != '\n' {
				i++
			}
			continue
		}
		if strings.HasPrefix(s[i:], "/*") {
			j := strings.Index(s[i+2:], "*/")
			if j < 0 {
				return nil, fmt.Errorf("unterminated IDL comment")
			}
			i += j + 4
			continue
		}
		if s[i] == '"' {
			start := i
			i++
			for i < len(s) && s[i] != '"' {
				if s[i] == '\\' {
					i++
				}
				i++
			}
			if i >= len(s) {
				return nil, fmt.Errorf("unterminated IDL string")
			}
			i++
			out = append(out, s[start:i])
			continue
		}
		if unicode.IsLetter(rune(s[i])) || s[i] == '_' || s[i] >= '0' && s[i] <= '9' {
			start := i
			for i < len(s) && (unicode.IsLetter(rune(s[i])) || unicode.IsDigit(rune(s[i])) || s[i] == '_') {
				i++
			}
			out = append(out, s[start:i])
			continue
		}
		if strings.ContainsRune("{}[]=;:(),.!<>-", rune(s[i])) {
			out = append(out, s[i:i+1])
			i++
			continue
		}
		return nil, fmt.Errorf("unsupported IDL character %q", s[i])
	}
	return out, nil
}
func (r *idlReader) take() string {
	if r.pos >= len(r.tokens) {
		return ""
	}
	v := r.tokens[r.pos]
	r.pos++
	return v
}
func (r *idlReader) peek() string {
	if r.pos >= len(r.tokens) {
		return ""
	}
	return r.tokens[r.pos]
}
func (r *idlReader) want(s string) error {
	if got := r.take(); got != s {
		return fmt.Errorf("expected %q, got %q", s, got)
	}
	return nil
}
func idlName(s string) bool {
	if s == "" {
		return false
	}
	for i, c := range s {
		if !(unicode.IsLetter(c) || c == '_' || i > 0 && unicode.IsDigit(c)) {
			return false
		}
	}
	return true
}
func objectIDL() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}
func propsIDL(m map[string]any) map[string]any { return m["properties"].(map[string]any) }
func addIDL(m map[string]any, name string, v map[string]any) error {
	if !idlName(name) {
		return fmt.Errorf("invalid IDL name %q", name)
	}
	p := propsIDL(m)
	if _, ok := p[name]; ok {
		return fmt.Errorf("duplicate IDL declaration %s", name)
	}
	p[name] = v
	return nil
}
func (r *idlReader) qualified() (string, error) {
	v := r.take()
	if v == "." {
		v = r.take()
	}
	if !idlName(v) {
		return "", fmt.Errorf("type name required")
	}
	for r.peek() == "." {
		r.take()
		n := r.take()
		if !idlName(n) {
			return "", fmt.Errorf("invalid qualified type")
		}
		v += "." + n
	}
	return v, nil
}
func decodeProto(data []byte) (map[string]any, error) {
	tokens, e := lexIDL(data, false)
	if e != nil {
		return nil, e
	}
	r := idlReader{tokens: tokens}
	doc := objectIDL()
	doc[idlFormat] = idlKind("protobuf")
	count := 0
	for r.peek() != "" {
		k := r.take()
		switch k {
		case "syntax":
			if e = r.want("="); e != nil {
				return nil, e
			}
			syntax := r.take()
			doc["x-radar-proto-syntax"] = syntax
			if syntax != `"proto2"` && syntax != `"proto3"` {
				return nil, fmt.Errorf("unsupported protobuf syntax")
			}
			if e = r.want(";"); e != nil {
				return nil, e
			}
		case "package":
			pkg, pe := r.qualified()
			if pe != nil {
				return nil, pe
			}
			doc["x-radar-package"] = pkg
			if e = r.want(";"); e != nil {
				return nil, e
			}
		case "option":
			if _, e = r.qualified(); e != nil {
				return nil, e
			}
			if e = r.want("="); e != nil {
				return nil, e
			}
			if v := r.take(); v == "" || v == ";" {
				return nil, fmt.Errorf("option value required")
			}
			if e = r.want(";"); e != nil {
				return nil, e
			}
		case "message":
			name := r.take()
			m, e := r.protoMessage(0)
			if e != nil {
				return nil, e
			}
			if e = addIDL(doc, name, m); e != nil {
				return nil, e
			}
			count++
		case "enum":
			name := r.take()
			m, e := r.protoEnum()
			if e != nil {
				return nil, e
			}
			if e = addIDL(doc, name, m); e != nil {
				return nil, e
			}
			count++
		default:
			return nil, fmt.Errorf("unsupported protobuf declaration %s (imports, services, oneof, extensions and maps require an external checker)", k)
		}
	}
	if count == 0 {
		return nil, fmt.Errorf("protobuf contract has no message or enum declarations")
	}
	if err := validateIDLReferences(doc); err != nil {
		return nil, err
	}
	return doc, nil
}
func (r *idlReader) protoEnum() (map[string]any, error) {
	if e := r.want("{"); e != nil {
		return nil, e
	}
	m := map[string]any{"type": "string", "x-radar-idl-kind": "enum"}
	var values []any
	numbers := map[string]any{}
	seen := map[string]bool{}
	seenNumbers := map[int64]bool{}
	for r.peek() != "}" {
		name := r.take()
		if !idlName(name) || seen[name] {
			return nil, fmt.Errorf("invalid or duplicate enum name")
		}
		seen[name] = true
		if e := r.want("="); e != nil {
			return nil, e
		}
		n := r.take()
		if n == "-" {
			n = "-" + r.take()
		}
		number, e := decimalProtoInteger(n, 32)
		if e != nil {
			return nil, fmt.Errorf("enum number must be a supported signed int32 literal: %w", e)
		}
		if seenNumbers[number] {
			return nil, fmt.Errorf("duplicate protobuf enum number requires unsupported allow_alias option")
		}
		seenNumbers[number] = true
		n = strconv.FormatInt(number, 10)
		if e := r.want(";"); e != nil {
			return nil, e
		}
		values = append(values, name)
		numbers[name] = n
	}
	r.take()
	if len(values) == 0 {
		return nil, fmt.Errorf("empty protobuf enum")
	}
	m["enum"] = values
	m["x-radar-enum-numbers"] = numbers
	return m, nil
}
func (r *idlReader) protoMessage(depth int) (map[string]any, error) {
	if depth > maxDepth {
		return nil, fmt.Errorf("protobuf nesting exceeds limit")
	}
	if e := r.want("{"); e != nil {
		return nil, e
	}
	m := objectIDL()
	m["x-radar-idl-kind"] = "message"
	nums := map[string]bool{}
	var required, reservedNums, reservedNames, reservedRanges []any
	for r.peek() != "}" {
		if r.peek() == "" {
			return nil, fmt.Errorf("unterminated protobuf message")
		}
		k := r.take()
		if k == "message" || k == "enum" {
			name := r.take()
			var v map[string]any
			var e error
			if k == "message" {
				v, e = r.protoMessage(depth + 1)
			} else {
				v, e = r.protoEnum()
			}
			if e != nil {
				return nil, e
			}
			if e = addIDL(m, name, v); e != nil {
				return nil, e
			}
			continue
		}
		if k == "reserved" {
			for {
				v := r.take()
				if strings.HasPrefix(v, `"`) {
					name, e := strconv.Unquote(v)
					if e != nil {
						return nil, e
					}
					reservedNames = append(reservedNames, name)
				} else {
					number, e := decimalProtoInteger(v, 32)
					n := int(number)
					if e != nil || n <= 0 || n > 536870911 {
						return nil, fmt.Errorf("unsupported protobuf reserved expression")
					}
					if r.peek() == "to" {
						r.take()
						end := r.take()
						upper := 536870911
						if end != "max" {
							var err error
							value, parseErr := decimalProtoInteger(end, 32)
							upper, err = int(value), parseErr
							if err != nil {
								return nil, fmt.Errorf("invalid protobuf reserved range")
							}
						}
						if upper < n || upper > 536870911 {
							return nil, fmt.Errorf("invalid protobuf reserved range")
						}
						reservedRanges = append(reservedRanges, map[string]any{"start": n, "end": upper})
					} else {
						reservedNums = append(reservedNums, strconv.Itoa(n))
					}
				}
				if r.peek() != "," {
					break
				}
				r.take()
			}
			if e := r.want(";"); e != nil {
				return nil, e
			}
			continue
		}
		cardinality := "optional"
		if k == "optional" || k == "required" || k == "repeated" {
			cardinality = k
			k = r.take()
		}
		if !idlName(k) {
			return nil, fmt.Errorf("unsupported protobuf field %s", k)
		}
		for r.peek() == "." {
			r.take()
			part := r.take()
			if !idlName(part) {
				return nil, fmt.Errorf("invalid protobuf type")
			}
			k += "." + part
		}
		name := r.take()
		if e := r.want("="); e != nil {
			return nil, e
		}
		num := r.take()
		number, e := decimalProtoInteger(num, 32)
		n := int(number)
		if e != nil || n <= 0 || n > 536870911 || n >= 19000 && n <= 19999 || nums[strconv.Itoa(n)] {
			return nil, fmt.Errorf("invalid or duplicate protobuf field number %s", num)
		}
		num = strconv.Itoa(n)
		nums[num] = true
		if e = r.want(";"); e != nil {
			return nil, fmt.Errorf("unsupported protobuf field options: %w", e)
		}
		field := map[string]any{"type": "string", "x-radar-number": num, "x-radar-wire-type": k, "x-radar-cardinality": cardinality}
		if e = addIDL(m, name, field); e != nil {
			return nil, e
		}
		if cardinality == "required" {
			required = append(required, name)
		}
	}
	r.take()
	m["required"] = required
	m["x-radar-reserved-numbers"] = reservedNums
	m["x-radar-reserved-names"] = reservedNames
	m["x-radar-reserved-ranges"] = reservedRanges
	return m, nil
}
func decodeGraphQL(data []byte) (map[string]any, error) {
	tokens, e := lexIDL(data, true)
	if e != nil {
		return nil, e
	}
	r := idlReader{tokens: tokens}
	doc := objectIDL()
	doc[idlFormat] = idlKind("graphql")
	count := 0
	for r.peek() != "" {
		k := r.take()
		name := r.take()
		switch k {
		case "scalar":
			if e = addIDL(doc, name, map[string]any{"type": "string", "x-radar-idl-kind": "scalar"}); e != nil {
				return nil, e
			}
			count++
		case "enum":
			if e = r.want("{"); e != nil {
				return nil, e
			}
			var values []any
			seen := map[string]bool{}
			for r.peek() != "}" {
				v := r.take()
				if !idlName(v) || seen[v] {
					return nil, fmt.Errorf("invalid or duplicate GraphQL enum value")
				}
				seen[v] = true
				values = append(values, v)
			}
			r.take()
			if len(values) == 0 {
				return nil, fmt.Errorf("empty GraphQL enum")
			}
			if e = addIDL(doc, name, map[string]any{"type": "string", "enum": values, "x-radar-idl-kind": "enum"}); e != nil {
				return nil, e
			}
			count++
		case "type", "input":
			if e = r.want("{"); e != nil {
				return nil, e
			}
			m := objectIDL()
			m["x-radar-graphql-kind"] = k
			m["x-radar-idl-kind"] = k
			for r.peek() != "}" {
				field := r.take()
				f := map[string]any{"type": "string"}
				if r.peek() == "(" {
					if k == "input" {
						return nil, fmt.Errorf("GraphQL input fields cannot have arguments")
					}
					r.take()
					args := map[string]any{}
					for r.peek() != ")" {
						arg := r.take()
						if !idlName(arg) {
							return nil, fmt.Errorf("invalid GraphQL argument")
						}
						if _, ok := args[arg]; ok {
							return nil, fmt.Errorf("duplicate GraphQL argument")
						}
						if e = r.want(":"); e != nil {
							return nil, e
						}
						t, e := r.graphqlType(0)
						if e != nil {
							return nil, e
						}
						args[arg] = t
					}
					r.take()
					f["x-radar-arguments"] = args
				}
				if e = r.want(":"); e != nil {
					return nil, e
				}
				t, e := r.graphqlType(0)
				if e != nil {
					return nil, e
				}
				f["x-radar-graphql-type"] = t
				if e = addIDL(m, field, f); e != nil {
					return nil, e
				}
			}
			r.take()
			if len(propsIDL(m)) == 0 {
				return nil, fmt.Errorf("empty GraphQL type")
			}
			if e = addIDL(doc, name, m); e != nil {
				return nil, e
			}
			count++
		default:
			return nil, fmt.Errorf("unsupported GraphQL declaration %s (extensions, directives, defaults, interfaces and unions require an external checker)", k)
		}
	}
	if count == 0 {
		return nil, fmt.Errorf("GraphQL contract has no declarations")
	}
	if err := validateIDLReferences(doc); err != nil {
		return nil, err
	}
	return doc, nil
}
func (r *idlReader) graphqlType(depth int) (string, error) {
	if depth > maxDepth {
		return "", fmt.Errorf("GraphQL nesting exceeds limit")
	}
	var t string
	if r.peek() == "[" {
		r.take()
		v, e := r.graphqlType(depth + 1)
		if e != nil {
			return "", e
		}
		if e = r.want("]"); e != nil {
			return "", e
		}
		t = "[" + v + "]"
	} else {
		t = r.take()
		if !idlName(t) {
			return "", fmt.Errorf("GraphQL type name required")
		}
	}
	if r.peek() == "!" {
		r.take()
		t += "!"
	}
	return t, nil
}

func compareIDL(before, after map[string]any, pointer string) ([]Change, error) {
	if before[idlFormat] != after[idlFormat] {
		return nil, fmt.Errorf("contract IDL format changed")
	}
	a, e := Pointer(before, pointer)
	if e != nil {
		return nil, e
	}
	b, e := Pointer(after, pointer)
	if e != nil {
		return nil, e
	}
	var out []Change
	walkIDL(a, b, "", string(before[idlFormat].(idlKind)), &out)
	// Local named references carry nested fields and enum obligations into
	// the consumer's selected schema, rather than treating names as scalars.
	normalizedBefore, err := Analyzable(before, pointer)
	if err != nil {
		return nil, err
	}
	normalizedAfter, err := Analyzable(after, pointer)
	if err != nil {
		return nil, err
	}
	seenStructural := map[string]bool{}
	for _, c := range out {
		seenStructural[c.Kind+"\x00"+c.Field] = true
	}
	for _, c := range diff(normalizedBefore, normalizedAfter, "") {
		switch c.Kind {
		case "field_removed":
			c.Kind = "idl_field_removed"
		case "enum_value_removed":
			c.Kind = "idl_enum_value_removed"
		default:
			continue
		}
		key := c.Kind + "\x00" + c.Field
		if !seenStructural[key] {
			seenStructural[key] = true
			out = append(out, c)
		}
	}
	// Named fields keep their spelling when a referenced declaration changes
	// kind. Compare declarations outside pointer too, then propagate only to
	// fields within the selected consumer schema.
	if before[idlFormat] == idlKind("protobuf") {
		changedKinds := map[string]bool{}
		protoKindChanges(before, after, "", changedKinds)
		scope := strings.ReplaceAll(strings.TrimPrefix(pointer, "/properties/"), "/properties/", ".")
		propagateProtoKinds(after, b, changedKinds, "", scope, &out)
	} else if before[idlFormat] == idlKind("graphql") {
		changedKinds := map[string]bool{}
		protoKindChanges(before, after, "", changedKinds)
		propagateGraphQLKinds(b, changedKinds, "", &out)
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Field != b.Field {
			return a.Field < b.Field
		}
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		return a.Explanation < b.Explanation
	})
	return out, nil
}
func walkIDL(a, b map[string]any, field, format string, out *[]Change) {
	add := func(kind, f, why string) { *out = append(*out, Change{Kind: kind, Field: f, Explanation: why}) }
	if format == "protobuf" {
		if a["x-radar-idl-kind"] != b["x-radar-idl-kind"] {
			add("idl_definition_kind_changed", field, "Protobuf declaration kind changed for "+label(field))
		}
		if a["x-radar-package"] != b["x-radar-package"] {
			add("protobuf_package_changed", field, "Protobuf package changed")
		}
		an, _ := a["x-radar-enum-numbers"].(map[string]any)
		bn, _ := b["x-radar-enum-numbers"].(map[string]any)
		for n, v := range an {
			if nv, ok := bn[n]; ok && v != nv {
				add("protobuf_enum_number_changed", field, "Protobuf enum number changed for "+n)
			}
		}
	}
	if format == "graphql" && a["x-radar-idl-kind"] != b["x-radar-idl-kind"] {
		add("idl_definition_kind_changed", field, "GraphQL declaration kind changed")
	}
	ap, _ := a["properties"].(map[string]any)
	bp, _ := b["properties"].(map[string]any)
	names := make([]string, 0, len(ap))
	for n := range ap {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		av := ap[name].(map[string]any)
		f := child(field, name)
		raw, ok := bp[name]
		if !ok {
			add("idl_field_removed", f, "declared IDL field "+f+" was removed")
			continue
		}
		bv := raw.(map[string]any)
		if format == "protobuf" && av["x-radar-number"] != nil {
			number := av["x-radar-number"]
			if number != bv["x-radar-number"] {
				add("protobuf_number_changed", f, "protobuf field number changed for "+f)
			}
			if av["x-radar-wire-type"] != bv["x-radar-wire-type"] || av["x-radar-cardinality"] != bv["x-radar-cardinality"] {
				add("protobuf_type_changed", f, "protobuf wire type or cardinality changed for "+f)
			}
		}
		if format == "graphql" {
			at, _ := av["x-radar-graphql-type"].(string)
			bt, _ := bv["x-radar-graphql-type"].(string)
			if at != bt {
				kind := "graphql_type_changed"
				if strings.TrimSuffix(at, "!") == strings.TrimSuffix(bt, "!") {
					if a["x-radar-graphql-kind"] == "input" {
						if strings.HasSuffix(bt, "!") {
							kind = "graphql_input_tightened"
						} else {
							kind = "graphql_compatible"
						}
					} else {
						if strings.HasSuffix(at, "!") {
							kind = "graphql_output_weakened"
						} else {
							kind = "graphql_compatible"
						}
					}
				} else if strings.ReplaceAll(at, "!", "") == strings.ReplaceAll(bt, "!", "") {
					kind = "unanalyzed"
				}
				add(kind, f, "GraphQL field type changed for "+f)
			}
			aa, _ := av["x-radar-arguments"].(map[string]any)
			ba, _ := bv["x-radar-arguments"].(map[string]any)
			for arg, t := range aa {
				nt, ok := ba[arg]
				if !ok {
					add("graphql_argument_removed", f, "GraphQL argument "+arg+" was removed from "+f)
				} else if t != nt {
					kind := "graphql_argument_changed"
					if strings.TrimSuffix(t.(string), "!") == strings.TrimSuffix(nt.(string), "!") && !strings.HasSuffix(nt.(string), "!") {
						kind = "graphql_compatible"
					}
					add(kind, f, "GraphQL argument "+arg+" changed from "+t.(string)+" to "+nt.(string))
				}
			}
			for arg, t := range ba {
				if aa[arg] == nil && strings.HasSuffix(t.(string), "!") {
					add("graphql_argument_required", f, "GraphQL field "+f+" added required argument "+arg)
				}
			}
		}
		walkIDL(av, bv, f, format, out)
	}
	if format == "graphql" && b["x-radar-graphql-kind"] == "input" {
		for name, raw := range bp {
			v := raw.(map[string]any)
			typ, _ := v["x-radar-graphql-type"].(string)
			if ap[name] == nil && strings.HasSuffix(typ, "!") {
				add("graphql_input_tightened", child(field, name), "New required GraphQL input field "+child(field, name))
			}
		}
	}
	if format == "protobuf" {
		for name, raw := range bp {
			bv := raw.(map[string]any)
			number := bv["x-radar-number"]
			if number == nil {
				continue
			}
			f := child(field, name)
			for _, doc := range []map[string]any{a, b} {
				ranges, _ := doc["x-radar-reserved-ranges"].([]any)
				n, _ := strconv.Atoi(number.(string))
				for _, raw := range ranges {
					bounds := raw.(map[string]any)
					if n >= bounds["start"].(int) && n <= bounds["end"].(int) {
						add("protobuf_reserved_violation", f, "protobuf field "+f+" uses a reserved field number range")
					}
				}
			}
			for old, raw := range ap {
				av := raw.(map[string]any)
				if old != name && av["x-radar-number"] == number {
					add("protobuf_number_reused", child(field, old), "protobuf field number "+number.(string)+" reused by "+f)
				}
			}
			for _, key := range []string{"x-radar-reserved-numbers", "x-radar-reserved-names"} {
				for _, doc := range []map[string]any{a, b} {
					list, _ := doc[key].([]any)
					for _, reserved := range list {
						if key == "x-radar-reserved-numbers" && reserved == number || key == "x-radar-reserved-names" && reserved == name {
							add("protobuf_reserved_violation", f, "protobuf field "+f+" uses a reserved number or name")
						}
					}
				}
			}
			if bv["x-radar-cardinality"] == "required" && ap[name] == nil {
				add("protobuf_required_added", f, "new required protobuf field "+f)
			}
		}
	}
	ae, _ := a["enum"].([]any)
	be, _ := b["enum"].([]any)
	for _, v := range ae {
		found := false
		for _, w := range be {
			found = found || v == w
		}
		if !found {
			add("idl_enum_value_removed", field, "IDL enum "+label(field)+" removed value "+v.(string))
		}
	}
}

// All referenced IDL types must resolve inside this bounded single document.
// External imports/extensions cannot be substituted with unconstrained strings.
func validateIDLReferences(doc map[string]any) error {
	format := doc[idlFormat].(idlKind)
	declared := map[string]map[string]any{}
	var collect func(map[string]any, string)
	collect = func(m map[string]any, scope string) {
		p, _ := m["properties"].(map[string]any)
		for name, raw := range p {
			v := raw.(map[string]any)
			if v["x-radar-idl-kind"] != nil {
				key := child(scope, name)
				declared[key] = v
				collect(v, key)
			}
		}
	}
	collect(doc, "")
	protoScalars := set("double", "float", "int32", "int64", "uint32", "uint64", "sint32", "sint64", "fixed32", "fixed64", "sfixed32", "sfixed64", "bool", "string", "bytes")
	graphqlScalars := set("String", "Int", "Float", "Boolean", "ID")
	resolveProto := func(typ, scope string) bool {
		if protoScalars[typ] || declared[typ] != nil {
			return true
		}
		pkg, _ := doc["x-radar-package"].(string)
		if pkg != "" && strings.HasPrefix(typ, pkg+".") && declared[strings.TrimPrefix(typ, pkg+".")] != nil {
			return true
		}
		for scope != "" {
			if declared[child(scope, typ)] != nil {
				return true
			}
			i := strings.LastIndex(scope, ".")
			if i < 0 {
				break
			}
			scope = scope[:i]
		}
		return false
	}
	resolveGraphQL := func(typ string, input bool) bool {
		name := strings.Trim(typ, "[]!")
		if graphqlScalars[name] {
			return true
		}
		v := declared[name]
		if v == nil {
			return false
		}
		kind, _ := v["x-radar-idl-kind"].(string)
		return kind == "scalar" || kind == "enum" || input && kind == "input" || !input && kind == "type"
	}
	for scope, d := range declared {
		if format == "protobuf" && doc["x-radar-proto-syntax"] == `"proto3"` && d["x-radar-idl-kind"] == "enum" {
			values := d["enum"].([]any)
			numbers := d["x-radar-enum-numbers"].(map[string]any)
			if numbers[values[0].(string)] != "0" {
				return fmt.Errorf("proto3 enum %s must start with zero", scope)
			}
		}
		fields, _ := d["properties"].(map[string]any)
		for name, raw := range fields {
			v := raw.(map[string]any)
			if format == "protobuf" {
				typ, _ := v["x-radar-wire-type"].(string)
				if typ == "" {
					continue
				}
				if doc["x-radar-proto-syntax"] == `"proto3"` && v["x-radar-cardinality"] == "required" {
					return fmt.Errorf("proto3 field %s cannot be required", child(scope, name))
				}
				if !resolveProto(typ, scope) {
					return fmt.Errorf("protobuf field %s references unresolved type %s", child(scope, name), typ)
				}
			} else {
				typ, _ := v["x-radar-graphql-type"].(string)
				if !resolveGraphQL(typ, d["x-radar-idl-kind"] == "input") {
					return fmt.Errorf("GraphQL field %s references unresolved or invalid type %s", child(scope, name), typ)
				}
				args, _ := v["x-radar-arguments"].(map[string]any)
				for arg, raw := range args {
					typ := raw.(string)
					if !resolveGraphQL(typ, true) {
						return fmt.Errorf("GraphQL argument %s.%s references unresolved or invalid input type %s", child(scope, name), arg, typ)
					}
				}
			}
		}
	}
	return nil
}

func protoKindChanges(a, b map[string]any, scope string, changed map[string]bool) {
	ap, _ := a["properties"].(map[string]any)
	bp, _ := b["properties"].(map[string]any)
	for name, raw := range ap {
		av := raw.(map[string]any)
		br, ok := bp[name]
		if !ok {
			continue
		}
		bv := br.(map[string]any)
		field := child(scope, name)
		if av["x-radar-idl-kind"] != bv["x-radar-idl-kind"] {
			changed[field] = true
		}
		protoKindChanges(av, bv, field, changed)
	}
}
func protoDeclaration(doc map[string]any, name string) bool {
	cur := doc
	for _, part := range strings.Split(name, ".") {
		p, _ := cur["properties"].(map[string]any)
		v, ok := p[part].(map[string]any)
		if !ok {
			return false
		}
		cur = v
	}
	return cur["x-radar-idl-kind"] != nil
}
func propagateProtoKinds(doc, m map[string]any, changed map[string]bool, fieldScope, typeScope string, out *[]Change) {
	p, _ := m["properties"].(map[string]any)
	for name, raw := range p {
		v := raw.(map[string]any)
		field := child(fieldScope, name)
		typ, _ := v["x-radar-wire-type"].(string)
		pkg, _ := doc["x-radar-package"].(string)
		target := strings.TrimPrefix(typ, pkg+".")
		resolved := ""
		for ancestor := typeScope; ancestor != ""; {
			candidate := child(ancestor, target)
			if protoDeclaration(doc, candidate) {
				resolved = candidate
				break
			}
			i := strings.LastIndex(ancestor, ".")
			if i < 0 {
				break
			}
			ancestor = ancestor[:i]
		}
		if resolved == "" && protoDeclaration(doc, target) {
			resolved = target
		}
		if typ != "" && changed[resolved] {
			*out = append(*out, Change{Kind: "protobuf_type_changed", Field: field, Explanation: "Protobuf field " + field + " references " + resolved + ", whose declaration kind changed"})
		}
		propagateProtoKinds(doc, v, changed, field, child(typeScope, name), out)
	}
}

func propagateGraphQLKinds(m map[string]any, changed map[string]bool, scope string, out *[]Change) {
	p, _ := m["properties"].(map[string]any)
	for name, raw := range p {
		v := raw.(map[string]any)
		field := child(scope, name)
		typ, _ := v["x-radar-graphql-type"].(string)
		if changed[strings.Trim(typ, "[]!")] {
			*out = append(*out, Change{Kind: "graphql_type_changed", Field: field, Explanation: "GraphQL field " + field + " references a declaration whose kind changed"})
		}
		args, _ := v["x-radar-arguments"].(map[string]any)
		for arg, raw := range args {
			if changed[strings.Trim(raw.(string), "[]!")] {
				*out = append(*out, Change{Kind: "graphql_argument_changed", Field: field, Explanation: "GraphQL argument " + field + "." + arg + " references a declaration whose kind changed"})
			}
		}
		propagateGraphQLKinds(v, changed, field, out)
	}
}

// Numeric literals outside the supported decimal subset are unknown rather
// than being interpreted using decimal rules for protobuf's octal/hex syntax.
func decimalProtoInteger(literal string, bits int) (int64, error) {
	digits := strings.TrimPrefix(literal, "-")
	if len(digits) > 1 && digits[0] == '0' {
		return 0, fmt.Errorf("noncanonical decimal protobuf integer is unsupported")
	}
	return strconv.ParseInt(literal, 10, bits)
}
