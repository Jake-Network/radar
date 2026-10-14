package discovery

import (
	"context"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/Jake-Network/radar/internal/model"
)

// Route records a literal method/path registration within a resolved application.
// Scope separates independent applications that legitimately share paths.
type Route struct {
	Scope, Method, Path string
	Location            model.Provenance
}

// Routes reads source data only. Dynamic mounts and unresolved registrations are
// diagnostics; their absence is not proof that an application has no routes.
func Routes(ctx context.Context, sources map[string]string, revision string) ([]Route, []model.Diagnostic, error) {
	x := &pyResolver{revision: revision, modules: map[string]*pyModule{}, diagnostics: []model.Diagnostic{}, files: []string{}, registrations: map[string][]pyRegistration{}, graph: map[string][]pyEdge{}}
	hasFastAPI := false
	for _, source := range sources {
		if strings.Contains(source, "fastapi") {
			hasFastAPI = true
			break
		}
	}
	for file, source := range sources {
		if !hasFastAPI || path.Ext(file) != ".py" {
			continue
		}
		x.files = append(x.files, file)
		// The contract parser requires response_model; route identity does not.
		// Supply an inert identifier as source data, retaining the original lines.
		transformed := routeDecorator.ReplaceAllStringFunc(source, func(s string) string {
			if strings.Contains(s, "response_model") {
				return s
			}
			return strings.TrimSuffix(s, ")") + ", response_model=_RadarRoute)"
		})
		m, ds, e := parsePyModule(ctx, file, transformed)
		if e != nil {
			return nil, nil, e
		}
		x.modules[file] = m
		for _, d := range ds {
			if strings.Contains(d.Message, "route") || strings.Contains(d.Message, "router") {
				x.diagnostics = append(x.diagnostics, d)
			}
		}
	}
	sort.Strings(x.files)
	x.resolveRouters()
	roots := x.routerGraph()
	out := []Route{}
	for _, root := range roots {
		x.registrations = map[string][]pyRegistration{}
		x.registrationCount = 0
		x.register(root, "", nil, map[string]bool{}, 0)
		for _, file := range x.files {
			for _, route := range x.modules[file].routes {
				key, _ := x.resolve(file, route.router, map[string]bool{}, 0)
				for _, reg := range x.registrations[key] {
					captured := true
					for _, l := range reg.locations {
						if l.Method == "fastapi-literal-router-registration" && l.Path == file && l.Line < route.line {
							captured = false
						}
					}
					if captured {
						out = append(out, Route{root, route.method, reg.prefix + route.url, provenance(revision, file, "fastapi-literal-route", route.line)})
					}
				}
			}
		}
	}
	files := []string{}
	for f := range sources {
		files = append(files, f)
	}
	sort.Strings(files)
	for _, file := range files {
		source := stripRouteComments(sources[file])
		ext := path.Ext(file)
		if ext == ".js" || ext == ".ts" || ext == ".go" || ext == ".rs" {
			rs, ds := literalRoutes(file, source, revision)
			out = append(out, rs...)
			x.diagnostics = append(x.diagnostics, ds...)
		} else if ext == ".java" {
			rs, ds := springRoutes(file, source, revision, sources)
			out = append(out, rs...)
			x.diagnostics = append(x.diagnostics, ds...)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return fmt.Sprintf("%s:%s:%s:%s:%d", out[i].Scope, out[i].Method, out[i].Path, out[i].Location.Path, out[i].Location.Line) < fmt.Sprintf("%s:%s:%s:%s:%d", out[j].Scope, out[j].Method, out[j].Path, out[j].Location.Path, out[j].Location.Line)
	})
	return out, x.diagnostics, ctx.Err()
}

