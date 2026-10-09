package discovery

import (
	"context"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/Jake-Network/radar/internal/model"
	sitter "github.com/tree-sitter/go-tree-sitter"
	pyGrammar "github.com/tree-sitter/tree-sitter-python/bindings/go"
)

type pyImport struct {
	module, name string
	line         int
}
type pyField struct{ name, typ string }
type pyClass struct {
	name   string
	bases  []string
	fields []pyField
	line   int
}
type pyRouter struct {
	kind, prefix string
	line         int
}
type pyInclude struct {
	parent, child, prefix string
	line                  int
}
type pyRoute struct {
	router, method, url, response string
	line                          int
}
type pyModule struct {
	file      string
	imports   map[string]pyImport
	classes   map[string]pyClass
	routers   map[string]pyRouter
	includes  []pyInclude
	routes    []pyRoute
	ambiguous map[string]bool
	wildcard  bool
}

// resolvePython observes syntax and resolves bounded local import relationships.
// It never imports Python modules or treats response serialization as observed.
func resolvePython(ctx context.Context, sources map[string]string, revision string) ([]Schema, []endpoint, []model.Diagnostic, error) {
	modules := map[string]*pyModule{}
	diagnostics := []model.Diagnostic{}
	warn := func(file, message string) {
		diagnostics = append(diagnostics, model.Diagnostic{Path: file, Message: message, Severity: model.SeverityWarning})
	}
	files := []string{}
	for file := range sources {
		if path.Ext(file) == ".py" {
			files = append(files, file)
		}
	}
	sort.Strings(files)
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return nil, nil, diagnostics, err
		}
		m, ds, err := parsePyModule(ctx, file, sources[file])
		if err != nil {
			return nil, nil, diagnostics, err
		}
		modules[file] = m
		diagnostics = append(diagnostics, ds...)
	}
	// Keys contain an unambiguous file and symbol, rather than a matching name.
	key := func(file, name string) string { return file + "#" + name }
	split := func(k string) (string, string) {
		parts := strings.SplitN(k, "#", 2)
		if len(parts) != 2 {
			return "", k
		}
		return parts[0], parts[1]
	}
	var resolve func(string, string, map[string]bool, int) (string, []model.Provenance)
	resolve = func(file, name string, seen map[string]bool, depth int) (string, []model.Provenance) {
		k := key(file, name)
		if depth > 32 || seen[k] {
			return "", nil
		}
		seen[k] = true
		m := modules[file]
		if m == nil || m.wildcard || m.ambiguous[name] {
			return "", nil
		}
		parts := strings.SplitN(name, ".", 2)
		if len(parts) == 2 {
			imp, ok := m.imports[parts[0]]
			if !ok || imp.name != "" {
				return "", nil
			}
			if (imp.module == "fastapi" || imp.module == "pydantic") && pythonModuleFile(file, imp.module, modules) == "" {
				return imp.module + "." + parts[1], []model.Provenance{provenance(revision, file, "python-import-resolution", imp.line)}
			}
			target := pythonModuleFile(file, imp.module, modules)
			if target == "" {
				return "", nil
			}
			targetKey, rel := resolve(target, parts[1], seen, depth+1)
			return targetKey, append([]model.Provenance{provenance(revision, file, "python-import-resolution", imp.line)}, rel...)
		}
		if _, ok := m.classes[name]; ok {
			return k, nil
		}
		if _, ok := m.routers[name]; ok {
			return k, nil
		}
		imp, ok := m.imports[name]
		if !ok {
			return "", nil
		}
		p := provenance(revision, file, "python-import-resolution", imp.line)
		if (imp.module == "pydantic" || imp.module == "fastapi") && pythonModuleFile(file, imp.module, modules) == "" {
			return imp.module + "." + imp.name, []model.Provenance{p}
		}
		target := pythonModuleFile(file, imp.module, modules)
		if target == "" {
			return "", nil
		}
		// A from-package import may name a submodule, e.g. from . import routes.
		if imp.name == "" {
			return "", nil
		}
		targetKey, rel := resolve(target, imp.name, seen, depth+1)
		return targetKey, append([]model.Provenance{p}, rel...)
	}
	// Resolve constructors only after all modules are available.
	for _, file := range files {
		m := modules[file]
		for name, router := range m.routers {
			kind, _ := resolve(file, router.kind, map[string]bool{}, 0)
			if kind != "fastapi.FastAPI" && kind != "fastapi.APIRouter" {
				delete(m.routers, name)
				continue
			}
			router.kind = kind
			m.routers[name] = router
		}
	}
	schemas := []Schema{}
	schemaFields := map[string][]string{}
	modelState := map[string]int{}
	var fields func(string, int) ([]string, bool)
	fields = func(k string, depth int) ([]string, bool) {
		if depth > 32 || modelState[k] == 1 {
			return nil, false
		}
		if modelState[k] == 2 {
			v, ok := schemaFields[k]
			return v, ok
		}
		file, name := split(k)
		m := modules[file]
		if m == nil || m.wildcard || m.ambiguous[name] {
			return nil, false
		}
		c, ok := m.classes[name]
		if !ok || len(c.bases) != 1 {
			return nil, false
		}
		modelState[k] = 1
		base, _ := resolve(file, c.bases[0], map[string]bool{}, 0)
		inherited := []string{}
		valid := base == "pydantic.BaseModel"
		if !valid && base != "" {
			inherited, valid = fields(base, depth+1)
		}
		if !valid {
			modelState[k] = 2
			return nil, false
		}
		names := map[string]bool{}
		for _, field := range inherited {
			names[field] = true
		}
		for _, field := range c.fields { // Overrides replace inherited nested observations.
			for old := range names {
				if old == field.name || strings.HasPrefix(old, field.name+".") {
					delete(names, old)
				}
			}
			names[field.name] = true
			typ, _ := resolve(file, field.typ, map[string]bool{}, 0)
			if typ != "" && typ != "pydantic.BaseModel" {
				if nested, found := fields(typ, depth+1); found {
					for _, n := range nested {
						names[field.name+"."+n] = true
						if len(names) > 10000 {
							warn(file, "nested model field limit exceeded; schema unresolved")
							modelState[k] = 2
							return nil, false
						}
					}
				} else if typ == k || modelState[typ] == 1 {
					warn(file, "recursive model field "+name+"."+field.name+" is unresolved")
				}
			}
		}
		out := []string{}
		for n := range names {
			out = append(out, n)
		}
		sort.Strings(out)
		schemaFields[k] = out
		modelState[k] = 2
		return out, true
	}
	for _, file := range files {
		m := modules[file]
		names := []string{}
		for name := range m.classes {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			c := m.classes[name]
			fs, ok := fields(key(file, name), 0)
			if !ok {
				if len(c.bases) > 0 {
					warn(file, "model "+name+" inheritance is unresolved or unsupported")
				}
				continue
			}
			schemas = append(schemas, Schema{ID: model.ContractID(file, "/models/"+name), Name: name, Path: file, Pointer: "/models/" + name, Fields: fs, Evidence: provenance(revision, file, "tree-sitter-pydantic-field-resolution", c.line)})
		}
	}
	type registration struct {
		prefix    string
		locations []model.Provenance
	}
	registrations := map[string][]registration{}
	type edge struct {
		child, prefix string
		location      model.Provenance
		relationships []model.Provenance
	}
	graph := map[string][]edge{}
	roots := []string{}
	for _, file := range files {
		m := modules[file]
		names := []string{}
		for n := range m.routers {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, name := range names {
			r := m.routers[name]
			if r.kind == "fastapi.FastAPI" {
				roots = append(roots, key(file, name))
			}
		}
		for _, in := range m.includes {
			parent, parentRel := resolve(file, in.parent, map[string]bool{}, 0)
			child, childRel := resolve(file, in.child, map[string]bool{}, 0)
			cf, cn := split(child)
			cm := modules[cf]
			if parent == "" || cm == nil {
				warn(file, "include_router target is unresolved")
				continue
			}
			r, ok := cm.routers[cn]
			if !ok || r.kind != "fastapi.APIRouter" {
				warn(file, "include_router target is not a resolved APIRouter")
				continue
			}
			graph[parent] = append(graph[parent], edge{child: child, prefix: in.prefix + r.prefix, location: provenance(revision, file, "fastapi-literal-router-registration", in.line), relationships: append(parentRel, childRel...)})
		}
	}
	registrationCount := 0
	registrationLimitReported := false
	var register func(string, string, []model.Provenance, map[string]bool, int)
	register = func(k, prefix string, locs []model.Provenance, seen map[string]bool, depth int) {
		registrationCount++
		if registrationCount > 10000 {
			if !registrationLimitReported {
				f, _ := split(k)
				warn(f, "router registration limit exceeded; coverage incomplete")
				registrationLimitReported = true
			}
			return
		}
		if depth > 32 || seen[k] {
			f, _ := split(k)
			warn(f, "router registration cycle or depth limit is unresolved")
			return
		}
		next := map[string]bool{}
		for k, v := range seen {
			next[k] = v
		}
		next[k] = true
		registrations[k] = append(registrations[k], registration{prefix, locs})
		for _, e := range graph[k] {
			chronologySupported := true
			for _, observed := range locs {
				if observed.Method == "fastapi-literal-router-registration" && observed.Path == e.location.Path && observed.Line < e.location.Line {
					warn(e.location.Path, "router registration occurs after parent inclusion; captured route chronology is unresolved")
					chronologySupported = false
					break
				}
			}
			if !chronologySupported {
				continue
			}
			locations := append(append([]model.Provenance{}, locs...), e.location)
			locations = append(locations, e.relationships...)
			register(e.child, prefix+e.prefix, locations, next, depth+1)
		}
	}
	sort.Strings(roots)
	for _, root := range roots {
		register(root, "", nil, map[string]bool{}, 0)
	}
	endpoints := []endpoint{}
	for _, file := range files {
		m := modules[file]
		for _, route := range m.routes {
			rk, routerRel := resolve(file, route.router, map[string]bool{}, 0)
			modelKey, modelRel := resolve(file, route.response, map[string]bool{}, 0)
			mf, mn := split(modelKey)
			if _, ok := schemaFields[modelKey]; !ok {
				warn(file, "response_model "+route.response+" is unresolved")
				continue
			}
			regs := registrations[rk]
			if len(regs) == 0 {
				warn(file, "route on "+route.router+" has no statically resolved FastAPI registration")
				continue
			}
			if len(regs) > 1 {
				warn(file, "route has multiple visible registrations; deployment origin relationship remains unresolved")
			}
			for _, reg := range regs {
				chronologySupported := true
				for _, observed := range reg.locations {
					if observed.Method == "fastapi-literal-router-registration" && observed.Path == file && observed.Line < route.line {
						warn(file, "route decorator occurs after router inclusion; route is absent from the captured registration")
						chronologySupported = false
						break
					}
				}
				if !chronologySupported {
					continue
				}
				locations := append(append([]model.Provenance{}, reg.locations...), routerRel...)
				locations = append(locations, modelRel...)
				endpoints = append(endpoints, endpoint{url: reg.prefix + route.url, schema: model.ContractID(mf, "/models/"+mn), method: route.method, location: provenance(revision, file, "fastapi-literal-response-model", route.line), relationships: locations})
			}
		}
	}
	sort.Slice(endpoints, func(i, j int) bool {
		a, b := endpoints[i], endpoints[j]
		return a.url+"\x00"+a.method+"\x00"+a.schema+a.location.Path < b.url+"\x00"+b.method+"\x00"+b.schema+b.location.Path
	})
	sort.Slice(diagnostics, func(i, j int) bool {
		return diagnostics[i].Path+diagnostics[i].Message < diagnostics[j].Path+diagnostics[j].Message
	})
	return schemas, endpoints, diagnostics, ctx.Err()
}

