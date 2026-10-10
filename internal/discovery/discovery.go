package discovery

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"slices"
	"sort"
	"strings"

	"github.com/Jake-Network/radar/internal/contracts"
	gitrepo "github.com/Jake-Network/radar/internal/git"
	"github.com/Jake-Network/radar/internal/model"
	"gopkg.in/yaml.v3"
)

var limitations = []string{
	"Candidates are proposed static relationships, never proof of runtime behavior or automatically accepted bindings. Input is limited to 10000 supported files, 64 MiB total and 4 MiB per file; dependency and state directories are excluded.",
	"Tree-sitter and bounded local import resolution support literal FastAPI/Pydantic producers and typed TypeScript fetch/Axios consumers; supported facts carry exact source provenance.",
	"Dynamic registration, computed schemas, validators, middleware, runtime URL origins, complex inheritance and ambiguous exports remain unsupported or uncertain. Discovery is incomplete even when no findings appear.",
}

type endpoint struct {
	url, schema   string
	method        string
	relationships []model.Provenance
	location      model.Provenance
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
				r.Schemas = append(r.Schemas, Schema{ID: model.ContractID(p, ptr), Name: name, Path: p, Pointer: ptr, Fields: propertyPaths(props, "", 0), Evidence: provenance(ref, p, "schema-properties", 1)})
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
				for method, op := range object(operations) {
					for code, response := range object(object(op)["responses"]) {
						if !strings.HasPrefix(code, "2") {
							continue
						}
						schema := object(object(object(object(response)["content"])["application/json"])["schema"])
						link, _ := schema["$ref"].(string)
						if strings.HasPrefix(link, "#/components/schemas/") {
							endpoints = append(endpoints, endpoint{url: url, schema: model.ContractID(p, strings.TrimPrefix(link, "#")), method: strings.ToUpper(method), location: provenance(ref, p, "openapi-response-ref", 1)})
						}
					}
				}
			}
		}
	}
	pythonSchemas, pythonEndpoints, pythonDiagnostics, err := resolvePython(ctx, sources, ref)
	if err != nil {
		return r, err
	}
	r.Schemas = append(r.Schemas, pythonSchemas...)
	endpoints = append(endpoints, pythonEndpoints...)
	r.Diagnostics = append(r.Diagnostics, pythonDiagnostics...)
	consumers, consumerDiagnostics, err := resolveConsumers(ctx, sources, ref)
	if err != nil {
		return r, err
	}
	r.Diagnostics = append(r.Diagnostics, consumerDiagnostics...)
	schemaByID := map[string]Schema{}
	matchedConsumers := make([]bool, len(consumers))
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
		for i, consumer := range consumers {
			if consumer.endpoint != ep.url || (consumer.method != "" && ep.method != "" && consumer.method != ep.method) {
				continue
			}
			fields := append([]string(nil), consumer.fields...)
			sort.Strings(fields)
			c := Candidate{ID: model.StableID(ep.schema, consumer.path, consumer.typePath, ep.url, ep.method), Producer: ep.location.Path, Consumer: consumer.path, Contract: ep.schema, Endpoint: ep.url, Method: ep.method, Fields: fields, Evidence: model.Inferred, Locations: []model.Provenance{schema.Evidence, ep.location}, Ambiguities: append([]string{"Literal path and statically resolved local type propose a relationship; deployment origin and runtime serialization remain unverified."}, consumer.ambiguities...)}
			c.Locations = append(c.Locations, ep.relationships...)
			c.Locations = append(c.Locations, consumer.locations...)
			count := 0
			for _, other := range endpoints {
				if other.url == ep.url && (other.method == ep.method || other.method == "" || ep.method == "") {
					count++
				}
			}
			if count > 1 {
				c.Evidence = model.Unknown
				c.Ambiguities = append(c.Ambiguities, "Multiple producers declare this method/path; deployment ownership unresolved.")
			}
			if consumer.method == "" || ep.method == "" {
				c.Ambiguities = append(c.Ambiguities, "HTTP method unresolved.")
			}
			r.Candidates = append(r.Candidates, c)
			found = true
			matchedConsumers[i] = true
			if strings.HasSuffix(schema.Path, ".json") && c.Evidence != model.Unknown && len(fields) > 0 {
				r.ProposedManifest.Bindings = append(r.ProposedManifest.Bindings, contracts.Binding{ID: c.ID, Schema: schema.Path, Pointer: schema.Pointer, Producer: c.Producer, Consumer: c.Consumer, Fields: fields, Direction: "response"})
			}
		}
		if !found {
			r.Candidates = append(r.Candidates, Candidate{ID: model.StableID(ep.schema, ep.url), Producer: ep.location.Path, Contract: ep.schema, Endpoint: ep.url, Method: ep.method, Fields: schema.Fields, Evidence: model.Proposed, Locations: []model.Provenance{schema.Evidence, ep.location}, Ambiguities: []string{"No supported typed consumer could be resolved."}})
		}
	}
	for i, consumer := range consumers {
		if matchedConsumers[i] {
			continue
		}
		fields := append([]string{}, consumer.fields...)
		sort.Strings(fields)
		r.Consumers = append(r.Consumers, ConsumerUse{Path: consumer.path, Endpoint: consumer.endpoint, Method: consumer.method, Type: consumer.typePath + "#" + consumer.typeName, Fields: fields, Locations: append([]model.Provenance{}, consumer.locations...)})
	}
	sort.Slice(r.Consumers, func(i, j int) bool {
		a, b := r.Consumers[i], r.Consumers[j]
		return model.StableID(a.Path, a.Endpoint, a.Method, a.Type) < model.StableID(b.Path, b.Endpoint, b.Method, b.Type)
	})
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
			if slices.Contains(a.Fields, field) && !slices.Contains(b.Fields, field) {
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

// propertyPaths records only literal JSON object property structure, bounded
// independently of the stronger declared-schema normalizer.
func propertyPaths(props map[string]any, prefix string, depth int) []string {
	if depth > 32 {
		return nil
	}
	out := []string{}
	for _, name := range keys(props) {
		field := prefix + name
		out = append(out, field)
		out = append(out, propertyPaths(object(object(props[name])["properties"]), field+".", depth+1)...)
	}
	return out
}
