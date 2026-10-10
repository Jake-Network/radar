// Package testselection recommends bounded verification commands from static
// repository evidence. Recommendations neither execute tests nor prove coverage.
package testselection

import (
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"path"
	"slices"
	"sort"
	"strings"

	gitrepo "github.com/Jake-Network/radar/internal/git"
	"github.com/Jake-Network/radar/internal/indexer"
	"github.com/Jake-Network/radar/internal/model"
	"github.com/Jake-Network/radar/internal/pathutil"
	"github.com/Jake-Network/radar/internal/planning"
)

const MaxFiles = 10000
const MaxFileBytes int64 = 1 << 20
const MaxTotalBytes int64 = 32 << 20

// Test is a conventional test file, not a compiler-validated executable case.
type Test struct {
	ID          string           `json:"id"`
	Path        string           `json:"path"`
	Framework   string           `json:"framework"`
	PackageRoot string           `json:"package_root"`
	Evidence    model.Provenance `json:"evidence"`
	// text is the bounded test source, kept in memory for lexical file
	// references; it is never serialized.
	text string
}
type Inventory struct {
	Revision    string             `json:"revision"`
	Tests       []Test             `json:"tests"`
	Diagnostics []model.Diagnostic `json:"diagnostics"`
	Manifests   []string           `json:"manifests"`
}
type Reason struct {
	Code         string             `json:"code"`
	Explanation  string             `json:"explanation"`
	Evidence     model.Evidence     `json:"evidence"`
	Locations    []model.Provenance `json:"locations,omitempty"`
	RelatedFiles []string           `json:"related_files,omitempty"`
}
type Command struct {
	ID              string   `json:"id"`
	Command         []string `json:"command"`
	CWD             string   `json:"cwd"`
	Framework       string   `json:"framework"`
	TestFiles       []string `json:"test_files"`
	Affected        []string `json:"affected"`
	EvidenceReasons []Reason `json:"evidence_reasons"`
	Priority        int      `json:"priority"`
	ToolAvailable   bool     `json:"tool_available"`
	// Tier is required or optional; set by Plan, empty in raw proposals.
	Tier string `json:"tier,omitempty"`
	// GroupedFrom lists the per-file candidate IDs merged into this command.
	GroupedFrom []string `json:"grouped_from,omitempty"`
}
type Proposal struct {
	Omitted     []Omission   `json:"omitted,omitempty"`
	Status      model.Status `json:"status"`
	Commands    []Command    `json:"commands"`
	Inventory   Inventory    `json:"inventory"`
	Limitations []string     `json:"limitations"`
}
type Options struct {
	Plan  *planning.Plan
	Limit int
	// ToolAvailable overrides PATH probing for deterministic tests or a known environment.
	ToolAvailable func(string) bool
}

// Discover reads working-tree or immutable commit files without importing code,
// invoking package managers, or evaluating repository scripts/configuration.
func Discover(ctx context.Context, root, ref string) (Inventory, error) {
	if ref == "" || ref == "WORKTREE" {
		p, e := indexer.NewWorktree(root)
		if e != nil {
			return Inventory{}, e
		}
		return DiscoverProvider(ctx, safeWorktree{Provider: p, root: root}, "WORKTREE")
	}
	sha, e := gitrepo.Resolve(ctx, root, ref)
	if e != nil {
		return Inventory{}, e
	}
	p := indexer.NewCommit(root, sha)
	defer p.Close()
	return DiscoverProvider(ctx, p, sha)
}

type safeWorktree struct {
	indexer.Provider
	root string
}

func (p safeWorktree) Read(ctx context.Context, e indexer.Entry, limit int64) ([]byte, error) {
	if _, err := pathutil.StatePath(p.root, e.Path); err != nil {
		return nil, err
	}
	return p.Provider.Read(ctx, e, limit)
}

