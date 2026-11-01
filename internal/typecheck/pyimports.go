package typecheck

import (
	"errors"
	"strings"

	sitter "github.com/tree-sitter/go-tree-sitter"
	pyGrammar "github.com/tree-sitter/tree-sitter-python/bindings/go"
)

// PythonImport is one import statement read from Python source as text:
// "import a.b" (Module "a.b"), or "from [.]*X import n, m" (Level dots,
// Module X, possibly empty for "from . import n", and Names). It is a
// syntactic reading, not name resolution: Radar never runs the file.
type PythonImport struct {
	Line   int
	Level  int
	Module string
	Names  []string
}

// Modules returns the dotted names the import may refer to: the module
// itself, and for a from-import each module.name too, since a name may be a
// submodule. A relative import is resolved against pkg, the package of the
// importing file; it returns nothing when it climbs above pkg.
func (i PythonImport) Modules(pkg string) []string {
	base := i.Module
	if i.Level > 0 {
		parts := []string{}
		if pkg != "" {
			parts = strings.Split(pkg, ".")
		}
		if i.Level-1 > len(parts) || len(parts) == 0 {
			return nil
		}
		parts = parts[:len(parts)-(i.Level-1)]
		if len(parts) == 0 {
			return nil
		}
		base = strings.Join(parts, ".")
		if i.Module != "" {
			base += "." + i.Module
		}
	}
	if base == "" {
		return nil
	}
	out := []string{base}
	for _, n := range i.Names {
		out = append(out, base+"."+n)
	}
	return out
}

// PythonImports reads syntactic imports, including relative, multiline and
// nested statements. Strings and comments are not imports.
func PythonImports(src []byte) []PythonImport {
	imports, _ := readPythonImports(src)
	return imports
}

func readPythonImports(src []byte) ([]PythonImport, error) {
	parser := sitter.NewParser()
	defer parser.Close()
	if err := parser.SetLanguage(sitter.NewLanguage(pyGrammar.Language())); err != nil {
		return nil, err
	}
	parser.SetTimeoutMicros(500_000)
	tree := parser.ParseWithOptions(func(offset int, _ sitter.Point) []byte {
		if offset >= len(src) {
			return nil
		}
		return src[offset:min(offset+4096, len(src))]
	}, nil, nil)
	if tree == nil {
		return nil, errors.New("Python import parse did not finish")
	}
	defer tree.Close()
	if tree.RootNode().HasError() {
		return nil, errors.New("Python import syntax is unsupported")
	}
	var out []PythonImport
	stack := []*sitter.Node{tree.RootNode()}
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if n.Kind() != "import_statement" && n.Kind() != "import_from_statement" {
			for j := int(n.NamedChildCount()) - 1; j >= 0; j-- {
				stack = append(stack, n.NamedChild(uint(j)))
			}
			continue
		}
		imp := PythonImport{Line: int(n.StartPosition().Row) + 1}
		mod := n.ChildByFieldName("module_name")
		if mod != nil {
			name := strings.Join(strings.Fields(strings.ReplaceAll(mod.Utf8Text(src), "\\\n", "")), "")
			imp.Level = len(name) - len(strings.TrimLeft(name, "."))
			imp.Module = name[imp.Level:]
		}
		for j := uint(0); j < n.NamedChildCount(); j++ {
			child := n.NamedChild(j)
			if mod != nil && child.StartByte() == mod.StartByte() {
				continue
			}
			if child.Kind() == "aliased_import" {
				child = child.ChildByFieldName("name")
			}
			if child == nil || child.Kind() != "dotted_name" {
				continue
			}
			name := strings.Join(strings.Fields(strings.ReplaceAll(child.Utf8Text(src), "\\\n", "")), "")
			if !dottedName(name) {
				continue
			}
			if n.Kind() == "import_statement" {
				out = append(out, PythonImport{Line: imp.Line, Module: name})
			} else {
				imp.Names = append(imp.Names, name)
			}
		}
		if n.Kind() == "import_from_statement" {
			out = append(out, imp)
		}
	}
	return out, nil
}

func dottedName(s string) bool {
	return s != "" && pythonDotted(strings.Split(s, "."))
}
