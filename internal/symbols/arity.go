package symbols

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path"
	"strconv"
)

// Arity records argument counts only; parameter types are never inferred.
type Arity struct{ Min, Max int }

func (a Arity) accepts(n int) bool { return n >= a.Min && (a.Max < 0 || n <= a.Max) }
func (a Arity) String() string {
	if a.Max < 0 {
		return fmt.Sprintf("at least %d arguments", a.Min)
	}
	return fmt.Sprintf("%d arguments", a.Max)
}

type Call struct {
	Reference Reference
	Arguments int
	Unknown   string
}

func callKey(c Call) string { return refKey(c.Reference) + fmt.Sprintf("\x00%d", c.Arguments) }

func goCalls(content []byte, file string, targets map[string][]string, packages map[string]string) (map[string]Arity, []Call) {
	signatures := map[string]Arity{}
	var calls []Call
	positions := token.NewFileSet()
	f, e := parser.ParseFile(positions, file, content, 0)
	if e != nil {
		return signatures, calls
	}
	type binding struct {
		module  string
		targets []string
	}
	bindings := map[string]binding{}
	for _, imp := range f.Imports {
		module, e := strconv.Unquote(imp.Path.Value)
		if e != nil {
			continue
		}
		alias := path.Base(module)
		resolved := targets[fmt.Sprintf("%s:%d", file, positions.Position(imp.Pos()).Line)]
		for _, target := range resolved {
			if packages[target] != "" {
				alias = packages[target]
				break
			}
		}
		if imp.Name != nil {
			alias = imp.Name.Name
		}
		if alias == "." || alias == "_" {
			continue
		}
		bindings[alias] = binding{module, targets[fmt.Sprintf("%s:%d", file, positions.Position(imp.Pos()).Line)]}
	}
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Recv != nil {
			continue
		}
		n := 0
		variadic := false
		for _, param := range fn.Type.Params.List {
			count := len(param.Names)
			if count == 0 {
				count = 1
			}
			n += count
			if _, ok := param.Type.(*ast.Ellipsis); ok {
				variadic = true
			}
		}
		a := Arity{n, n}
		if variadic {
			a.Min = n - 1
			a.Max = -1
		}
		signatures[file+"\x00"+fn.Name.Name] = a
	}
	ast.Inspect(f, func(n ast.Node) bool {
		c, ok := n.(*ast.CallExpr)
		if !ok || c.Ellipsis.IsValid() {
			return true
		}
		sel, ok := c.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		id, ok := sel.X.(*ast.Ident)
		if !ok || id.Obj != nil {
			return true
		}
		binding, ok := bindings[id.Name]
		if !ok {
			return true
		}
		for _, target := range binding.targets {
			calls = append(calls, Call{Reference: Reference{file, binding.module, target, sel.Sel.Name, positions.Position(c.Pos()).Line}, Arguments: len(c.Args)})
		}
		return true
	})
	return signatures, calls
}
