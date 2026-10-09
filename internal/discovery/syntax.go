package discovery

import (
	"context"
	"fmt"
	"path"
	"regexp"
	"time"

	"github.com/Jake-Network/radar/internal/model"
	sitter "github.com/tree-sitter/go-tree-sitter"
	pyGrammar "github.com/tree-sitter/tree-sitter-python/bindings/go"
	tsGrammar "github.com/tree-sitter/tree-sitter-typescript/bindings/go"
)

// memberAccesses uses syntax nodes so comments, strings and unrelated variables
// do not establish field use. Same-name lexical scopes remain an explicit limit.
func memberAccesses(ctx context.Context, file, revision, source, variable string, variableOffset int, endpoint, typ, module string) ([]string, []model.Provenance, error) {
	parser := sitter.NewParser()
	defer parser.Close()
	grammar := sitter.NewLanguage(tsGrammar.LanguageTypescript())
	if len(file) > 4 && file[len(file)-4:] == ".tsx" {
		grammar = sitter.NewLanguage(tsGrammar.LanguageTSX())
	}
	if e := parser.SetLanguage(grammar); e != nil {
		return nil, nil, e
	}
	parser.SetTimeoutMicros(5_000_000)
	parseCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	b := []byte(source)
	tree := parser.ParseWithOptions(func(offset int, _ sitter.Point) []byte {
		if parseCtx.Err() != nil || offset >= len(b) {
			return nil
		}
		end := offset + 4096
		if end > len(b) {
			end = len(b)
		}
		return b[offset:end]
	}, nil, nil)
	if tree == nil {
		return nil, nil, fmt.Errorf("consumer syntax parse failed")
	}
	defer tree.Close()
	fields := []string{}
	locations := []model.Provenance{}
	seen := map[string]bool{}
	validInitializer, validImport := false, false
	var walk func(*sitter.Node)
	walk = func(n *sitter.Node) {
		if n.Kind() == "import_statement" {
			text := n.Utf8Text(b)
			match := importRE.FindStringSubmatchIndex(text)
			if match != nil && match[0] == 0 && text[match[2]:match[3]] == typ && path.Clean(path.Join(path.Dir(file), text[match[4]:match[5]])) == module {
				validImport = true
			}
		}
		if n.Kind() == "variable_declarator" && int(n.StartByte()) == variableOffset {
			validInitializer = literalFetchJSON(n.ChildByFieldName("value"), b, endpoint)
		}
		if n.Kind() == "member_expression" {
			obj, prop := n.ChildByFieldName("object"), n.ChildByFieldName("property")
			if obj != nil && prop != nil && obj.Kind() == "identifier" && obj.Utf8Text(b) == variable && prop.Kind() == "property_identifier" {
				name := prop.Utf8Text(b)
				if !seen[name] {
					fields = append(fields, name)
					seen[name] = true
					locations = append(locations, provenance(revision, file, "tree-sitter-member-access", int(n.StartPosition().Row)+1))
				}
			}
		}
		for i := uint(0); i < n.NamedChildCount(); i++ {
			walk(n.NamedChild(i))
		}
	}
	walk(tree.RootNode())
	if !validInitializer || !validImport {
		return nil, nil, parseCtx.Err()
	}
	return fields, locations, parseCtx.Err()
}

type pythonFacts struct {
	fields     map[string][]string
	decorators map[int]bool
	apps       map[string]bool
	baseModel  bool
}

