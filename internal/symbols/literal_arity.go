package symbols

import (
	"fmt"
	"strings"

	"github.com/Jake-Network/radar/internal/languages"
	"github.com/Jake-Network/radar/internal/model"
	sitter "github.com/tree-sitter/go-tree-sitter"
)

// Only explicit imports bind calls. Types, defaults, variadics and overloads
// are unsupported, so their argument counts cannot produce a failure.
func literalCalls(file string, source []byte, parsed languages.Result, targets map[string][]string, texts ...map[int]string) (map[string]Arity, []Call, map[string]bool, map[string]model.Provenance) {
	signatures := map[string]Arity{}
	unsupported := map[string]bool{}
	locations := map[string]model.Provenance{}
	var calls []Call
	tree := sourceTree(file, source)
	if tree == nil {
		unsupported[file+"\x00*"] = true
		return signatures, calls, unsupported, locations
	}
	defer tree.Close()
	if tree.RootNode().HasError() {
		unsupported[file+"\x00*"] = true
		return signatures, calls, unsupported, locations
	}
	definitions := map[string]model.Node{}
	for _, n := range parsed.Nodes {
		if n.Kind == "function" {
			definitions[fmt.Sprintf("%d:%s", n.Provenance.Line, n.Name)] = n
		}
	}
	bindings := map[string][]Reference{}
	shadowed := shadowedNames(tree.RootNode(), source)
	var headers []Reference
	for _, imp := range parsed.Imports {
		resolved := targets[fmt.Sprintf("%s:%d", file, imp.Line)]
		if imp.Language == "c" || imp.Language == "cpp" {
			for _, target := range resolved {
				headers = append(headers, Reference{From: file, Module: imp.Module, Target: target})
			}
			continue
		}
		for _, name := range importedNames(source, imp, texts...) {
			local := importedAlias(source, imp, name, texts...)
			for _, target := range resolved {
				bindings[local] = append(bindings[local], Reference{From: file, Module: imp.Module, Target: target, Name: name})
			}
		}
	}
	stack := []*sitter.Node{tree.RootNode()}
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		switch n.Kind() {
		case "function_definition", "function_declaration", "function_item", "method_declaration", "declaration":
			name := n.ChildByFieldName("name")
			params := n.ChildByFieldName("parameters")
			if d := n.ChildByFieldName("declarator"); d != nil {
				for d != nil && d.Kind() != "function_declarator" {
					d = d.ChildByFieldName("declarator")
				}
				if d != nil {
					params = d.ChildByFieldName("parameters")
					name = d.ChildByFieldName("declarator")
				}
			}
			if name != nil && params != nil {
				decl, ok := definitions[fmt.Sprintf("%d:%s", int(n.StartPosition().Row)+1, name.Utf8Text(source))]
				q := decl.Properties["qualified_name"]
				if ok && (!strings.Contains(q, ".") || n.Kind() == "method_declaration" && staticMethod(n, source)) {
					key := file + "\x00" + q
					count, known := literalParameterCount(params, source)
					if _, duplicate := signatures[key]; duplicate {
						known = false
					}
					if !known {
						unsupported[key] = true
					}
					signatures[key] = Arity{count, count}
					locations[key] = decl.Provenance
				}
			}
		}
		var function, args *sitter.Node
		javaMethod := ""
		switch n.Kind() {
		case "call", "call_expression":
			function = n.ChildByFieldName("function")
			args = n.ChildByFieldName("arguments")
		case "method_invocation":
			function = n.ChildByFieldName("object")
			args = n.ChildByFieldName("arguments")
			if name := n.ChildByFieldName("name"); name != nil {
				javaMethod = name.Utf8Text(source)
			}
		}
		if function != nil && args != nil && function.Kind() == "identifier" {
			name := function.Utf8Text(source)
			refs := bindings[name]
			if len(headers) > 0 {
				for _, header := range headers {
					header.Name = name
					refs = append(refs, header)
				}
			}
			count := 0
			unknown := ""
			if shadowed[name] {
				unknown = "local binding shadows import"
			}
			for i := uint(0); i < args.NamedChildCount(); i++ {
				arg := args.NamedChild(i)
				if arg.Kind() == "comment" {
					continue
				}
				count++
				switch arg.Kind() {
				case "list_splat", "dictionary_splat", "spread_element", "keyword_argument":
					unknown = "expanded or named arguments"
				}
			}
			for _, ref := range refs {
				if javaMethod != "" {
					ref.Name += "." + javaMethod
				}
				ref.Line = int(n.StartPosition().Row) + 1
				calls = append(calls, Call{Reference: ref, Arguments: count, Unknown: unknown})
			}
		}
		for i := uint(0); i < n.NamedChildCount(); i++ {
			stack = append(stack, n.NamedChild(i))
		}
	}
	return signatures, calls, unsupported, locations
}

