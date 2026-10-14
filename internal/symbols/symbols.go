// Package symbols compares syntax declarations and resolved named imports.
// Relationships are inferred: no compiler or repository code is executed.
package symbols

import (
	"context"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/Jake-Network/radar/internal/checkpoint"
	"github.com/Jake-Network/radar/internal/indexer"
	"github.com/Jake-Network/radar/internal/languages"
	"github.com/Jake-Network/radar/internal/model"
)

const CheckID = "no_symbol_conflicts"

type Result struct {
	Applicable        bool
	Status            model.Status
	Evidence          model.Evidence
	Explanation       string
	Findings          []model.Finding
	Diagnostics       []model.Diagnostic
	SupportedStatus   model.Status
	SupportedEvidence model.Evidence
	UnknownCount      int
}

// Reference is an explicitly named import resolved to a repository file.
// An empty Target means resolution is unavailable, never that a name is absent.
type Reference struct {
	From, Module, Target, Name string
	Line                       int
}
type Tree struct {
	Snapshot   model.Snapshot
	References []Reference
	// Exports maps an explicit re-export reference to its exported spelling.
	Exports               map[string]string
	Signatures            map[string]Arity
	Calls                 []Call
	UnsupportedSignatures map[string]bool
	SignatureLocations    map[string]model.Provenance
}

// Analyze reads immutable trees. Inputs are retained for API consistency; the
// evidence does not attribute a cause to a branch without standalone proof.
func Analyze(ctx context.Context, root, base string, inputs []string, combined string, changed []string) (Result, error) {
	return AnalyzeWithSnapshot(ctx, root, base, combined, changed, nil)
}

// AnalyzeWithSnapshot lets integration reuse its candidate index.
func AnalyzeWithSnapshot(ctx context.Context, root, base, combined string, changed []string, candidate *model.Snapshot) (Result, error) {
	return AnalyzeWithCache(ctx, root, base, combined, changed, candidate, nil)
}

// AnalyzeWithCache shares bounded syntax results with candidate indexing.
func AnalyzeWithCache(ctx context.Context, root, base, combined string, changed []string, candidate *model.Snapshot, cache *indexer.ParseCache) (Result, error) {
	plausible := false
	for _, p := range changed {
		if _, ok := languages.ForPath(p); ok && !indexer.ExcludedPath(p) {
			plausible = true
		}
	}
	if !plausible {
		return Result{}, nil
	}
	baseSnapshot, err := checkpoint.IndexWithCache(ctx, root, base, cache)
	if err != nil {
		return Result{}, err
	}
	if candidate == nil {
		snapshot, e := checkpoint.IndexWithCache(ctx, root, combined, cache)
		if e != nil {
			return Result{}, e
		}
		candidate = &snapshot
	}
	selected := sourceNeighborhood(baseSnapshot, *candidate, changed)
	b, err := loadWithCache(ctx, root, base, &baseSnapshot, cache, selected)
	if err != nil {
		return Result{}, err
	}
	c, err := loadWithCache(ctx, root, combined, candidate, cache, selected)
	if err != nil {
		return Result{}, err
	}
	return Evaluate(b, c, changed), nil
}

func load(ctx context.Context, root, revision string, snapshot *model.Snapshot) (Tree, error) {
	return loadWithCache(ctx, root, revision, snapshot, nil)
}

func sourceNeighborhood(base, combined model.Snapshot, changed []string) map[string]bool {
	selected := map[string]bool{}
	direct := map[string]bool{}
	for _, p := range changed {
		selected[p] = true
		direct[p] = true
	}
	edges := append(append([]model.Edge{}, base.Edges...), combined.Edges...)
	for _, e := range edges {
		if e.Kind == "DEPENDS_ON" && direct[model.PathFromID(e.From)] {
			selected[model.PathFromID(e.To)] = true
		}
	}
	// Barrels can chain through several literal named re-exports. Their import
	// neighborhood is conservative; Python/Go/Java targets need one hop only.
	for {
		progress := false
		for _, e := range edges {
			from, to := model.PathFromID(e.From), model.PathFromID(e.To)
			ext := path.Ext(from)
			if e.Kind == "DEPENDS_ON" && selected[from] && (ext == ".ts" || ext == ".tsx" || ext == ".js" || ext == ".jsx") && !selected[to] {
				selected[to] = true
				progress = true
			}
		}
		if !progress {
			break
		}
	}
	return selected
}