var routeDecorator = regexp.MustCompile(`(?m)@\w+\.(?:get|post|put|patch|delete|head|options|trace)\([^\n]*\)`)
var receiverRoute = regexp.MustCompile(`(?m)^\s*(\w+)\.(get|post|put|patch|delete|head|options|GET|POST|PUT|PATCH|DELETE|HEAD|OPTIONS)\(\s*(["'])([^"'\n]*)["']`)
var receiverConstruction = regexp.MustCompile(`(?m)(?:const|let|var)?\s*(\w+)\s*(?::=|=)\s*(express\(\)|express\.Router\(\)|gin\.(?:Default|New)\(\))`)

func literalRoutes(file, source, revision string) ([]Route, []model.Diagnostic) {
	out := []Route{}
	ds := []model.Diagnostic{}
	mask := routeCodeMask(source)
	trusted := map[string]bool{}
	constructors := map[string]int{}
	// Alias/module validation is intentionally literal: ambiguous aliases remain unknown.
	express := false
	for _, m := range expressImport.FindAllStringIndex(source, -1) {
		if strings.TrimSpace(mask[m[0]:m[1]]) != "" {
			express = true
		}
	}
	gin := false
	for _, m := range ginImport.FindAllStringIndex(source, -1) {
		if strings.Contains(mask[m[0]:m[1]], "import") {
			gin = true
		}
	}
	if routerFactoryShadow.MatchString(mask) {
		express = false
		gin = false
	}
	for _, m := range goImportBlock.FindAllStringSubmatchIndex(source, -1) {
		if strings.Contains(mask[m[0]:m[2]], "import") && ginImportLine.MatchString(source[m[2]:m[3]]) {
			gin = true
		}
	}
	if routerFactoryShadow.MatchString(mask) {
		express = false
		gin = false
	}
	hasFramework := strings.Contains(source, "express") || strings.Contains(source, "github.com/gin-gonic/gin")
	for _, m := range receiverConstruction.FindAllStringSubmatchIndex(source, -1) {
		name, construction := source[m[2]:m[3]], source[m[4]:m[5]]
		if strings.TrimSpace(mask[m[2]:m[3]]) == "" {
			continue
		}
		if strings.HasPrefix(construction, "express") && express || strings.HasPrefix(construction, "gin") && gin {
			scope := routeScope(mask, m[2])
			trusted[name+"@"+scope] = true
			constructors[name+"@"+scope]++
		}
	}
	for name, count := range constructors {
		if count > 1 {
			delete(trusted, name)
			ds = append(ds, model.Diagnostic{Path: file, Message: "Multiple router constructions obscure lexical scope for " + name, Severity: model.SeverityWarning})
		}
	}
	literalStarts := map[int]bool{}
	for _, m := range receiverRoute.FindAllStringSubmatchIndex(source, -1) {
		literalStarts[m[0]] = true
	}
	for _, m := range receiverAnyRoute.FindAllStringSubmatchIndex(source, -1) {
		if strings.TrimSpace(mask[m[2]:m[3]]) != "" && trusted[source[m[2]:m[3]]+"@"+routeScope(mask, m[2])] && !literalStarts[m[0]] {
			ds = append(ds, model.Diagnostic{Path: file, Message: "Dynamic route path is unresolved.", Severity: model.SeverityWarning})
		}
	}
	for _, m := range receiverRoute.FindAllStringSubmatchIndex(source, -1) {
		receiver := source[m[2]:m[3]]
		if strings.TrimSpace(mask[m[2]:m[3]]) == "" {
			continue
		}
		scope := routeScope(mask, m[2])
		if !trusted[receiver+"@"+scope] {
			if hasFramework {
				ds = append(ds, model.Diagnostic{Path: file, Message: "Route receiver lexical scope or registration is unresolved.", Severity: model.SeverityWarning})
			}
			continue
		}
		line := lineAt(source, m[0])
		if strings.Contains(source, receiver+".use(") || strings.Contains(source, receiver+".Group(") {
			ds = append(ds, model.Diagnostic{Path: file, Message: "Router middleware/group prefixes are unresolved for " + receiver, Severity: model.SeverityWarning})
			continue
		}
		out = append(out, Route{file + "#" + receiver + "@" + scope, strings.ToUpper(source[m[4]:m[5]]), source[m[8]:m[9]], provenance(revision, file, "literal-router-route", line)})
	}
	if path.Ext(file) == ".rs" && strings.Contains(source, "axum") {
		rs, more := axumRoutes(file, source, revision)
		out = append(out, rs...)
		ds = append(ds, more...)
	}
	return out, ds
}

