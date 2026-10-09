package discovery

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/Jake-Network/radar/internal/contracts"
	gitrepo "github.com/Jake-Network/radar/internal/git"
	"github.com/Jake-Network/radar/internal/languages"
	"github.com/Jake-Network/radar/internal/model"
	"gopkg.in/yaml.v3"
)

var limitations = []string{"Candidates are proposed static relationships, never proof of runtime behavior or automatically accepted bindings. Input is limited to 10000 supported files, 64 MiB total and 4 MiB per file; node_modules, vendor, venv and internal state directories are excluded.", "Supported: local OpenAPI response $refs, JSON Schema properties, direct Pydantic BaseModel fields and literal FastAPI response_model decorators; relative TypeScript imports and explicitly typed fetch(...).json() locals.", "Dynamic URLs, aliases, re-exports, inheritance, computed fields, validators, middleware, runtime serialization and untyped consumers are not resolved. HTTP method, deployment origin and same-name variable scope shadowing are not established."}
var classRE = regexp.MustCompile(`(?m)^class\s+(\w+)\(BaseModel\):[^\n]*\n((?:[ \t]+[^\n]*\n|\n)*)`)
var endpointRE = regexp.MustCompile(`@(\w+)\.(?:get|post|put|patch|delete)\(\s*["']([^"']+)["'][^\n]*response_model\s*=\s*(\w+)`)
var importRE = regexp.MustCompile(`import\s+(?:type\s+)?\{\s*(\w+)\s*\}\s+from\s+["']([^"']+)["']`)
var localRE = regexp.MustCompile(`(?:const|let)\s+(\w+)\s*:\s*(\w+)\s*=\s*([^;]+);`)

type endpoint struct {
	url, schema string
	location    model.Provenance
}

