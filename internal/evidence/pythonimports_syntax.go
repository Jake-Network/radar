package evidence

import (
	"context"
	"fmt"
	"strings"

	sitter "github.com/tree-sitter/go-tree-sitter"
	pyGrammar "github.com/tree-sitter/tree-sitter-python/bindings/go"
)

// pythonExecutableSource masks syntax-proven comments and literal strings before
// import scope scanning. It uses the existing embedded grammar, never Python or
// repository code. Executable interpolation expressions are preserved, including
// nested format expressions; their literal strings are masked recursively.
func pythonExecutableSource(source []byte) ([]byte, error) {
	return pythonExecutableSourceContext(context.Background(), source)
}

func pythonExecutableSourceContext(ctx context.Context, source []byte) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	parser := sitter.NewParser()
	defer parser.Close()
	if err := parser.SetLanguage(sitter.NewLanguage(pyGrammar.Language())); err != nil {
		return nil, err
	}
	parser.SetTimeoutMicros(100_000)
	tree := parser.ParseWithOptions(func(offset int, _ sitter.Point) []byte {
		if ctx.Err() != nil || offset >= len(source) {
			return nil
		}
		end := offset + 4096
		if end > len(source) {
			end = len(source)
		}
		return source[offset:end]
	}, nil, nil)
	if err := ctx.Err(); err != nil {
		if tree != nil {
			tree.Close()
		}
		return nil, err
	}
	if tree == nil {
		return nil, fmt.Errorf("Python import syntax unavailable")
	}
	defer tree.Close()
	root := tree.RootNode()
	if root.HasError() {
		return nil, fmt.Errorf("Python import syntax incomplete")
	}
	masked := append([]byte(nil), source...)

	mask := func(n *sitter.Node) {
		for i := n.StartByte(); i < n.EndByte(); i++ {
			if masked[i] != '\n' && masked[i] != '\r' {
				masked[i] = ' '
			}
		}
	}
	nodes := 0
	syntaxNodes := []*sitter.Node{}
	var walk func(*sitter.Node, int) error
	walk = func(n *sitter.Node, depth int) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		nodes++
		syntaxNodes = append(syntaxNodes, n)
		if depth > 256 || nodes > 100000 {
			return fmt.Errorf("Python import syntax traversal budget exceeded")
		}
		switch n.Kind() {
		case "comment":
			mask(n)
			return nil
		case "string":
			mask(n)
		case "interpolation", "format_expression":
			expression := n.ChildByFieldName("expression")
			if expression == nil {
				return fmt.Errorf("Python interpolation expression unavailable")
			}
			copy(masked[expression.StartByte():expression.EndByte()], source[expression.StartByte():expression.EndByte()])
			if err := walk(expression, depth+1); err != nil {
				return err
			}
			if format := n.ChildByFieldName("format_specifier"); format != nil {
				return walk(format, depth+1)
			}
			return nil
		}
		for i := uint(0); i < n.NamedChildCount(); i++ {
			if err := walk(n.NamedChild(i), depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(root, 0); err != nil {
		return nil, err
	}
	if err := pythonUnsafeCalls(ctx, source, syntaxNodes); err != nil {
		return nil, err
	}
	return masked, nil
}

// Track literal module/function aliases rather than matching words. Attribute
// methods such as model.eval or session.exec are not Python builtin execution.
func pythonUnsafeCalls(ctx context.Context, source []byte, nodes []*sitter.Node) error {
	aliases := map[string]string{"eval": "danger", "exec": "danger", "__import__": "danger", "__builtins__": "builtins"}
	classify := func(module, name string) string {
		switch module {
		case "importlib":
			if name == "import_module" {
				return "danger"
			}
		case "builtins":
			if name == "eval" || name == "exec" || name == "__import__" {
				return "danger"
			}
		case "runpy":
			if name == "run_module" || name == "run_path" {
				return "danger"
			}
		}
		return ""
	}
	for _, n := range nodes {
		if err := ctx.Err(); err != nil {
			return err
		}
		if n.Kind() != "import_statement" && n.Kind() != "import_from_statement" {
			continue
		}
		module := ""
		if m := n.ChildByFieldName("module_name"); m != nil {
			module = m.Utf8Text(source)
		}
		for i := uint(0); i < n.NamedChildCount(); i++ {
			item := n.NamedChild(i)
			name, alias := "", ""
			if item.Kind() == "aliased_import" {
				original := item.ChildByFieldName("name")
				a := item.ChildByFieldName("alias")
				if original == nil || a == nil {
					return fmt.Errorf("Python import alias unavailable")
				}
				name = original.Utf8Text(source)
				alias = a.Utf8Text(source)
			} else if item.Kind() == "dotted_name" || item.Kind() == "identifier" {
				name = item.Utf8Text(source)
				if m := n.ChildByFieldName("module_name"); m != nil && item.StartByte() == m.StartByte() {
					continue
				}
			} else if item.Kind() == "wildcard_import" && (module == "importlib" || module == "builtins" || module == "runpy") {
				return fmt.Errorf("Python wildcard loader import is unverified")
			} else {
				continue
			}
			if n.Kind() == "import_statement" {
				if alias == "" {
					alias = strings.SplitN(name, ".", 2)[0]
				}
				if name == "importlib" || name == "builtins" || name == "runpy" {
					aliases[alias] = name
				} else if alias == "importlib" && strings.HasPrefix(name, "importlib.") {
					aliases[alias] = "importlib"
				}
			} else {
				if alias == "" {
					alias = name
				}
				if kind := classify(module, name); kind != "" {
					aliases[alias] = kind
				}
			}
		}
	}
	var resolve func(*sitter.Node, int) string
	resolve = func(n *sitter.Node, depth int) string {
		if n == nil {
			return ""
		}
		if depth > 32 {
			return "danger"
		}
		switch n.Kind() {
		case "identifier":
			return aliases[n.Utf8Text(source)]
		case "parenthesized_expression":
			if n.NamedChildCount() == 1 {
				return resolve(n.NamedChild(0), depth+1)
			}
		case "call":
			function := n.ChildByFieldName("function")
			arguments := n.ChildByFieldName("arguments")
			if function != nil && function.Kind() == "identifier" && function.Utf8Text(source) == "getattr" && arguments != nil && arguments.NamedChildCount() >= 2 {
				module := resolve(arguments.NamedChild(0), depth+1)
				if module == "importlib" || module == "builtins" || module == "runpy" {
					attribute := arguments.NamedChild(1)
					if attribute.Kind() != "string" {
						return "danger"
					}
					text := attribute.Utf8Text(source)
					if strings.ContainsAny(text, "\\{}") {
						return "danger"
					}
					return classify(module, strings.Trim(text, "\"'"))
				}
			}
		case "attribute":
			object := n.ChildByFieldName("object")
			attribute := n.ChildByFieldName("attribute")
			if attribute != nil {
				return classify(resolve(object, depth+1), attribute.Utf8Text(source))
			}
		}
		return ""
	}
	for pass := 0; pass < 32; pass++ {
		changed := false
		for _, n := range nodes {
			if n.Kind() != "assignment" {
				continue
			}
			left := n.ChildByFieldName("left")
			right := n.ChildByFieldName("right")
			if left == nil || left.Kind() != "identifier" {
				continue
			}
			kind := resolve(right, 0)
			name := left.Utf8Text(source)
			if kind != "" && aliases[name] != kind {
				aliases[name] = kind
				changed = true
			}
		}
		if !changed {
			break
		}
		if pass == 31 {
			return fmt.Errorf("Python loader alias budget exceeded")
		}
	}
	for _, n := range nodes {
		if err := ctx.Err(); err != nil {
			return err
		}
		if n.Kind() == "call" && resolve(n.ChildByFieldName("function"), 0) == "danger" {
			return fmt.Errorf("dynamic Python imports require unavailable runtime provenance")
		}
	}
	return nil
}
