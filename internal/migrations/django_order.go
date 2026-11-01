package migrations

import (
	"context"
	"fmt"
	"path"
	"sort"
	"strings"
)

// validateDjangoOrdering checks cross-app ordering without merging app leaf
// identities. Django dependencies from installed apps are not repository facts.
func validateDjangoOrdering(ctx context.Context, t Tree, config []configuredLocation, read Reader, targets map[location]bool) (string, error) {
	applicable := false
	for l := range targets {
		applicable = applicable || l.tool == toolDjango
	}
	if !applicable {
		return "", nil
	}
	type appData struct {
		m     map[string]djangoMigration
		paths map[string]string
	}
	groups := map[location]*appData{}
	reasons := map[location]string{}
	dirs := filesByDir(t)
	for _, f := range t.Files {
		l, ok := classifyConfigured(f.Path, dirs, config)
		if !ok || l.tool != toolDjango || path.Base(f.Path) == "__init__.py" {
			continue
		}
		content, e := read(f.OID)
		if e != nil {
			reasons[l] = f.Path + ": cannot read Django ordering"
			continue
		}
		migration, e := parseDjango(ctx, content)
		if e != nil {
			return "", e
		}
		if migration.skip {
			continue
		}
		if migration.reason != "" {
			reasons[l] = f.Path + ": " + migration.reason
			continue
		}
		if groups[l] == nil {
			groups[l] = &appData{map[string]djangoMigration{}, map[string]string{}}
		}
		name := strings.TrimSuffix(path.Base(f.Path), ".py")
		groups[l].m[name] = migration
		groups[l].paths[name] = f.Path
	}
	apps := map[string]*appData{}
	g := newGraph()
	labels := map[location]string{}
	locations := make([]location, 0, len(groups))
	for l := range groups {
		locations = append(locations, l)
	}
	sort.Slice(locations, func(i, j int) bool { return locations[i].dir < locations[j].dir })
	for _, l := range locations {
		a := groups[l]
		label, _, why := djangoGraph(l, a.m, a.paths)
		if why != "" {
			reasons[l] = why
			label = path.Base(path.Dir(l.dir))
		}
		if apps[label] != nil {
			reasons[l] = fmt.Sprintf("Django app label %s belongs to multiple repository migration directories", label)
		}
		labels[l] = label
		apps[label] = a
	}
	relevant := map[string]bool{}
	for l := range targets {
		if l.tool == toolDjango {
			if reasons[l] != "" {
				return reasons[l], nil
			}
			if label := labels[l]; label != "" {
				relevant[label] = true
			}
		}
	}
	for changed := true; changed; {
		changed = false
		for app, a := range apps {
			for _, m := range a.m {
				for _, pairs := range [][][2]string{m.dependencies, m.runBefore} {
					for _, pair := range pairs {
						if apps[pair[0]] != nil && (relevant[app] || relevant[pair[0]]) {
							if !relevant[app] || !relevant[pair[0]] {
								changed = true
							}
							relevant[app] = true
							relevant[pair[0]] = true
						}
					}
				}
			}
		}
	}
	for _, l := range locations {
		label := labels[l]
		if relevant[label] && reasons[l] != "" {
			return reasons[l], nil
		}
	}
	for label, a := range apps {
		if !relevant[label] {
			delete(apps, label)
			continue
		}
		for _, name := range sortedKeys(keysOf(a.m)) {
			m := a.m[name]
			_ = g.add(label+"/"+name, nil, a.paths[name], m.line)
		}
	}
	for _, app := range sortedKeys(keysOf(apps)) {
		a := apps[app]
		for _, name := range sortedKeys(keysOf(a.m)) {
			m := a.m[name]
			id := app + "/" + name
			for _, dep := range m.dependencies {
				if apps[dep[0]] == nil {
					continue
				}
				if strings.HasPrefix(dep[1], "__") {
					return a.paths[name] + ": special cross-app migration dependency is not resolved", nil
				}
				target := dep[0] + "/" + dep[1]
				if _, ok := g.paths[target]; !ok {
					return a.paths[name] + ": repository migration dependency is missing", nil
				}
				g.parents[id] = append(g.parents[id], target)
			}
			for _, before := range m.runBefore {
				if apps[before[0]] == nil {
					return a.paths[name] + ": run_before targets an app outside the repository", nil
				}
				target := before[0] + "/" + before[1]
				if _, ok := g.paths[target]; !ok {
					return a.paths[name] + ": run_before target is missing", nil
				}
				g.parents[target] = append(g.parents[target], id)
			}
		}
	}
	_, why := g.forks()
	return why, nil
}