var springAnnotation = regexp.MustCompile(`@(Get|Post|Put|Patch|Delete)Mapping\(\s*(?:value\s*=\s*|path\s*=\s*)?["']([^"']*)["']\s*\)`)
var springPrefix = regexp.MustCompile(`@RequestMapping\(\s*(?:value\s*=\s*|path\s*=\s*)?["']([^"']*)["']\s*\)`)

func springRoutes(file, source, revision string, sources map[string]string) ([]Route, []model.Diagnostic) {
	boundSpring := false
	mask := routeCodeMask(source)
	for _, m := range springImport.FindAllStringIndex(source, -1) {
		if strings.Contains(mask[m[0]:m[1]], "import") {
			boundSpring = true
		}
	}
	if !boundSpring {
		return nil, nil
	}
	out := []Route{}
	ds := []model.Diagnostic{}
	class := strings.Index(mask, "class ")
	if class < 0 {
		return nil, nil
	}
	if !strings.Contains(mask[:class], "@RestController") && !strings.Contains(mask[:class], "@Controller") {
		return nil, []model.Diagnostic{{Path: file, Message: "Spring mapping class is not a resolved controller.", Severity: model.SeverityWarning}}
	}
	if strings.Count(mask, "class ") > 1 {
		return nil, []model.Diagnostic{{Path: file, Message: "Multiple Spring classes require separate prefix/scope resolution.", Severity: model.SeverityWarning}}
	}
	prefix := ""
	before := source[:class]
	if strings.Contains(mask[:class], "@RequestMapping") {
		m := springPrefix.FindStringSubmatch(before)
		if m == nil {
			return nil, []model.Diagnostic{{Path: file, Message: "Dynamic Spring class mapping is unresolved.", Severity: model.SeverityWarning}}
		}
		prefix = m[1]
	}
	root := path.Dir(file)
	for d := path.Dir(file); ; d = path.Dir(d) {
		if _, ok := sources[path.Join(d, "pom.xml")]; ok {
			root = d
			break
		}
		if _, ok := sources[path.Join(d, "build.gradle")]; ok {
			root = d
			break
		}
		if d == "." {
			break
		}
	}
	methodMappings := springAnnotation.FindAllStringSubmatchIndex(source, -1)
	recognizedMappings := 0
	for _, m := range methodMappings {
		if mask[m[0]] == '@' {
			recognizedMappings++
		}
	}
	if recognizedMappings < len(springAnyAnnotation.FindAllStringIndex(mask, -1)) {
		ds = append(ds, model.Diagnostic{Path: file, Message: "Dynamic Spring method/path mapping is unresolved.", Severity: model.SeverityWarning})
	}
	for _, m := range methodMappings {
		if m[0] < class || mask[m[0]] != '@' {
			continue
		}
		out = append(out, Route{root + "#spring", strings.ToUpper(source[m[2]:m[3]]), prefix + source[m[4]:m[5]], provenance(revision, file, "spring-literal-route", lineAt(source, m[0]))})
	}
	for _, m := range springRequestMapping.FindAllStringSubmatchIndex(source, -1) {
		if m[0] < class || mask[m[0]] != '@' {
			continue
		}
		out = append(out, Route{root + "#spring", source[m[4]:m[5]], prefix + source[m[2]:m[3]], provenance(revision, file, "spring-literal-route", lineAt(source, m[0]))})
	}
	for _, m := range springRequestAny.FindAllStringIndex(mask, -1) {
		if m[0] < class {
			continue
		}
		recognized := false
		for _, known := range springRequestMapping.FindAllStringIndex(source, -1) {
			if known[0] == m[0] {
				recognized = true
			}
		}
		if !recognized {
			ds = append(ds, model.Diagnostic{Path: file, Message: "Spring RequestMapping method/path composition is unresolved.", Severity: model.SeverityWarning})
		}
	}
	return out, ds
}

