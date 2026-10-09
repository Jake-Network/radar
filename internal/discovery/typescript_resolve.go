package discovery

import (
	"context"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/Jake-Network/radar/internal/model"
	sitter "github.com/tree-sitter/go-tree-sitter"
	tsGrammar "github.com/tree-sitter/tree-sitter-typescript/bindings/go"
)

// consumerResponse is a proposal grounded in a literal call and a resolved type.
// It is never an accepted declaration or evidence of runtime compatibility.
type consumerResponse struct {
	path, endpoint, method, typePath, typeName string
	fields                                     []string
	locations                                  []model.Provenance
	ambiguities                                []string
}
type tsReference struct{ module, name string }
type tsModule struct {
	path         string
	source       []byte
	tree         *sitter.Tree
	declarations map[string][]*sitter.Node
	imports      map[string][]tsReference
	exports      map[string][]tsReference
	stars        []string
	axios        map[string]bool
}

func tsWalk(n *sitter.Node, visit func(*sitter.Node)) {
	if n == nil {
		return
	}
	visit(n)
	for i := uint(0); i < n.NamedChildCount(); i++ {
		tsWalk(n.NamedChild(i), visit)
	}
}
func tsText(n *sitter.Node, b []byte) string {
	if n == nil {
		return ""
	}
	return n.Utf8Text(b)
}
func tsLiteral(n *sitter.Node, b []byte) (string, bool) {
	if n == nil || n.Kind() != "string" {
		return "", false
	}
	s := n.Utf8Text(b)
	if len(s) < 2 || strings.ContainsAny(s[1:len(s)-1], "\\\n\r") {
		return "", false
	}
	return s[1 : len(s)-1], true
}
func tsFirst(n *sitter.Node, kind string) *sitter.Node {
	if n == nil {
		return nil
	}
	for i := uint(0); i < n.NamedChildCount(); i++ {
		c := n.NamedChild(i)
		if c.Kind() == kind {
			return c
		}
	}
	return nil
}
func tsParse(ctx context.Context, file, source string) (*tsModule, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	parser := sitter.NewParser()
	defer parser.Close()
	grammar := tsGrammar.LanguageTypescript()
	if strings.HasSuffix(file, ".tsx") {
		grammar = tsGrammar.LanguageTSX()
	}
	if err := parser.SetLanguage(sitter.NewLanguage(grammar)); err != nil {
		return nil, err
	}
	parser.SetTimeoutMicros(5_000_000)
	b := []byte(source)
	tree := parser.ParseWithOptions(func(offset int, _ sitter.Point) []byte {
		if ctx.Err() != nil || offset >= len(b) {
			return nil
		}
		end := offset + 4096
		if end > len(b) {
			end = len(b)
		}
		return b[offset:end]
	}, nil, nil)
	if tree == nil {
		return nil, fmt.Errorf("TypeScript parse failed or exceeded budget")
	}
	if err := ctx.Err(); err != nil {
		tree.Close()
		return nil, err
	}
	m := &tsModule{path: file, source: b, tree: tree, declarations: map[string][]*sitter.Node{}, imports: map[string][]tsReference{}, exports: map[string][]tsReference{}, axios: map[string]bool{}}
	if tree.RootNode().HasError() {
		return m, fmt.Errorf("TypeScript syntax errors leave consumer relationships unresolved")
	}
	root := tree.RootNode()
	for i := uint(0); i < root.NamedChildCount(); i++ {
		n := root.NamedChild(i)
		if n.Kind() == "import_statement" {
			module, ok := tsLiteral(n.ChildByFieldName("source"), b)
			if !ok {
				continue
			}
			tsWalk(n, func(c *sitter.Node) {
				if c.Kind() == "import_specifier" {
					name := tsText(c.ChildByFieldName("name"), b)
					alias := tsText(c.ChildByFieldName("alias"), b)
					if alias == "" {
						alias = name
					}
					m.imports[alias] = append(m.imports[alias], tsReference{module, name})
				}
			})
			clause := tsFirst(n, "import_clause")
			if clause != nil {
				for j := uint(0); j < clause.NamedChildCount(); j++ {
					c := clause.NamedChild(j)
					if c.Kind() == "identifier" {
						name := c.Utf8Text(b)
						m.imports[name] = append(m.imports[name], tsReference{module, "default"})
						if module == "axios" && !strings.HasPrefix(strings.TrimSpace(n.Utf8Text(b)), "import type ") {
							m.axios[name] = true
						}
					}
				}
			}
		}
		if n.Kind() == "interface_declaration" || n.Kind() == "type_alias_declaration" {
			name := tsText(n.ChildByFieldName("name"), b)
			m.declarations[name] = append(m.declarations[name], n)
		}
		if n.Kind() == "export_statement" {
			declaration := n.ChildByFieldName("declaration")
			if declaration != nil && (declaration.Kind() == "interface_declaration" || declaration.Kind() == "type_alias_declaration") {
				name := tsText(declaration.ChildByFieldName("name"), b)
				m.declarations[name] = append(m.declarations[name], declaration)
				m.exports[name] = append(m.exports[name], tsReference{"", name})
			}
			module, _ := tsLiteral(n.ChildByFieldName("source"), b)
			clause := tsFirst(n, "export_clause")
			if clause != nil {
				tsWalk(clause, func(c *sitter.Node) {
					if c.Kind() == "export_specifier" {
						name := tsText(c.ChildByFieldName("name"), b)
						alias := tsText(c.ChildByFieldName("alias"), b)
						if alias == "" {
							alias = name
						}
						m.exports[alias] = append(m.exports[alias], tsReference{module, name})
					}
				})
			} else if declaration == nil && module != "" && strings.Contains(n.Utf8Text(b), "*") {
				m.stars = append(m.stars, module)
			}
		}
	}
	return m, nil
}
func tsResolveModule(modules map[string]*tsModule, file, spec string) (string, bool) {
	if !strings.HasPrefix(spec, ".") {
		return "", false
	}
	base := path.Clean(path.Join(path.Dir(file), spec))
	if base == ".." || strings.HasPrefix(base, "../") {
		return "", false
	}
	candidates := []string{base, base + ".ts", base + ".tsx", path.Join(base, "index.ts"), path.Join(base, "index.tsx")}
	if strings.HasSuffix(base, ".js") {
		candidates = append(candidates, strings.TrimSuffix(base, ".js")+".ts", strings.TrimSuffix(base, ".js")+".tsx")
	}
	found := ""
	for _, p := range candidates {
		if modules[p] != nil {
			if found != "" && found != p {
				return "", false
			}
			found = p
		}
	}
	return found, found != ""
}

