// Package lockfiles compares direct dependency names with committed lockfiles.
// It performs no dependency resolution or installation.
package lockfiles

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"

	gitrepo "github.com/Jake-Network/radar/internal/git"
	"github.com/Jake-Network/radar/internal/migrations"
	"github.com/Jake-Network/radar/internal/model"
	"gopkg.in/yaml.v3"
)

const CheckID = "lockfile_consistency"

type Result = migrations.Result

type pair struct{ manifest, lock, tool string }

func Analyze(ctx context.Context, root, base string, inputs []string, combined string, changed []string) (Result, error) {
	relevant := false
	for _, p := range changed {
		switch path.Base(p) {
		case "package.json", "package-lock.json", "pnpm-lock.yaml", "pyproject.toml", "uv.lock", "Cargo.toml", "Cargo.lock", "go.mod", "go.sum":
			relevant = true
		}
	}
	if !relevant {
		return Result{}, nil
	}
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

func Evaluate(ctx context.Context, trees []migrations.Tree, changed []string, read migrations.Reader) (Result, error) {
	if len(trees) < 2 {
		return Result{}, fmt.Errorf("lockfile comparison needs base and combined trees")
	}
	pairs := map[pair]bool{}
	for _, tree := range []migrations.Tree{trees[0], trees[len(trees)-1]} {
		files := map[string]bool{}
		for _, f := range tree.Files {
			files[f.Path] = true
		}
		for _, f := range tree.Files {
			locks := []string{}
			tool := ""
			switch path.Base(f.Path) {
			case "package.json":
				locks = []string{"package-lock.json", "pnpm-lock.yaml"}
				tool = "npm"
			case "pyproject.toml":
				locks = []string{"uv.lock"}
				tool = "uv"
			case "Cargo.toml":
				locks = []string{"Cargo.lock"}
				tool = "cargo"
			case "go.mod":
				locks = []string{"go.sum"}
				tool = "go"
			}
			for _, name := range locks {
				lp := ""
				for dir := path.Dir(f.Path); ; dir = path.Dir(dir) {
					candidate := path.Join(dir, name)
					if files[candidate] {
						lp = candidate
						break
					}
					if dir == "." || tool == "go" {
						break
					}
				}
				if lp == "" {
					continue
				}
				for _, c := range changed {
					if c == f.Path || c == lp || tool == "cargo" && c == path.Join(path.Dir(lp), "Cargo.toml") {
						pairs[pair{f.Path, lp, tool}] = true
					}
				}
			}
		}
	}
	out := Result{Applicable: len(pairs) > 0, Status: model.StatusPassed, Evidence: model.VerifiedStatic, Findings: []model.Finding{}, Diagnostics: []model.Diagnostic{}}
	if !out.Applicable {
		return out, nil
	}
	ordered := []pair{}
	for p := range pairs {
		ordered = append(ordered, p)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].manifest+ordered[i].lock < ordered[j].manifest+ordered[j].lock })
	for _, p := range ordered {
		states := make([]map[string]bool, len(trees))
		unknown := make([]string, len(trees))
		majors := make([]map[string]string, len(trees))
		for i, t := range trees {
			if e := ctx.Err(); e != nil {
				return Result{}, e
			}
			if t.Unavailable != "" {
				unknown[i] = t.Unavailable
				continue
			}
			contents := map[string][]byte{}
			for _, f := range t.Files {
				if f.Path != p.manifest && f.Path != p.lock && !(p.tool == "cargo" && f.Path == path.Join(path.Dir(p.lock), "Cargo.toml")) {
					continue
				}
				b, e := read(f.OID)
				if e != nil {
					unknown[i] = "file cannot be read within analysis limit"
					continue
				}
				if len(b) > 1<<20 {
					unknown[i] = "file exceeds analysis limit"
					continue
				}
				contents[f.Path] = b
			}
			if unknown[i] != "" {
				continue
			}
			if contents[p.manifest] == nil {
				states[i] = map[string]bool{}
				continue
			}
			deps, locked, e := dependencies(p, contents[p.manifest], contents[p.lock], contents[path.Join(path.Dir(p.lock), "Cargo.toml")])
			if e != nil {
				unknown[i] = e.Error()
				continue
			}
			majors[i] = declarationMajors(p, contents[p.manifest])
			states[i] = map[string]bool{}
			for name := range deps {
				if !locked[name] {
					states[i][name] = true
				}
			}
		}
		last := len(trees) - 1
		out.Findings = append(out.Findings, divergentMajors(p, trees, majors)...)
		if unknown[last] != "" || (unknown[0] != "" && len(states[last]) > 0) {
			if out.Status != model.StatusFailed {
				out.Status = model.StatusUnknown
				out.Evidence = model.Unknown
			}
			out.Diagnostics = append(out.Diagnostics, model.Diagnostic{Path: p.manifest, Message: "Cannot compare direct dependencies: " + unknown[last] + unknown[0], Severity: model.SeverityWarning})
			continue
		}
		names := []string{}
		for n := range states[last] {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			f := model.NewFinding("lockfile_dependency_missing", fmt.Sprintf("%s declares %s, which is absent from %s.", p.manifest, n, p.lock), model.VerifiedStatic)
			f.Locations = []model.Provenance{{Repository: model.RepositoryID, Revision: trees[last].Revision, Path: p.manifest, Method: CheckID, Evidence: model.VerifiedStatic}, {Repository: model.RepositoryID, Revision: trees[last].Revision, Path: p.lock, Method: CheckID, Evidence: model.VerifiedStatic}}
			if states[0][n] {
				f.Code += "_on_base"
				f.Explanation += " The base already has this inconsistency."
			} else {
				f.Severity = model.SeverityError
				f.Explanation += " It is new relative to the base."
				for i := 1; i < last; i++ {
					if states[i][n] {
						f.Explanation += " It also occurs with " + trees[i].Label + "."
					}
				}
				out.Status = model.StatusFailed
				out.Evidence = model.VerifiedStatic
			}
			switch p.tool {
			case "npm":
				if path.Base(p.lock) == "pnpm-lock.yaml" {
					f.Remediation = "Regenerate the lockfile with pnpm install --lockfile-only."
				} else {
					f.Remediation = "Regenerate the lockfile with npm install --package-lock-only."
				}
			case "uv":
				f.Remediation = "Regenerate the lockfile with uv lock."
			case "cargo":
				f.Remediation = "Regenerate the lockfile with cargo generate-lockfile."
			case "go":
				f.Remediation = "Regenerate module checksums with go mod tidy."
			}
			out.Findings = append(out.Findings, f)
		}
	}
	out.Explanation = "Compared committed direct dependency names with lockfiles; version-range resolution is not checked."
	return out, nil
}