func sourceCoverageUnknown(d model.Diagnostic) bool {
	for _, phrase := range []string{"syntax errors:", "structural analysis unavailable:", "source exceeds ", "source files skipped", "source budget", "symlink source skipped", "non-regular source skipped", "conditional Go compilation", "conditional C/C++ compilation", "literal reference parsing unavailable", "ambiguous or cyclic Rust include"} {
		if strings.Contains(d.Message, phrase) {
			return true
		}
	}
	return false
}

func loadWithCache(ctx context.Context, root, revision string, snapshot *model.Snapshot, cache *indexer.ParseCache, selection ...map[string]bool) (Tree, error) {
	t := Tree{Exports: map[string]string{}, Signatures: map[string]Arity{}, UnsupportedSignatures: map[string]bool{}, SignatureLocations: map[string]model.Provenance{}}
	var err error
	if snapshot == nil {
		t.Snapshot, err = checkpoint.IndexWithCache(ctx, root, revision, cache)
	} else {
		t.Snapshot = *snapshot
		t.Snapshot.Nodes = append([]model.Node{}, snapshot.Nodes...)
		t.Snapshot.Diagnostics = append([]model.Diagnostic{}, snapshot.Diagnostics...)
	}
	if err != nil {
		return t, err
	}
	targets := map[string][]string{}
	packageNames := map[string]string{}
	for _, n := range t.Snapshot.Nodes {
		if n.Language == "go" && n.Kind == "package" {
			packageNames[n.Provenance.Path] = n.Name
		}
	}
	for _, e := range t.Snapshot.Edges {
		if e.Kind == "DEPENDS_ON" && (strings.HasPrefix(e.Provenance.Method, "import_path_resolution:") || strings.HasPrefix(e.Provenance.Method, "workspace_member:")) {
			k := fmt.Sprintf("%s:%d", e.Provenance.Path, e.Provenance.Line)
			targets[k] = append(targets[k], model.PathFromID(e.To))
		}
	}
	provider := indexer.NewCommit(root, revision)
	defer provider.Close()
	entries, err := provider.Entries(ctx)
	if err != nil {
		return t, err
	}
	moduleRoots := map[string]bool{}
	for _, entry := range entries {
		switch path.Base(entry.Path) {
		case "pom.xml", "build.gradle", "build.gradle.kts":
			moduleRoots[path.Dir(entry.Path)] = true
		}
	}
	for i, n := range t.Snapshot.Nodes {
		if n.Language != "java" {
			continue
		}
		root := "."
		for d := path.Dir(n.Provenance.Path); d != "."; d = path.Dir(d) {
			if moduleRoots[d] {
				root = d
				break
			}
		}
		props := map[string]string{}
		for k, v := range n.Properties {
			props[k] = v
		}
		props["symbol_module_root"] = root
		if strings.Contains("/"+n.Provenance.Path, "/src/test/") {
			props["symbol_source_set"] = "test"
		} else {
			props["symbol_source_set"] = "main"
		}
		t.Snapshot.Nodes[i].Properties = props
	}
	var count int
	var total int64
	includedBy := map[string][]string{}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return t, err
		}
		adapter, ok := languages.ForPath(entry.Path)
		if !ok || indexer.ExcludedPath(entry.Path) || entry.Mode != indexer.ModeRegular || entry.Size > indexer.MaxSourceBytes || count >= indexer.MaxSourceFiles || total+entry.Size > indexer.MaxTotalSourceBytes {
			continue
		}
		selected := len(selection) == 0 || selection[0][entry.Path]
		if !selected && !strings.HasSuffix(entry.Path, ".go") && !strings.HasSuffix(entry.Path, ".rs") {
			continue
		}
		content, e := provider.Read(ctx, entry, indexer.MaxSourceBytes)
		if e != nil {
			return t, e
		}
		count++
		total += int64(len(content))
		if constrainedGo(entry.Path, content) {
			kept := make([]model.Node, 0, len(t.Snapshot.Nodes))
			for _, n := range t.Snapshot.Nodes {
				if n.Provenance.Path != entry.Path {
					kept = append(kept, n)
				}
			}
			t.Snapshot.Nodes = kept
			t.Snapshot.Diagnostics = append(t.Snapshot.Diagnostics, model.Diagnostic{Path: entry.Path, Severity: model.SeverityInfo, Message: "conditional Go compilation is outside symbol namespace resolution"})
			continue
		}
		if !selected {
			if strings.HasSuffix(entry.Path, ".rs") && strings.Contains(string(content), "include!") {
				for _, included := range rustIncludes(entry.Path, content) {
					includedBy[included] = append(includedBy[included], entry.Path)
				}
			}
			continue
		}
		if strings.HasSuffix(entry.Path, ".go") {
			signatures, calls := goCalls(content, entry.Path, targets, packageNames)
			for key, arity := range signatures {
				t.Signatures[key] = arity
			}
			t.Calls = append(t.Calls, calls...)
		}
		parsed, e := cache.Parse(ctx, adapter, languages.Source{Repository: root, Path: entry.Path, Revision: revision, Content: content})
		if e != nil {
			continue
		}
		if len(parsed.Diagnostics) > 0 {
			continue
		}
		texts := importTexts(entry.Path, content)
		if strings.HasSuffix(entry.Path, ".rs") {
			for _, included := range rustIncludes(entry.Path, content) {
				includedBy[included] = append(includedBy[included], entry.Path)
			}
		}
		if !strings.HasSuffix(entry.Path, ".go") {
			signatures, calls, unsupported, locations := literalCalls(entry.Path, content, parsed, targets, texts)
			for key, arity := range signatures {
				t.Signatures[key] = arity
			}
			for key := range unsupported {
				t.UnsupportedSignatures[key] = true
				if key == entry.Path+"\x00*" {
					t.Snapshot.Diagnostics = append(t.Snapshot.Diagnostics, model.Diagnostic{Path: entry.Path, Severity: model.SeverityWarning, Message: "literal reference parsing unavailable"})
				}
			}
			for key, p := range locations {
				t.SignatureLocations[key] = p
			}
			t.Calls = append(t.Calls, calls...)
		}
		if dynamicNamespace(entry.Path, content) {
			t.Snapshot.Diagnostics = append(t.Snapshot.Diagnostics, model.Diagnostic{Path: entry.Path, Severity: model.SeverityInfo, Message: "dynamic or wildcard export namespace is outside named symbol resolution"})
		}
		for _, ref := range dynamicReferences(entry.Path, content) {
			if ref.Module == "@conditional" {
				t.Snapshot.Diagnostics = append(t.Snapshot.Diagnostics, model.Diagnostic{Path: entry.Path, Severity: model.SeverityInfo, Message: "conditional C/C++ compilation is outside symbol namespace resolution"})
				continue
			}
			if ref.Module == "@macro" {
				t.Snapshot.Nodes = append(t.Snapshot.Nodes, model.Node{Kind: "symbol", Name: ref.Name, Properties: map[string]string{"qualified_name": ref.Name}, Provenance: model.Provenance{Path: entry.Path, Line: ref.Line, Revision: revision, Method: "macro_syntax", Evidence: model.VerifiedStatic}})
			} else {
				t.References = append(t.References, ref)
			}
		}
		for _, imp := range parsed.Imports {
			resolved := targets[fmt.Sprintf("%s:%d", entry.Path, imp.Line)]
			target := ""
			if len(resolved) == 1 {
				target = resolved[0]
			}
			named := importedNames(content, imp, texts)
			if imp.Language == "go" {
				alias := ""
				for _, target := range resolved {
					if packageNames[target] != "" {
						alias = packageNames[target]
						break
					}
				}
				if alias != "" {
					named = goSelectorsNamed(content, imp, alias)
				}
			}
			for _, name := range named {
				if name != "*" {
					if exported := reExportedName(content, imp, name, texts); exported != "" {
						t.Exports[refKey(Reference{entry.Path, imp.Module, "", name, imp.Line})] = exported
					}
					// Package imports may resolve to several files. Pick a live
					// declaration when available, otherwise compare every file.
					if len(resolved) > 1 {
						selected := ""
						for _, n := range t.Snapshot.Nodes {
							for _, p := range resolved {
								if top(n) && n.Name == name && n.Provenance.Path == p {
									selected = p
								}
							}
						}
						if selected != "" {
							t.References = append(t.References, Reference{entry.Path, imp.Module, selected, name, imp.Line})
						} else {
							for _, p := range resolved {
								t.References = append(t.References, Reference{entry.Path, imp.Module, p, name, imp.Line})
							}
						}
						continue
					}
					t.References = append(t.References, Reference{entry.Path, imp.Module, target, name, imp.Line})
				}
			}
		}
	}
	for i, n := range t.Snapshot.Nodes {
		if n.Language != "rust" {
			continue
		}
		namespace := n.Provenance.Path
		visited := map[string]bool{}
		for len(includedBy[namespace]) == 1 && !visited[namespace] {
			visited[namespace] = true
			namespace = includedBy[namespace][0]
		}
		if visited[namespace] || len(includedBy[namespace]) > 1 {
			t.Snapshot.Diagnostics = append(t.Snapshot.Diagnostics, model.Diagnostic{Path: n.Provenance.Path, Severity: model.SeverityInfo, Message: "ambiguous or cyclic Rust include namespace"})
			continue
		}
		props := map[string]string{}
		for k, v := range n.Properties {
			props[k] = v
		}
		props["symbol_rust_namespace"] = namespace
		t.Snapshot.Nodes[i].Properties = props
	}
	return t, nil
}