func provenance(ref, p, method string, line int) model.Provenance {
	return model.Provenance{Revision: ref, Path: p, Line: line, Method: method, Evidence: model.VerifiedStatic}
}
func lineAt(s string, n int) int { return 1 + strings.Count(s[:n], "\n") }
func keys(m map[string]any) []string {
	out := []string{}
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
func object(x any) map[string]any { m, _ := x.(map[string]any); return m }

// Discover reads only bounded regular files through Radar's Git/path helpers.
func Discover(ctx context.Context, root, ref string) (Report, error) {
	if ref == "" {
		ref = "WORKTREE"
	}
	r := Report{Status: model.StatusIncomplete, Revision: ref, Schemas: []Schema{}, Candidates: []Candidate{}, ProposedManifest: contracts.Manifest{Version: 1, Bindings: []contracts.Binding{}}, Diagnostics: []model.Diagnostic{}, Limitations: append([]string(nil), limitations...)}
	var files []string
	var err error
	if ref == "WORKTREE" {
		files, err = gitrepo.WorkingFiles(ctx, root)
	} else {
		r.Revision, err = gitrepo.Resolve(ctx, root, ref)
		if err == nil {
			files, err = gitrepo.Files(ctx, root, r.Revision)
			ref = r.Revision
		}
	}
	if err != nil {
		return r, err
	}
	read := func(p string) ([]byte, error) { return gitrepo.ReadFile(ctx, root, ref, p) }
	if ref != "WORKTREE" {
		entries, entryErr := gitrepo.Entries(ctx, root, ref)
		if entryErr != nil {
			return r, entryErr
		}
		objects := map[string]gitrepo.TreeEntry{}
		for _, entry := range entries {
			objects[entry.Path] = entry
		}
		reader, readerErr := gitrepo.OpenBlobReader(ctx, root)
		if readerErr != nil {
			return r, readerErr
		}
		defer reader.Close()
		read = func(p string) ([]byte, error) {
			entry := objects[p]
			if entry.Type != "blob" || (entry.Mode != "100644" && entry.Mode != "100755") {
				return nil, fmt.Errorf("non-regular source refused")
			}
			return reader.Read(entry.OID, gitrepo.MaxFileBytes)
		}
	}
	totalBytes, supportedFiles := 0, 0
	sources := map[string]string{}
	parsedSources := map[string]languages.Result{}
	endpoints := []endpoint{}
	for _, p := range files {
		if err := ctx.Err(); err != nil {
			return r, err
		}
		if excluded(p) {
			continue
		}
		ext := strings.ToLower(path.Ext(p))
		if ext != ".json" && ext != ".yaml" && ext != ".yml" && ext != ".py" && ext != ".ts" && ext != ".tsx" {
			continue
		}
		supportedFiles++
		if supportedFiles > 10000 {
			r.Diagnostics = append(r.Diagnostics, model.Diagnostic{Message: "discovery limited to 10000 supported files; coverage incomplete", Severity: model.SeverityWarning})
			break
		}
		b, e := read(p)
		if e != nil {
			r.Diagnostics = append(r.Diagnostics, model.Diagnostic{Path: p, Message: e.Error(), Severity: model.SeverityWarning})
			continue
		}
		totalBytes += len(b)
		if totalBytes > 64<<20 {
			r.Diagnostics = append(r.Diagnostics, model.Diagnostic{Path: p, Message: "discovery limited to 64 MiB total input; coverage incomplete", Severity: model.SeverityWarning})
			break
		}
		s := string(b)
		if ext == ".py" || ext == ".ts" || ext == ".tsx" {
			adapter, _ := languages.ForPath(p)
			parsed, parseErr := adapter.Parse(ctx, languages.Source{Path: p, Revision: ref, Content: b})
			if parseErr != nil {
				r.Diagnostics = append(r.Diagnostics, model.Diagnostic{Path: p, Message: parseErr.Error(), Severity: model.SeverityWarning})
				continue
			}
			if len(parsed.Diagnostics) > 0 {
				r.Diagnostics = append(r.Diagnostics, parsed.Diagnostics...)
				continue
			}
			parsedSources[p] = parsed
		}
		sources[p] = s
		if ext == ".json" || ext == ".yaml" || ext == ".yml" {
			var doc map[string]any
			if ext == ".json" {
				e = json.Unmarshal(b, &doc)
			} else {
				e = yaml.Unmarshal(b, &doc)
			}
			if e != nil {
				if strings.Contains(s, "openapi") || strings.Contains(s, "$schema") {
					r.Diagnostics = append(r.Diagnostics, model.Diagnostic{Path: p, Message: "invalid schema document: " + e.Error(), Severity: model.SeverityWarning})
				}
				continue
			}
			add := func(name, ptr string, v any) {
				props := object(object(v)["properties"])
				if props == nil {
					return
				}
				r.Schemas = append(r.Schemas, Schema{ID: model.ContractID(p, ptr), Name: name, Path: p, Pointer: ptr, Fields: keys(props), Evidence: provenance(ref, p, "schema-properties", 1)})
			}
			if doc["$schema"] != nil {
				name, _ := doc["title"].(string)
				add(name, "", doc)
			}
			for name, v := range object(object(doc["components"])["schemas"]) {
				add(name, "/components/schemas/"+escapePointer(name), v)
			}
			for name, v := range object(doc["$defs"]) {
				add(name, "/$defs/"+escapePointer(name), v)
			}
			for url, operations := range object(doc["paths"]) {
				for _, op := range object(operations) {
					for code, response := range object(object(op)["responses"]) {
						if !strings.HasPrefix(code, "2") {
							continue
						}
						schema := object(object(object(object(response)["content"])["application/json"])["schema"])
						link, _ := schema["$ref"].(string)
						if strings.HasPrefix(link, "#/components/schemas/") {
							endpoints = append(endpoints, endpoint{url, model.ContractID(p, strings.TrimPrefix(link, "#")), provenance(ref, p, "openapi-response-ref", 1)})
						}
					}
				}
			}
		}
		if ext == ".py" {
			// Require a direct Pydantic import, not a lexical class name alone.
			facts, structureErr := pythonStructure(ctx, s)
			if structureErr != nil {
				return r, structureErr
			}
			if !facts.baseModel {
				continue
			}
			for _, m := range classRE.FindAllStringSubmatchIndex(s, -1) {
				name := s[m[2]:m[3]]
				if !declaration(parsedSources[p], "class", name, lineAt(s, m[0])) {
					continue
				}
				fields := facts.fields[name]
				sort.Strings(fields)
				r.Schemas = append(r.Schemas, Schema{ID: model.ContractID(p, "/models/"+name), Name: name, Path: p, Pointer: "/models/" + name, Fields: fields, Evidence: provenance(ref, p, "pydantic-direct-fields", lineAt(s, m[0]))})
			}
			for _, m := range endpointRE.FindAllStringSubmatchIndex(s, -1) {
				if !facts.decorators[lineAt(s, m[0])] || !facts.apps[s[m[2]:m[3]]] {
					continue
				}
				endpoints = append(endpoints, endpoint{s[m[4]:m[5]], model.ContractID(p, "/models/"+s[m[6]:m[7]]), provenance(ref, p, "fastapi-literal-response-model", lineAt(s, m[0]))})
			}
		}
	}
	schemaByID := map[string]Schema{}
	for _, s := range r.Schemas {
		schemaByID[s.ID] = s
	}
	for _, ep := range endpoints {
		schema, ok := schemaByID[ep.schema]
		if !ok {
			r.Diagnostics = append(r.Diagnostics, model.Diagnostic{Path: ep.location.Path, Message: "unresolved response schema " + ep.schema, Severity: model.SeverityWarning})
			continue
		}
		found := false
		for p, s := range sources {
			if path.Ext(p) != ".ts" && path.Ext(p) != ".tsx" {
				continue
			}
			imports := map[string]string{}
			for _, match := range importRE.FindAllStringSubmatchIndex(s, -1) {
				name, module := s[match[2]:match[3]], s[match[4]:match[5]]
				if strings.HasPrefix(module, ".") && imported(parsedSources[p], module, lineAt(s, match[0])) {
					imports[name] = path.Clean(path.Join(path.Dir(p), module))
				}
			}
			for _, m := range localRE.FindAllStringSubmatchIndex(s, -1) {
				variable, typ := s[m[2]:m[3]], s[m[4]:m[5]]
				if !declaration(parsedSources[p], "symbol", variable, lineAt(s, m[0])) {
					continue
				}
				module, ok := imports[typ]
				if !ok {
					continue
				}
				// Resolve the imported declaration in this repository. Type names do not bind producers.
				resolved := ""
				for _, candidate := range []string{module, module + ".ts", path.Join(module, "index.ts")} {
					if _, ok := sources[candidate]; ok && (declaration(parsedSources[candidate], "interface", typ, 0) || declaration(parsedSources[candidate], "type", typ, 0)) {
						resolved = candidate
						break
					}
				}
				if resolved == "" {
					continue
				}
				fields, accesses, accessErr := memberAccesses(ctx, p, ref, s, variable, m[2], ep.url, typ, module)
				if accessErr != nil {
					return r, accessErr
				}
				if fields == nil {
					continue
				}
				sort.Strings(fields)
				c := Candidate{ID: model.StableID(ep.schema, p, variable, ep.url), Producer: ep.location.Path, Consumer: p, Contract: ep.schema, Endpoint: ep.url, Fields: fields, Evidence: model.Inferred, Locations: []model.Provenance{schema.Evidence, ep.location, provenance(ref, p, "typed-literal-fetch-local", lineAt(s, m[0])), provenance(ref, resolved, "resolved-relative-type-import", 1)}, Ambiguities: []string{"URL and imported local type establish a candidate only; server origin, HTTP method, casts and runtime serialization are not established."}}
				c.Locations = append(c.Locations, accesses...)
				count := 0
				for _, other := range endpoints {
					if other.url == ep.url {
						count++
					}
				}
				if count > 1 {
					c.Evidence = model.Unknown
					c.Ambiguities = append(c.Ambiguities, "Multiple producers declare this URL.")
				}
				r.Candidates = append(r.Candidates, c)
				found = true
				// Only JSON/OpenAPI pointers are usable by the existing schema reader.
				if strings.HasSuffix(schema.Path, ".json") && c.Evidence != model.Unknown {
					r.ProposedManifest.Bindings = append(r.ProposedManifest.Bindings, contracts.Binding{ID: c.ID, Schema: schema.Path, Pointer: schema.Pointer, Producer: c.Producer, Consumer: c.Consumer, Fields: fields, Direction: "response"})
				}
			}
		}
		if !found {
			r.Candidates = append(r.Candidates, Candidate{ID: model.StableID(ep.schema, ep.url), Producer: ep.location.Path, Contract: ep.schema, Endpoint: ep.url, Fields: schema.Fields, Evidence: model.Proposed, Locations: []model.Provenance{schema.Evidence, ep.location}, Ambiguities: []string{"No supported typed consumer could be resolved."}})
		}
	}
	sort.Slice(r.ProposedManifest.Bindings, func(i, j int) bool { return r.ProposedManifest.Bindings[i].ID < r.ProposedManifest.Bindings[j].ID })
	sort.Slice(r.Schemas, func(i, j int) bool { return r.Schemas[i].ID < r.Schemas[j].ID })
	sort.Slice(r.Candidates, func(i, j int) bool { return r.Candidates[i].ID < r.Candidates[j].ID })
	return r, nil
}

// Compare reports removed declared fields still accessed by a supported candidate
// in the new tree. Runtime compatibility remains unknown; findings are warnings.
func Compare(ctx context.Context, root, base, head string) (Comparison, error) {
	before, e := Discover(ctx, root, base)
	if e != nil {
		return Comparison{}, e
	}
	after, e := Discover(ctx, root, head)
	if e != nil {
		return Comparison{}, e
	}
	r := Comparison{Status: model.StatusIncomplete, Base: before.Revision, Head: after.Revision, Findings: []model.Finding{}, Diagnostics: append(before.Diagnostics, after.Diagnostics...), Limitations: append([]string(nil), limitations...)}
	old := map[string]Schema{}
	for _, s := range before.Schemas {
		old[s.ID] = s
	}
	current := map[string]Schema{}
	for _, s := range after.Schemas {
		current[s.ID] = s
	}
	for _, c := range after.Candidates {
		if c.Consumer == "" || c.Evidence == model.Unknown {
			continue
		}
		a, ok := old[c.Contract]
		if !ok {
			continue
		}
		b, ok := current[c.Contract]
		if !ok {
			continue
		}
		for _, field := range c.Fields {
			if contains(a.Fields, field) && !contains(b.Fields, field) {
				f := model.NewFinding("discovered_response_field_removed", fmt.Sprintf("Declared response field %s was removed from %s; %s still accesses it via %s", field, c.Contract, c.Consumer, c.Endpoint), model.Inferred)
				f.ID = model.StableID(f.Code, c.ID, field)
				f.Contract = c.Contract
				f.Producer = c.Producer
				f.Consumer = c.Consumer
				f.Locations = c.Locations
				f.Remediation = "Restore the response field or migrate the typed consumer and its contract together; inspect and accept the candidate binding before treating it as authoritative."
				f.Verification = "Run the producer-consumer integration test against the combined source state and repeat radar check."
				r.Findings = append(r.Findings, f)
			}
		}
	}
	if len(r.Findings) > 0 {
		r.Status = model.StatusWarning
	}
	return r, nil
}
func contains(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}

func declaration(parsed languages.Result, kind, name string, line int) bool {
	for _, n := range parsed.Nodes {
		if n.Kind == kind && n.Name == name && (line == 0 || n.Provenance.Line == line) {
			return true
		}
	}
	return false
}

func escapePointer(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "~", "~0"), "/", "~1")
}

func excluded(p string) bool {
	for _, part := range strings.Split(p, "/") {
		switch part {
		case ".radar", "node_modules", ".venv", "venv", "vendor", ".git":
			return true
		}
	}
	return false
}

func imported(parsed languages.Result, module string, line int) bool {
	for _, i := range parsed.Imports {
		if i.Module == module && i.Line == line {
			return true
		}
	}
	return false
}