func object(data []byte) (map[string]any, error) {
	var m map[string]any
	e := json.Unmarshal(data, &m)
	if e == nil && m == nil {
		e = fmt.Errorf("expected JSON object")
	}
	return m, e
}
func names(m map[string]any, sections ...string) (map[string]bool, error) {
	out := map[string]bool{}
	for _, s := range sections {
		if v, ok := m[s]; ok {
			deps, ok := v.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("invalid dependency map %s", s)
			}
			for n, v := range deps {
				if _, ok := v.(string); !ok {
					return nil, fmt.Errorf("nonliteral dependency %s", n)
				}
				out[n] = true
			}
		}
	}
	return out, nil
}
func dependencies(p pair, manifest, lock []byte, workspace ...[]byte) (map[string]bool, map[string]bool, error) {
	deps, locked := map[string]bool{}, map[string]bool{}
	if len(lock) == 0 && p.tool != "go" {
		return nil, nil, fmt.Errorf("committed lockfile is missing or empty")
	}
	switch p.tool {
	case "npm":
		m, e := object(manifest)
		if e != nil {
			return nil, nil, e
		}
		deps, e = names(m, "dependencies", "devDependencies", "optionalDependencies")
		if e != nil {
			return nil, nil, e
		}
		if path.Base(p.lock) == "package-lock.json" {
			l, e := object(lock)
			if e != nil {
				return nil, nil, e
			}
			if packs, ok := l["packages"].(map[string]any); ok {
				root, ok := packs[importerPath(p)].(map[string]any)
				if !ok {
					return nil, nil, fmt.Errorf("missing root lockfile importer")
				}
				locked, e = names(root, "dependencies", "devDependencies", "optionalDependencies")
				if e != nil {
					return nil, nil, e
				}
			} else {
				d, ok := l["dependencies"].(map[string]any)
				if !ok {
					return nil, nil, fmt.Errorf("unsupported package-lock format")
				}
				for n := range d {
					locked[n] = true
				}
			}
		} else {
			var l map[string]any
			if e := yaml.Unmarshal(lock, &l); e != nil {
				return nil, nil, e
			}
			importers, ok := l["importers"].(map[string]any)
			if !ok {
				return nil, nil, fmt.Errorf("unsupported pnpm lockfile: no importers")
			}
			importer := importerPath(p)
			if importer == "" {
				importer = "."
			}
			root, ok := importers[importer].(map[string]any)
			if !ok {
				return nil, nil, fmt.Errorf("missing root pnpm importer")
			}
			for _, section := range []string{"dependencies", "devDependencies", "optionalDependencies"} {
				if v, ok := root[section]; ok {
					m, ok := v.(map[string]any)
					if !ok {
						return nil, nil, fmt.Errorf("invalid pnpm importer")
					}
					for n := range m {
						locked[n] = true
					}
				}
			}
		}
	case "go":
		replacements, e := goReplacements(string(manifest))
		if e != nil {
			return nil, nil, e
		}
		require := false
		for _, line := range strings.Split(string(manifest), "\n") {
			line = strings.TrimSpace(strings.SplitN(line, "//", 2)[0])
			if line == "" {
				continue
			}
			if line == "require (" {
				require = true
				continue
			}
			if line == ")" {
				require = false
				continue
			}
			fields := strings.Fields(line)
			if len(fields) > 0 && fields[0] == "require" {
				fields = fields[1:]
			} else if !require {
				continue
			}
			if len(fields) != 2 {
				return nil, nil, fmt.Errorf("nonliteral go require")
			}
			name, version := fields[0], fields[1]
			replacement, replaced := replacements[name+"@"+version]
			if !replaced {
				replacement, replaced = replacements[name]
			}
			if replaced {
				if replacement == "" { // Local replacements have no module checksums.
					continue
				}
				deps[replacement] = true
			} else {
				deps[name+"@"+version] = true
			}
		}
		for _, line := range strings.Split(string(lock), "\n") {
			f := strings.Fields(line)
			if len(f) == 3 {
				locked[f[0]+"@"+strings.TrimSuffix(f[1], "/go.mod")] = true
			}
		}
	case "cargo":
		var e error
		inheritSource := string(manifest)
		if len(workspace) > 0 && len(workspace[0]) > 0 {
			inheritSource = string(workspace[0])
		}
		inherited, e := cargoWorkspaceExpressions(inheritSource)
		if e != nil {
			return nil, nil, e
		}
		deps, e = cargoDependencies(string(manifest), inherited)
		if e != nil {
			return nil, nil, e
		}
		blocks, e := packageBlocks(string(lock))
		if e != nil {
			return nil, nil, e
		}
		for _, block := range blocks {
			header := strings.SplitN(block, "[", 2)[0]
			names := tomlName.FindAllStringSubmatch(header, -1)
			if len(names) != 1 {
				return nil, nil, fmt.Errorf("Cargo lockfile package name missing or duplicated")
			}
			locked[names[0][1]] = true
		}

	case "uv":
		project := projectSection(string(manifest))
		if project == "" && !strings.Contains(string(manifest), "[tool.uv.workspace]") {
			return nil, nil, fmt.Errorf("unsupported project manifest: no project table")
		}
		dependencyFields := projectDependencyField.FindStringIndex(project)
		if strings.Contains(project, "dynamic") && strings.Contains(project, "dependencies") {
			return nil, nil, fmt.Errorf("dynamic project dependencies unsupported")
		}
		if dependencyFields == nil {
			return deps, locked, nil
		}
		declared, e := literalStringArray(project[dependencyFields[1]:])
		if e != nil {
			return nil, nil, e
		}
		for _, requirement := range declared {
			name := dependencyName.FindString(requirement)
			if name == "" {
				return nil, nil, fmt.Errorf("invalid project dependency")
			}
			deps[normalize(name)] = true
		}
		projectName := ""
		if m := tomlName.FindStringSubmatch(project); m != nil {
			projectName = normalize(m[1])
		}
		if projectName == "" {
			return nil, nil, fmt.Errorf("project name absent from manifest")
		}
		blocks, e := packageBlocks(string(lock))
		if e != nil {
			return nil, nil, e
		}
		seen := false
		for _, b := range blocks {
			n := tomlName.FindStringSubmatch(b)
			if n == nil || normalize(n[1]) != projectName {
				continue
			}
			seen = true
			metadata, e := uvMetadataNames(b)
			if e != nil {
				return nil, nil, e
			}
			for name := range metadata {
				locked[normalize(name)] = true
			}
		}
		if !seen {
			return nil, nil, fmt.Errorf("root project metadata absent from uv lockfile")
		}

	}
	return deps, locked, nil
}

