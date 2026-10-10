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
	x := &pyResolver{revision: revision, modules: map[string]*pyModule{}, diagnostics: []model.Diagnostic{}, files: []string{}, schemaFields: map[string][]string{}, modelState: map[string]int{}, registrations: map[string][]pyRegistration{}, graph: map[string][]pyEdge{}}
	for file := range sources {
		if path.Ext(file) == ".py" {
			x.files = append(x.files, file)
		}
	}
	sort.Strings(x.files)
	for _, file := range x.files {
		if err := ctx.Err(); err != nil {
			return nil, nil, x.diagnostics, err
		}
		m, ds, err := parsePyModule(ctx, file, sources[file])
		if err != nil {
			return nil, nil, x.diagnostics, err
		}
		x.modules[file] = m
		x.diagnostics = append(x.diagnostics, ds...)
	}
	x.resolveRouters()
	schemas := x.schemas()
	roots := x.routerGraph()
	for _, root := range roots {
		x.register(root, "", nil, map[string]bool{}, 0)
	}
	endpoints := x.endpoints()
	sort.Slice(x.diagnostics, func(i, j int) bool {
		return x.diagnostics[i].Path+x.diagnostics[i].Message < x.diagnostics[j].Path+x.diagnostics[j].Message
	})
	return schemas, endpoints, x.diagnostics, ctx.Err()
}

// pyResolver resolves symbols across the parsed modules of one revision.
type pyResolver struct {
	revision    string
	modules     map[string]*pyModule
	files       []string
	diagnostics []model.Diagnostic
	// schemaFields and modelState memoize model field resolution; state 1
	// marks a model being resolved, 2 a finished one.
	schemaFields map[string][]string
	modelState   map[string]int
	// registrations maps a router key to its statically resolved mounts.
	registrations             map[string][]pyRegistration
	graph                     map[string][]pyEdge
	registrationCount         int
	registrationLimitReported bool
}

type pyRegistration struct {
	prefix    string
	locations []model.Provenance
}

type pyEdge struct {
	child, prefix string
	location      model.Provenance
	relationships []model.Provenance
}

// pyKey names an unambiguous file and symbol, rather than a matching name.
func pyKey(file, name string) string { return file + "#" + name }

func pySplit(k string) (string, string) {
	parts := strings.SplitN(k, "#", 2)
	if len(parts) != 2 {
		return "", k
	}
	return parts[0], parts[1]
}

func (x *pyResolver) warn(file, message string) {
	x.diagnostics = append(x.diagnostics, model.Diagnostic{Path: file, Message: message, Severity: model.SeverityWarning})
}