func pythonModuleFile(file, module string, modules map[string]*pyModule) string {
	roots := []string{}
	if strings.HasPrefix(module, ".") {
		count := len(module) - len(strings.TrimLeft(module, "."))
		root := path.Dir(file)
		for i := 1; i < count; i++ {
			root = path.Dir(root)
		}
		module = strings.TrimLeft(module, ".")
		roots = append(roots, root)
	} else {
		for root := path.Dir(file); ; root = path.Dir(root) {
			roots = append(roots, root)
			if root == "." || root == "/" {
				break
			}
		}
	}
	matches := map[string]bool{}
	for _, root := range roots {
		base := path.Join(root, strings.ReplaceAll(module, ".", "/"))
		for _, candidate := range []string{base + ".py", path.Join(base, "__init__.py")} {
			if modules[candidate] != nil {
				matches[candidate] = true
			}
		}
	}
	if len(matches) != 1 {
		return ""
	}
	for candidate := range matches {
		return candidate
	}
	return ""
}

func parsePyModule(ctx context.Context, file, source string) (*pyModule, []model.Diagnostic, error) {
	m := &pyModule{file: file, imports: map[string]pyImport{}, classes: map[string]pyClass{}, routers: map[string]pyRouter{}, ambiguous: map[string]bool{}}
	ds := []model.Diagnostic{}
	warn := func(msg string) {
		ds = append(ds, model.Diagnostic{Path: file, Message: msg, Severity: model.SeverityWarning})
	}
	p := sitter.NewParser()
	defer p.Close()
	if err := p.SetLanguage(sitter.NewLanguage(pyGrammar.Language())); err != nil {
		return m, ds, err
	}
	p.SetTimeoutMicros(5_000_000)
	b := []byte(source)
	tree := p.Parse(b, nil)
	if tree == nil {
		return m, ds, fmt.Errorf("Python syntax parse failed for %s", file)
	}
	defer tree.Close()
	if err := ctx.Err(); err != nil {
		return m, ds, err
	}
	root := tree.RootNode()
	if root.HasError() {
		warn("Python syntax errors prevent authoritative discovery")
		return m, ds, nil
	}
	text := func(n *sitter.Node) string {
		if n == nil {
			return ""
		}
		return n.Utf8Text(b)
	}
	line := func(n *sitter.Node) int { return int(n.StartPosition().Row) + 1 }
	counts := map[string]int{}
	define := func(name string) {
		counts[name]++
		if counts[name] > 1 {
			m.ambiguous[name] = true
			warn("Python symbol " + name + " is multiply defined; resolution unavailable")
		}
	}
	// Only module-level bindings are eligible: function locals cannot define a router.
	for i := uint(0); i < root.NamedChildCount(); i++ {
		n := root.NamedChild(i)
		switch n.Kind() {
		case "function_definition":
			define(text(n.ChildByFieldName("name")))
		case "decorated_definition":
			definition := n.ChildByFieldName("definition")
			if definition != nil {
				define(text(definition.ChildByFieldName("name")))
				if definition.Kind() == "class_definition" {
					warn("decorated Python model declaration is unsupported")
				}
			}
		case "import_from_statement", "import_statement":
			module := ""
			if n.Kind() == "import_from_statement" {
				module = text(n.ChildByFieldName("module_name"))
			}
			for j := uint(0); j < n.NamedChildCount(); j++ {
				child := n.NamedChild(j)
				if child.Kind() == "wildcard_import" {
					m.wildcard = true
					warn("wildcard Python import is unresolved")
					continue
				}
				if child.Kind() != "dotted_name" && child.Kind() != "aliased_import" {
					continue
				}
				if n.Kind() == "import_from_statement" && n.ChildByFieldName("module_name") != nil && child.StartByte() == n.ChildByFieldName("module_name").StartByte() {
					continue
				}
				name, alias := text(child), ""
				if child.Kind() == "aliased_import" {
					name = text(child.ChildByFieldName("name"))
					alias = text(child.ChildByFieldName("alias"))
				}
				if alias == "" {
					alias = name
				}
				if n.Kind() == "import_statement" {
					if !strings.Contains(name, ".") || child.Kind() == "aliased_import" {
						define(alias)
						m.imports[alias] = pyImport{module: name, line: line(child)}
					}
				} else {
					define(alias)
					m.imports[alias] = pyImport{module: module, name: name, line: line(child)}
				}
			}
		case "class_definition":
			name := text(n.ChildByFieldName("name"))
			define(name)
			c := pyClass{name: name, line: line(n)}
			base := n.ChildByFieldName("superclasses")
			if base != nil {
				for j := uint(0); j < base.NamedChildCount(); j++ {
					c.bases = append(c.bases, text(base.NamedChild(j)))
				}
			}
			body := n.ChildByFieldName("body")
			if body != nil {
				for j := uint(0); j < body.NamedChildCount(); j++ {
					child := body.NamedChild(j)
					if child.Kind() == "decorated_definition" {
						warn("model " + name + " decorated runtime behavior is unsupported")
					}
					if child.Kind() != "expression_statement" || child.NamedChildCount() != 1 {
						continue
					}
					a := child.NamedChild(0)
					if a.Kind() != "assignment" {
						continue
					}
					left, typ := a.ChildByFieldName("left"), a.ChildByFieldName("type")
					if left != nil && text(left) == "model_config" {
						warn("model " + name + " runtime model_config is unsupported")
						continue
					}
					if left != nil && left.Kind() == "identifier" && typ != nil {
						tn := typ
						if typ.NamedChildCount() == 1 {
							tn = typ.NamedChild(0)
						}
						annotation := text(tn)
						if strings.HasPrefix(text(left), "_") {
							warn("private model attribute " + name + "." + text(left) + " is excluded from response field observations")
							continue
						}
						classVariable := strings.HasPrefix(annotation, "ClassVar[") || strings.Contains(annotation, ".ClassVar[")
						if bracket := strings.IndexByte(annotation, '['); bracket > 0 {
							if imported, ok := m.imports[annotation[:bracket]]; ok && imported.module == "typing" && imported.name == "ClassVar" {
								classVariable = true
							}
						}
						if classVariable {
							warn("ClassVar model attribute " + name + "." + text(left) + " is excluded from response field observations")
							continue
						}
						if strings.Contains(annotation, "[") || tn.Kind() == "string" || tn.Kind() == "binary_operator" {
							warn("complex model annotation " + name + "." + text(left) + " has unresolved nested shape")
						}
						if value := a.ChildByFieldName("right"); value != nil && value.Kind() == "call" {
							warn("model " + name + "." + text(left) + " runtime field configuration is unsupported")
						}
						c.fields = append(c.fields, pyField{name: text(left), typ: annotation})
					}
				}
			}
			m.classes[name] = c
		case "expression_statement":
			if n.NamedChildCount() != 1 {
				continue
			}
			a := n.NamedChild(0)
			if a.Kind() == "assignment" {
				left, right := a.ChildByFieldName("left"), a.ChildByFieldName("right")
				if left == nil || left.Kind() != "identifier" {
					continue
				}
				name := text(left)
				define(name)
				if right != nil && right.Kind() == "call" {
					fn := text(right.ChildByFieldName("function"))
					prefix, ok := pyKeywordLiteral(right.ChildByFieldName("arguments"), b, "prefix")
					if !ok {
						warn("router prefix is dynamic; registration unresolved")
						continue
					}
					m.routers[name] = pyRouter{kind: fn, prefix: prefix, line: line(a)}
				}
			}
		}
	}
	// Conditional module bindings may shadow a previously resolved import,
	// router, or model. Function/class bodies have their own scopes.
	var uncertainBindings func(*sitter.Node)
	uncertainBindings = func(n *sitter.Node) {
		switch n.Kind() {
		case "identifier":
			name := text(n)
			m.ambiguous[name] = true
			warn("conditional or destructured Python binding " + name + " is unresolved")
			return
		case "function_definition", "class_definition":
			name := text(n.ChildByFieldName("name"))
			m.ambiguous[name] = true
			warn("conditional Python declaration " + name + " is unresolved")
			return
		case "assignment", "augmented_assignment":
			if left := n.ChildByFieldName("left"); left != nil {
				uncertainBindings(left)
			}
			return
		case "import_statement", "import_from_statement":
			for j := uint(0); j < n.NamedChildCount(); j++ {
				child := n.NamedChild(j)
				if child.Kind() == "wildcard_import" {
					m.wildcard = true
					warn("conditional wildcard Python import is unresolved")
					continue
				}
				if child.Kind() != "dotted_name" && child.Kind() != "aliased_import" {
					continue
				}
				module := n.ChildByFieldName("module_name")
				if module != nil && child.StartByte() == module.StartByte() {
					continue
				}
				name := text(child)
				if child.Kind() == "aliased_import" {
					name = text(child.ChildByFieldName("alias"))
				}
				m.ambiguous[name] = true
				warn("conditional Python import " + name + " is unresolved")
			}
			return
		case "call":
			fn := n.ChildByFieldName("function")
			if fn != nil && fn.Kind() == "attribute" && text(fn.ChildByFieldName("attribute")) == "include_router" {
				warn("conditional include_router registration is unsupported")
			}
			return
		}
		// Bare expressions and conditions do not bind their identifiers.
		if n.Kind() == "attribute" || n.Kind() == "binary_operator" || n.Kind() == "comparison_operator" {
			return
		}
		for j := uint(0); j < n.NamedChildCount(); j++ {
			uncertainBindings(n.NamedChild(j))
		}
	}
	for i := uint(0); i < root.NamedChildCount(); i++ {
		n := root.NamedChild(i)
		switch n.Kind() {
		case "if_statement", "for_statement", "while_statement", "try_statement", "with_statement", "match_statement":
			// Traverse only suites, except loop/with binding targets.
			for j := uint(0); j < n.NamedChildCount(); j++ {
				child := n.NamedChild(j)
				if child.Kind() == "block" || strings.HasSuffix(child.Kind(), "clause") {
					uncertainBindings(child)
				}
			}
			if left := n.ChildByFieldName("left"); left != nil {
				uncertainBindings(left)
			}
		case "expression_statement":
			if n.NamedChildCount() == 1 {
				a := n.NamedChild(0)
				if a.Kind() == "assignment" {
					left := a.ChildByFieldName("left")
					if left != nil && left.Kind() != "identifier" {
						uncertainBindings(left)
					}
				} else if a.Kind() == "augmented_assignment" {
					uncertainBindings(a)
				}
			}
		case "delete_statement":
			uncertainBindings(n)
		}
	}
	// Module decorators and include calls are syntactic facts; other dynamic
	// registrations are diagnostics, never guessed endpoints.
	for i := uint(0); i < root.NamedChildCount(); i++ {
		n := root.NamedChild(i)
		if n.Kind() == "expression_statement" && n.NamedChildCount() == 1 {
			call := n.NamedChild(0)
			if call.Kind() == "call" {
				fn := call.ChildByFieldName("function")
				if fn != nil && fn.Kind() == "attribute" && text(fn.ChildByFieldName("attribute")) == "include_router" {
					args := call.ChildByFieldName("arguments")
					prefix, ok := pyKeywordLiteral(args, b, "prefix")
					if !ok || args == nil || args.NamedChildCount() == 0 {
						warn("dynamic include_router registration is unsupported")
						continue
					}
					child := args.NamedChild(0)
					if child.Kind() != "identifier" && child.Kind() != "attribute" {
						warn("dynamic include_router target is unsupported")
						continue
					}
					m.includes = append(m.includes, pyInclude{parent: text(fn.ChildByFieldName("object")), child: text(child), prefix: prefix, line: line(call)})
				}
			}
		}
		if n.Kind() != "decorated_definition" {
			continue
		}
		for j := uint(0); j < n.NamedChildCount(); j++ {
			d := n.NamedChild(j)
			if d.Kind() != "decorator" || d.NamedChildCount() != 1 {
				continue
			}
			call := d.NamedChild(0)
			if call.Kind() != "call" {
				continue
			}
			fn := call.ChildByFieldName("function")
			if fn == nil || fn.Kind() != "attribute" {
				continue
			}
			method := text(fn.ChildByFieldName("attribute"))
			switch method {
			case "get", "post", "put", "patch", "delete", "head", "options", "trace":
			default:
				continue
			}
			args := call.ChildByFieldName("arguments")
			if args == nil || args.NamedChildCount() == 0 {
				continue
			}
			url, ok := pyString(args.NamedChild(0), b)
			response := ""
			for k := uint(0); k < args.NamedChildCount(); k++ {
				arg := args.NamedChild(k)
				if arg.Kind() == "dictionary_splat" || arg.Kind() == "list_splat" {
					warn("route decorator dynamic argument expansion is unsupported")
				}
				if arg.Kind() == "keyword_argument" {
					option := text(arg.ChildByFieldName("name"))
					if strings.HasPrefix(option, "response_model_") || option == "response_class" {
						warn("route decorator runtime response configuration " + option + " is unsupported")
					}
				}
				if arg.Kind() == "keyword_argument" && text(arg.ChildByFieldName("name")) == "response_model" {
					v := arg.ChildByFieldName("value")
					if v != nil && (v.Kind() == "identifier" || v.Kind() == "attribute") {
						response = text(v)
					}
				}
			}
			if !ok || response == "" {
				warn("dynamic route path or response_model is unresolved")
				continue
			}
			m.routes = append(m.routes, pyRoute{router: text(fn.ChildByFieldName("object")), method: strings.ToUpper(method), url: url, response: response, line: line(d)})
		}
	}
	return m, ds, nil
}
func pyString(n *sitter.Node, b []byte) (string, bool) {
	if n == nil || n.Kind() != "string" {
		return "", false
	}
	s := n.Utf8Text(b)
	if len(s) < 2 || (s[0] != '\'' && s[0] != '"') || s[len(s)-1] != s[0] || strings.Contains(s, "\\") || strings.Contains(s, "\n") || strings.HasPrefix(s, "\"\"\"") || strings.HasPrefix(s, "'''") {
		return "", false
	}
	return s[1 : len(s)-1], true
}
func pyKeywordLiteral(args *sitter.Node, b []byte, name string) (string, bool) {
	value, valid := "", true
	found := false
	if args == nil {
		return "", false
	}
	for i := uint(0); i < args.NamedChildCount(); i++ {
		arg := args.NamedChild(i)
		if arg.Kind() == "dictionary_splat" || arg.Kind() == "list_splat" {
			return "", false
		}
		if arg.Kind() == "keyword_argument" {
			n := arg.ChildByFieldName("name")
			if n != nil && n.Utf8Text(b) == name {
				if found {
					return "", false
				}
				found = true
				value, valid = pyString(arg.ChildByFieldName("value"), b)
			}
		}
	}
	return value, valid
}