var receiverAnyRoute = regexp.MustCompile(`(?m)^\s*(\w+)\.(?:get|post|put|patch|delete|head|options|GET|POST|PUT|PATCH|DELETE|HEAD|OPTIONS)\(`)
var axumBuilder = regexp.MustCompile(`(?s)let\s+(?:mut\s+)?(\w+)\s*=\s*Router::new\(\)([^;]*);`)
var axumRoute = regexp.MustCompile(`\.route\(\s*"([^"\n]*)"\s*,\s*(get|post|put|patch|delete|head|options)\(`)

func axumRoutes(file, source, revision string) ([]Route, []model.Diagnostic) {
	out := []Route{}
	ds := []model.Diagnostic{}
	mask := routeCodeMask(source)
	boundRouter := axumRouterImport.MatchString(mask)
	if !boundRouter || strings.Contains(mask, "struct Router") {
		if strings.Contains(mask, ".route(") {
			return nil, []model.Diagnostic{{Path: file, Message: "Axum Router import/ownership is unresolved.", Severity: model.SeverityWarning}}
		}
		return nil, nil
	}
	builders := axumBuilder.FindAllStringSubmatchIndex(source, -1)
	if strings.Contains(source, ".nest(") || strings.Contains(source, ".merge(") {
		return nil, []model.Diagnostic{{Path: file, Message: "Axum nested/merged Router composition remains unresolved.", Severity: model.SeverityWarning}}
	}
	constructions := map[string]int{}
	for _, b := range builders {
		if strings.TrimSpace(mask[b[2]:b[3]]) == "" {
			continue
		}
		name := source[b[2]:b[3]]
		owner := name + "@" + routeScope(mask, b[2])
		ordinal := constructions[owner]
		constructions[owner]++
		body := source[b[4]:b[5]]
		matches := axumRoute.FindAllStringSubmatchIndex(body, -1)
		if strings.Count(body, ".route(") != len(matches) {
			ds = append(ds, model.Diagnostic{Path: file, Message: "Dynamic Axum path or method is unresolved.", Severity: model.SeverityWarning})
		}
		for _, m := range matches {
			if mask[b[4]+m[0]] != '.' {
				continue
			}
			// Scope separates same-named locals in independent functions. A
			// local ordinal also distinguishes separate constructions there.
			scope := fmt.Sprintf("%s#%s@%d", file, owner, ordinal)
			out = append(out, Route{scope, strings.ToUpper(body[m[4]:m[5]]), body[m[2]:m[3]], provenance(revision, file, "axum-literal-route", lineAt(source, b[4]+m[0]))})
		}
	}
	if len(builders) == 0 && strings.Contains(source, ".route(") {
		ds = append(ds, model.Diagnostic{Path: file, Message: "Axum Router ownership is unresolved.", Severity: model.SeverityWarning})
	}
	return out, ds
}