type tsIdentity struct{ path, name string }

func tsResolveType(modules map[string]*tsModule, file, name string, exported bool, seen map[string]bool, depth int) (tsIdentity, bool) {
	key := fmt.Sprintf("%s:%s:%t", file, name, exported)
	if depth > 32 || seen[key] {
		return tsIdentity{}, false
	}
	seen[key] = true
	defer delete(seen, key)
	m := modules[file]
	if m == nil {
		return tsIdentity{}, false
	}
	var refs []tsReference
	if exported {
		refs = m.exports[name]
	} else {
		declarations := m.declarations[name]
		if len(declarations) > 0 {
			if len(declarations) != 1 || len(m.imports[name]) > 0 {
				return tsIdentity{}, false
			}
			d := declarations[0]
			value := d.ChildByFieldName("value")
			if d.Kind() == "type_alias_declaration" && value != nil && value.Kind() == "type_identifier" {
				return tsResolveType(modules, file, value.Utf8Text(m.source), false, seen, depth+1)
			}
			return tsIdentity{file, name}, true
		}
		refs = m.imports[name]
	}
	var identities []tsIdentity
	for _, ref := range refs {
		target := file
		if ref.module != "" {
			var ok bool
			target, ok = tsResolveModule(modules, file, ref.module)
			if !ok {
				return tsIdentity{}, false
			}
		}
		id, ok := tsResolveType(modules, target, ref.name, ref.module != "", seen, depth+1)
		if !ok {
			return tsIdentity{}, false
		}
		identities = append(identities, id)
	}
	if exported && len(refs) == 0 {
		for _, star := range m.stars {
			target, ok := tsResolveModule(modules, file, star)
			if !ok {
				return tsIdentity{}, false
			}
			if id, ok := tsResolveType(modules, target, name, true, seen, depth+1); ok {
				identities = append(identities, id)
			} else if tsMayExport(modules, target, name, map[string]bool{}, 0) {
				return tsIdentity{}, false
			}
		}
	}
	if len(identities) != 1 {
		return tsIdentity{}, false
	}
	return identities[0], true
}
func tsMayExport(modules map[string]*tsModule, file, name string, seen map[string]bool, depth int) bool {
	if depth > 32 || seen[file] {
		return true
	}
	seen[file] = true
	defer delete(seen, file)
	m := modules[file]
	if m == nil {
		return true
	}
	if len(m.exports[name]) > 0 {
		return true
	}
	for _, s := range m.stars {
		p, ok := tsResolveModule(modules, file, s)
		if !ok || tsMayExport(modules, p, name, seen, depth+1) {
			return true
		}
	}
	return false
}
func tsUnwrap(n *sitter.Node) *sitter.Node {
	for n != nil && (n.Kind() == "await_expression" || n.Kind() == "parenthesized_expression") {
		if n.NamedChildCount() != 1 {
			return n
		}
		n = n.NamedChild(0)
	}
	return n
}
func tsSimpleType(n *sitter.Node, b []byte) string {
	if n == nil {
		return ""
	}
	if n.Kind() == "type_identifier" {
		return n.Utf8Text(b)
	}
	if (n.Kind() == "type_annotation" || n.Kind() == "type_arguments") && n.NamedChildCount() == 1 {
		return tsSimpleType(n.NamedChild(0), b)
	}
	return ""
}
func tsScope(n *sitter.Node) *sitter.Node {
	for n != nil {
		switch n.Kind() {
		case "program", "statement_block", "function_declaration", "function_expression", "arrow_function", "method_definition", "catch_clause":
			return n
		}
		n = n.Parent()
	}
	return nil
}
func tsSame(a, b *sitter.Node) bool { return a != nil && b != nil && a.Id() == b.Id() }

