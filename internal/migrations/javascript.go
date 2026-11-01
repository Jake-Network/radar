package migrations

import (
	"context"
	"fmt"
	"path"
	"regexp"
	"strings"
	"time"

	sitter "github.com/tree-sitter/go-tree-sitter"
	grammar "github.com/tree-sitter/tree-sitter-typescript/bindings/go"
)

var migrationClassTimestamp = regexp.MustCompile(`[0-9]{13}$`)

// Javascript migration files are data, never imported. Explicit locations
// declare the loaded migration scope; computed registration remains outside
// this detector's supported contract.
func (h *history) javascriptIssues(ctx context.Context, l location, files []File, read Reader) error {
	identities := map[string][]File{}
	timestamps := map[string][]File{}
	for _, f := range files {
		if path.Dir(f.Path) != l.dir {
			h.unknown = append(h.unknown, f.Path+": nested JavaScript migration loading is not declared")
			continue
		}
		name := path.Base(f.Path)
		if strings.HasSuffix(name, ".d.ts") {
			continue
		}
		if l.tool == toolKnex {
			identities[name] = append(identities[name], f)
			if prefix, _, ok := strings.Cut(name, "_"); ok && numericName.MatchString(prefix) {
				timestamps[prefix] = append(timestamps[prefix], f)
			}
			continue
		}
		content, e := read(f.OID)
		if e != nil {
			h.unknown = append(h.unknown, f.Path+": migration source cannot be read")
			continue
		}
		parser := sitter.NewParser()
		if e := parser.SetLanguage(sitter.NewLanguage(grammar.LanguageTypescript())); e != nil {
			parser.Close()
			return e
		}
		parser.SetTimeoutMicros(5_000_000)
		parseCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		tree := parser.ParseWithOptions(func(offset int, _ sitter.Point) []byte {
			if parseCtx.Err() != nil || offset >= len(content) {
				return nil
			}
			return content[offset:min(offset+4096, len(content))]
		}, nil, nil)
		parser.Close()
		cancel()
		if e := ctx.Err(); e != nil {
			if tree != nil {
				tree.Close()
			}
			return e
		}
		if tree == nil {
			h.unknown = append(h.unknown, f.Path+": migration syntax analysis did not finish")
			continue
		}
		root := tree.RootNode()
		if root.HasError() {
			h.unknown = append(h.unknown, f.Path+": malformed migration syntax")
			tree.Close()
			continue
		}
		// TypeORM's directory loader enumerates module exports, not every
		// class in a file. Only direct literal class exports are supported;
		// aliases, re-exports and computed export values need resolution.
		var classes []*sitter.Node
		if typeORMCommonJSExports(root, content) {
			h.unknown = append(h.unknown, f.Path+": CommonJS migration exports are not resolved")
		}
		for i := uint(0); i < root.NamedChildCount(); i++ {
			n := root.NamedChild(i)
			if n.Kind() != "export_statement" {
				continue
			}
			declaration := n.ChildByFieldName("declaration")
			if declaration != nil && declaration.Kind() == "class_declaration" {
				classes = append(classes, declaration)
			} else if typeORMPrimitiveExport(n, content) {
				// The loader ignores primitive values; only functions,
				// arrays and objects can contribute migration classes.
				continue
			} else if declaration == nil || declaration.Kind() != "interface_declaration" && declaration.Kind() != "type_alias_declaration" {
				h.unknown = append(h.unknown, f.Path+": migration export is not a direct literal class declaration")
			}
		}
		found := false
		for _, node := range classes {
			if node.Kind() == "class_declaration" {
				body := node.ChildByFieldName("body")
				className := node.ChildByFieldName("name")
				if body == nil || className == nil {
					h.unknown = append(h.unknown, f.Path+": anonymous migration class")
					continue
				}
				identity := className.Utf8Text(content)
				up, down := false, false
				why := ""
				for i := uint(0); i < body.NamedChildCount(); i++ {
					member := body.NamedChild(i)
					n := member.ChildByFieldName("name")
					if n == nil {
						continue
					}
					key := n.Utf8Text(content)
					if member.Kind() == "method_definition" {
						up = up || key == "up"
						down = down || key == "down"
						if key == "constructor" {
							why = "constructor-defined migration identity is unsupported"
						}
					}
					if key == "name" {
						value := member.ChildByFieldName("value")
						if value == nil || value.Kind() != "string" {
							why = "computed migration name"
						} else {
							text := value.Utf8Text(content)
							if len(text) < 2 || strings.ContainsAny(text[1:len(text)-1], "\\\r\n") {
								why = "escaped migration name is unsupported"
							} else {
								identity = text[1 : len(text)-1]
							}
						}
					}
				}
				if !up || !down {
					continue
				}
				found = true
				if why != "" || !migrationClassTimestamp.MatchString(identity) {
					h.unknown = append(h.unknown, f.Path+": unsupported migration identity "+why)
					continue
				}
				identities[identity] = append(identities[identity], f)
				timestamps[migrationClassTimestamp.FindString(identity)] = append(timestamps[migrationClassTimestamp.FindString(identity)], f)
				continue
			}
		}
		if !found {
			h.unknown = append(h.unknown, f.Path+": no literal migration class with up and down methods")
		}
		tree.Close()
	}
	for _, identity := range sortedKeys(keysOf(identities)) {
		if len(identities[identity]) > 1 {
			h.metadataProblem(identity, "Loaded migrations repeat identity "+identity, identities[identity]...)
		}
	}
	for _, timestamp := range sortedKeys(keysOf(timestamps)) {
		if len(timestamps[timestamp]) > 1 {
			h.metadataProblem("timestamp:"+timestamp, fmt.Sprintf("Multiple migrations share timestamp %s; execution order needs review", timestamp), timestamps[timestamp]...)
			h.issues[len(h.issues)-1].Kind = kindRisk
		}
	}
	return nil
}

