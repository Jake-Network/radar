package symbols

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Jake-Network/radar/internal/languages"
	sitter "github.com/tree-sitter/go-tree-sitter"
	cppGrammar "github.com/tree-sitter/tree-sitter-cpp/bindings/go"
)

var namedList = regexp.MustCompile(`\{([^{}]+)\}`)
var identifier = regexp.MustCompile(`^[A-Za-z_$][A-Za-z0-9_$]*$`)
var uncertainExport = regexp.MustCompile(`(?m)^\s*(?:export\s+(?:type\s+)?\*|from\s+\S+\s+import\s+\*|(?:pub(?:\([^)]*\))?\s+)?use\s+[^;]*\*|pub\s+use\s+|def\s+__getattr__\s*\()`)

func importText(source []byte, imp languages.Import, spans ...map[int]string) string {
	if len(spans) > 0 {
		if text, ok := spans[0][imp.Line]; ok {
			return text
		}
		lines := strings.Split(string(source), "\n")
		if imp.Line < 1 || imp.Line > len(lines) {
			return ""
		}
		return lines[imp.Line-1]
	}
	extensions := map[string]string{"python": ".py", "typescript": ".ts", "javascript": ".js", "java": ".java", "rust": ".rs"}
	tree := sourceTree("source"+extensions[imp.Language], source)
	if tree != nil {
		defer tree.Close()
		stack := []*sitter.Node{tree.RootNode()}
		for len(stack) > 0 {
			n := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if int(n.StartPosition().Row)+1 == imp.Line {
				switch n.Kind() {
				case "import_statement", "export_statement", "import_declaration", "use_declaration", "import_from_statement":
					return n.Utf8Text(source)
				}
			}
			for i := uint(0); i < n.NamedChildCount(); i++ {
				stack = append(stack, n.NamedChild(i))
			}
		}
	}
	lines := strings.Split(string(source), "\n")
	if imp.Line < 1 || imp.Line > len(lines) {
		return ""
	}
	return lines[imp.Line-1]
}

func importTexts(file string, source []byte) map[int]string {
	out := map[int]string{}
	tree := sourceTree(file, source)
	if tree == nil {
		return out
	}
	defer tree.Close()
	stack := []*sitter.Node{tree.RootNode()}
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		switch n.Kind() {
		case "import_statement", "export_statement", "import_declaration", "use_declaration", "import_from_statement":
			out[int(n.StartPosition().Row)+1] = n.Utf8Text(source)
		}
		for i := uint(0); i < n.NamedChildCount(); i++ {
			stack = append(stack, n.NamedChild(i))
		}
	}
	return out
}

func dynamicNamespace(p string, content []byte) bool {
	switch path.Ext(p) {
	case ".py", ".ts", ".tsx", ".js", ".jsx", ".rs":
		return uncertainExport.Match(content)
	}
	return false
}

// Import syntax and line numbers come from the shared Tree-sitter adapter.
// The small literal subset below never scans comments as import statements.
func importedNames(source []byte, imp languages.Import, spans ...map[int]string) []string {
	if imp.Language == "python" {
		return imp.Names
	}
	if imp.Language == "go" {
		return goSelectors(source, imp)
	}
	if imp.Language == "c" || imp.Language == "cpp" {
		return headerCalls(source)
	}
	line := importText(source, imp, spans...)
	switch imp.Language {
	case "javascript", "typescript":
		m := namedList.FindStringSubmatch(line)
		if m == nil {
			return nil
		}
		var names []string
		for _, item := range strings.Split(m[1], ",") {
			fields := strings.Fields(strings.TrimSpace(item))
			if len(fields) > 0 && fields[0] == "type" {
				fields = fields[1:]
			}
			if len(fields) > 0 && identifier.MatchString(fields[0]) {
				names = append(names, fields[0])
			}
		}
		return names
	case "java":
		if strings.Contains(line, " static ") || strings.HasSuffix(imp.Module, ".*") {
			return nil
		}
		_, name, ok := strings.Cut(strings.TrimSuffix(imp.Module, ";"), ".")
		if ok {
			parts := strings.Split(name, ".")
			return []string{parts[len(parts)-1]}
		}
	case "rust":
		// Only a literal single use path, excluding globs and grouped imports.
		if strings.ContainsAny(line, "{}*") {
			return nil
		}
		item := strings.TrimSuffix(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "use ")), ";")
		item = strings.Split(item, " as ")[0]
		parts := strings.Split(item, "::")
		if len(parts) > 1 && identifier.MatchString(parts[len(parts)-1]) {
			return []string{parts[len(parts)-1]}
		}
	}
	return nil
}

