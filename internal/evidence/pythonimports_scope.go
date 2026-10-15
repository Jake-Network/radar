package evidence

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var pythonStaticImport = regexp.MustCompile(`(?m)(?:^|;|:)\s*(?:from\s+([A-Za-z_][A-Za-z0-9_]*)(?:\.[A-Za-z_][A-Za-z0-9_]*)*\s+import\b|import\s+([^;#\n]+))`)

// Scope package checks to the command's project(s), then follow statically
// referenced workspace member packages transitively. A different member's app,
// tests or utils names cannot contaminate an unrelated test command.
func firstPartyPythonForCommand(root, dir string, argv []string) ([]string, error) {
	return firstPartyPythonForCommandContext(context.Background(), root, dir, argv)
}

func firstPartyPythonForCommandContext(ctx context.Context, root, dir string, argv []string) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	all, err := firstPartyPythonContext(ctx, root)
	if err != nil || len(all) == 0 {
		return all, err
	}
	projects := []string{}
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, e error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if e != nil {
			return e
		}
		if d.IsDir() {
			if p != root && (strings.HasPrefix(d.Name(), ".") || d.Name() == "node_modules" || d.Name() == "venv" || d.Name() == "__pycache__") {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() == "pyproject.toml" {
			projects = append(projects, filepath.Dir(p))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	// Integration tests may live above member pyprojects. Scan that root as
	// command scope too, without inventing an installed application package.
	rootProject := false
	for _, project := range projects {
		rootProject = rootProject || project == root
	}
	if !rootProject {
		projects = append(projects, root)
	}
	owners := map[string][]string{}
	for _, project := range projects {
		for _, name := range all {
			for _, layout := range []string{project, filepath.Join(project, "src")} {
				st, e := os.Lstat(filepath.Join(layout, name))
				if e == nil && st.IsDir() && st.Mode()&os.ModeSymlink == 0 {
					owners[name] = append(owners[name], project)
					break
				}
				st, e = os.Lstat(filepath.Join(layout, name+".py"))
				if e == nil && st.Mode().IsRegular() {
					owners[name] = append(owners[name], project)
					break
				}
			}
		}
	}
	nearest := func(p string) string {
		best := ""
		for _, project := range projects {
			rel, e := filepath.Rel(project, p)
			if e == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && len(project) > len(best) {
				best = project
			}
		}
		return best
	}
	active := map[string]bool{}
	explicitScope, aggregateScope := false, false
	if project := nearest(dir); project != "" {
		active[project] = true
	}
	for _, arg := range argv[1:] {
		arg = strings.SplitN(arg, "::", 2)[0]
		if strings.HasPrefix(arg, "-") {
			continue
		}
		target := arg
		if !filepath.IsAbs(target) {
			target = filepath.Join(dir, target)
		}
		if _, e := os.Stat(target); e == nil {
			if project := nearest(target); project != "" {
				active[project] = true
				explicitScope = true
				for _, member := range projects {
					if member == project {
						continue
					}
					rel, e := filepath.Rel(target, member)
					if e == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
						aggregateScope = true
					}
				}
			}
		} else {
			// unittest dotted modules and pytest --pyargs refer to packages,
			// rather than filesystem paths. Keep their member boundary.
			module := strings.SplitN(arg, ".", 2)[0]
			if pythonModuleName.MatchString(module) {
				candidates := owners[module]
				if len(candidates) > 1 {
					return nil, fmt.Errorf("ambiguous command Python module")
				}
				for _, owner := range candidates {
					active[owner] = true
				}
			}
		}
	}
	needed := map[string]bool{}
	pyargs := false
	for _, arg := range argv {
		pyargs = pyargs || arg == "--pyargs"
	}
	for _, arg := range argv[1:] {
		arg = strings.SplitN(arg, "::", 2)[0]
		// Test filesystem paths are already pinned candidate inputs. Do not
		// confuse their directory names with imported top-level packages.
		moduleArg := pyargs || isUnittest(argv) || (strings.Contains(arg, ".") && !strings.HasSuffix(arg, ".py"))
		if moduleArg && !strings.ContainsAny(arg, "/\\") {
			first := strings.SplitN(arg, ".", 2)[0]
			if pythonModuleName.MatchString(first) {
				needed[first] = true
			}
		}
	}
	selected := map[string]bool{}
	scanned := map[string]bool{}
	files, bytes := 0, 0
	for {
		changed := false
		for project := range active {
			if scanned[project] {
				continue
			}
			scanned[project] = true
			changed = true
			for name, roots := range owners {
				for _, r := range roots {
					if r == project {
						if flatNamespaceDirectory(name) || needed[name] {
							selected[name] = true
						}
					}
				}
			}
			err = filepath.WalkDir(project, func(p string, d fs.DirEntry, e error) error {
				if err := ctx.Err(); err != nil {
					return err
				}
				if e != nil {
					return e
				}
				if d.IsDir() {
					if p != project {
						if strings.HasPrefix(d.Name(), ".") || d.Name() == "node_modules" || d.Name() == "venv" || d.Name() == "__pycache__" {
							return filepath.SkipDir
						}
						if _, e := os.Lstat(filepath.Join(p, "pyproject.toml")); e == nil {
							return filepath.SkipDir
						}
					}
					return nil
				}
				if d.Type()&os.ModeSymlink != 0 || !strings.HasSuffix(d.Name(), ".py") {
					return nil
				}
				st, e := d.Info()
				if e != nil {
					return e
				}
				files++
				bytes += int(st.Size())
				if files > 4096 || st.Size() > 1<<20 || bytes > 32<<20 {
					return fmt.Errorf("Python import scope exceeds source budget")
				}
				data, e := os.ReadFile(p)
				if e != nil {
					return e
				}
				code, e := pythonExecutableSourceContext(ctx, data)
				if e != nil {
					return e
				}
				for _, imported := range staticPythonImports(code) {
					needed[imported] = true
					candidates := owners[imported]
					local := false
					for _, owner := range candidates {
						local = local || active[owner]
					}
					if local {
						continue
					}
					if len(candidates) > 1 {
						return fmt.Errorf("ambiguous workspace Python module")
					}
					for _, owner := range candidates {
						active[owner] = true
					}
				}
				return nil
			})
			if err != nil {
				return nil, err
			}
		}
		if !changed {
			break
		}
	}
	out := []string{}
	for name, roots := range owners {
		for _, owner := range roots {
			if active[owner] && needed[name] {
				selected[name] = true
			}
		}
	}
	for name := range selected {
		out = append(out, name)
	}
	if len(out) == 0 && (!explicitScope || aggregateScope) {
		// Packages exist, but an aggregate workspace command supplied no
		// project/test relationship. This is unknown scope, not an empty set
		// of first-party imports (for example root-level `uv run pytest`).
		return nil, fmt.Errorf("Python command package scope is unverified")
	}
	sort.Strings(out)
	return out, nil
}

func staticPythonImports(data []byte) []string {
	text := strings.ReplaceAll(string(data), "\\\n", " ")
	names := []string{}
	for _, match := range pythonStaticImport.FindAllStringSubmatch(text, -1) {
		if match[1] != "" {
			names = append(names, match[1])
			continue
		}
		for _, item := range strings.Split(match[2], ",") {
			fields := strings.Fields(item)
			if len(fields) == 0 {
				continue
			}
			name := strings.SplitN(fields[0], ".", 2)[0]
			if pythonModuleName.MatchString(name) {
				names = append(names, name)
			}
		}
	}
	return names
}