// Evaluate only fails for a newly introduced duplicate or a newly added
// explicit reference to a declaration that existed on the base and vanished.
// Missing external imports, existing defects and ambiguous resolution are not
// interpreted as source failures.
func Evaluate(base, combined Tree, changed []string) Result {
	r := Result{Status: model.StatusPassed, Evidence: model.Inferred, SupportedStatus: model.StatusPassed, SupportedEvidence: model.Inferred}
	blockingUnknown := false
	touched := map[string]bool{}
	for _, p := range changed {
		touched[p] = true
	}
	for _, d := range combined.Snapshot.Diagnostics {
		if sourceCoverageUnknown(d) && (touched[d.Path] || d.Path == "") {
			r.Applicable = true
			blockingUnknown = true
			r.Diagnostics = append(r.Diagnostics, d)
		}
	}
	for _, d := range base.Snapshot.Diagnostics {
		if sourceCoverageUnknown(d) && d.Path == "" {
			r.Applicable = true
			blockingUnknown = true
			r.Diagnostics = append(r.Diagnostics, d)
		}
	}
	for _, n := range combined.Snapshot.Nodes {
		if top(n) && touched[n.Provenance.Path] {
			r.Applicable = true
		}
	}
	bd, cd := declarations(base.Snapshot), declarations(combined.Snapshot)
	keys := make([]string, 0, len(cd))
	for k := range cd {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		defs := cd[k]
		if len(defs) < 2 || len(defs) <= len(bd[k]) {
			continue
		}
		relevant := false
		for _, n := range defs {
			relevant = relevant || touched[n.Provenance.Path]
		}
		if !relevant {
			continue
		}
		uncertainDefs := false
		for _, n := range defs {
			if (n.Language == "javascript" || n.Language == "typescript") && (n.Kind == "symbol" || n.Kind == "interface") {
				uncertainDefs = true
			}
			for _, d := range combined.Snapshot.Diagnostics {
				if d.Path == n.Provenance.Path && strings.Contains(d.Message, "conditional C/C++") {
					uncertainDefs = true
				}
			}
		}
		if uncertainDefs {
			r.Applicable = true
			r.UnknownCount++
			for _, n := range defs {
				for _, d := range combined.Snapshot.Diagnostics {
					if d.Path == n.Provenance.Path && strings.Contains(d.Message, "conditional C/C++") {
						blockingUnknown = true
					}
				}
			}
			r.Diagnostics = append(r.Diagnostics, model.Diagnostic{Path: defs[0].Provenance.Path, Severity: model.SeverityInfo, Message: "declaration merging or conditional namespace requires compiler context"})
			continue
		}
		f := model.NewFinding("symbol_duplicate_definition", "New duplicate declaration "+k+" in the combined tree.", model.Inferred)
		f.Severity = model.SeverityError
		f.Remediation = "Rename or remove one declaration, then repeat the gate."
		for _, n := range defs {
			p := n.Provenance
			p.Method = "symbol_namespace_resolution"
			p.Evidence = model.Inferred
			f.Locations = append(f.Locations, p)
		}
		r.Findings = append(r.Findings, f)
	}
	oldRefs := map[string]bool{}
	modules := map[string]string{}
	globalModules := map[string]string{}
	for _, ref := range base.References {
		oldRefs[refKey(ref)] = true
		if ref.Target != "" {
			modules[ref.From+"\x00"+ref.Module] = ref.Target
			if prev, ok := globalModules[ref.Module]; !ok || prev == ref.Target {
				globalModules[ref.Module] = ref.Target
			} else {
				globalModules[ref.Module] = ""
			}
		}
	}
	baseNames, candidateNames := exportedNames(base), exportedNames(combined)
	uncertain := map[string]bool{}
	conditionalDirs := map[string]bool{}
	for _, d := range append(append([]model.Diagnostic{}, base.Snapshot.Diagnostics...), combined.Snapshot.Diagnostics...) {
		if sourceCoverageUnknown(d) || strings.Contains(d.Message, "dynamic or wildcard export") {
			uncertain[d.Path] = true
		}
		if strings.Contains(d.Message, "conditional Go compilation") {
			conditionalDirs[path.Dir(d.Path)] = true
		}
	}
	seen := map[string]bool{}
	oldCalls := map[string]int{}
	for _, call := range base.Calls {
		oldCalls[callKey(call)]++
	}
	for _, call := range combined.Calls {
		k := callKey(call)
		if oldCalls[k] > 0 {
			oldCalls[k]--
			continue
		}
		if !touched[call.Reference.From] {
			continue
		}
		key := call.Reference.Target + "\x00" + call.Reference.Name
		if base.UnsupportedSignatures[key] || combined.UnsupportedSignatures[key] || call.Unknown != "" {
			r.UnknownCount++
			r.Diagnostics = append(r.Diagnostics, model.Diagnostic{Path: call.Reference.From, Severity: model.SeverityInfo, Message: "argument-count binding is unsupported for " + call.Reference.Name})
			continue
		}
		old, ok := base.Signatures[key]
		current, currentOK := combined.Signatures[key]
		if !ok || !currentOK || uncertain[call.Reference.Target] || conditionalDirs[path.Dir(call.Reference.Target)] || !old.accepts(call.Arguments) || current.accepts(call.Arguments) {
			continue
		}
		r.Applicable = true
		f := model.NewFinding("symbol_argument_count", fmt.Sprintf("%s newly calls %s with %d arguments; the combined declaration now accepts %s.", call.Reference.From, call.Reference.Name, call.Arguments, current.String()), model.Inferred)
		f.Severity = model.SeverityError
		f.Remediation = "Update the call and function parameters together, then repeat the gate."
		location := candidateNames[key].Provenance
		if p, ok := combined.SignatureLocations[key]; ok {
			location = p
		}
		location.Evidence = model.Inferred
		location.Method = "literal_import_call_arity"
		f.Locations = []model.Provenance{{Path: call.Reference.From, Line: call.Reference.Line, Revision: combined.Snapshot.Revision, Method: "literal_import_call_arity", Evidence: model.Inferred}, location}
		r.Findings = append(r.Findings, f)
	}
	for _, ref := range combined.References {
		if !touched[ref.From] || oldRefs[refKey(ref)] {
			continue
		}
		if ref.Target == "" {
			ref.Target = modules[ref.From+"\x00"+ref.Module]
			if ref.Target == "" {
				ref.Target = globalModules[ref.Module]
			}
		}
		if ref.Target == "" {
			message := "named import " + ref.Name + " has unresolved or ambiguous repository target"
			if ref.Module == "@dynamic" {
				message = "receiver call " + ref.Name + " cannot be bound to a declaration statically"
			}
			r.Diagnostics = append(r.Diagnostics, model.Diagnostic{Path: ref.From, Severity: model.SeverityInfo, Message: message})
			r.UnknownCount++
			continue
		}
		key := ref.Target + "\x00" + ref.Name
		if uncertain[ref.Target] || (strings.HasSuffix(ref.Target, ".go") && conditionalDirs[path.Dir(ref.Target)]) {
			blockingUnknown = true
			r.Applicable = true
			r.Diagnostics = append(r.Diagnostics, model.Diagnostic{Path: ref.Target, Severity: model.SeverityInfo, Message: "declaration source has incomplete structural coverage"})
			continue
		}
		declaration, existed := baseNames[key]
		if !existed && strings.HasSuffix(ref.Target, ".go") {
			for _, n := range base.Snapshot.Nodes {
				if top(n) && n.Language == "go" && n.Name == ref.Name && path.Dir(n.Provenance.Path) == path.Dir(ref.Target) {
					declaration = n
					existed = true
					break
				}
			}
		}
		if !existed {
			continue
		}
		r.Applicable = true
		if _, exists := candidateNames[key]; exists {
			continue
		}
		if seen[refKey(ref)] {
			continue
		}
		seen[refKey(ref)] = true
		f := model.NewFinding("symbol_dangling_reference", ref.From+" newly references removed name "+ref.Name+" from "+ref.Target+".", model.Inferred)
		f.Severity = model.SeverityError
		f.Remediation = "Update the named import and the declaration together, then repeat the gate."
		p := declaration.Provenance
		p.Evidence = model.Inferred
		p.Method = "symbol_named_import_resolution"
		f.Locations = []model.Provenance{p, {Path: ref.From, Line: ref.Line, Revision: combined.Snapshot.Revision, Method: "symbol_named_import_resolution", Evidence: model.Inferred}}
		r.Findings = append(r.Findings, f)
	}
	r.Applicable = r.Applicable || len(r.Findings) > 0
	if len(r.Findings) > 0 {
		r.Status = model.StatusFailed
		r.SupportedStatus = model.StatusFailed
	} else if len(r.Diagnostics) > 0 {
		r.Status = model.StatusUnknown
		r.Evidence = model.Unknown
	}
	if blockingUnknown && r.SupportedStatus != model.StatusFailed {
		r.SupportedStatus = model.StatusUnknown
		r.SupportedEvidence = model.Unknown
	}
	r.Explanation = fmt.Sprintf("Compared supported namespace declarations, named references and literal call argument counts: %d conflicts; %d unresolved relationships retained separately. Receiver binding, parameter types, overloads, defaults and macro expansion are not checked.", len(r.Findings), r.UnknownCount)
	return r
}