// Resolve the lexical declaration rather than matching identifier text globally.
// Duplicate declarations, destructuring and parameter shadowing are conservative.
func tsLookup(use *sitter.Node, name string, b []byte) (*sitter.Node, bool) {
	for scope := tsScope(use.Parent()); scope != nil; scope = tsScope(scope.Parent()) {
		var declarations []*sitter.Node
		shadow := false
		var scan func(*sitter.Node)
		scan = func(n *sitter.Node) {
			if !tsSame(n, scope) && (n.Kind() == "function_declaration" || n.Kind() == "class_declaration") && tsText(n.ChildByFieldName("name"), b) == name {
				shadow = true
			}
			if n.Kind() == "catch_clause" && tsText(n.ChildByFieldName("parameter"), b) == name {
				shadow = true
			}
			if !tsSame(n, scope) && tsSame(tsScope(n), n) {
				return
			}
			if n.Kind() == "variable_declarator" {
				left := n.ChildByFieldName("name")
				if left != nil && left.Kind() == "identifier" && left.Utf8Text(b) == name {
					declarations = append(declarations, n)
				} else if left != nil && left.Kind() != "identifier" {
					tsWalk(left, func(c *sitter.Node) {
						if c.Kind() == "identifier" && c.Utf8Text(b) == name {
							shadow = true
						}
					})
				}
			}
			if n.Kind() == "required_parameter" || n.Kind() == "optional_parameter" {
				tsWalk(n.ChildByFieldName("pattern"), func(c *sitter.Node) {
					if c.Kind() == "identifier" && c.Utf8Text(b) == name {
						shadow = true
					}
				})
			}
			for i := uint(0); i < n.NamedChildCount(); i++ {
				scan(n.NamedChild(i))
			}
		}
		scan(scope)
		if shadow || len(declarations) > 1 {
			return nil, true
		}
		if len(declarations) == 1 {
			return declarations[0], true
		}
		// Arrow functions may use one unwrapped identifier parameter.
		if parameter := scope.ChildByFieldName("parameter"); parameter != nil && parameter.Kind() == "identifier" && parameter.Utf8Text(b) == name {
			return nil, true
		}
	}
	return nil, false
}
func tsBinding(use *sitter.Node, name string, b []byte) *sitter.Node {
	binding, _ := tsLookup(use, name, b)
	return binding
}
func tsIsShadowed(use *sitter.Node, name string, b []byte) bool {
	_, found := tsLookup(use, name, b)
	return found
}
func tsMembers(n *sitter.Node, b []byte) (*sitter.Node, []string) {
	if n == nil {
		return nil, nil
	}
	if n.Kind() == "identifier" {
		return n, nil
	}
	if n.Kind() != "member_expression" {
		return nil, nil
	}
	prop := n.ChildByFieldName("property")
	if prop == nil || prop.Kind() != "property_identifier" {
		return nil, nil
	}
	root, fields := tsMembers(n.ChildByFieldName("object"), b)
	if root == nil {
		return nil, nil
	}
	return root, append(fields, prop.Utf8Text(b))
}
func tsFetch(call *sitter.Node, b []byte) (string, string, bool) {
	call = tsUnwrap(call)
	if call == nil || call.Kind() != "call_expression" || tsText(tsUnwrap(call.ChildByFieldName("function")), b) != "fetch" {
		return "", "", false
	}
	args := call.ChildByFieldName("arguments")
	if args == nil || args.NamedChildCount() < 1 {
		return "", "", false
	}
	endpoint, ok := tsLiteral(args.NamedChild(0), b)
	if !ok {
		return "", "", false
	}
	method := "GET"
	if args.NamedChildCount() > 1 {
		options := args.NamedChild(1)
		if options.Kind() != "object" {
			return "", "", false
		}
		for i := uint(0); i < options.NamedChildCount(); i++ {
			pair := options.NamedChild(i)
			if pair.Kind() != "pair" {
				return "", "", false
			}
			key := tsText(pair.ChildByFieldName("key"), b)
			if key == "method" || key == "\"method\"" || key == "'method'" {
				v, ok := tsLiteral(pair.ChildByFieldName("value"), b)
				if !ok {
					return "", "", false
				}
				method = strings.ToUpper(v)
			}
		}
	}
	return endpoint, method, true
}
func tsAxiosReceiver(m *tsModule, receiver *sitter.Node) (bool, []string) {
	if receiver == nil || receiver.Kind() != "identifier" {
		return false, nil
	}
	name := receiver.Utf8Text(m.source)
	_, shadowed := tsLookup(receiver, name, m.source)
	if m.axios[name] && len(m.imports[name]) == 1 && !shadowed && !tsMutated(m, name, nil) {
		return true, []string{"Axios runtime origin and interceptors are not established by static inspection"}
	}
	binding := tsBinding(receiver, name, m.source)
	if binding == nil || tsMutated(m, name, binding) {
		return false, nil
	}
	v := tsUnwrap(binding.ChildByFieldName("value"))
	if v == nil || v.Kind() != "call_expression" {
		return false, nil
	}
	fn := v.ChildByFieldName("function")
	root, fields := tsMembers(fn, m.source)
	if root == nil || len(fields) != 1 || fields[0] != "create" || !m.axios[root.Utf8Text(m.source)] || len(m.imports[root.Utf8Text(m.source)]) != 1 || tsIsShadowed(root, root.Utf8Text(m.source), m.source) || tsMutated(m, root.Utf8Text(m.source), nil) {
		return false, nil
	}
	args := v.ChildByFieldName("arguments")
	if args != nil && args.NamedChildCount() > 0 {
		if reason := tsAxiosConfig(args.NamedChild(0), m.source); reason != "" {
			return false, []string{reason}
		}
	}
	return true, []string{"Axios instance runtime origin and interceptors are not established by static inspection"}
}
func resolveConsumers(ctx context.Context, sources map[string]string, revision string) ([]consumerResponse, []model.Diagnostic, error) {
	out := []consumerResponse{}
	diagnostics := []model.Diagnostic{}
	modules := map[string]*tsModule{}
	files := []string{}
	for file := range sources {
		if strings.HasSuffix(file, ".ts") || strings.HasSuffix(file, ".tsx") {
			files = append(files, file)
		}
	}
	sort.Strings(files)
	defer func() {
		for _, m := range modules {
			m.tree.Close()
		}
	}()
	for _, file := range files {
		m, err := tsParse(ctx, file, sources[file])
		if ctx.Err() != nil {
			if m != nil {
				m.tree.Close()
			}
			return nil, nil, ctx.Err()
		}
		if err != nil {
			if m != nil {
				m.tree.Close()
			}
			diagnostics = append(diagnostics, model.Diagnostic{Path: file, Message: err.Error(), Severity: model.SeverityWarning})
			continue
		}
		modules[file] = m
	}
	for _, file := range files {
		m := modules[file]
		if m == nil {
			continue
		}
		b := m.source
		tsWalk(m.tree.RootNode(), func(n *sitter.Node) {
			if n.Kind() != "variable_declarator" {
				return
			}
			nameNode := n.ChildByFieldName("name")
			if nameNode == nil || nameNode.Kind() != "identifier" {
				return
			}
			name := nameNode.Utf8Text(b)
			value := tsUnwrap(n.ChildByFieldName("value"))
			typ := tsSimpleType(n.ChildByFieldName("type"), b)
			if value != nil && value.Kind() == "as_expression" && value.NamedChildCount() == 2 {
				typ = tsSimpleType(value.NamedChild(1), b)
				value = tsUnwrap(value.NamedChild(0))
			}
			if value == nil || value.Kind() != "call_expression" {
				return
			}
			fn := tsUnwrap(value.ChildByFieldName("function"))
			if fn == nil {
				return
			}
			receiver := fn.ChildByFieldName("object")
			property := fn.ChildByFieldName("property")
			parts := []string{tsText(property, b)}
			if fn.Kind() != "member_expression" || property == nil || property.Kind() != "property_identifier" {
				return
			}
			method := ""
			endpoint := ""
			axios := false
			ambiguities := []string{}
			if parts[0] == "json" {
				obj := fn.ChildByFieldName("object")
				if obj != nil && obj.Kind() == "identifier" {
					fetchBinding := tsBinding(obj, obj.Utf8Text(b), b)
					if fetchBinding == nil || tsMutated(m, obj.Utf8Text(b), fetchBinding) || fetchBinding.EndByte() >= n.StartByte() {
						return
					}
					obj = fetchBinding.ChildByFieldName("value")
				}
				endpoint, method, _ = tsFetch(obj, b)
				if typ == "" {
					return
				}
				fetchCall := tsUnwrap(obj)
				if fetchCall != nil && fetchCall.Kind() == "call_expression" {
					fetchID := tsUnwrap(fetchCall.ChildByFieldName("function"))
					if fetchID != nil && (tsIsShadowed(fetchID, "fetch", b) || len(m.imports["fetch"]) > 0 || tsMutated(m, "fetch", nil)) {
						return
					}
				}
			} else {
				var ok bool
				ok, ambiguities = tsAxiosReceiver(m, receiver)
				if !ok {
					for _, reason := range ambiguities {
						diagnostics = append(diagnostics, model.Diagnostic{Path: file, Message: reason, Severity: model.SeverityWarning})
					}
					return
				}
				switch parts[0] {
				case "get", "post", "put", "patch", "delete", "head", "options":
					method = strings.ToUpper(parts[0])
				default:
					return
				}
				axios = true
				typ = tsSimpleType(value.ChildByFieldName("type_arguments"), b)
				args := value.ChildByFieldName("arguments")
				if args != nil && args.NamedChildCount() > 0 {
					endpoint, _ = tsLiteral(args.NamedChild(0), b)
					configIndex := uint(1)
					if method == "POST" || method == "PUT" || method == "PATCH" {
						configIndex = 2
					}
					if args.NamedChildCount() > configIndex {
						if reason := tsAxiosConfig(args.NamedChild(configIndex), b); reason != "" {
							diagnostics = append(diagnostics, model.Diagnostic{Path: file, Message: reason, Severity: model.SeverityWarning})
							return
						}
					}
				}
			}
			if endpoint == "" {
				diagnostics = append(diagnostics, model.Diagnostic{Path: file, Message: "dynamic or unsupported endpoint leaves response relationship unresolved", Severity: model.SeverityWarning})
				return
			}
			if typ == "" {
				diagnostics = append(diagnostics, model.Diagnostic{Path: file, Message: "response type is not a statically resolvable named declaration", Severity: model.SeverityWarning})
				return
			}
			if tsMutated(m, name, n) || tsLocalTypeShadow(n, typ, b) {
				diagnostics = append(diagnostics, model.Diagnostic{Path: file, Message: "response variable mutation or local type shadow leaves relationship unresolved", Severity: model.SeverityWarning})
				return
			}
			identity, ok := tsResolveType(modules, file, typ, false, map[string]bool{}, 0)
			if !ok {
				diagnostics = append(diagnostics, model.Diagnostic{Path: file, Message: "unresolved or ambiguous response type " + typ, Severity: model.SeverityWarning})
				return
			}
			response := consumerResponse{path: file, endpoint: endpoint, method: method, typePath: identity.path, typeName: identity.name, fields: []string{}, locations: []model.Provenance{provenance(revision, file, "tree-sitter-typed-response-call", int(n.StartPosition().Row)+1), provenance(revision, identity.path, "statically-resolved-type-declaration", int(modules[identity.path].declarations[identity.name][0].StartPosition().Row)+1)}, ambiguities: ambiguities}
			seenFields := map[string]bool{}
			tsWalk(m.tree.RootNode(), func(access *sitter.Node) {
				if access.Kind() != "member_expression" {
					return
				}
				if parent := access.Parent(); parent != nil && parent.Kind() == "member_expression" && tsSame(parent.ChildByFieldName("object"), access) {
					return
				}
				root, fields := tsMembers(access, b)
				if root == nil || root.Utf8Text(b) != name || !tsSame(tsBinding(root, name, b), n) || access.StartByte() <= n.EndByte() {
					return
				}
				if axios {
					if len(fields) == 0 || fields[0] != "data" {
						return
					}
					fields = fields[1:]
				}
				if len(fields) == 0 {
					return
				}
				field := strings.Join(fields, ".")
				if !seenFields[field] {
					seenFields[field] = true
					response.fields = append(response.fields, field)
					response.locations = append(response.locations, provenance(revision, file, "tree-sitter-scope-resolved-member-access", int(access.StartPosition().Row)+1))
				}
			})
			sort.Strings(response.fields)
			out = append(out, response)
		})
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
	}
	return out, diagnostics, nil
}

