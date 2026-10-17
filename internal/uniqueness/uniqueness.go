// Package uniqueness checks explicitly declared keys in committed source data.
package uniqueness

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/Jake-Network/radar/internal/discovery"
	gitrepo "github.com/Jake-Network/radar/internal/git"
	"github.com/Jake-Network/radar/internal/jsonptr"
	"github.com/Jake-Network/radar/internal/migrations"
	"github.com/Jake-Network/radar/internal/model"
)

const CheckID = "unique_keys"
const ManifestPath = ".radar/unique.json"

const maxKeyObservations = 100000

type Result = migrations.Result
type Rule struct {
	Name        string   `json:"name"`
	Files       []string `json:"files"`
	Pattern     string   `json:"pattern,omitempty"`
	JSONPointer *string  `json:"json_pointer,omitempty"`
}
type Manifest struct {
	Version int    `json:"version"`
	Rules   []Rule `json:"rules"`
}

func Analyze(ctx context.Context, root, base string, inputs []string, combined string, changed []string) (Result, error) {
	trees, e := migrations.LoadTrees(ctx, root, base, inputs, combined)
	if e != nil {
		return Result{}, e
	}
	blobs, e := gitrepo.OpenBlobReader(ctx, root)
	if e != nil {
		return Result{}, e
	}
	defer blobs.Close()
	return Evaluate(ctx, trees, changed, func(oid string) ([]byte, error) { return blobs.Read(oid, 1<<20) })
}
func manifest(t migrations.Tree, read migrations.Reader) (Manifest, bool, error) {
	for _, f := range t.Files {
		if f.Path != ManifestPath {
			continue
		}
		b, e := read(f.OID)
		if e != nil {
			return Manifest{}, true, fmt.Errorf("uniqueness manifest cannot be read")
		}
		if len(b) > 1<<20 {
			return Manifest{}, true, fmt.Errorf("uniqueness manifest exceeds limit")
		}
		var m Manifest
		d := json.NewDecoder(bytes.NewReader(b))
		d.DisallowUnknownFields()
		if e = d.Decode(&m); e != nil {
			return m, true, fmt.Errorf("malformed uniqueness manifest")
		}
		var trailing any
		if e = d.Decode(&trailing); e != io.EOF {
			return m, true, fmt.Errorf("uniqueness manifest contains trailing data")
		}
		if m.Version != 1 || len(m.Rules) == 0 || len(m.Rules) > 128 {
			return m, true, fmt.Errorf("uniqueness manifest requires version 1 and 1..128 rules")
		}
		seen := map[string]bool{}
		for _, r := range m.Rules {
			if r.Name == "" || seen[r.Name] || len(r.Files) == 0 || len(r.Files) > 128 || (r.Pattern == "") == (r.JSONPointer == nil) {
				return m, true, fmt.Errorf("invalid uniqueness rule")
			}
			seen[r.Name] = true
			for _, g := range r.Files {
				if e := validateGlob(g); e != nil {
					return m, true, e
				}
			}
			if r.Pattern != "" {
				if len(r.Pattern) > 4096 {
					return m, true, fmt.Errorf("regex exceeds limit")
				}
				if _, e := regexp.Compile(r.Pattern); e != nil {
					return m, true, fmt.Errorf("invalid uniqueness regex")
				}
			}
			if r.JSONPointer != nil {
				if e := jsonptr.Validate(*r.JSONPointer); e != nil {
					return m, true, e
				}
			}
		}
		return m, true, nil
	}
	return Manifest{}, false, nil
}
func evaluateRules(ctx context.Context, trees []migrations.Tree, changed []string, read migrations.Reader) (Result, error) {
	if len(trees) < 2 {
		return Result{}, fmt.Errorf("uniqueness comparison needs base and combined trees")
	}
	last := len(trees) - 1
	base, bExists, bErr := manifest(trees[0], read)
	current, cExists, cErr := manifest(trees[last], read)
	if !bExists && !cExists {
		return Result{}, nil
	}
	out := Result{Applicable: true, Status: model.StatusPassed, Evidence: model.VerifiedStatic, Findings: []model.Finding{}, Diagnostics: []model.Diagnostic{}}
	unknown := func(reason string) {
		if out.Status != model.StatusFailed {
			out.Status = model.StatusUnknown
			out.Evidence = model.Unknown
		}
		out.Diagnostics = append(out.Diagnostics, model.Diagnostic{Path: ManifestPath, Message: reason, Severity: model.SeverityWarning})
	}
	if bErr != nil || cErr != nil || !cExists {
		unknown("Uniqueness declarations are malformed, unsupported, or removed from the candidate.")
		out.Explanation = "Declared uniqueness could not be checked."
		return out, nil
	}
	rules := current.Rules
	for _, r := range base.Rules {
		found := false
		for _, c := range current.Rules {
			if equalRule(r, c) {
				found = true
			}
		}
		if !found {
			unknown("Base uniqueness rule " + r.Name + " was removed or changed; its obligation remains.")
			rules = append(rules, r)
		}
	}
	for _, r := range rules {
		relevant := false
		for _, p := range changed {
			if p == ManifestPath || matches(r.Files, p) {
				relevant = true
			}
		}
		if !relevant {
			continue
		}
		states := make([]map[string][]model.Provenance, len(trees))
		reasons := make([]string, len(trees))
		for i, t := range trees {
			if e := ctx.Err(); e != nil {
				return Result{}, e
			}
			if t.Unavailable != "" {
				reasons[i] = t.Unavailable
				continue
			}
			states[i], reasons[i] = extract(t, r, read)
		}
		if reasons[last] != "" {
			unknown(r.Name + ": " + reasons[last])
			continue
		}
		keys := []string{}
		for k, locs := range states[last] {
			if len(locs) > 1 {
				keys = append(keys, k)
			}
		}
		sort.Strings(keys)
		for _, k := range keys {
			locs := states[last][k]
			f := model.NewFinding("duplicate_unique_key", fmt.Sprintf("Rule %s finds duplicate key %q at %d locations.", r.Name, k, len(locs)), model.VerifiedStatic)
			f.Locations = locs
			if reasons[0] != "" {
				unknown(r.Name + ": base cannot be read to distinguish existing duplicates")
				continue
			}
			if subset(locs, states[0][k]) {
				f.Code += "_on_base"
				f.Explanation += " The base already contains these locations."
			} else {
				f.Severity = model.SeverityError
				out.Status = model.StatusFailed
				out.Evidence = model.VerifiedStatic
				f.Explanation += " This duplicate is new relative to the base."
				for i := 1; i < last; i++ {
					if subset(locs, states[i][k]) {
						f.Explanation += " It already occurs with " + trees[i].Label + "."
					}
				}
				f.Remediation = "Give each location a distinct " + r.Name + " key, or remove the duplicate declaration."
			}
			out.Findings = append(out.Findings, f)
		}
	}
	out.Explanation = "Compared explicitly declared uniqueness rules in the combined tree; regex matches may include comments."
	return out, nil
}
func equalRule(a, b Rule) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return bytes.Equal(x, y)
}
func subset(a, b []model.Provenance) bool {
	if len(b) < 2 {
		return false
	}
	counts := map[string]int{}
	for _, l := range b {
		counts[l.Path]++
	}
	for _, l := range a {
		key := l.Path
		if counts[key] == 0 {
			return false
		}
		counts[key]--
	}
	return true
}

