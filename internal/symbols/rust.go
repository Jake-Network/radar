package symbols

import (
	"path"
	"strings"

	sitter "github.com/tree-sitter/go-tree-sitter"
)

// A literal file-level include! inserts the file in the including module.
// Nested, computed and repeated-owner includes cannot establish one namespace.
func rustIncludes(file string, source []byte) []string {
	tree := sourceTree(file, source)
	if tree == nil {
		return nil
	}
	defer tree.Close()
	var out []string
	root := tree.RootNode()
	for i := uint(0); i < root.NamedChildCount(); i++ {
		n := root.NamedChild(i)
		if n.Kind() == "expression_statement" && n.NamedChildCount() == 1 {
			n = n.NamedChild(0)
		}
		if n.Kind() != "macro_invocation" {
			continue
		}
		macro := n.ChildByFieldName("macro")
		if macro == nil || macro.Utf8Text(source) != "include" {
			continue
		}
		stack := []*sitter.Node{n}
		for len(stack) > 0 {
			child := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if child.Kind() == "string_literal" {
				text := child.Utf8Text(source)
				if len(text) >= 2 && strings.HasPrefix(text, "\"") && !strings.Contains(text, "\\") {
					p := path.Clean(path.Join(path.Dir(file), strings.Trim(text, "\"")))
					if p != ".." && !strings.HasPrefix(p, "../") && !path.IsAbs(p) {
						out = append(out, p)
					}
				}
			}
			for j := uint(0); j < child.NamedChildCount(); j++ {
				stack = append(stack, child.NamedChild(j))
			}
		}
	}
	return out
}