// DiscoverProvider shares identical discovery logic for source-state providers.
func DiscoverProvider(ctx context.Context, p indexer.Provider, revision string) (Inventory, error) {
	inv := Inventory{Revision: revision, Tests: []Test{}, Diagnostics: []model.Diagnostic{}, Manifests: []string{}}
	entries, e := p.Entries(ctx)
	if e != nil {
		return inv, e
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	contents := map[string]string{}
	var total int64
	count := 0
	diagnostic := func(file, msg string) {
		inv.Diagnostics = append(inv.Diagnostics, model.Diagnostic{Path: file, Severity: model.SeverityWarning, Message: msg})
	}
	for _, entry := range entries {
		if e := ctx.Err(); e != nil {
			return inv, e
		}
		if _, e := pathutil.RepoRelative(entry.Path); e != nil {
			diagnostic(entry.Path, "unsafe inventory path skipped")
			continue
		}
		if indexer.ExcludedPath(entry.Path) {
			continue
		}
		if !relevant(entry.Path) {
			continue
		}
		if entry.Mode != indexer.ModeRegular {
			diagnostic(entry.Path, "symlink or non-regular test/configuration skipped")
			continue
		}
		if entry.Size > MaxFileBytes {
			diagnostic(entry.Path, "test/configuration exceeds 1 MiB discovery limit")
			continue
		}
		if count >= MaxFiles || total+entry.Size > MaxTotalBytes {
			diagnostic(entry.Path, "test inventory read budget exceeded; inventory incomplete")
			continue
		}
		data, e := p.Read(ctx, entry, MaxFileBytes)
		if e != nil {
			if errors.Is(e, context.Canceled) || errors.Is(e, context.DeadlineExceeded) {
				return inv, e
			}
			diagnostic(entry.Path, "test/configuration unreadable or unsafe; skipped")
			continue
		}
		if int64(len(data)) > MaxFileBytes {
			diagnostic(entry.Path, "provider exceeded discovery read limit; skipped")
			continue
		}
		count++
		total += int64(len(data))
		if total > MaxTotalBytes {
			diagnostic(entry.Path, "test inventory read budget exceeded; inventory incomplete")
			continue
		}
		contents[entry.Path] = string(data)
		if isManifest(entry.Path) {
			inv.Manifests = append(inv.Manifests, entry.Path)
			if path.Base(entry.Path) == "package.json" {
				var metadata map[string]json.RawMessage
				if json.Unmarshal(data, &metadata) != nil || metadata == nil {
					diagnostic(entry.Path, "malformed package.json object; framework metadata unavailable")
				}
			}
		}
	}
	for _, entry := range entries {
		content, ok := contents[entry.Path]
		if !ok || isManifest(entry.Path) {
			continue
		}
		framework := classify(entry.Path, content, contents)
		if framework == "" {
			continue
		}
		ecosystem := framework
		if framework == "jest" || framework == "vitest" || framework == "node-test" || framework == "javascript-unknown" {
			ecosystem = "javascript"
		}
		if framework == "pytest" || framework == "unittest" {
			ecosystem = "python"
		}
		root := packageRoot(entry.Path, ecosystem, contents)
		provenance := model.Provenance{Revision: revision, Path: entry.Path, Method: "static_test_convention:" + framework, Evidence: model.Inferred}
		inv.Tests = append(inv.Tests, Test{ID: model.StableID("test", entry.Path, framework), Path: entry.Path, Framework: framework, PackageRoot: root, Evidence: provenance, text: content})
	}
	return inv, nil
}
func relevant(p string) bool {
	if isManifest(p) {
		return true
	}
	switch path.Ext(p) {
	case ".py", ".go", ".rs", ".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs", ".mts", ".cts":
		return true
	}
	return false
}
func isManifest(p string) bool {
	switch path.Base(p) {
	case "go.mod", "Cargo.toml", "package.json", "pyproject.toml", "pytest.ini", "setup.cfg", "tox.ini", "requirements.txt":
		return true
	}
	return false
}
func packageRoot(file, ecosystem string, contents map[string]string) string {
	for dir := path.Dir(file); ; dir = path.Dir(dir) {
		names := []string{}
		switch ecosystem {
		case "go":
			names = []string{"go.mod"}
		case "cargo":
			names = []string{"Cargo.toml"}
		case "javascript":
			names = []string{"package.json"}
		case "python":
			names = []string{"pyproject.toml", "pytest.ini", "setup.cfg", "tox.ini", "requirements.txt"}
		}
		for _, name := range names {
			if _, ok := contents[path.Join(dir, name)]; ok {
				return dir
			}
		}
		if dir == "." {
			return "."
		}
	}
}
func classify(file, content string, contents map[string]string) string {
	base := path.Base(file)
	switch path.Ext(file) {
	case ".go":
		if strings.HasSuffix(file, "_test.go") {
			return "go"
		}
	case ".rs":
		if strings.Contains(content, "#[test]") || strings.Contains(content, "#[tokio::test]") || strings.Contains(file, "/tests/") || strings.HasPrefix(file, "tests/") {
			return "cargo"
		}
	case ".py":
		if !(strings.HasPrefix(base, "test_") || strings.HasSuffix(base, "_test.py")) {
			return ""
		}
		if framework := pythonFramework(content); framework != "" {
			return framework
		}
		root := packageRoot(file, "python", contents)
		for _, name := range []string{"pyproject.toml", "pytest.ini", "setup.cfg", "tox.ini", "requirements.txt"} {
			if strings.Contains(contents[path.Join(root, name)], "pytest") {
				return "pytest"
			}
		}
	case ".js", ".jsx", ".ts", ".tsx", ".mjs", ".cjs", ".mts", ".cts":
		if !(strings.Contains(base, ".test.") || strings.Contains(base, ".spec.") || strings.Contains("/"+file, "/__tests__/")) {
			return ""
		}
		if strings.Contains(content, "node:test") {
			return "node-test"
		}
		if strings.Contains(content, "from 'vitest'") || strings.Contains(content, "from \"vitest\"") {
			return "vitest"
		}
		if strings.Contains(content, "@jest/globals") {
			return "jest"
		}
		root := packageRoot(file, "javascript", contents)
		var manifest struct {
			Dependencies    map[string]json.RawMessage `json:"dependencies"`
			DevDependencies map[string]json.RawMessage `json:"devDependencies"`
		}
		if json.Unmarshal([]byte(contents[path.Join(root, "package.json")]), &manifest) == nil {
			if _, ok := manifest.DevDependencies["vitest"]; ok {
				return "vitest"
			}
			if _, ok := manifest.Dependencies["vitest"]; ok {
				return "vitest"
			}
			if _, ok := manifest.DevDependencies["jest"]; ok {
				return "jest"
			}
			if _, ok := manifest.Dependencies["jest"]; ok {
				return "jest"
			}
		}
		// Unknown conventional JS tests remain inventory candidates, never auto-run.
		return "javascript-unknown"
	}
	return ""
}

func Recommend(ctx context.Context, root, ref string, snapshot model.Snapshot, changed []string, plan *planning.Plan) (Proposal, error) {
	inv, e := Discover(ctx, root, ref)
	if e != nil {
		return Proposal{}, e
	}
	return Select(inv, snapshot, changed, Options{Plan: plan})
}

// Select ranks declared intent and inferred impact. All commands remain
// proposals: static reachability does not establish complete behavioral coverage.
func Select(inv Inventory, snapshot model.Snapshot, changed []string, o Options) (Proposal, error) {
	result := Proposal{Status: model.StatusIncomplete, Commands: []Command{}, Inventory: inv, Limitations: []string{
		"Recommendations are inferred static candidates, not complete test coverage or evidence of passing tests.",
		"Dynamic imports, external services, path aliases and framework configuration can require additional tests.",
		"Static naming and content markers can match comments or strings; test classification is inferred, not compiler-validated.",
		"TOML and INI configuration markers are read statically; their syntax and complete runner semantics are not validated.",
		"Go modules with concrete package or dependency matches omit unrelated package fallback commands; unrecorded dependencies can still require broader tests.",
		"Tool availability checks executables only; dependencies, setup and environment are not verified or installed.",
	}}
	if e := validateInventory(inv); e != nil {
		return result, e
	}
	available := o.ToolAvailable
	if available == nil {
		available = func(tool string) bool { _, e := exec.LookPath(tool); return e == nil }
	}
	changedSet := map[string]bool{}
	for _, file := range changed {
		clean, e := pathutil.RepoRelative(file)
		if e != nil {
			return result, e
		}
		changedSet[clean] = true
	}
	affected, routes, contractImpact, pathTruncated := dependencyImpact(snapshot, changedSet)
	if pathTruncated {
		result.Limitations = append(result.Limitations, "Impact traversal bounded to 50000 nodes and 16 evidence locations per path; inspect the indexed graph for omitted detail.")
	}
	groups := commandGroups{}
	if o.Plan != nil {
		if e := groups.addPlanCommands(*o.Plan, changed, available); e != nil {
			return result, e
		}
	}
	owners, roots := packageOwners(inv, changedSet)
	directGoRoots := goRootsWithDirectMatch(inv, affected, changedSet)
	for _, test := range inv.Tests {
		priority, reason, files := testPriority(test, changedSet, changed, affected, contractImpact, owners, roots, directGoRoots)
		if priority == 0 {
			continue
		}
		omitUnsupported := func(explanation string) {
			tier := TierRequired
			if reason == "package_fallback" {
				tier = TierOptional
			}
			result.Omitted = append(result.Omitted, Omission{ID: test.ID, CWD: test.PackageRoot, TestFiles: []string{test.Path}, Tier: tier, Reason: "unsupported_configuration", Explanation: explanation})
		}
		if test.Framework == "cargo" && !slices.Contains(inv.Manifests, path.Join(test.PackageRoot, "Cargo.toml")) {
			result.Limitations = append(result.Limitations, "Cargo test has no discovered Cargo.toml: "+test.Path)
			omitUnsupported("Cargo test has no discovered Cargo.toml")
			continue
		}
		argv, cwd := commandFor(test)
		if test.Framework == "go" && reason == "package_fallback" {
			argv = []string{"go", "test", "-json", "./..."}
		}
		if len(argv) == 0 {
			result.Limitations = append(result.Limitations, "No safely recognized runner for "+test.Path+"; inspect test configuration manually.")
			omitUnsupported("No safely recognized runner for " + test.Framework)
			continue
		}
		description := reasonDescriptions[reason]
		locations := []model.Provenance{test.Evidence}
		if reason == "dependency_impact" || reason == "declared_contract_impact" {
			locations = append(locations, routes[test.Path]...)
		}
		toolAvailable := available(argv[0])
		if test.Framework == "jest" || test.Framework == "vitest" {
			toolAvailable = false
		}
		groups.add(Command{Command: argv, CWD: cwd, Framework: test.Framework, TestFiles: []string{test.Path}, Affected: unique(files), EvidenceReasons: []Reason{{Code: reason, Explanation: description, Evidence: model.Inferred, Locations: locations, RelatedFiles: unique(files)}}, Priority: priority, ToolAvailable: toolAvailable})
	}
	result.Commands = groups.commands()
	if o.Limit > 0 && len(result.Commands) > o.Limit {
		for _, c := range result.Commands[o.Limit:] {
			c.Tier = tierFor(c)
			result.Omitted = append(result.Omitted, omission(c, "recommendation_limit", "Recommendation limit reached before execution planning."))
		}
		result.Commands = result.Commands[:o.Limit]
		result.Limitations = append(result.Limitations, "Recommendation limit reached; inspect inventory for omitted candidates.")
	}
	if len(result.Commands) == 0 {
		result.Limitations = append(result.Limitations, "No supported relevant test commands discovered; supply explicit plan test_run rules.")
	}
	if len(inv.Diagnostics) > 0 {
		result.Limitations = append(result.Limitations, "Inventory contains skipped inputs; inspect diagnostics.")
	}
	result.Limitations = unique(result.Limitations)
	return result, nil
}

// validateInventory rejects test paths that escape the repository or their
// declared package root.
func validateInventory(inv Inventory) error {
	for _, test := range inv.Tests {
		if _, e := pathutil.RepoRelative(test.Path); e != nil {
			return e
		}
		if _, e := pathutil.RepoRelative(test.PackageRoot); e != nil {
			return e
		}
		if !inside(test.PackageRoot, test.Path) {
			return errors.New("test path is outside its declared package root")
		}
	}
	return nil
}

// commandGroups merges candidates that resolve to the same command ID.
type commandGroups map[string]*Command

func (groups commandGroups) add(c Command) {
	c.ID = commandID(c)
	key := c.ID
	if existing, ok := groups[key]; ok {
		existing.TestFiles = unique(append(existing.TestFiles, c.TestFiles...))
		existing.Affected = unique(append(existing.Affected, c.Affected...))
		existing.EvidenceReasons = append(existing.EvidenceReasons, c.EvidenceReasons...)
		if c.Priority > existing.Priority {
			existing.Priority = c.Priority
		}
		return
	}
	groups[key] = &c
}

// addPlanCommands adds the exact test_run commands declared by a plan.
func (groups commandGroups) addPlanCommands(plan planning.Plan, changed []string, available func(string) bool) error {
	rules := []*planning.Rule{}
	for _, criterion := range plan.Acceptance {
		rules = append(rules, criterion.Rule)
	}
	for _, constraint := range plan.Constraints {
		rules = append(rules, constraint.Rule)
	}
	for _, rule := range rules {
		if rule == nil || rule.Kind != "test_run" {
			continue
		}
		if e := planning.ValidateRule(*rule); e != nil {
			return e
		}
		cwd := rule.CWD
		if cwd == "" {
			cwd = "."
		}
		groups.add(Command{Command: append([]string(nil), rule.Command...), CWD: cwd, Framework: "plan-declared", TestFiles: []string{}, Affected: unique(changed), EvidenceReasons: []Reason{{Code: "plan_declared", Explanation: "Exact command declared by the supplied plan; explicit setup and environment still need review.", Evidence: model.Proposed}}, Priority: 100, ToolAvailable: available(rule.Command[0])})
	}
	return nil
}

// commands returns the merged commands, highest priority first.
func (groups commandGroups) commands() []Command {
	commands := []Command{}
	for _, c := range groups {
		c.TestFiles = unique(c.TestFiles)
		c.Affected = unique(c.Affected)
		sort.Slice(c.EvidenceReasons, func(i, j int) bool {
			a, b := c.EvidenceReasons[i], c.EvidenceReasons[j]
			return a.Code+strings.Join(a.RelatedFiles, "\x00") < b.Code+strings.Join(b.RelatedFiles, "\x00")
		})
		commands = append(commands, *c)
	}
	sort.Slice(commands, func(i, j int) bool {
		if commands[i].Priority != commands[j].Priority {
			return commands[i].Priority > commands[j].Priority
		}
		return commands[i].ID < commands[j].ID
	})
	return commands
}

// packageOwners maps each changed non-test file to the deepest test package
// root that contains it, and returns the set of those roots.
func packageOwners(inv Inventory, changedSet map[string]bool) (map[string]string, map[string]bool) {
	roots := map[string]bool{}
	owners := map[string]string{}
	isTest := map[string]bool{}
	for _, test := range inv.Tests {
		isTest[test.Path] = true
	}
	for file := range changedSet {
		// A changed test file is selected directly and cannot affect its
		// siblings; shared fixtures (conftest.py, helpers) are not test files
		// and still fall back to their package root.
		if documentation(file) || isTest[file] {
			continue
		}
		best := ""
		for _, test := range inv.Tests {
			if inside(test.PackageRoot, file) && (best == "" || rootDepth(test.PackageRoot) > rootDepth(best)) {
				best = test.PackageRoot
			}
		}
		if best != "" {
			owners[file] = best
			roots[best] = true
		}
	}
	return owners, roots
}

// goRootsWithDirectMatch prefers concrete Go package/dependency matches over
// every test package in a root module. Without a match one conservative
// whole-module command is retained.
func goRootsWithDirectMatch(inv Inventory, affected map[string]string, changedSet map[string]bool) map[string]bool {
	directGoRoots := map[string]bool{}
	for _, test := range inv.Tests {
		if test.Framework != "go" {
			continue
		}
		if _, ok := affected[test.Path]; ok {
			directGoRoots[test.PackageRoot] = true
		}
		for file := range changedSet {
			if strings.HasSuffix(file, ".go") && path.Dir(file) == path.Dir(test.Path) {
				directGoRoots[test.PackageRoot] = true
			}
		}
	}
	return directGoRoots
}

// testPriority ranks why a test is relevant to the change. A zero priority
// means the test is not selected.
func testPriority(test Test, changedSet map[string]bool, changed []string, affected map[string]string, contractImpact map[string]bool, owners map[string]string, roots, directGoRoots map[string]bool) (int, string, []string) {
	priority := 0
	reason := ""
	files := []string{}
	if changedSet[test.Path] {
		priority = 90
		reason = "changed_test"
		files = append(files, test.Path)
	} else if source, ok := affected[test.Path]; ok {
		priority = 80
		reason = "dependency_impact"
		if contractImpact[test.Path] {
			reason = "declared_contract_impact"
		}
		files = append(files, source)
	}
	// A test naming a changed data, schema or configuration file reads it
	// at runtime more often than not; the match is lexical, not proof.
	if priority == 0 {
		for file := range changedSet {
			if referenced(test, file) {
				priority = 75
				reason = "file_reference"
				files = append(files, file)
			}
		}
	}
	if test.Framework == "go" && priority == 0 {
		for file := range changedSet {
			if strings.HasSuffix(file, ".go") && path.Dir(file) == path.Dir(test.Path) {
				priority = 70
				reason = "go_package_companion"
				files = append(files, file)
			}
		}
	}
	if priority == 0 {
		for file := range changedSet {
			if owners[file] == test.PackageRoot && !(test.Framework == "go" && directGoRoots[test.PackageRoot]) {
				priority = 30
				reason = "package_fallback"
				files = append(files, file)
			}
		}
	}
	if len(roots) > 1 && (test.PackageRoot == "." || priority > 0) && (strings.Contains("/"+test.Path, "/integration/") || strings.Contains(path.Base(test.Path), "integration") || strings.Contains("/"+test.Path, "/e2e/")) {
		if priority < 85 {
			priority = 85
		}
		reason = "cross_component_integration"
		if len(files) == 0 {
			files = unique(changed)
		}
	}
	return priority, reason, files
}

var reasonDescriptions = map[string]string{"changed_test": "Changed conventional test file.", "dependency_impact": "Test imports a changed file through recorded inferred dependency paths.", "declared_contract_impact": "Test imports a consumer reached through explicit producer/schema and declared contract dependencies; runtime usage remains unproven.", "go_package_companion": "Go test shares a package directory with changed Go source.", "package_fallback": "Conservative package-root fallback; a direct dependency was not established.", "file_reference": "Test source names a changed non-code file (for example a schema or fixture); lexical reference, runtime use unproven.", "cross_component_integration": "Integration-named suite prioritized because changes span multiple test package roots; naming is not semantic proof."}

func inside(root, file string) bool { return root == "." || strings.HasPrefix(file, root+"/") }
func unique(values []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, v := range values {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	sort.Strings(out)
	return out
}
func commandFor(t Test) ([]string, string) {
	rel := strings.TrimPrefix(t.Path, t.PackageRoot+"/")
	if t.PackageRoot == "." {
		rel = t.Path
	}
	switch t.Framework {
	case "go":
		dir := path.Dir(rel)
		if dir == "." {
			return []string{"go", "test", "-json", "."}, t.PackageRoot
		}
		return []string{"go", "test", "-json", "./" + dir}, t.PackageRoot
	case "pytest":
		return []string{"python3", "-m", "pytest", "./" + rel}, t.PackageRoot
	case "unittest":
		// Discovery works for non-package test directories and avoids importing a guessed module name.
		return []string{"python3", "-m", "unittest", "discover", "-s", path.Dir(rel), "-p", path.Base(rel)}, t.PackageRoot
	case "node-test":
		switch path.Ext(rel) {
		case ".js", ".mjs", ".cjs":
		default:
			return nil, t.PackageRoot
		}
		return []string{"node", "--test", "--test-reporter=tap", "./" + rel}, t.PackageRoot
	case "jest":
		return []string{"./node_modules/.bin/jest", "--runInBand", "--runTestsByPath", "./" + rel}, t.PackageRoot
	case "vitest":
		return []string{"./node_modules/.bin/vitest", "run", "./" + rel}, t.PackageRoot
	case "cargo":
		return []string{"cargo", "test", "--manifest-path", path.Join(t.PackageRoot, "Cargo.toml")}, "."
	}
	return nil, t.PackageRoot
}

func rootDepth(root string) int {
	if root == "." || root == "" {
		return 0
	}
	return strings.Count(root, "/") + 1
}

// documentation reports changes that cannot affect test outcomes on their own.
func documentation(p string) bool {
	switch strings.ToLower(path.Ext(p)) {
	case ".md", ".rst", ".adoc", ".png", ".jpg", ".jpeg", ".gif", ".svg":
		return true
	}
	base := path.Base(p)
	return base == "LICENSE" || base == "CHANGES" || base == "AUTHORS"
}

// referenced reports whether a test names a changed non-code file by its
// base name. Short or generic names are ignored to bound false positives.
func referenced(t Test, file string) bool {
	if t.text == "" || relevant(file) || documentation(file) || t.Path == file {
		return false
	}
	base := path.Base(file)
	if len(base) < 8 || !strings.Contains(base, ".") {
		return false
	}
	return strings.Contains(t.text, base)
}