func literalParameterCount(params *sitter.Node, source []byte) (int, bool) {
	if params.NamedChildCount() == 1 && params.NamedChild(0).Utf8Text(source) == "void" {
		return 0, true
	}
	count := 0
	known := true
	for i := uint(0); i < params.NamedChildCount(); i++ {
		p := params.NamedChild(i)
		switch p.Kind() {
		case "comment":
			continue
		case "identifier", "typed_parameter", "parameter", "parameter_declaration", "formal_parameter", "required_parameter":
			count++
		default:
			known = false
			count++
		}
		if p.ChildByFieldName("value") != nil || p.ChildByFieldName("default") != nil {
			known = false
		}
		if p.Kind() == "parameter_declaration" {
			if d := p.ChildByFieldName("declarator"); d != nil && d.Kind() == "variadic_declarator" {
				known = false
			}
		}
	}
	return count, known
}

func shadowedNames(root *sitter.Node, source []byte) map[string]bool {
	out := map[string]bool{}
	stack := []*sitter.Node{root}
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		switch n.Kind() {
		case "identifier":
			if parent := n.Parent(); parent != nil && (parent.Kind() == "parameters" || parent.Kind() == "formal_parameters") {
				out[n.Utf8Text(source)] = true
			}
		case "assignment", "assignment_expression", "variable_declarator", "parameter", "parameter_declaration", "formal_parameter", "required_parameter", "function_definition", "function_declaration", "function_item", "class_definition", "class_declaration", "let_declaration", "preproc_def", "preproc_function_def":
			for _, field := range []string{"left", "name", "pattern", "declarator"} {
				d := n.ChildByFieldName(field)
				for d != nil && d.Kind() != "identifier" {
					d = d.ChildByFieldName("declarator")
				}
				if d != nil {
					out[d.Utf8Text(source)] = true
				}
			}
		}
		for i := uint(0); i < n.NamedChildCount(); i++ {
			stack = append(stack, n.NamedChild(i))
		}
	}
	return out
}
func staticMethod(n *sitter.Node, source []byte) bool {
	for i := uint(0); i < n.NamedChildCount(); i++ {
		c := n.NamedChild(i)
		if c.Kind() == "modifiers" {
			for _, word := range strings.Fields(c.Utf8Text(source)) {
				if word == "static" {
					return true
				}
			}
		}
	}
	return false
}
func importedAlias(source []byte, imp languages.Import, name string, texts ...map[int]string) string {
	line := importText(source, imp, texts...)
	if m := namedList.FindStringSubmatch(line); m != nil {
		line = m[1]
	}
	line = strings.ReplaceAll(line, ";", "")
	for _, item := range strings.Split(line, ",") {
		fields := strings.Fields(strings.TrimSpace(item))
		for i := 0; i+2 < len(fields); i++ {
			if fields[i] == name && fields[i+1] == "as" && identifier.MatchString(fields[i+2]) {
				return fields[i+2]
			}
		}
	}
	return name
}