func refKey(r Reference) string { return r.From + "\x00" + r.Module + "\x00" + r.Name }
func top(n model.Node) bool {
	q := n.Properties["qualified_name"]
	return q != "" && !strings.Contains(q, ".") && (n.Kind == "function" || n.Kind == "class" || n.Kind == "type" || n.Kind == "interface" || n.Kind == "symbol" || n.Kind == "variable" || (n.Language == "rust" && n.Kind == "module"))
}
func names(s model.Snapshot) map[string]model.Node {
	out := map[string]model.Node{}
	for _, n := range s.Nodes {
		if top(n) {
			out[n.Provenance.Path+"\x00"+n.Name] = n
		}
	}
	return out
}

func exportedNames(t Tree) map[string]model.Node {
	out := names(t.Snapshot)
	for iteration := 0; iteration < len(t.References); iteration++ {
		changed := false
		for _, ref := range t.References {
			exported := t.Exports[refKey(ref)]
			if exported == "" || ref.Target == "" {
				continue
			}
			source, ok := out[ref.Target+"\x00"+ref.Name]
			if !ok {
				continue
			}
			key := ref.From + "\x00" + exported
			if _, exists := out[key]; !exists {
				out[key] = source
				changed = true
			}
		}
		if !changed {
			break
		}
	}
	return out
}
func declarations(s model.Snapshot) map[string][]model.Node {
	out := map[string][]model.Node{}
	packages := map[string]string{}
	for _, n := range s.Nodes {
		if n.Kind == "package" {
			packages[n.Provenance.Path] = n.Name
		}
	}
	for _, n := range s.Nodes {
		if !top(n) {
			continue
		}
		namespace := n.Provenance.Path
		switch n.Language {
		case "go":
			if strings.HasSuffix(namespace, "_test.go") {
				continue
			}
			if n.Name == "init" && n.Kind == "function" {
				continue
			}
			namespace = path.Dir(namespace) + ":" + packages[namespace]
		case "java":
			namespace = n.Properties["symbol_module_root"] + ":" + n.Properties["symbol_source_set"] + ":" + packages[namespace]
			if n.Kind != "class" && n.Kind != "type" && n.Kind != "interface" {
				continue
			}
		case "javascript", "typescript", "rust": // Literal Rust include! may share a module.
			if n.Language == "rust" && n.Properties["symbol_rust_namespace"] != "" {
				namespace = n.Properties["symbol_rust_namespace"]
			}
			if n.Properties["syntax_kind"] == "function_signature" {
				continue
			}
		case "c", "cpp":
			if n.Kind != "class" && n.Kind != "type" {
				continue
			} // Overloads and prototypes are valid.
		default:
			continue // Rust module wiring and C++ overloads require more evidence.
		}
		key := n.Language + ":" + namespace + ":" + n.Name
		out[key] = append(out[key], n)
	}
	return out
}
