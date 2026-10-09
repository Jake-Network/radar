package testselection

import (
	"strings"

	sitter "github.com/tree-sitter/go-tree-sitter"
	pyGrammar "github.com/tree-sitter/tree-sitter-python/bindings/go"
)

// pythonFramework distinguishes actual imported TestCase ancestry from mock
// imports. This is syntax evidence only: rebinding and inherited custom bases
// require runtime resolution. Comments and strings never select a framework.
func pythonFramework(content string) string {
	data := []byte(content)
	parser := sitter.NewParser()
	defer parser.Close()
	if parser.SetLanguage(sitter.NewLanguage(pyGrammar.Language())) != nil {
		return ""
	}
	parser.SetTimeoutMicros(500_000)
	tree := parser.Parse(data, nil)
	if tree == nil {
		return ""
	}
	defer tree.Close()
	root := tree.RootNode()
	modules := map[string]bool{}
	bases := map[string]bool{}
	pytestImported := false
	for i := uint(0); i < root.NamedChildCount(); i++ {
		n := root.NamedChild(i)
		switch n.Kind() {
		case "import_statement":
			for j := uint(0); j < n.NamedChildCount(); j++ {
				item := n.NamedChild(j)
				name, alias := importName(item, data)
				if name == "unittest" {
					if alias == "" {
						alias = "unittest"
					}
					modules[alias] = true
				}
				if name == "pytest" {
					pytestImported = true
				}
			}
		case "import_from_statement":
			module := n.ChildByFieldName("module_name")
			if module == nil {
				continue
			}
			if module.Utf8Text(data) == "pytest" {
				pytestImported = true
			}
			if module.Utf8Text(data) != "unittest" {
				continue
			}
			for j := uint(0); j < n.NamedChildCount(); j++ {
				item := n.NamedChild(j)
				if item.StartByte() == module.StartByte() {
					continue
				}
				name, alias := importName(item, data)
				if name == "TestCase" {
					if alias == "" {
						alias = name
					}
					bases[alias] = true
				}
			}
		}
	}
	// Only module/class-level definitions are considered. Nested helper functions
	// are not pytest collection candidates solely because their names start test_.
	pytestFunction := false
	stack := []*sitter.Node{root}
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if n.IsError() || n.IsMissing() {
			continue
		}
		if n.Kind() == "class_definition" {
			if parents := n.ChildByFieldName("superclasses"); parents != nil {
				for i := uint(0); i < parents.NamedChildCount(); i++ {
					parent := parents.NamedChild(i)
					if parent.Kind() == "identifier" && bases[parent.Utf8Text(data)] {
						return "unittest"
					}
					if parent.Kind() == "attribute" {
						object, attribute := parent.ChildByFieldName("object"), parent.ChildByFieldName("attribute")
						if object != nil && attribute != nil && modules[object.Utf8Text(data)] && attribute.Utf8Text(data) == "TestCase" {
							return "unittest"
						}
					}
				}
			}
		}
		if n.Kind() == "function_definition" {
			name := n.ChildByFieldName("name")
			if name != nil && strings.HasPrefix(name.Utf8Text(data), "test_") {
				pytestFunction = true
			}
			continue
		}
		// Imports already handled; walking them isn't useful for declarations.
		if n.Kind() == "import_statement" || n.Kind() == "import_from_statement" {
			continue
		}
		for i := n.NamedChildCount(); i > 0; i-- {
			stack = append(stack, n.NamedChild(i-1))
		}
	}
	if pytestImported || pytestFunction {
		return "pytest"
	}
	return ""
}
func importName(n *sitter.Node, data []byte) (string, string) {
	if n.Kind() == "aliased_import" {
		name := n.ChildByFieldName("name")
		alias := n.ChildByFieldName("alias")
		if name == nil {
			return "", ""
		}
		a := ""
		if alias != nil {
			a = alias.Utf8Text(data)
		}
		return name.Utf8Text(data), a
	}
	if n.Kind() == "dotted_name" || n.Kind() == "identifier" {
		return n.Utf8Text(data), ""
	}
	return "", ""
}