func extract(t migrations.Tree, r Rule, read migrations.Reader) (map[string][]model.Provenance, string) {
	out := map[string][]model.Provenance{}
	var re *regexp.Regexp
	if r.Pattern != "" {
		re = regexp.MustCompile(r.Pattern)
	}
	count, total, observations := 0, 0, 0
	for _, f := range t.Files {
		if !matches(r.Files, f.Path) {
			continue
		}
		count++
		if count > 10000 {
			return out, "file count exceeds limit"
		}
		b, e := read(f.OID)
		if e != nil || len(b) > 1<<20 {
			return out, "matching file cannot be read within analysis limit"
		}
		total += len(b)
		if total > 64<<20 {
			return out, "matching source exceeds analysis budget"
		}
		loc := model.Provenance{Repository: model.RepositoryID, Revision: t.Revision, Path: f.Path, Method: CheckID, Evidence: model.VerifiedStatic}
		if re != nil {
			matches := re.FindAllSubmatchIndex(b, maxKeyObservations-observations+1)
			observations += len(matches)
			if observations > maxKeyObservations {
				return out, "key observation count exceeds limit"
			}
			line, previousStart := 1, 0
			for _, m := range matches {
				start, end := m[0], m[1]
				if len(m) >= 4 {
					start, end = m[2], m[3]
					if start < 0 {
						continue
					}
				}
				if end == start {
					continue
				}
				l := loc
				line += bytes.Count(b[previousStart:start], []byte("\n"))
				previousStart = start
				l.Line = line
				out[string(b[start:end])] = append(out[string(b[start:end])], l)
				if len(out) > maxKeyObservations {
					return out, "key count exceeds limit"
				}
			}
		} else {
			var doc any
			if e := json.Unmarshal(b, &doc); e != nil {
				return out, "matching JSON file is malformed"
			}
			value, found, e := jsonptr.Lookup(doc, *r.JSONPointer)
			if e != nil || !found {
				return out, "declared JSON pointer cannot be resolved"
			}
			loc.Method = CheckID + ":json_pointer:" + *r.JSONPointer
			switch v := value.(type) {
			case string:
				observations++
				if observations > maxKeyObservations {
					return out, "key observation count exceeds limit"
				}
				out[v] = append(out[v], loc)
			case []any:
				observations += len(v)
				if observations > maxKeyObservations {
					return out, "key observation count exceeds limit"
				}
				for i, key := range v {
					s, ok := key.(string)
					if !ok {
						return out, "JSON pointer array must contain strings"
					}
					l := loc
					l.Method += fmt.Sprintf("/%d", i)
					out[s] = append(out[s], l)
				}
			case map[string]any:
				observations += len(v)
				if observations > maxKeyObservations {
					return out, "key observation count exceeds limit"
				}
				for key := range v {
					l := loc
					l.Method += "/" + jsonptr.Escape(key)
					out[key] = append(out[key], l)
				}
			default:
				return out, "JSON pointer must select a string, string array, or object"
			}
		}
	}
	return out, ""
}
func validateGlob(g string) error {
	if len(g) > 4096 || len(strings.Split(g, "/")) > 256 || g == "" || strings.HasPrefix(g, "/") || strings.Contains(g, "\\") {
		return fmt.Errorf("uniqueness glob must be repository relative")
	}
	for _, s := range strings.Split(g, "/") {
		if s == ".." {
			return fmt.Errorf("uniqueness glob escapes repository")
		}
	}
	_, e := path.Match(strings.ReplaceAll(g, "**", "*"), "")
	return e
}
func matches(globs []string, p string) bool {
	for _, g := range globs {
		if match(g, p) {
			return true
		}
	}
	return false
}
func match(g, p string) bool {
	gs, ps := strings.Split(g, "/"), strings.Split(p, "/")
	memo := map[[2]int]bool{}
	seen := map[[2]int]bool{}
	var visit func(int, int) bool
	visit = func(i, j int) (result bool) {
		state := [2]int{i, j}
		if seen[state] {
			return memo[state]
		}
		seen[state] = true
		defer func() { memo[state] = result }()
		if i == len(gs) {
			return j == len(ps)
		}
		if gs[i] == "**" {
			if visit(i+1, j) {
				return true
			}
			return j < len(ps) && visit(i, j+1)
		}
		if j == len(ps) {
			return false
		}
		ok, _ := path.Match(gs[i], ps[j])
		return ok && visit(i+1, j+1)
	}
	return visit(0, 0)
}

