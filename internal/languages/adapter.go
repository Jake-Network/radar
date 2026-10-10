// Package languages provides syntax-only adapters backed by upstream Tree-sitter grammars.
package languages

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/Jake-Network/radar/internal/model"
	sitter "github.com/tree-sitter/go-tree-sitter"
	cGrammar "github.com/tree-sitter/tree-sitter-c/bindings/go"
	cppGrammar "github.com/tree-sitter/tree-sitter-cpp/bindings/go"
	goGrammar "github.com/tree-sitter/tree-sitter-go/bindings/go"
	javaGrammar "github.com/tree-sitter/tree-sitter-java/bindings/go"
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
	// Package is the declared Java package, which import resolution and test
	// selection key on instead of the directory layout.
	Package string
}

// Import is one import specifier as written in a source file. Names lists
// imported members, which may themselves be submodules (Python `from . import x`).
type Import struct {
	Language string
	Module   string
	Names    []string
	Line     int
	// System marks a C/C++ #include <...>, searched only in include
	// directories, not next to the including file.
	System bool
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
	case ".java":
		a = syntaxAdapter{"java", sitter.NewLanguage(javaGrammar.Language())}
	case ".c":
		a = syntaxAdapter{"c", sitter.NewLanguage(cGrammar.Language())}
	case ".cc", ".cpp", ".cxx", ".c++", ".hpp", ".hh", ".hxx", ".h++", ".ipp", ".tpp", ".inl":
		a = syntaxAdapter{"cpp", sitter.NewLanguage(cppGrammar.Language())}
	case ".h":
		return headerAdapter{syntaxAdapter{"c", sitter.NewLanguage(cGrammar.Language())}, syntaxAdapter{"cpp", sitter.NewLanguage(cppGrammar.Language())}}, true
	default:
		return nil, false
	}
	return a, true
}
func Capabilities() []Capability {
	var out []Capability
	for _, p := range []string{"x.ts", "x.js", "x.py", "x.go", "x.rs", "x.java", "x.c", "x.cpp"} {
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
		kind := definitionKind(n, a.language)
		names := []*sitter.Node{declarationName(n)}
		if cFamily(a.language) {
			kind, names = cDefinitionKind(n), cDeclarators(n)
		}
		var nameNode *sitter.Node
		if len(names) > 0 {
			nameNode = names[0]
		}
		if n.Kind() == "method_declaration" {
			if receiver := n.ChildByFieldName("receiver"); receiver != nil && receiver.NamedChildCount() > 0 {
				if typ := receiver.NamedChild(0).ChildByFieldName("type"); typ != nil {
					scope = qualify(scope, strings.TrimLeft(typ.Utf8Text(s.Content), "*"))
				}
			}
		}
		if kind != "" && !n.HasError() {
			nextOwner, nextScope := owner, scope
			for _, nn := range names {
				if nn == nil {
					continue
				}
				name := nn.Utf8Text(s.Content)
				qualified := qualify(scope, name)
				if cFamily(a.language) {
					var prefix string
					prefix, name = splitQualified(name)
					if prefix != "" {
						qualified = qualify(qualify(scope, prefix), name)
					} else {
						qualified = qualify(scope, name)
					}
					if name == "" || kind == "function" && testMacros[name] {
						continue
					}
				}
				key := kind + "\x00" + qualified
				occurrence := duplicates[key]
				duplicates[key]++
				id := model.EntityID(kind, s.Path, qualified, occurrence)
				if kind == "package" && a.language == "java" && result.Package == "" {
					result.Package = name
				}
				props := map[string]string{"qualified_name": qualified, "syntax_kind": n.Kind()}
				result.Nodes = append(result.Nodes, model.Node{ID: id, Kind: kind, Name: name, Language: a.language, Properties: props, Provenance: provenance(n)})
				result.Edges = append(result.Edges, model.Edge{ID: model.StableID(owner, "DEFINES", id), From: owner, To: id, Kind: "DEFINES", Provenance: provenance(n)})
				// Only a single declaration scopes what it contains; in C and
				// C++ only records and functions do (struct tags are not
				// nested in the typedef or variable that mentions them).
				if len(names) == 1 && (!cFamily(a.language) || scopesChildren(n)) {
					nextOwner, nextScope = id, qualified
				}
			}
			owner, scope = nextOwner, nextScope
		}
		// A C++ namespace scopes its declarations without being an entity:
		// namespaces reopen across files and within one.
		if cFamily(a.language) && n.Kind() == "namespace_definition" {
			if name := n.ChildByFieldName("name"); name != nil {
				scope = qualify(scope, strings.ReplaceAll(name.Utf8Text(s.Content), "::", "."))
			}
		}
		// impl blocks have no declaration name, but their type supplies the method scope.
		if n.Kind() == "impl_item" {
			if typ := n.ChildByFieldName("type"); typ != nil {
				scope = qualify(scope, typ.Utf8Text(s.Content))
			}
		}
		imported := importNames(n, s.Content, a.language)
		if len(imported) > 0 {
			members := importedMembers(n, s.Content, a.language)
			system := false
			if n.Kind() == "preproc_include" {
				_, system = includePath(n, s.Content)
			}
			for _, module := range imported {
				if module != "" {
					result.Imports = append(result.Imports, Import{Language: a.language, Module: module, Names: members, Line: int(n.StartPosition().Row) + 1, System: system})
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

// declarationName returns the node naming a declaration. Package clauses
// carry their name as the first identifier-shaped child, not a name field.
func declarationName(n *sitter.Node) *sitter.Node {
	switch n.Kind() {
	case "package_clause", "package_declaration":
		for i := uint(0); i < n.NamedChildCount(); i++ {
			switch c := n.NamedChild(i); c.Kind() {
			case "package_identifier", "identifier", "scoped_identifier":
				return c
			}
		}
		return nil
	}
	return n.ChildByFieldName("name")
}

func definitionKind(n *sitter.Node, language string) string {
	switch n.Kind() {
	case "package_clause", "package_declaration":
		return "package"
	case "function_declaration", "function_definition", "function_item", "method_definition", "method_declaration", "function_signature", "constructor_declaration", "compact_constructor_declaration":
		return "function"
	case "class_declaration", "class_definition", "abstract_class_declaration":
		return "class"
	case "interface_declaration", "trait_item", "annotation_type_declaration":
		return "interface"
	case "type_alias_declaration", "type_spec", "struct_item", "enum_item", "type_item", "enum_declaration", "record_declaration":
		return "type"
	case "mod_item":
		return "module"
	case "variable_declarator":
		if language == "java" {
			// Fields and interface constants; method locals are not entities.
			if parent := n.Parent(); parent != nil && (parent.Kind() == "field_declaration" || parent.Kind() == "constant_declaration") {
				return "symbol"
			}
			return ""
		}
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
	case "call_expression":
		// CommonJS require('x') and dynamic import('x') with one literal
		// specifier. Computed specifiers stay unknown rather than guessed.
		if language != "typescript" && language != "javascript" {
			return nil
		}
		fn, args := n.ChildByFieldName("function"), n.ChildByFieldName("arguments")
		if fn == nil || args == nil || args.NamedChildCount() != 1 {
			return nil
		}
		if name := fn.Utf8Text(source); fn.Kind() != "import" && !(fn.Kind() == "identifier" && name == "require") {
			return nil
		}
		if arg := args.NamedChild(0); arg.Kind() == "string" {
			return []string{unquote(arg.Utf8Text(source))}
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
	case "preproc_include":
		if cFamily(language) {
			if p, _ := includePath(n, source); p != "" {
				return []string{p}
			}
		}
	case "import_declaration":
		// import a.b.C; import a.b.*; import static a.b.C.m; Static
		// members resolve through their enclosing type.
		if language != "java" {
			return nil
		}
		name, wildcard := "", false
		for i := uint(0); i < n.NamedChildCount(); i++ {
			switch c := n.NamedChild(i); c.Kind() {
			case "scoped_identifier", "identifier":
				name = c.Utf8Text(source)
			case "asterisk":
				wildcard = true
			}
		}
		if name == "" {
			return nil
		}
		if wildcard {
			name += ".*"
		}
		return []string{name}
	}
	return nil
}
