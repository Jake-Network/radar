package symbols

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path"
	"strings"
	"time"

	sitter "github.com/tree-sitter/go-tree-sitter"
	cpp "github.com/tree-sitter/tree-sitter-cpp/bindings/go"
	java "github.com/tree-sitter/tree-sitter-java/bindings/go"
	js "github.com/tree-sitter/tree-sitter-javascript/bindings/go"
	py "github.com/tree-sitter/tree-sitter-python/bindings/go"
	rust "github.com/tree-sitter/tree-sitter-rust/bindings/go"
	ts "github.com/tree-sitter/tree-sitter-typescript/bindings/go"
)

func sourceTree(file string, source []byte) *sitter.Tree {
	var language *sitter.Language
	switch path.Ext(file) {
	case ".py":
		language = sitter.NewLanguage(py.Language())
	case ".js", ".jsx":
		language = sitter.NewLanguage(js.Language())
	case ".ts":
		language = sitter.NewLanguage(ts.LanguageTypescript())
	case ".tsx":
		language = sitter.NewLanguage(ts.LanguageTSX())
	case ".java":
		language = sitter.NewLanguage(java.Language())
	case ".rs":
		language = sitter.NewLanguage(rust.Language())
	case ".c", ".cpp", ".cc", ".cxx", ".h", ".hpp":
		language = sitter.NewLanguage(cpp.Language())
	default:
		return nil
	}
	p := sitter.NewParser()
	defer p.Close()
	if p.SetLanguage(language) != nil {
		return nil
	}
	started := time.Now()
	return p.ParseWithOptions(func(offset int, _ sitter.Point) []byte {
		if int(offset) >= len(source) {
			return nil
		}
		return source[offset:]
	}, nil, &sitter.ParseOptions{ProgressCallback: func(sitter.ParseState) bool { return time.Since(started) > time.Second }})
}

// New receiver calls are unknown relationships rather than absent symbols.
// Syntax nodes prevent comments and string contents from creating diagnostics.
func dynamicReferences(file string, source []byte) []Reference {
	if path.Ext(file) == ".go" {
		return goDynamicReferences(file, source)
	}
	var language *sitter.Language
	switch path.Ext(file) {
	case ".py":
		language = sitter.NewLanguage(py.Language())
	case ".js", ".jsx":
		language = sitter.NewLanguage(js.Language())
	case ".ts":
		language = sitter.NewLanguage(ts.LanguageTypescript())
	case ".tsx":
		language = sitter.NewLanguage(ts.LanguageTSX())
	case ".java":
		language = sitter.NewLanguage(java.Language())
	case ".rs":
		language = sitter.NewLanguage(rust.Language())
	case ".cpp", ".cc", ".cxx", ".h", ".hpp":
		language = sitter.NewLanguage(cpp.Language())
	default:
		return nil
	}
	p := sitter.NewParser()
	defer p.Close()
	if p.SetLanguage(language) != nil {
		return nil
	}
	started := time.Now()
	tree := p.ParseWithOptions(func(offset int, _ sitter.Point) []byte {
		if int(offset) >= len(source) {
			return nil
		}
		return source[offset:]
	}, nil, &sitter.ParseOptions{ProgressCallback: func(sitter.ParseState) bool { return time.Since(started) > time.Second }})
	if tree == nil {
		return nil
	}
	defer tree.Close()
	if tree.RootNode().HasError() {
		return nil
	}
	var refs []Reference
	seen := map[string]bool{}
	stack := []*sitter.Node{tree.RootNode()}
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		var expression *sitter.Node
		if n.Kind() == "preproc_if" || n.Kind() == "preproc_ifdef" {
			refs = append(refs, Reference{From: file, Module: "@conditional", Line: int(n.StartPosition().Row) + 1})
		}
		if n.Kind() == "macro_invocation" {
			if name := n.ChildByFieldName("macro"); name != nil && name.Utf8Text(source) != "include" {
				refs = append(refs, Reference{From: file, Module: "@dynamic", Name: name.Utf8Text(source) + "!", Line: int(n.StartPosition().Row) + 1})
			}
		}
		if n.Kind() == "preproc_def" || n.Kind() == "preproc_function_def" {
			if name := n.ChildByFieldName("name"); name != nil {
				refs = append(refs, Reference{From: file, Module: "@macro", Name: name.Utf8Text(source), Line: int(n.StartPosition().Row) + 1})
			}
		}
		switch n.Kind() {
		case "call", "call_expression":
			expression = n.ChildByFieldName("function")
		case "method_invocation":
			if n.ChildByFieldName("object") != nil {
				expression = n
			}
		}
		if expression != nil {
			switch expression.Kind() {
			case "attribute", "member_expression", "field_expression", "method_invocation":
				text := strings.Join(strings.Fields(expression.Utf8Text(source)), " ")
				if !seen[text] {
					refs = append(refs, Reference{From: file, Module: "@dynamic", Name: text, Line: int(n.StartPosition().Row) + 1})
					seen[text] = true
				}
			}
		}
		for i := uint(0); i < n.NamedChildCount(); i++ {
			stack = append(stack, n.NamedChild(i))
		}
	}
	return refs
}

func goDynamicReferences(file string, source []byte) []Reference {
	positions := token.NewFileSet()
	f, e := parser.ParseFile(positions, file, source, 0)
	if e != nil {
		return nil
	}
	var refs []Reference
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		id, ok := sel.X.(*ast.Ident)
		if !ok || id.Obj != nil {
			receiver := "receiver"
			if ok {
				receiver = id.Name
			}
			refs = append(refs, Reference{From: file, Module: "@dynamic", Name: receiver + "." + sel.Sel.Name, Line: positions.Position(call.Pos()).Line})
		}
		return true
	})
	return refs
}