// resolve follows imports from file to the module that defines name.
func (x *pyResolver) resolve(file, name string, seen map[string]bool, depth int) (string, []model.Provenance) {
	k := pyKey(file, name)
	if depth > 32 || seen[k] {
		return "", nil
	}
	seen[k] = true
	m := x.modules[file]
	if m == nil || m.wildcard || m.ambiguous[name] {
		return "", nil
	}
	parts := strings.SplitN(name, ".", 2)
	if len(parts) == 2 {
		imp, ok := m.imports[parts[0]]
		if !ok || imp.name != "" {
			return "", nil
		}
		if (imp.module == "fastapi" || imp.module == "pydantic") && pythonModuleFile(file, imp.module, x.modules) == "" {
			return imp.module + "." + parts[1], []model.Provenance{provenance(x.revision, file, "python-import-resolution", imp.line)}
		}
		target := pythonModuleFile(file, imp.module, x.modules)
		if target == "" {
			return "", nil
		}
		targetKey, rel := x.resolve(target, parts[1], seen, depth+1)
		return targetKey, append([]model.Provenance{provenance(x.revision, file, "python-import-resolution", imp.line)}, rel...)
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
	p := provenance(x.revision, file, "python-import-resolution", imp.line)
	if (imp.module == "pydantic" || imp.module == "fastapi") && pythonModuleFile(file, imp.module, x.modules) == "" {
		return imp.module + "." + imp.name, []model.Provenance{p}
	}
	target := pythonModuleFile(file, imp.module, x.modules)
	if target == "" {
		return "", nil
	}
	// A from-package import may name a submodule, e.g. from . import routes.
	if imp.name == "" {
		return "", nil
	}
	targetKey, rel := x.resolve(target, imp.name, seen, depth+1)
	return targetKey, append([]model.Provenance{p}, rel...)
}

// resolveRouters keeps only routers constructed by FastAPI. Constructors are
// resolved only after all modules are available.
func (x *pyResolver) resolveRouters() {
	// Resolve constructors only after all x.modules are available.
	for _, file := range x.files {
		m := x.modules[file]
		for name, router := range m.routers {
			kind, _ := x.resolve(file, router.kind, map[string]bool{}, 0)
			if kind != "fastapi.FastAPI" && kind != "fastapi.APIRouter" {
				delete(m.routers, name)
				continue
			}
			router.kind = kind
			m.routers[name] = router
		}
	}
}

// fields resolves the field names of a pydantic model, including inherited
// and nested model fields.
func (x *pyResolver) fields(k string, depth int) ([]string, bool) {
	if depth > 32 || x.modelState[k] == 1 {
		return nil, false
	}
	if x.modelState[k] == 2 {
		v, ok := x.schemaFields[k]
		return v, ok
	}
	file, name := pySplit(k)
	m := x.modules[file]
	if m == nil || m.wildcard || m.ambiguous[name] {
		return nil, false
	}
	c, ok := m.classes[name]
	if !ok || len(c.bases) != 1 {
		return nil, false
	}
	x.modelState[k] = 1
	base, _ := x.resolve(file, c.bases[0], map[string]bool{}, 0)
	inherited := []string{}
	valid := base == "pydantic.BaseModel"
	if !valid && base != "" {
		inherited, valid = x.fields(base, depth+1)
	}
	if !valid {
		x.modelState[k] = 2
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
		typ, _ := x.resolve(file, field.typ, map[string]bool{}, 0)
		if typ != "" && typ != "pydantic.BaseModel" {
			if nested, found := x.fields(typ, depth+1); found {
				for _, n := range nested {
					names[field.name+"."+n] = true
					if len(names) > 10000 {
						x.warn(file, "nested model field limit exceeded; schema unresolved")
						x.modelState[k] = 2
						return nil, false
					}
				}
			} else if typ == k || x.modelState[typ] == 1 {
				x.warn(file, "recursive model field "+name+"."+field.name+" is unresolved")
			}
		}
	}
	out := []string{}
	for n := range names {
		out = append(out, n)
	}
	sort.Strings(out)
	x.schemaFields[k] = out
	x.modelState[k] = 2
	return out, true
}

// schemas resolves every model class to a schema.
func (x *pyResolver) schemas() []Schema {
	schemas := []Schema{}
	for _, file := range x.files {
		m := x.modules[file]
		names := []string{}
		for name := range m.classes {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			c := m.classes[name]
			fs, ok := x.fields(pyKey(file, name), 0)
			if !ok {
				if len(c.bases) > 0 {
					x.warn(file, "model "+name+" inheritance is unresolved or unsupported")
				}
				continue
			}
			schemas = append(schemas, Schema{ID: model.ContractID(file, "/models/"+name), Name: name, Path: file, Pointer: "/models/" + name, Fields: fs, Evidence: provenance(x.revision, file, "tree-sitter-pydantic-field-resolution", c.line)})
		}
	}
	return schemas
}

// routerGraph records include_router edges and returns the sorted FastAPI
// application roots.
func (x *pyResolver) routerGraph() []string {
	roots := []string{}
	for _, file := range x.files {
		m := x.modules[file]
		names := []string{}
		for n := range m.routers {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, name := range names {
			r := m.routers[name]
			if r.kind == "fastapi.FastAPI" {
				roots = append(roots, pyKey(file, name))
			}
		}
		for _, in := range m.includes {
			parent, parentRel := x.resolve(file, in.parent, map[string]bool{}, 0)
			child, childRel := x.resolve(file, in.child, map[string]bool{}, 0)
			cf, cn := pySplit(child)
			cm := x.modules[cf]
			if parent == "" || cm == nil {
				x.warn(file, "include_router target is unresolved")
				continue
			}
			r, ok := cm.routers[cn]
			if !ok || r.kind != "fastapi.APIRouter" {
				x.warn(file, "include_router target is not a resolved APIRouter")
				continue
			}
			x.graph[parent] = append(x.graph[parent], pyEdge{child: child, prefix: in.prefix + r.prefix, location: provenance(x.revision, file, "fastapi-literal-router-registration", in.line), relationships: append(parentRel, childRel...)})
		}
	}
	sort.Strings(roots)
	return roots
}

// register mounts router k and, recursively, the routers it includes.
func (x *pyResolver) register(k, prefix string, locs []model.Provenance, seen map[string]bool, depth int) {
	x.registrationCount++
	if x.registrationCount > 10000 {
		if !x.registrationLimitReported {
			f, _ := pySplit(k)
			x.warn(f, "router registration limit exceeded; coverage incomplete")
			x.registrationLimitReported = true
		}
		return
	}
	if depth > 32 || seen[k] {
		f, _ := pySplit(k)
		x.warn(f, "router registration cycle or depth limit is unresolved")
		return
	}
	next := map[string]bool{}
	for k, v := range seen {
		next[k] = v
	}
	next[k] = true
	x.registrations[k] = append(x.registrations[k], pyRegistration{prefix, locs})
	for _, e := range x.graph[k] {
		chronologySupported := true
		for _, observed := range locs {
			if observed.Method == "fastapi-literal-router-registration" && observed.Path == e.location.Path && observed.Line < e.location.Line {
				x.warn(e.location.Path, "router registration occurs after parent inclusion; captured route chronology is unresolved")
				chronologySupported = false
				break
			}
		}
		if !chronologySupported {
			continue
		}
		locations := append(append([]model.Provenance{}, locs...), e.location)
		locations = append(locations, e.relationships...)
		x.register(e.child, prefix+e.prefix, locations, next, depth+1)
	}
}

// endpoints joins routes with their resolved registrations and response models.
func (x *pyResolver) endpoints() []endpoint {
	endpoints := []endpoint{}
	for _, file := range x.files {
		m := x.modules[file]
		for _, route := range m.routes {
			rk, routerRel := x.resolve(file, route.router, map[string]bool{}, 0)
			modelKey, modelRel := x.resolve(file, route.response, map[string]bool{}, 0)
			mf, mn := pySplit(modelKey)
			if _, ok := x.schemaFields[modelKey]; !ok {
				x.warn(file, "response_model "+route.response+" is unresolved")
				continue
			}
			regs := x.registrations[rk]
			if len(regs) == 0 {
				x.warn(file, "route on "+route.router+" has no statically resolved FastAPI registration")
				continue
			}
			if len(regs) > 1 {
				x.warn(file, "route has multiple visible registrations; deployment origin relationship remains unresolved")
			}
			for _, reg := range regs {
				chronologySupported := true
				for _, observed := range reg.locations {
					if observed.Method == "fastapi-literal-router-registration" && observed.Path == file && observed.Line < route.line {
						x.warn(file, "route decorator occurs after router inclusion; route is absent from the captured registration")
						chronologySupported = false
						break
					}
				}
				if !chronologySupported {
					continue
				}
				locations := append(append([]model.Provenance{}, reg.locations...), routerRel...)
				locations = append(locations, modelRel...)
				endpoints = append(endpoints, endpoint{url: reg.prefix + route.url, schema: model.ContractID(mf, "/models/"+mn), method: route.method, location: provenance(x.revision, file, "fastapi-literal-response-model", route.line), relationships: locations})
			}
		}
	}
	sort.Slice(endpoints, func(i, j int) bool {
		a, b := endpoints[i], endpoints[j]
		return a.url+"\x00"+a.method+"\x00"+a.schema+a.location.Path < b.url+"\x00"+b.method+"\x00"+b.schema+b.location.Path
	})
	return endpoints
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
	x := &pyParser{m: m, ds: []model.Diagnostic{}}
	p := sitter.NewParser()
	defer p.Close()
	if err := p.SetLanguage(sitter.NewLanguage(pyGrammar.Language())); err != nil {
		return m, x.ds, err
	}
	p.SetTimeoutMicros(5_000_000)
	b := []byte(source)
	x.b = b
	tree := p.Parse(b, nil)
	if tree == nil {
		return m, x.ds, fmt.Errorf("Python syntax parse failed for %s", file)
	}
	defer tree.Close()
	if err := ctx.Err(); err != nil {
		return m, x.ds, err
	}
	root := tree.RootNode()
	if root.HasError() {
		x.warn("Python syntax errors prevent authoritative discovery")
		return m, x.ds, nil
	}
	x.declarations(root)
	x.conditionalBindings(root)
	x.registrations(root)
	return m, x.ds, nil
}

// pyParser collects the module-level facts of one Python source file.
type pyParser struct {
	m      *pyModule
	ds     []model.Diagnostic
	b      []byte
	counts map[string]int
}

func (x *pyParser) warn(msg string) {
	x.ds = append(x.ds, model.Diagnostic{Path: x.m.file, Message: msg, Severity: model.SeverityWarning})
}

func (x *pyParser) text(n *sitter.Node) string {
	if n == nil {
		return ""
	}
	return n.Utf8Text(x.b)
}

func (x *pyParser) line(n *sitter.Node) int { return int(n.StartPosition().Row) + 1 }

func (x *pyParser) define(name string) {
	if x.counts == nil {
		x.counts = map[string]int{}
	}
	x.counts[name]++
	if x.counts[name] > 1 {
		x.m.ambiguous[name] = true
		x.warn("Python symbol " + name + " is multiply defined; resolution unavailable")
	}
}

// declarations records module-level bindings. Only module-level bindings are
// eligible: function locals cannot define a router.
func (x *pyParser) declarations(root *sitter.Node) {
	for i := uint(0); i < root.NamedChildCount(); i++ {
		n := root.NamedChild(i)
		switch n.Kind() {
		case "function_definition":
			x.define(x.text(n.ChildByFieldName("name")))
		case "decorated_definition":
			definition := n.ChildByFieldName("definition")
			if definition != nil {
				x.define(x.text(definition.ChildByFieldName("name")))
				if definition.Kind() == "class_definition" {
					x.warn("decorated Python model declaration is unsupported")
				}
			}
		case "import_from_statement", "import_statement":
			x.imports(n)
		case "class_definition":
			x.class(n)
		case "expression_statement":
			x.assignment(n)
		}
	}
}

// imports records the names bound by a module-level import statement.
func (x *pyParser) imports(n *sitter.Node) {
	module := ""
	if n.Kind() == "import_from_statement" {
		module = x.text(n.ChildByFieldName("module_name"))
	}
	for j := uint(0); j < n.NamedChildCount(); j++ {
		child := n.NamedChild(j)
		if child.Kind() == "wildcard_import" {
			x.m.wildcard = true
			x.warn("wildcard Python import is unresolved")
			continue
		}
		if child.Kind() != "dotted_name" && child.Kind() != "aliased_import" {
			continue
		}
		if n.Kind() == "import_from_statement" && n.ChildByFieldName("module_name") != nil && child.StartByte() == n.ChildByFieldName("module_name").StartByte() {
			continue
		}
		name, alias := x.text(child), ""
		if child.Kind() == "aliased_import" {
			name = x.text(child.ChildByFieldName("name"))
			alias = x.text(child.ChildByFieldName("alias"))
		}
		if alias == "" {
			alias = name
		}
		if n.Kind() == "import_statement" {
			if !strings.Contains(name, ".") || child.Kind() == "aliased_import" {
				x.define(alias)
				x.m.imports[alias] = pyImport{module: name, line: x.line(child)}
			}
		} else {
			x.define(alias)
			x.m.imports[alias] = pyImport{module: module, name: name, line: x.line(child)}
		}
	}
}

// class records a model class and its annotated fields.
func (x *pyParser) class(n *sitter.Node) {
	name := x.text(n.ChildByFieldName("name"))
	x.define(name)
	c := pyClass{name: name, line: x.line(n)}
	base := n.ChildByFieldName("superclasses")
	if base != nil {
		for j := uint(0); j < base.NamedChildCount(); j++ {
			c.bases = append(c.bases, x.text(base.NamedChild(j)))
		}
	}
	body := n.ChildByFieldName("body")
	if body != nil {
		for j := uint(0); j < body.NamedChildCount(); j++ {
			child := body.NamedChild(j)
			if child.Kind() == "decorated_definition" {
				x.warn("model " + name + " decorated runtime behavior is unsupported")
			}
			if child.Kind() != "expression_statement" || child.NamedChildCount() != 1 {
				continue
			}
			a := child.NamedChild(0)
			if a.Kind() != "assignment" {
				continue
			}
			left, typ := a.ChildByFieldName("left"), a.ChildByFieldName("type")
			if left != nil && x.text(left) == "model_config" {
				x.warn("model " + name + " runtime model_config is unsupported")
				continue
			}
			if left != nil && left.Kind() == "identifier" && typ != nil {
				tn := typ
				if typ.NamedChildCount() == 1 {
					tn = typ.NamedChild(0)
				}
				annotation := x.text(tn)
				if strings.HasPrefix(x.text(left), "_") {
					x.warn("private model attribute " + name + "." + x.text(left) + " is excluded from response field observations")
					continue
				}
				classVariable := strings.HasPrefix(annotation, "ClassVar[") || strings.Contains(annotation, ".ClassVar[")
				if bracket := strings.IndexByte(annotation, '['); bracket > 0 {
					if imported, ok := x.m.imports[annotation[:bracket]]; ok && imported.module == "typing" && imported.name == "ClassVar" {
						classVariable = true
					}
				}
				if classVariable {
					x.warn("ClassVar model attribute " + name + "." + x.text(left) + " is excluded from response field observations")
					continue
				}
				if strings.Contains(annotation, "[") || tn.Kind() == "string" || tn.Kind() == "binary_operator" {
					x.warn("complex model annotation " + name + "." + x.text(left) + " has unresolved nested shape")
				}
				if value := a.ChildByFieldName("right"); value != nil && value.Kind() == "call" {
					x.warn("model " + name + "." + x.text(left) + " runtime field configuration is unsupported")
				}
				c.fields = append(c.fields, pyField{name: x.text(left), typ: annotation})
			}
		}
	}
	x.m.classes[name] = c
}

// assignment records a module-level assignment, including router creation.
func (x *pyParser) assignment(n *sitter.Node) {
	if n.NamedChildCount() != 1 {
		return
	}
	a := n.NamedChild(0)
	if a.Kind() == "assignment" {
		left, right := a.ChildByFieldName("left"), a.ChildByFieldName("right")
		if left == nil || left.Kind() != "identifier" {
			return
		}
		name := x.text(left)
		x.define(name)
		if right != nil && right.Kind() == "call" {
			fn := x.text(right.ChildByFieldName("function"))
			prefix, ok := pyKeywordLiteral(right.ChildByFieldName("arguments"), x.b, "prefix")
			if !ok {
				x.warn("router prefix is dynamic; registration unresolved")
				return
			}
			x.m.routers[name] = pyRouter{kind: fn, prefix: prefix, line: x.line(a)}
		}
	}
}

// uncertainBindings marks names bound under conditional module code as
// ambiguous. Function/class bodies have their own scopes.
func (x *pyParser) uncertainBindings(n *sitter.Node) {
	switch n.Kind() {
	case "identifier":
		name := x.text(n)
		x.m.ambiguous[name] = true
		x.warn("conditional or destructured Python binding " + name + " is unresolved")
		return
	case "function_definition", "class_definition":
		name := x.text(n.ChildByFieldName("name"))
		x.m.ambiguous[name] = true
		x.warn("conditional Python declaration " + name + " is unresolved")
		return
	case "assignment", "augmented_assignment":
		if left := n.ChildByFieldName("left"); left != nil {
			x.uncertainBindings(left)
		}
		return
	case "import_statement", "import_from_statement":
		for j := uint(0); j < n.NamedChildCount(); j++ {
			child := n.NamedChild(j)
			if child.Kind() == "wildcard_import" {
				x.m.wildcard = true
				x.warn("conditional wildcard Python import is unresolved")
				continue
			}
			if child.Kind() != "dotted_name" && child.Kind() != "aliased_import" {
				continue
			}
			module := n.ChildByFieldName("module_name")
			if module != nil && child.StartByte() == module.StartByte() {
				continue
			}
			name := x.text(child)
			if child.Kind() == "aliased_import" {
				name = x.text(child.ChildByFieldName("alias"))
			}
			x.m.ambiguous[name] = true
			x.warn("conditional Python import " + name + " is unresolved")
		}
		return
	case "call":
		fn := n.ChildByFieldName("function")
		if fn != nil && fn.Kind() == "attribute" && x.text(fn.ChildByFieldName("attribute")) == "include_router" {
			x.warn("conditional include_router registration is unsupported")
		}
		return
	}
	// Bare expressions and conditions do not bind their identifiers.
	if n.Kind() == "attribute" || n.Kind() == "binary_operator" || n.Kind() == "comparison_operator" {
		return
	}
	for j := uint(0); j < n.NamedChildCount(); j++ {
		x.uncertainBindings(n.NamedChild(j))
	}
}

// conditionalBindings handles module bindings that may shadow a previously
// resolved import, router, or model.
func (x *pyParser) conditionalBindings(root *sitter.Node) {
	for i := uint(0); i < root.NamedChildCount(); i++ {
		n := root.NamedChild(i)
		switch n.Kind() {
		case "if_statement", "for_statement", "while_statement", "try_statement", "with_statement", "match_statement":
			// Traverse only suites, except loop/with binding targets.
			for j := uint(0); j < n.NamedChildCount(); j++ {
				child := n.NamedChild(j)
				if child.Kind() == "block" || strings.HasSuffix(child.Kind(), "clause") {
					x.uncertainBindings(child)
				}
			}
			if left := n.ChildByFieldName("left"); left != nil {
				x.uncertainBindings(left)
			}
		case "expression_statement":
			if n.NamedChildCount() == 1 {
				a := n.NamedChild(0)
				if a.Kind() == "assignment" {
					left := a.ChildByFieldName("left")
					if left != nil && left.Kind() != "identifier" {
						x.uncertainBindings(left)
					}
				} else if a.Kind() == "augmented_assignment" {
					x.uncertainBindings(a)
				}
			}
		case "delete_statement":
			x.uncertainBindings(n)
		}
	}
}

// registrations records include_router calls and route decorators. Module
// decorators and include calls are syntactic facts; other dynamic
// registrations are diagnostics, never guessed endpoints.
func (x *pyParser) registrations(root *sitter.Node) {
	for i := uint(0); i < root.NamedChildCount(); i++ {
		n := root.NamedChild(i)
		if n.Kind() == "expression_statement" && n.NamedChildCount() == 1 {
			call := n.NamedChild(0)
			if call.Kind() == "call" {
				fn := call.ChildByFieldName("function")
				if fn != nil && fn.Kind() == "attribute" && x.text(fn.ChildByFieldName("attribute")) == "include_router" {
					x.includeRouter(call, fn)
				}
			}
		}
		if n.Kind() != "decorated_definition" {
			continue
		}
		x.routes(n)
	}
}

func (x *pyParser) includeRouter(call, fn *sitter.Node) {
	args := call.ChildByFieldName("arguments")
	prefix, ok := pyKeywordLiteral(args, x.b, "prefix")
	if !ok || args == nil || args.NamedChildCount() == 0 {
		x.warn("dynamic include_router registration is unsupported")
		return
	}
	child := args.NamedChild(0)
	if child.Kind() != "identifier" && child.Kind() != "attribute" {
		x.warn("dynamic include_router target is unsupported")
		return
	}
	x.m.includes = append(x.m.includes, pyInclude{parent: x.text(fn.ChildByFieldName("object")), child: x.text(child), prefix: prefix, line: x.line(call)})
}

// routes records the HTTP route decorators of a decorated definition.
func (x *pyParser) routes(n *sitter.Node) {
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
		method := x.text(fn.ChildByFieldName("attribute"))
		switch method {
		case "get", "post", "put", "patch", "delete", "head", "options", "trace":
		default:
			continue
		}
		args := call.ChildByFieldName("arguments")
		if args == nil || args.NamedChildCount() == 0 {
			continue
		}
		url, ok := pyString(args.NamedChild(0), x.b)
		response := ""
		for k := uint(0); k < args.NamedChildCount(); k++ {
			arg := args.NamedChild(k)
			if arg.Kind() == "dictionary_splat" || arg.Kind() == "list_splat" {
				x.warn("route decorator dynamic argument expansion is unsupported")
			}
			if arg.Kind() == "keyword_argument" {
				option := x.text(arg.ChildByFieldName("name"))
				if strings.HasPrefix(option, "response_model_") || option == "response_class" {
					x.warn("route decorator runtime response configuration " + option + " is unsupported")
				}
			}
			if arg.Kind() == "keyword_argument" && x.text(arg.ChildByFieldName("name")) == "response_model" {
				v := arg.ChildByFieldName("value")
				if v != nil && (v.Kind() == "identifier" || v.Kind() == "attribute") {
					response = x.text(v)
				}
			}
		}
		if !ok || response == "" {
			x.warn("dynamic route path or response_model is unresolved")
			continue
		}
		x.m.routes = append(x.m.routes, pyRoute{router: x.text(fn.ChildByFieldName("object")), method: strings.ToUpper(method), url: url, response: response, line: x.line(d)})
	}
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
