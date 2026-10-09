// Package languages provides syntax-only adapters backed by upstream Tree-sitter grammars.
package languages

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/radar-engine/radar/internal/model"
	sitter "github.com/tree-sitter/go-tree-sitter"
	goGrammar "github.com/tree-sitter/tree-sitter-go/bindings/go"
	jsGrammar "github.com/tree-sitter/tree-sitter-javascript/bindings/go"
	pyGrammar "github.com/tree-sitter/tree-sitter-python/bindings/go"
	rustGrammar "github.com/tree-sitter/tree-sitter-rust/bindings/go"
	tsGrammar "github.com/tree-sitter/tree-sitter-typescript/bindings/go"
)

type Source struct {
	Repository, Revision, Path string
	Content                    []byte
}
type Result struct {
	Nodes       []model.Node
	Edges       []model.Edge
	Diagnostics []model.Diagnostic
	// Imports keeps raw specifiers for later repository-level path resolution.
	Imports []Import
}

// Import is one import specifier as written in a source file. Names lists
// imported members, which may themselves be submodules (Python `from . import x`).
type Import struct {
	Language string
	Module   string
	Names    []string
	Line     int
}
type Capability struct {
	Language   string `json:"language"`
	Structural bool   `json:"structural"`
	Semantic   bool   `json:"semantic"`
	Method     string `json:"method"`
}
type Adapter interface {
	Capability() Capability
	Parse(context.Context, Source) (Result, error)
}
type syntaxAdapter struct {
	language string
	grammar  *sitter.Language
}

func ForPath(path string) (Adapter, bool) {
	var a syntaxAdapter
	switch strings.ToLower(filepath.Ext(path)) {
	case ".ts", ".mts", ".cts":
		a = syntaxAdapter{"typescript", sitter.NewLanguage(tsGrammar.LanguageTypescript())}
	case ".tsx":
		a = syntaxAdapter{"typescript", sitter.NewLanguage(tsGrammar.LanguageTSX())}
	case ".js", ".jsx", ".mjs", ".cjs":
		a = syntaxAdapter{"javascript", sitter.NewLanguage(jsGrammar.Language())}
	case ".py":
		a = syntaxAdapter{"python", sitter.NewLanguage(pyGrammar.Language())}
	case ".go":
		a = syntaxAdapter{"go", sitter.NewLanguage(goGrammar.Language())}
	case ".rs":
		a = syntaxAdapter{"rust", sitter.NewLanguage(rustGrammar.Language())}
	default:
		return nil, false
	}
	return a, true
}
func Capabilities() []Capability {
	var out []Capability
	for _, p := range []string{"x.ts", "x.js", "x.py", "x.go", "x.rs"} {
		a, _ := ForPath(p)
		out = append(out, a.Capability())
	}
	return out
}
func (a syntaxAdapter) Capability() Capability {
	return Capability{a.language, true, false, "tree-sitter structural syntax"}
}

