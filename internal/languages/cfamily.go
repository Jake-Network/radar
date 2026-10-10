package languages

import (
	"context"
	"regexp"
	"strings"

	sitter "github.com/tree-sitter/go-tree-sitter"
)

func cFamily(language string) bool { return language == "c" || language == "cpp" }

// headerAdapter decides a .h file's language, which its path does not say.
// The C grammar accepts some C++ without errors (it reads `namespace x {}`
// as an old-style function), so a header is C++ when the C++ grammar parses
// it cleanly and either C cannot or the text uses C++-only syntax.
type headerAdapter struct{ c, cpp syntaxAdapter }

var (
	cppSyntax     = regexp.MustCompile(`\b(?:namespace|class|template|typename|constexpr|nullptr)\b|\b(?:public|private|protected)\s*:|::`)
	cBlockComment = regexp.MustCompile(`(?s)/\*.*?\*/`)
	cLineComment  = regexp.MustCompile(`(?m)//.*$`)
)

func (h headerAdapter) Capability() Capability { return h.c.Capability() }

func (h headerAdapter) Parse(ctx context.Context, s Source) (Result, error) {
	c, err := h.c.Parse(ctx, s)
	if err != nil {
		return c, err
	}
	cpp, err := h.cpp.Parse(ctx, s)
	if err != nil {
		if ctx.Err() != nil {
			return Result{}, err
		}
		return c, nil
	}
	text := cLineComment.ReplaceAll(cBlockComment.ReplaceAll(s.Content, nil), nil)
	if len(cpp.Diagnostics) == 0 && (len(c.Diagnostics) > 0 || cppSyntax.Match(text)) {
		return cpp, nil
	}
	return c, nil
}

// testMacros are test-framework macros that the grammars read as function
// definitions (TEST(Suite, Name) { ... }); they are not repository functions.
var testMacros = map[string]bool{"TEST": true, "TEST_F": true, "TEST_P": true, "TYPED_TEST": true, "TYPED_TEST_P": true, "TEST_CASE": true, "TEST_CASE_METHOD": true, "SCENARIO": true, "BOOST_AUTO_TEST_CASE": true, "BOOST_FIXTURE_TEST_CASE": true}

// cDefinitionKind classifies C and C++ declarations. Only declarations with
// a body define a struct, class, union or enum; `struct node *p` refers to
// one. Prototypes are functions; function pointers and other declarations
// at file or namespace scope are symbols, and locals are not entities.
func cDefinitionKind(n *sitter.Node) string {
	switch n.Kind() {
	case "function_definition":
		return "function"
	case "declaration":
		if prototype(n.ChildByFieldName("declarator")) {
			return "function"
		}
		if parent := n.Parent(); parent != nil {
			switch parent.Kind() {
			case "translation_unit", "declaration_list", "linkage_specification", "preproc_if", "preproc_ifdef", "preproc_else", "preproc_elif", "preproc_elifdef":
				return "symbol"
			}
		}
	case "field_declaration":
		if prototype(n.ChildByFieldName("declarator")) {
			return "function"
		}
		return "symbol"
	case "struct_specifier", "union_specifier", "enum_specifier":
		if n.ChildByFieldName("body") != nil {
			return "type"
		}
	case "class_specifier":
		if n.ChildByFieldName("body") != nil {
			return "class"
		}
	case "type_definition", "alias_declaration":
		return "type"
	case "concept_definition":
		return "interface"
	case "preproc_function_def":
		return "symbol"
	case "preproc_def":
		// A value-less #define is an include guard or a feature flag.
		if n.ChildByFieldName("value") != nil {
			return "symbol"
		}
	}
	return ""
}

func scopesChildren(n *sitter.Node) bool {
	switch n.Kind() {
	case "struct_specifier", "union_specifier", "enum_specifier", "class_specifier", "function_definition":
		return true
	}
	return false
}

// prototype reports whether a declarator declares a function rather than a
// function pointer: pointer and reference wrappers may surround the
// function declarator (char *dup(void)), but a parenthesized name inside it
// (int (*handler)(int)) is a pointer variable.
func prototype(d *sitter.Node) bool {
	for d != nil {
		switch d.Kind() {
		case "pointer_declarator", "attributed_declarator":
			d = d.ChildByFieldName("declarator")
		case "reference_declarator":
			d = firstNamed(d)
		case "function_declarator":
			inner := d.ChildByFieldName("declarator")
			return inner != nil && inner.Kind() != "parenthesized_declarator"
		default:
			return false
		}
	}
	return false
}

func firstNamed(n *sitter.Node) *sitter.Node {
	if n.NamedChildCount() == 0 {
		return nil
	}
	return n.NamedChild(0)
}

// cDeclarators returns the name nodes of a C or C++ declaration: one per
// declarator (int x, y;) for declarations, the name field otherwise.
func cDeclarators(n *sitter.Node) []*sitter.Node {
	switch n.Kind() {
	case "function_definition", "type_definition":
		return []*sitter.Node{declaratorName(n.ChildByFieldName("declarator"))}
	case "declaration", "field_declaration":
		var out []*sitter.Node
		for i := uint(0); i < n.ChildCount(); i++ {
			if n.FieldNameForChild(uint32(i)) == "declarator" {
				out = append(out, declaratorName(n.Child(i)))
			}
		}
		return out
	}
	return []*sitter.Node{n.ChildByFieldName("name")}
}

// declaratorName follows a declarator chain to the declared name.
func declaratorName(d *sitter.Node) *sitter.Node {
	for d != nil {
		switch d.Kind() {
		case "identifier", "field_identifier", "type_identifier", "qualified_identifier", "destructor_name", "operator_name", "primitive_type":
			if d.Kind() == "primitive_type" {
				return nil
			}
			return d
		case "function_declarator", "pointer_declarator", "init_declarator", "array_declarator", "attributed_declarator":
			d = d.ChildByFieldName("declarator")
		case "reference_declarator", "parenthesized_declarator":
			d = firstNamed(d)
		default:
			return nil
		}
	}
	return nil
}

// splitQualified turns a C++ qualified name such as ns::Box<T>::get into its
// scope (ns.Box) and name (get). Template arguments are not part of IDs; an
// operator name (Box<T>::operator<<) keeps its own symbols.
func splitQualified(name string) (string, string) {
	operator := ""
	if i := strings.LastIndex(name, "::operator"); i >= 0 {
		name, operator = name[:i+2], name[i+2:]
	} else if strings.HasPrefix(name, "operator") {
		name, operator = "", name
	}
	var b strings.Builder
	depth := 0
	for _, r := range name {
		switch {
		case r == '<':
			depth++
		case r == '>' && depth > 0:
			depth--
		case depth == 0:
			b.WriteRune(r)
		}
	}
	parts := strings.Split(strings.TrimPrefix(strings.TrimSpace(b.String()), "::"), "::")
	if operator != "" {
		parts[len(parts)-1] = operator
	}
	return strings.Join(parts[:len(parts)-1], "."), parts[len(parts)-1]
}

// includePath reads #include "x.h" (quoted) and #include <x.h> (system).
// A macro-computed include has no literal path and stays unknown.
func includePath(n *sitter.Node, source []byte) (string, bool) {
	p := n.ChildByFieldName("path")
	if p == nil {
		return "", false
	}
	switch p.Kind() {
	case "string_literal":
		return strings.Trim(p.Utf8Text(source), `"`), false
	case "system_lib_string":
		return strings.Trim(p.Utf8Text(source), "<>"), true
	}
	return "", false
}