// Mutable response variables and clients can lose their initializer relationship.
// Ignore such bindings rather than trying to approximate control flow.
func tsMutated(m *tsModule, name string, binding *sitter.Node) bool {
	changed := false
	tsWalk(m.tree.RootNode(), func(n *sitter.Node) {
		if n.Kind() != "assignment_expression" && n.Kind() != "augmented_assignment_expression" && n.Kind() != "update_expression" {
			return
		}
		target := n.ChildByFieldName("left")
		if target == nil {
			target = n.ChildByFieldName("argument")
		}
		for target != nil && (target.Kind() == "member_expression" || target.Kind() == "subscript_expression") {
			target = target.ChildByFieldName("object")
		}
		if target == nil || target.Kind() != "identifier" || target.Utf8Text(m.source) != name {
			return
		}
		resolved, found := tsLookup(target, name, m.source)
		if binding == nil {
			if !found {
				changed = true
			}
		} else if tsSame(resolved, binding) {
			changed = true
		}
	})
	return changed
}
func tsLocalTypeShadow(use *sitter.Node, name string, b []byte) bool {
	for scope := tsScope(use.Parent()); scope != nil && scope.Kind() != "program"; scope = tsScope(scope.Parent()) {
		shadow := false
		var scan func(*sitter.Node)
		scan = func(n *sitter.Node) {
			if !tsSame(n, scope) && tsSame(tsScope(n), n) {
				return
			}
			if (n.Kind() == "interface_declaration" || n.Kind() == "type_alias_declaration" || n.Kind() == "type_parameter") && tsText(n.ChildByFieldName("name"), b) == name {
				shadow = true
			}
			for i := uint(0); i < n.NamedChildCount(); i++ {
				scan(n.NamedChild(i))
			}
		}
		scan(scope)
		if shadow {
			return true
		}
	}
	return false
}

// baseURL changes route identity. Do not match the unprefixed endpoint or infer
// origin from Axios configuration; reviewed declarations can express that link.
func tsAxiosConfig(config *sitter.Node, b []byte) string {
	unresolved := "Axios configured or dynamic base URL leaves endpoint relationship unresolved"
	if config == nil {
		return ""
	}
	if config.Kind() != "object" {
		return unresolved
	}
	for i := uint(0); i < config.NamedChildCount(); i++ {
		pair := config.NamedChild(i)
		if pair.Kind() != "pair" {
			return unresolved
		}
		key := pair.ChildByFieldName("key")
		if key == nil {
			return unresolved
		}
		name := tsText(key, b)
		if key.Kind() == "string" {
			var ok bool
			name, ok = tsLiteral(key, b)
			if !ok {
				return unresolved
			}
		} else if key.Kind() != "property_identifier" {
			return unresolved
		}
		if name == "baseURL" {
			return unresolved
		}
	}
	return ""
}