// Only unqualified calls are considered. Member calls, templates, operators
// and macro calls have no binding evidence here and stay outside this subset.
func headerCalls(source []byte) []string {
	parser := sitter.NewParser()
	defer parser.Close()
	if parser.SetLanguage(sitter.NewLanguage(cppGrammar.Language())) != nil {
		return nil
	}
	started := time.Now()
	tree := parser.ParseWithOptions(func(offset int, _ sitter.Point) []byte {
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
	calls := map[string]bool{}
	declared := map[string]bool{}
	stack := []*sitter.Node{tree.RootNode()}
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if n.Kind() == "call_expression" {
			f := n.ChildByFieldName("function")
			if f != nil && f.Kind() == "identifier" {
				calls[f.Utf8Text(source)] = true
			}
		}
		if n.Kind() == "declaration" || n.Kind() == "function_definition" || n.Kind() == "parameter_declaration" {
			d := n.ChildByFieldName("declarator")
			for d != nil && d.Kind() != "identifier" {
				d = d.ChildByFieldName("declarator")
			}
			if d != nil {
				declared[d.Utf8Text(source)] = true
			}
		}
		if n.Kind() == "preproc_def" || n.Kind() == "preproc_function_def" {
			if name := n.ChildByFieldName("name"); name != nil {
				declared[name.Utf8Text(source)] = true
			}
		}
		for i := uint(0); i < n.NamedChildCount(); i++ {
			stack = append(stack, n.NamedChild(i))
		}
	}
	var out []string
	for name := range calls {
		if !declared[name] {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

func reExportedName(source []byte, imp languages.Import, name string, spans ...map[int]string) string {
	if imp.Language != "javascript" && imp.Language != "typescript" {
		return ""
	}
	line := strings.TrimSpace(importText(source, imp, spans...))
	if !strings.HasPrefix(line, "export ") {
		return ""
	}
	m := namedList.FindStringSubmatch(line)
	if m == nil {
		return ""
	}
	for _, item := range strings.Split(m[1], ",") {
		fields := strings.Fields(strings.TrimSpace(item))
		if len(fields) > 0 && fields[0] == "type" {
			fields = fields[1:]
		}
		if len(fields) > 0 && fields[0] == name {
			if len(fields) == 3 && fields[1] == "as" && identifier.MatchString(fields[2]) {
				return fields[2]
			}
			return name
		}
	}
	return ""
}

func goSelectors(source []byte, imp languages.Import) []string {
	return goSelectorsNamed(source, imp, path.Base(imp.Module))
}

func goSelectorsNamed(source []byte, imp languages.Import, packageName string) []string {
	f, e := parser.ParseFile(token.NewFileSet(), "source.go", source, 0)
	if e != nil {
		return nil
	}
	alias := packageName
	for _, spec := range f.Imports {
		module, e := strconv.Unquote(spec.Path.Value)
		if e == nil && module == imp.Module && spec.Name != nil {
			alias = spec.Name.Name
		}
	}
	if alias == "." || alias == "_" {
		return nil
	}
	var names []string
	seen := map[string]bool{}
	ast.Inspect(f, func(n ast.Node) bool {
		s, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		id, ok := s.X.(*ast.Ident)
		if ok && id.Name == alias && id.Obj == nil && !seen[s.Sel.Name] {
			seen[s.Sel.Name] = true
			names = append(names, s.Sel.Name)
		}
		return true
	})
	return names
}

func constrainedGo(p string, content []byte) bool {
	if !strings.HasSuffix(p, ".go") {
		return false
	}
	if strings.Contains(string(content), "//go:build ") || strings.Contains(string(content), "// +build ") {
		return true
	}
	parts := strings.Split(strings.TrimSuffix(path.Base(p), ".go"), "_")
	platforms := map[string]bool{"aix": true, "android": true, "darwin": true, "dragonfly": true, "freebsd": true, "illumos": true, "ios": true, "js": true, "linux": true, "netbsd": true, "openbsd": true, "plan9": true, "solaris": true, "wasip1": true, "windows": true, "386": true, "amd64": true, "arm": true, "arm64": true, "loong64": true, "mips": true, "mipsle": true, "mips64": true, "mips64le": true, "ppc64": true, "ppc64le": true, "riscv64": true, "s390x": true, "wasm": true}
	for _, part := range parts[1:] {
		if platforms[part] {
			return true
		}
	}
	return false
}