func (a syntaxAdapter) Parse(ctx context.Context, s Source) (Result, error) {
	var result Result
	if err := ctx.Err(); err != nil {
		return result, err
	}
	// This bound also applies to callers that bypass repository indexing.
	if len(s.Content) > 2*1024*1024 {
		return result, fmt.Errorf("source exceeds 2 MiB structural analysis limit")
	}
	parseCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	parser := sitter.NewParser()
	defer parser.Close()
	if err := parser.SetLanguage(a.grammar); err != nil {
		return result, err
	}
	// ParseCtx starts an unjoined cancellation goroutine in upstream v0.25.0;
	// cancellation can race Close. Use bounded input callbacks without a goroutine.
	parser.SetTimeoutMicros(5_000_000)
	tree := parser.ParseWithOptions(func(offset int, _ sitter.Point) []byte {
		if parseCtx.Err() != nil || offset >= len(s.Content) {
			return nil
		}
		end := offset + 4096
		if end > len(s.Content) {
			end = len(s.Content)
		}
		return s.Content[offset:end]
	}, nil, nil)
	if tree == nil {
		if err := parseCtx.Err(); err != nil {
			return result, err
		}
		return result, fmt.Errorf("parser did not produce a syntax tree")
	}
	defer tree.Close()
	if err := parseCtx.Err(); err != nil {
		return Result{}, err
	}
	root := tree.RootNode()
	fileID := model.FileID(s.Path)
	provenance := func(n *sitter.Node) model.Provenance {
		return model.Provenance{Repository: s.Repository, Revision: s.Revision, Path: s.Path, Line: int(n.StartPosition().Row) + 1, EndLine: int(n.EndPosition().Row) + 1, Method: "tree-sitter:" + a.language, Evidence: model.VerifiedStatic}
	}
	result.Nodes = append(result.Nodes, model.Node{ID: fileID, Kind: "file", Name: s.Path, Language: a.language, Provenance: provenance(root)})
	if root.HasError() {
		result.Diagnostics = append(result.Diagnostics, model.Diagnostic{Path: s.Path, Severity: "warning", Message: "syntax errors: malformed declarations are excluded; remaining entities are structural only"})
	}
	type frame struct {
		node         *sitter.Node
		owner, scope string
	}
	stack := []frame{{root, fileID, ""}}
	duplicates := map[string]int{}
	imports := map[string]bool{}
	for len(stack) > 0 {
		if err := parseCtx.Err(); err != nil {
			return Result{}, err
		}
		f := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		n := f.node
		if n.IsError() || n.IsMissing() {
			continue
		}
		owner, scope := f.owner, f.scope
		kind := definitionKind(n)
		nameNode := n.ChildByFieldName("name")
		if n.Kind() == "package_clause" && n.NamedChildCount() > 0 {
			nameNode = n.NamedChild(0)
		}
		if n.Kind() == "method_declaration" {
			if receiver := n.ChildByFieldName("receiver"); receiver != nil && receiver.NamedChildCount() > 0 {
				if typ := receiver.NamedChild(0).ChildByFieldName("type"); typ != nil {
					scope = qualify(scope, strings.TrimLeft(typ.Utf8Text(s.Content), "*"))
				}
			}
		}
		if kind != "" && nameNode != nil && !n.HasError() {
			name := nameNode.Utf8Text(s.Content)
			qualified := qualify(scope, name)
			key := kind + "\x00" + qualified
			occurrence := duplicates[key]
			duplicates[key]++
			id := model.EntityID(kind, s.Path, qualified, occurrence)
			props := map[string]string{"qualified_name": qualified, "syntax_kind": n.Kind()}
			result.Nodes = append(result.Nodes, model.Node{ID: id, Kind: kind, Name: name, Language: a.language, Properties: props, Provenance: provenance(n)})
			result.Edges = append(result.Edges, model.Edge{ID: model.StableID(owner, "DEFINES", id), From: owner, To: id, Kind: "DEFINES", Provenance: provenance(n)})
			owner, scope = id, qualified
		}
		// impl blocks have no declaration name, but their type supplies the method scope.
		if n.Kind() == "impl_item" {
			if typ := n.ChildByFieldName("type"); typ != nil {
				scope = qualify(scope, typ.Utf8Text(s.Content))
			}
		}
		imported := importNames(n, s.Content, a.language)
		if len(imported) > 0 {
			names := importedMembers(n, s.Content, a.language)
			for _, module := range imported {
				if module != "" {
					result.Imports = append(result.Imports, Import{Language: a.language, Module: module, Names: names, Line: int(n.StartPosition().Row) + 1})
				}
			}
		}
		// A Rust `mod name;` declaration loads a sibling module file.
		if n.Kind() == "mod_item" && n.ChildByFieldName("body") == nil && nameNode != nil {
			result.Imports = append(result.Imports, Import{Language: a.language, Module: "self::" + nameNode.Utf8Text(s.Content), Line: int(n.StartPosition().Row) + 1})
		}
		for _, module := range imported {
			if module == "" {
				continue
			}
			id := model.ModuleID(a.language, module)
			if !imports[id] {
				imports[id] = true
				result.Nodes = append(result.Nodes, model.Node{ID: id, Kind: "module", Name: module, Language: a.language, Properties: map[string]string{"resolution": "unresolved", "origin": "import syntax"}, Provenance: provenance(n)})
			}
			result.Edges = append(result.Edges, model.Edge{ID: model.StableID(fileID, "IMPORTS", id, fmt.Sprint(n.StartByte())), From: fileID, To: id, Kind: "IMPORTS", Provenance: provenance(n)})
		}
		for i := int(n.NamedChildCount()) - 1; i >= 0; i-- {
			stack = append(stack, frame{n.NamedChild(uint(i)), owner, scope})
		}
	}
	return result, nil
}