// Evaluate combines explicit declarations and resolved built-in route identities.
func Evaluate(ctx context.Context, trees []migrations.Tree, changed []string, read migrations.Reader) (Result, error) {
	blobCache := map[string][]byte{}
	errorCache := map[string]error{}
	originalRead := read
	read = func(oid string) ([]byte, error) {
		if b, ok := blobCache[oid]; ok {
			return b, errorCache[oid]
		}
		b, e := originalRead(oid)
		blobCache[oid] = b
		errorCache[oid] = e
		return b, e
	}
	rules, e := evaluateRules(ctx, trees, changed, read)
	if e != nil {
		return rules, e
	}
	routes, e := evaluateRoutes(ctx, trees, changed, read)
	if e != nil {
		return routes, e
	}
	if !rules.Applicable {
		return routes, nil
	}
	if !routes.Applicable {
		return rules, nil
	}
	rules.Findings = append(rules.Findings, routes.Findings...)
	rules.Diagnostics = append(rules.Diagnostics, routes.Diagnostics...)
	if rules.Status == model.StatusFailed || routes.Status == model.StatusFailed {
		rules.Status = model.StatusFailed
		rules.Evidence = model.VerifiedStatic
	} else if rules.Status == model.StatusUnknown || routes.Status == model.StatusUnknown {
		rules.Status = model.StatusUnknown
		rules.Evidence = model.Unknown
	}
	rules.Explanation = "Compared declared unique keys and statically resolved route identities."
	return rules, nil
}
func evaluateRoutes(ctx context.Context, trees []migrations.Tree, changed []string, read migrations.Reader) (Result, error) {
	last := len(trees) - 1
	relevantExtensions := map[string]bool{}
	compositionChanged := false
	signalUnknown := false
	for _, p := range changed {
		switch path.Ext(p) {
		case ".py", ".js", ".ts", ".go", ".rs", ".java":
		default:
			continue
		}
		signatures := [2]string{}
		compositions := [2]bool{}
		for j, index := range []int{0, last} {
			for _, f := range trees[index].Files {
				if f.Path != p {
					continue
				}
				b, e := read(f.OID)
				if e != nil || len(b) > 1<<20 {
					signalUnknown = true
					continue
				}
				signatures[j], compositions[j] = routeSignature(p, string(b))
			}
		}
		if signatures[0] != signatures[1] {
			ext := path.Ext(p)
			relevantExtensions[ext] = true
			if ext == ".js" || ext == ".ts" {
				relevantExtensions[".js"] = true
				relevantExtensions[".ts"] = true
			}
			compositionChanged = compositionChanged || compositions[0] || compositions[1]
		}
	}
	if len(relevantExtensions) == 0 && !signalUnknown {
		return Result{}, nil
	}
	type parsedRoutes struct {
		routes      []discovery.Route
		diagnostics []model.Diagnostic
	}
	parseCache := map[string]parsedRoutes{}
	states := make([]map[string][]model.Provenance, len(trees))
	uncertain := make([]bool, len(trees))
	allDiagnostics := []model.Diagnostic{}
	found := false
	for i, t := range trees {
		states[i] = map[string][]model.Provenance{}
		if t.Unavailable != "" {
			uncertain[i] = true
			continue
		}
		sources := map[string]string{}
		total, count := 0, 0
		for _, f := range t.Files {
			if !relevantExtensions[path.Ext(f.Path)] && !(relevantExtensions[".java"] && (path.Base(f.Path) == "pom.xml" || path.Base(f.Path) == "build.gradle")) {
				continue
			}
			switch path.Ext(f.Path) {
			case ".py", ".js", ".ts", ".go", ".java", ".rs":
			default:
				if path.Base(f.Path) != "pom.xml" && path.Base(f.Path) != "build.gradle" {
					continue
				}
			}
			if strings.Contains(f.Path, "node_modules/") || strings.Contains(f.Path, "vendor/") || strings.Contains(f.Path, ".venv/") {
				continue
			}
			count++
			if count > 10000 {
				uncertain[i] = true
				break
			}
			b, e := read(f.OID)
			if e != nil || len(b) > 1<<20 {
				uncertain[i] = true
				continue
			}
			total += len(b)
			if total > 64<<20 {
				uncertain[i] = true
				break
			}
			sources[f.Path] = string(b)
		}
		parts := []string{}
		for _, f := range t.Files {
			if _, ok := sources[f.Path]; ok {
				parts = append(parts, f.Path+"\x00"+f.OID)
			}
		}
		sort.Strings(parts)
		key := strings.Join(parts, "\x01")
		parsed, ok := parseCache[key]
		if !ok {
			routes, ds, e := discovery.Routes(ctx, sources, "")
			if e != nil {
				return Result{}, e
			}
			parsed = parsedRoutes{routes, ds}
			parseCache[key] = parsed
		}
		routes, ds := parsed.routes, parsed.diagnostics
		found = found || len(routes) > 0
		for _, d := range ds {
			for _, p := range changed {
				if p == d.Path {
					uncertain[i] = true
					if i == last {
						allDiagnostics = append(allDiagnostics, d)
					}
				}
			}
		}
		for _, r := range routes {
			key := r.Scope + " " + r.Method + " " + r.Path
			location := r.Location
			location.Revision = t.Revision
			states[i][key] = append(states[i][key], location)
		}
	}
	if !found && !uncertain[last] {
		return Result{}, nil
	}
	out := Result{Applicable: true, Status: model.StatusPassed, Evidence: model.VerifiedStatic, Findings: []model.Finding{}, Diagnostics: allDiagnostics, Explanation: "Compared literal route method/path keys within resolved applications; dynamic routing is not checked."}
	if uncertain[last] || signalUnknown {
		out.Status = model.StatusUnknown
		out.Evidence = model.Unknown
		out.Explanation = "Route uniqueness could not be established for dynamic or unreadable changed routing."
	}
	keys := []string{}
	for k, ls := range states[last] {
		if len(ls) > 1 {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		ls := states[last][k]
		changedLocation := compositionChanged
		for _, l := range ls {
			for _, p := range changed {
				if p == l.Path {
					changedLocation = true
				}
			}
		}
		if !changedLocation {
			continue
		}
		f := model.NewFinding("duplicate_route", fmt.Sprintf("Resolved application registers %s at %d locations.", k, len(ls)), model.VerifiedStatic)
		f.Locations = ls
		if subset(ls, states[0][k]) {
			f.Code += "_on_base"
			f.Explanation += " These registrations already occur on the base."
		} else if uncertain[0] {
			out.Status = model.StatusUnknown
			out.Evidence = model.Unknown
			continue
		} else {
			f.Severity = model.SeverityError
			f.Remediation = "Remove or rename one route registration in this application."
			out.Status = model.StatusFailed
			out.Evidence = model.VerifiedStatic
			for i := 1; i < last; i++ {
				if subset(ls, states[i][k]) {
					f.Explanation += " It also occurs with " + trees[i].Label + "."
				}
			}
		}
		out.Findings = append(out.Findings, f)
	}
	return out, nil
}

var pythonRouteSignal = regexp.MustCompile(`(?m)^\s*@\w+(?:\.\w+)*\.(?:get|post|put|patch|delete|head|options|trace)\(`)
var routeCallSignal = regexp.MustCompile(`\.(?:get|post|put|patch|delete|head|options|GET|POST|PUT|PATCH|DELETE|HEAD|OPTIONS|route)\(`)
var springRouteSignal = regexp.MustCompile(`@(?:Get|Post|Put|Patch|Delete|Request)Mapping`)

// RouteRelevant reports whether this source participates in the supported route
// detector. It is a conservative selection signal, never route validity evidence.
func RouteRelevant(file, source string) bool {
	signature, composition := routeSignature(file, source)
	return signature != "" || composition
}

// routeSignature is a relevance filter, never failure evidence. Keep complete
// routing/import/composition lines so computed registrations become unknown
// when changed, while ordinary application configuration edits do no route scan.
func routeSignature(file, source string) (string, bool) {
	ext := path.Ext(file)
	framework := false
	switch ext {
	case ".py":
		framework = strings.Contains(source, "fastapi") || pythonRouteSignal.MatchString(source) || strings.Contains(source, ".include_router(")
	case ".js", ".ts":
		framework = strings.Contains(source, "express")
	case ".go":
		framework = strings.Contains(source, "github.com/gin-gonic/gin")
	case ".rs":
		framework = strings.Contains(source, "axum")
	case ".java":
		framework = strings.Contains(source, "org.springframework.web.bind.annotation")
	default:
		return "", false
	}
	if !framework {
		return "", false
	}
	lines := []string{}
	composition := false
	hasRouting := false
	continuation := 0
	for _, line := range strings.Split(source, "\n") {
		trim := strings.TrimSpace(line)
		registration := false
		switch ext {
		case ".py":
			registration = pythonRouteSignal.MatchString(line)
		case ".java":
			registration = springRouteSignal.MatchString(line)
		default:
			registration = routeCallSignal.MatchString(line)
		}
		mount := strings.Contains(line, "include_router(") || strings.Contains(line, "FastAPI(") || strings.Contains(line, "APIRouter(") || strings.Contains(line, "express(") || strings.Contains(line, "express.Router(") || strings.Contains(line, "gin.Default(") || strings.Contains(line, "gin.New(") || strings.Contains(line, "Router::new(") || strings.Contains(line, ".use(") || strings.Contains(line, ".Group(") || strings.Contains(line, ".nest(") || strings.Contains(line, ".merge(")
		if registration || mount || continuation > 0 {
			continuation += signatureParens(line)
			if continuation < 0 {
				continuation = 0
			}
			lines = append(lines, trim)
			hasRouting = true
			composition = composition || mount
		}
	}
	if !hasRouting {
		return "", false
	}
	for _, line := range strings.Split(source, "\n") {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "from ") || strings.HasPrefix(trim, "import ") || strings.Contains(trim, "require(") {
			lines = append(lines, trim)
		}
	}
	return strings.Join(lines, "\n"), composition
}

func signatureParens(line string) int {
	depth := 0
	quote := byte(0)
	for i := 0; i < len(line); i++ {
		c := line[i]
		if quote != 0 {
			if c == '\\' {
				i++
				continue
			}
			if c == quote {
				quote = 0
			}
			continue
		}
		if c == '"' || c == '\'' || c == '`' {
			quote = c
			continue
		}
		if c == '#' {
			break
		}
		if c == '(' {
			depth++
		}
		if c == ')' {
			depth--
		}
	}
	return depth
}