// Immutable primitive exports cannot contain classes. Mutable bindings and
// object/array values need export resolution even when initialized literally.
func typeORMPrimitiveExport(n *sitter.Node, content []byte) bool {
	if value := n.ChildByFieldName("value"); value != nil {
		return typeORMPrimitive(value, content, 0)
	}
	declaration := n.ChildByFieldName("declaration")
	if declaration == nil || declaration.Kind() != "lexical_declaration" {
		return false
	}
	kind := declaration.ChildByFieldName("kind")
	if kind == nil || kind.Utf8Text(content) != "const" {
		return false
	}
	found := false
	for i := uint(0); i < declaration.NamedChildCount(); i++ {
		binding := declaration.NamedChild(i)
		if binding.Kind() == "comment" {
			continue
		}
		name, value := binding.ChildByFieldName("name"), binding.ChildByFieldName("value")
		if binding.Kind() != "variable_declarator" || name == nil || name.Kind() != "identifier" || !typeORMPrimitive(value, content, 0) {
			return false
		}
		found = true
	}
	return found
}

func typeORMPrimitive(n *sitter.Node, content []byte, depth int) bool {
	if n == nil || depth > 8 {
		return false
	}
	switch n.Kind() {
	case "string", "number", "true", "false", "null":
		return true
	case "parenthesized_expression":
		return n.NamedChildCount() == 1 && typeORMPrimitive(n.NamedChild(0), content, depth+1)
	case "unary_expression":
		argument, operator := n.ChildByFieldName("argument"), n.ChildByFieldName("operator")
		return argument != nil && argument.Kind() == "number" && operator != nil && (operator.Utf8Text(content) == "-" || operator.Utf8Text(content) == "+")
	}
	return false
}

// A CommonJS export reference can replace or mutate the exported values even
// alongside direct ESM class exports. Do not infer the resulting loaded set.
func typeORMCommonJSExports(root *sitter.Node, content []byte) bool {
	stack := []*sitter.Node{root}
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if n.Kind() == "identifier" && n.Utf8Text(content) == "exports" {
			return true
		}
		if n.Kind() == "member_expression" || n.Kind() == "subscript_expression" {
			object, property := n.ChildByFieldName("object"), n.ChildByFieldName("property")
			if object != nil && object.Kind() == "identifier" && object.Utf8Text(content) == "module" && (n.Kind() == "subscript_expression" || property != nil && property.Utf8Text(content) == "exports") {
				return true
			}
		}
		for i := uint(0); i < n.NamedChildCount(); i++ {
			stack = append(stack, n.NamedChild(i))
		}
	}
	return false
}

var numericName = regexp.MustCompile(`^[0-9]+$`)