// stripRouteComments preserves byte offsets and lines while excluding comment
// text from the small literal route recognizers. Quoted strings stay intact.
func stripRouteComments(s string) string {
	b := []byte(s)
	quote := byte(0)
	for i := 0; i < len(b); i++ {
		if quote != 0 {
			if b[i] == '\\' && quote != '`' {
				i++
				continue
			}
			if b[i] == quote {
				quote = 0
			}
			continue
		}
		if b[i] == '"' || b[i] == '\'' || b[i] == '`' {
			quote = b[i]
			continue
		}
		if i+1 < len(b) && b[i] == '/' && b[i+1] == '/' {
			for i < len(b) && b[i] != '\n' {
				b[i] = ' '
				i++
			}
			continue
		}
		if i+1 < len(b) && b[i] == '/' && b[i+1] == '*' {
			b[i] = ' '
			i++
			b[i] = ' '
			for i+1 < len(b) {
				i++
				if b[i] == '*' && b[i+1] == '/' {
					b[i] = ' '
					i++
					b[i] = ' '
					break
				}
				if b[i] != '\n' {
					b[i] = ' '
				}
			}
		}
	}
	return string(b)
}

var expressImport = regexp.MustCompile(`(?:\b(?:const|let|var)\s+express\s*=\s*require\(\s*["']express["']\s*\)|\bimport\s+express\s+from\s+["']express["'])`)

// routeCodeMask removes all quoted string/template bodies while preserving
// positions. Recognizers validate their code token starts against this mask,
// then read literal arguments from the original source.
func routeCodeMask(s string) string {
	b := []byte(stripRouteComments(s))
	for i := 0; i < len(b); i++ {
		if b[i] != '"' && b[i] != '\'' && b[i] != '`' {
			continue
		}
		quote := b[i]
		b[i] = ' '
		for i+1 < len(b) {
			i++
			c := b[i]
			if c != '\n' {
				b[i] = ' '
			}
			if c == '\\' && quote != '`' {
				if i+1 < len(b) {
					i++
					if b[i] != '\n' {
						b[i] = ' '
					}
				}
				continue
			}
			if c == quote {
				break
			}
		}
	}
	return string(b)
}

var routeFunction = regexp.MustCompile(`(?s)(?:function|func|fn)\s+(\w+)\s*\([^{};]*\)\s*(?:->\s*[^{};]+)?$`)

func routeScope(mask string, end int) string {
	stack := []string{}
	last := 0
	for i := 0; i < end && i < len(mask); i++ {
		if mask[i] == '{' {
			header := mask[last:i]
			name := "block"
			if m := routeFunction.FindStringSubmatch(header); m != nil {
				name = "function:" + m[1]
			}
			stack = append(stack, name)
			last = i + 1
		} else if mask[i] == '}' {
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
			last = i + 1
		} else if mask[i] == ';' {
			last = i + 1
		}
	}
	return strings.Join(stack, "/")
}

var springAnyAnnotation = regexp.MustCompile(`@(Get|Post|Put|Patch|Delete)Mapping\(`)

var ginImport = regexp.MustCompile(`(?s)\bimport\s*(?:\(\s*)?"github.com/gin-gonic/gin"`)
var routerFactoryShadow = regexp.MustCompile(`(?m)(?:\bfunction\s+express\s*\(|\bgin\s*(?::=|=)|\b(?:func|function)\s+\w+\s*\([^)]*\b(?:gin|express)\b)`)
var springImport = regexp.MustCompile(`(?m)^\s*import\s+org\.springframework\.web\.bind\.annotation\.[\w*]+\s*;`)
var axumRouterImport = regexp.MustCompile(`(?s)\buse\s+axum::(?:Router\s*;|\{[^}]*\bRouter\b[^}]*\}\s*;)`)

var goImportBlock = regexp.MustCompile(`(?s)\bimport\s*\((.*?)\)`)
var ginImportLine = regexp.MustCompile(`(?m)^\s*"github.com/gin-gonic/gin"\s*$`)
var springRequestMapping = regexp.MustCompile(`@RequestMapping\(\s*(?:(?:value|path)\s*=\s*)?"([^"\n]*)"\s*,\s*method\s*=\s*RequestMethod\.(GET|POST|PUT|PATCH|DELETE|HEAD|OPTIONS|TRACE)\s*\)`)
var springRequestAny = regexp.MustCompile(`@RequestMapping\(`)