func pythonStructure(ctx context.Context, source string) (pythonFacts, error) {
	facts := pythonFacts{fields: map[string][]string{}, decorators: map[int]bool{}, apps: map[string]bool{}}
	parser := sitter.NewParser()
	defer parser.Close()
	if e := parser.SetLanguage(sitter.NewLanguage(pyGrammar.Language())); e != nil {
		return facts, e
	}
	parser.SetTimeoutMicros(5_000_000)
	b := []byte(source)
	tree := parser.Parse(b, nil)
	if tree == nil {
		return facts, fmt.Errorf("Python parse failed")
	}
	defer tree.Close()
	if e := ctx.Err(); e != nil {
		return facts, e
	}
	root := tree.RootNode()
	fastAPI := false
	for i := uint(0); i < root.NamedChildCount(); i++ {
		n := root.NamedChild(i)
		if n.Kind() == "import_from_statement" {
			text := n.Utf8Text(b)
			if regexp.MustCompile(`^from pydantic import (?:[^\n]*\b)?BaseModel\b`).MatchString(text) {
				facts.baseModel = true
			}
			if regexp.MustCompile(`^from fastapi import (?:[^\n]*\b)?FastAPI\b`).MatchString(text) {
				fastAPI = true
			}
		}
	}
	var walk func(*sitter.Node)
	walk = func(n *sitter.Node) {
		switch n.Kind() {
		case "assignment":
			left, right := n.ChildByFieldName("left"), n.ChildByFieldName("right")
			if fastAPI && left != nil && right != nil && left.Kind() == "identifier" && right.Kind() == "call" {
				fn := right.ChildByFieldName("function")
				if fn != nil && fn.Utf8Text(b) == "FastAPI" {
					facts.apps[left.Utf8Text(b)] = true
				}
			}
		case "decorator":
			facts.decorators[int(n.StartPosition().Row)+1] = true
		case "class_definition":
			name, body := n.ChildByFieldName("name"), n.ChildByFieldName("body")
			if name != nil && body != nil {
				fields := []string{}
				for i := uint(0); i < body.NamedChildCount(); i++ {
					child := body.NamedChild(i)
					if child.Kind() != "expression_statement" {
						continue
					}
					for j := uint(0); j < child.NamedChildCount(); j++ {
						assignment := child.NamedChild(j)
						if assignment.Kind() != "assignment" || assignment.ChildByFieldName("type") == nil {
							continue
						}
						left := assignment.ChildByFieldName("left")
						if left != nil && left.Kind() == "identifier" {
							fields = append(fields, left.Utf8Text(b))
						}
					}
				}
				facts.fields[name.Utf8Text(b)] = fields
			}
		}
		for i := uint(0); i < n.NamedChildCount(); i++ {
			walk(n.NamedChild(i))
		}
	}
	walk(root)
	return facts, nil
}

// literalFetchJSON accepts only awaited JSON from an awaited direct literal
// fetch call. A string containing that text or another call is not evidence.
func literalFetchJSON(value *sitter.Node, source []byte, endpoint string) bool {
	unwrap := func(n *sitter.Node) (*sitter.Node, bool) {
		awaited := false
		for n != nil && (n.Kind() == "await_expression" || n.Kind() == "parenthesized_expression") {
			if n.Kind() == "await_expression" {
				awaited = true
			}
			if n.NamedChildCount() != 1 {
				return nil, false
			}
			n = n.NamedChild(0)
		}
		return n, awaited
	}
	jsonCall, awaited := unwrap(value)
	if jsonCall == nil || !awaited || jsonCall.Kind() != "call_expression" {
		return false
	}
	fn, args := jsonCall.ChildByFieldName("function"), jsonCall.ChildByFieldName("arguments")
	if fn == nil || fn.Kind() != "member_expression" || args == nil || args.NamedChildCount() != 0 {
		return false
	}
	prop := fn.ChildByFieldName("property")
	if prop == nil || prop.Utf8Text(source) != "json" {
		return false
	}
	fetch, awaited := unwrap(fn.ChildByFieldName("object"))
	if fetch == nil || !awaited || fetch.Kind() != "call_expression" {
		return false
	}
	callee, args := fetch.ChildByFieldName("function"), fetch.ChildByFieldName("arguments")
	if callee == nil || callee.Kind() != "identifier" || callee.Utf8Text(source) != "fetch" || args == nil || args.NamedChildCount() != 1 {
		return false
	}
	literal := args.NamedChild(0)
	if literal.Kind() != "string" {
		return false
	}
	raw := literal.Utf8Text(source)
	return len(raw) >= 2 && raw[1:len(raw)-1] == endpoint
}