// goReplacements reads literal replace directives without resolving modules.
// A blank target denotes a local directory; remote targets retain their exact
// module/version checksum obligation. Version-specific replacements take
// precedence over replacements applying to every version of a module.
func goReplacements(source string) (map[string]string, error) {
	out := map[string]string{}
	block := false
	for _, line := range strings.Split(source, "\n") {
		line = strings.TrimSpace(strings.SplitN(line, "//", 2)[0])
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if !block {
			if fields[0] != "replace" {
				continue
			}
			fields = fields[1:]
			if len(fields) == 1 && fields[0] == "(" {
				block = true
				continue
			}
		} else if line == ")" {
			block = false
			continue
		}
		arrow := -1
		for i, field := range fields {
			if field == "=>" {
				arrow = i
			}
		}
		if arrow < 1 || arrow > 2 || len(fields)-arrow < 2 || len(fields)-arrow > 3 {
			return nil, fmt.Errorf("nonliteral Go replacement")
		}
		unquote := func(value string) string {
			if decoded, err := strconv.Unquote(value); err == nil {
				return decoded
			}
			return value
		}
		key := unquote(fields[0])
		if arrow == 2 {
			key += "@" + unquote(fields[1])
		}
		target := ""
		if len(fields)-arrow == 3 {
			target = unquote(fields[arrow+1]) + "@" + unquote(fields[arrow+2])
		} else {
			local := unquote(fields[arrow+1])
			if !strings.HasPrefix(local, "./") && !strings.HasPrefix(local, "../") && !path.IsAbs(local) && !strings.Contains(local, "\\") {
				return nil, fmt.Errorf("Go replacement without a version must be a local path")
			}
		}
		out[key] = target
	}
	if block {
		return nil, fmt.Errorf("unterminated Go replacement block")
	}
	return out, nil
}

