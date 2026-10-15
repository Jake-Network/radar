package evidence

import (
	"context"
	"fmt"

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
	var walk func(*sitter.Node, int) error
	walk = func(n *sitter.Node, depth int) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		nodes++
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
	return masked, nil
}