// qualify joins declaration scopes with dots, e.g. Server.Handle.
func qualify(scope, name string) string {
	if scope == "" {
		return name
	}
	return scope + "." + name
}

// importedMembers returns the member names of a Python `from X import a, b`.
func importedMembers(n *sitter.Node, source []byte, language string) []string {
	if language != "python" || n.Kind() != "import_from_statement" {
		return nil
	}
	var names []string
	module := n.ChildByFieldName("module_name")
	for i := uint(0); i < n.NamedChildCount(); i++ {
		c := n.NamedChild(i)
		if module != nil && c.StartByte() == module.StartByte() {
			continue
		}
		switch c.Kind() {
		case "dotted_name":
			names = append(names, c.Utf8Text(source))
		case "aliased_import":
			if name := c.ChildByFieldName("name"); name != nil {
				names = append(names, name.Utf8Text(source))
			}
		}
	}
	return names
}

func definitionKind(n *sitter.Node) string {
	switch n.Kind() {
	case "package_clause":
		return "package"
	case "function_declaration", "function_definition", "function_item", "method_definition", "method_declaration", "function_signature":
		return "function"
	case "class_declaration", "class_definition", "abstract_class_declaration":
		return "class"
	case "interface_declaration", "trait_item":
		return "interface"
	case "type_alias_declaration", "type_spec", "struct_item", "enum_item", "type_item", "enum_declaration":
		return "type"
	case "mod_item":
		return "module"
	case "variable_declarator":
		if v := n.ChildByFieldName("value"); v != nil && (v.Kind() == "arrow_function" || v.Kind() == "function_expression") {
			return "function"
		}
		return "symbol"
	case "const_item", "static_item":
		return "symbol"
	}
	return ""
}

func importNames(n *sitter.Node, source []byte, language string) []string {
	if n.HasError() {
		return nil
	}
	unquote := func(s string) string { return strings.Trim(s, "\"'`") }
	switch n.Kind() {
	case "import_statement":
		if language == "typescript" || language == "javascript" {
			if s := n.ChildByFieldName("source"); s != nil {
				return []string{unquote(s.Utf8Text(source))}
			}
		}
		if language == "python" {
			var names []string
			for i := uint(0); i < n.NamedChildCount(); i++ {
				c := n.NamedChild(i)
				if c.Kind() == "dotted_name" {
					names = append(names, c.Utf8Text(source))
				}
				if c.Kind() == "aliased_import" {
					if name := c.ChildByFieldName("name"); name != nil {
						names = append(names, name.Utf8Text(source))
					}
				}
			}
			return names
		}
	case "export_statement":
		if s := n.ChildByFieldName("source"); s != nil {
			return []string{unquote(s.Utf8Text(source))}
		}
	case "import_from_statement":
		if s := n.ChildByFieldName("module_name"); s != nil {
			return []string{s.Utf8Text(source)}
		}
	case "import_spec":
		if s := n.ChildByFieldName("path"); s != nil {
			return []string{unquote(s.Utf8Text(source))}
		}
	case "use_declaration":
		if s := n.ChildByFieldName("argument"); s != nil {
			return []string{s.Utf8Text(source)}
		}
	}
	return nil
}