var packageName = regexp.MustCompile(`package\s*=\s*["']([^"']+)["']`)
var tomlName = regexp.MustCompile(`(?m)^\s*name\s*=\s*["']([^"']+)["']`)
var dependencyName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*`)

func normalize(s string) string {
	return strings.ToLower(strings.NewReplacer("_", "-", ".", "-").Replace(s))
}

func importerPath(p pair) string {
	root := path.Dir(p.lock)
	member := path.Dir(p.manifest)
	if member == root {
		return ""
	}
	if root == "." {
		return member
	}
	return strings.TrimPrefix(member, root+"/")
}

func cargoDependencies(source string, inherited map[string]string) (map[string]bool, error) {
	out := map[string]bool{}
	section := ""
	tables := map[string]map[string]string{}
	for _, line := range strings.Split(source, "\n") {
		line = strings.TrimSpace(strings.SplitN(line, "#", 2)[0])
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[") {
			if !strings.HasSuffix(line, "]") {
				return nil, fmt.Errorf("malformed Cargo section")
			}
			section = strings.Trim(line, "[]")
			continue
		}
		if strings.HasPrefix(section, "workspace.") {
			continue
		}
		tableName := ""
		for _, kind := range []string{"dependencies", "dev-dependencies", "build-dependencies"} {
			if strings.HasPrefix(section, kind+".") {
				tableName = strings.Trim(section[len(kind)+1:], "\"'")
			} else if i := strings.Index(section, "."+kind+"."); i >= 0 {
				tableName = strings.Trim(section[i+len(kind)+2:], "\"'")
			}
		}
		direct := section == "dependencies" || section == "dev-dependencies" || section == "build-dependencies" || strings.HasSuffix(section, ".dependencies") || strings.HasSuffix(section, ".dev-dependencies") || strings.HasSuffix(section, ".build-dependencies")
		if !direct && tableName == "" {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("nonliteral Cargo dependency")
		}
		key := strings.Trim(strings.TrimSpace(parts[0]), "\"'")
		value := strings.TrimSpace(parts[1])
		if value == "" {
			return nil, fmt.Errorf("missing Cargo dependency value")
		}
		if tableName != "" {
			if tables[tableName] == nil {
				tables[tableName] = map[string]string{}
			}
			tables[tableName][key] = value
			continue
		}
		if strings.HasPrefix(value, "{") {
			if !strings.HasSuffix(value, "}") {
				return nil, fmt.Errorf("multiline Cargo inline dependencies unsupported")
			}
		} else if !((value[0] == '"' && strings.HasSuffix(value, "\"")) || (value[0] == '\'' && strings.HasSuffix(value, "'"))) {
			return nil, fmt.Errorf("nonliteral Cargo dependency value")
		}
		if strings.Contains(value, "workspace") {
			if !workspaceTrue.MatchString(value) {
				return nil, fmt.Errorf("nonliteral Cargo workspace inheritance")
			}
			resolved, ok := inherited[key]
			if !ok {
				return nil, fmt.Errorf("workspace dependency declaration missing")
			}
			value = resolved
		}
		if strings.Contains(value, "path") {
			continue
		}
		name := key
		if m := packageName.FindStringSubmatch(value); m != nil {
			name = m[1]
		}
		out[name] = true
	}
	for name, fields := range tables {
		if workspace, ok := fields["workspace"]; ok {
			if workspace != "true" {
				return nil, fmt.Errorf("nonliteral Cargo workspace inheritance")
			}
			expression, ok := inherited[name]
			if !ok {
				return nil, fmt.Errorf("workspace dependency declaration missing")
			}
			if strings.Contains(expression, "path") {
				continue
			}
			if m := packageName.FindStringSubmatch(expression); m != nil {
				name = m[1]
			}
		}
		if _, ok := fields["path"]; ok {
			continue
		}
		if pkg, ok := fields["package"]; ok {
			name = strings.Trim(pkg, "\"'")
		}
		out[name] = true
	}
	return out, nil
}

var projectDependencyField = regexp.MustCompile(`(?m)^\s*dependencies\s*=`)

func projectSection(source string) string {
	lines := []string{}
	active := false
	for _, line := range strings.Split(source, "\n") {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "[") {
			if trim == "[project]" {
				active = true
				continue
			}
			if active {
				break
			}
		}
		if active {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n")
}
func literalStringArray(source string) ([]string, error) {
	values := []string{}
	i := 0
	skip := func() {
		for i < len(source) {
			if source[i] == '#' {
				for i < len(source) && source[i] != '\n' {
					i++
				}
				continue
			}
			if strings.ContainsRune(" \t\r\n", rune(source[i])) {
				i++
				continue
			}
			break
		}
	}
	skip()
	if i >= len(source) || source[i] != '[' {
		return nil, fmt.Errorf("nonliteral project dependencies")
	}
	i++
	for {
		skip()
		if i >= len(source) {
			return nil, fmt.Errorf("unterminated project dependency array")
		}
		if source[i] == ']' {
			return values, nil
		}
		quote := source[i]
		if quote != '"' && quote != '\'' {
			return nil, fmt.Errorf("nonliteral project dependency element")
		}
		start := i
		i++
		for i < len(source) && source[i] != quote {
			if source[i] == '\\' && quote == '"' {
				i++
			}
			i++
		}
		if i >= len(source) {
			return nil, fmt.Errorf("unterminated project dependency string")
		}
		i++
		value := source[start+1 : i-1]
		if quote == '"' {
			var e error
			value, e = strconv.Unquote(source[start:i])
			if e != nil {
				return nil, fmt.Errorf("unsupported project dependency escape")
			}
		}
		values = append(values, value)
		skip()
		if i < len(source) && source[i] == ',' {
			i++
			continue
		}
		if i < len(source) && source[i] == ']' {
			return values, nil
		}
		return nil, fmt.Errorf("project dependency array needs separators")
	}
}

var workspaceTrue = regexp.MustCompile(`workspace\s*=\s*true(?:\s*[,}]|$)`)

func cargoWorkspaceExpressions(source string) (map[string]string, error) {
	out := map[string]string{}
	tables := map[string][]string{}
	section := ""
	for _, line := range strings.Split(source, "\n") {
		line = strings.TrimSpace(strings.SplitN(line, "#", 2)[0])
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[") {
			section = strings.Trim(line, "[]")
			continue
		}
		if section != "workspace.dependencies" && !strings.HasPrefix(section, "workspace.dependencies.") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("nonliteral workspace dependency")
		}
		if section == "workspace.dependencies" {
			key := strings.Trim(strings.TrimSpace(parts[0]), "\"'")
			value := strings.TrimSpace(parts[1])
			if value == "" || strings.HasPrefix(value, "{") && !strings.HasSuffix(value, "}") || value != "" && !strings.HasPrefix(value, "{") && !((value[0] == '"' && strings.HasSuffix(value, "\"")) || (value[0] == '\'' && strings.HasSuffix(value, "'"))) {
				return nil, fmt.Errorf("nonliteral workspace dependency")
			}
			out[key] = value
		} else {
			name := strings.Trim(strings.TrimPrefix(section, "workspace.dependencies."), "\"'")
			tables[name] = append(tables[name], line)
		}
	}
	for name, fields := range tables {
		out[name] = "{" + strings.Join(fields, ",") + "}"
	}
	return out, nil
}

var literalMajor = regexp.MustCompile(`^[~^]?v?([0-9]+)(?:\.|$)`)

func declarationMajors(p pair, data []byte) map[string]string {
	versions := map[string]string{}
	put := func(name, version string) {
		if m := literalMajor.FindStringSubmatch(strings.TrimSpace(version)); m != nil {
			versions[name] = m[1]
		}
	}
	switch p.tool {
	case "npm":
		m, e := object(data)
		if e != nil {
			return versions
		}
		for _, section := range []string{"dependencies", "devDependencies", "optionalDependencies"} {
			if deps, ok := m[section].(map[string]any); ok {
				for name, v := range deps {
					if version, ok := v.(string); ok {
						put(name, version)
					}
				}
			}
		}
	case "uv":
		project := projectSection(string(data))
		if loc := projectDependencyField.FindStringIndex(project); loc != nil {
			if deps, e := literalStringArray(project[loc[1]:]); e == nil {
				for _, dep := range deps {
					name := dependencyName.FindString(dep)
					version := strings.TrimSpace(strings.TrimPrefix(dep, name))
					version = strings.TrimLeft(version, "=~ ")
					put(normalize(name), version)
				}
			}
		}
	case "go":
		for _, line := range strings.Split(string(data), "\n") {
			f := strings.Fields(line)
			if len(f) > 0 && f[0] == "require" {
				f = f[1:]
			}
			if len(f) == 2 && strings.HasPrefix(f[1], "v") {
				put(f[0], f[1])
			}
		}
	case "cargo":
		section := ""
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "[") {
				section = strings.Trim(line, "[]")
				continue
			}
			if section != "dependencies" && section != "dev-dependencies" && section != "build-dependencies" {
				continue
			}
			parts := strings.SplitN(line, "=", 2)
			if len(parts) != 2 {
				continue
			}
			name := strings.Trim(strings.TrimSpace(parts[0]), "\"'")
			value := strings.TrimSpace(parts[1])
			if strings.HasPrefix(value, "{") {
				if m := packageName.FindStringSubmatch(value); m != nil {
					name = m[1]
				}
				if m := cargoVersion.FindStringSubmatch(value); m != nil {
					put(name, m[1])
				}
			} else {
				put(name, strings.Trim(value, "\"'"))
			}
		}
	}
	return versions
}

var cargoVersion = regexp.MustCompile(`version\s*=\s*["']([^"']+)["']`)

func divergentMajors(p pair, trees []migrations.Tree, versions []map[string]string) []model.Finding {
	out := []model.Finding{}
	upgrades := map[string]map[string][]int{}
	for i := 1; i < len(trees)-1; i++ {
		for name, major := range versions[i] {
			base, ok := versions[0][name]
			if !ok || major == base {
				continue
			}
			if upgrades[name] == nil {
				upgrades[name] = map[string][]int{}
			}
			upgrades[name][major] = append(upgrades[name][major], i)
		}
	}
	names := []string{}
	for name, majors := range upgrades {
		if len(majors) > 1 {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		majors := []string{}
		for major := range upgrades[name] {
			majors = append(majors, major)
		}
		sort.Strings(majors)
		f := model.NewFinding("dependency_major_versions_diverge", fmt.Sprintf("Branches change %s in %s to different literal major versions (%s); dependency resolution is not checked.", name, p.manifest, strings.Join(majors, ", ")), model.VerifiedStatic)
		for _, major := range majors {
			for _, i := range upgrades[name][major] {
				f.Locations = append(f.Locations, model.Provenance{Repository: model.RepositoryID, Revision: trees[i].Revision, Path: p.manifest, Method: CheckID, Evidence: model.VerifiedStatic})
			}
		}
		f.Remediation = "Choose compatible dependency requirements and regenerate the lockfile."
		out = append(out, f)
	}
	return out
}

var packageHeader = regexp.MustCompile(`(?m)^\s*\[\[package\]\]\s*(?:#[^\n]*)?$`)

func packageBlocks(source string) ([]string, error) {
	if strings.Contains(source, `"""`) || strings.Contains(source, "'''") {
		return nil, fmt.Errorf("multiline lockfile strings unsupported")
	}
	headers := packageHeader.FindAllStringIndex(source, -1)
	if len(headers) == 0 {
		return nil, fmt.Errorf("lockfile has no supported package records")
	}
	blocks := []string{}
	for i, h := range headers {
		end := len(source)
		if i+1 < len(headers) {
			end = headers[i+1][0]
		}
		blocks = append(blocks, source[h[1]:end])
	}
	return blocks, nil
}

var uvRequires = regexp.MustCompile(`(?m)^\s*requires-dist\s*=`)
var uvNameField = regexp.MustCompile(`(?:^|[,\{])\s*name\s*=`)

func uvMetadataNames(block string) (map[string]bool, error) {
	out := map[string]bool{}
	section := strings.Index(block, "[package.metadata]")
	if section < 0 {
		return out, nil
	}
	metadata := block[section+len("[package.metadata]"):]
	loc := uvRequires.FindStringIndex(metadata)
	if loc == nil {
		return out, nil
	}
	array := metadata[loc[1]:]
	mask := tomlCodeMask(array)
	start := 0
	for start < len(mask) && strings.ContainsRune(" \t\r\n", rune(mask[start])) {
		start++
	}
	if start == len(mask) || mask[start] != '[' {
		return nil, fmt.Errorf("nonliteral uv requires-dist")
	}
	arrayDepth, objectDepth, objects, end := 0, 0, 0, -1
	depths := map[int][2]int{}
	for i := start; i < len(mask); i++ {
		depths[i] = [2]int{arrayDepth, objectDepth}
		switch mask[i] {
		case '[':
			arrayDepth++
		case ']':
			arrayDepth--
			if arrayDepth == 0 {
				end = i
			}
		case '{':
			objectDepth++
			if objectDepth == 1 && arrayDepth == 1 {
				objects++
			}
		case '}':
			objectDepth--
		}
		if objectDepth < 0 || arrayDepth < 0 {
			return nil, fmt.Errorf("malformed uv requires-dist")
		}
		if end >= 0 {
			break
		}
	}
	if end < 0 || objectDepth != 0 {
		return nil, fmt.Errorf("unterminated uv requires-dist")
	}
	count := 0
	for _, m := range uvNameField.FindAllStringIndex(mask[start:end], -1) {
		position := start + m[1]
		depth := depths[position-1]
		if depth[0] != 1 || depth[1] != 1 {
			continue
		}
		for position < len(array) && strings.ContainsRune(" \t\r\n", rune(array[position])) {
			position++
		}
		if position >= len(array) || (array[position] != '"' && array[position] != '\'') {
			return nil, fmt.Errorf("nonliteral uv dependency name")
		}
		quote := array[position]
		last := position + 1
		for last < len(array) && array[last] != quote {
			if array[last] == '\\' && quote == '"' {
				last++
			}
			last++
		}
		if last >= len(array) {
			return nil, fmt.Errorf("unterminated uv dependency name")
		}
		out[array[position+1:last]] = true
		count++
	}
	if count != objects {
		return nil, fmt.Errorf("uv dependency record name missing or duplicated")
	}
	return out, nil
}
func tomlCodeMask(source string) string {
	b := []byte(source)
	for i := 0; i < len(b); i++ {
		if b[i] == '#' {
			for i < len(b) && b[i] != '\n' {
				b[i] = ' '
				i++
			}
			continue
		}
		if b[i] != '"' && b[i] != '\'' {
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
			if c == '\\' && quote == '"' {
				if i+1 < len(b) {
					i++
					b[i] = ' '
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
