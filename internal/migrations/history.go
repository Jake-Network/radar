package migrations

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"path"
	"sort"
	"strings"

	gitrepo "github.com/Jake-Network/radar/internal/git"
)

// parsedFile caches one blob's parse per tool; trees share most blobs.
type parsedFile struct {
	alembic alembicRevision
	django  djangoMigration
	reason  string
}

// analyzeLocation reads one location's files at one revision.
func analyzeLocation(ctx context.Context, l location, files []File, read Reader, cache map[string]parsedFile) (*history, error) {
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	h := &history{members: map[string]bool{}, confirmed: l.tool != toolDjango}
	load := func(f File) (parsedFile, error) {
		key := l.tool + "\x00" + f.OID
		if p, ok := cache[key]; ok {
			return p, nil
		}
		var p parsedFile
		content, e := read(f.OID)
		switch {
		case errors.Is(e, gitrepo.ErrBlobTooLarge) || (e == nil && len(content) > maxFileBytes):
			p.reason, e = fmt.Sprintf("file exceeds the %d KiB migration analysis limit", maxFileBytes>>10), nil
		case e != nil:
			return p, e
		case l.tool == toolAlembic:
			p.alembic, e = parseAlembic(ctx, content)
			p.reason = p.alembic.reason
		default:
			p.django, e = parseDjango(ctx, content)
			p.reason = p.django.reason
		}
		if e != nil {
			return p, e
		}
		cache[key] = p
		return p, nil
	}
	switch l.tool {
	case toolAlembic:
		g := newGraph()
		for _, f := range files {
			if path.Base(f.Path) == "__init__.py" {
				continue
			}
			if path.Dir(f.Path) != l.dir {
				// Nested directories load only with recursive_version_locations,
				// which lives in configuration Radar does not read.
				h.unknown = append(h.unknown, f.Path+": nested version directory")
				continue
			}
			p, e := load(f)
			if e != nil {
				return nil, e
			}
			switch {
			case p.reason != "":
				h.unknown = append(h.unknown, f.Path+": "+p.reason)
			case p.alembic.skip:
				h.unknown = append(h.unknown, f.Path+": no revision identifier")
			default:
				if why := g.add(p.alembic.id, p.alembic.parents, f.Path, p.alembic.line); why != "" {
					h.unknown = append(h.unknown, why)
				}
				for _, label := range p.alembic.labels {
					g.labeled[p.alembic.id] = true
					g.labels[label] = append(g.labels[label], p.alembic.id)
				}
			}
		}
		if len(h.unknown) == 0 {
			h.finish(g, "")
		}
	case toolDjango:
		migrations := map[string]djangoMigration{}
		paths := map[string]string{}
		for _, f := range files {
			name := strings.TrimSuffix(path.Base(f.Path), ".py")
			p, e := load(f)
			if e != nil {
				return nil, e
			}
			switch {
			case p.reason != "":
				h.unknown = append(h.unknown, f.Path+": "+p.reason)
			case p.django.skip:
				if djangoName.MatchString(name) {
					h.unknown = append(h.unknown, f.Path+": numbered module without a Migration class")
				}
			default:
				migrations[name] = p.django
				paths[name] = f.Path
			}
		}
		// Without any Migration class this is another tool's directory;
		// its unreadable files say nothing about Django.
		if h.confirmed = len(migrations) > 0; !h.confirmed {
			h.unknown = nil
			return h, nil
		}
		if len(h.unknown) == 0 {
			app, g, why := djangoGraph(l, migrations, paths)
			if why != "" {
				h.unknown = append(h.unknown, why)
			} else {
				h.finish(g, app)
			}
		}
	case toolLiquibase:
		h.liquibaseIssues(l, files, read)
	default:
		h.versionIssues(l, files)
	}
	return h, nil
}

// graph is a revision DAG: each node lists the nodes it follows.
type graph struct {
	parents map[string][]string
	paths   map[string]string
	lines   map[string]int
	// labeled revisions start an Alembic named branch; labels maps each
	// branch label to the revisions declaring it.
	labeled map[string]bool
	labels  map[string][]string
}

func newGraph() *graph {
	return &graph{parents: map[string][]string{}, paths: map[string]string{}, lines: map[string]int{}, labeled: map[string]bool{}, labels: map[string][]string{}}
}

func (g *graph) add(id string, parents []string, p string, line int) string {
	if other, dup := g.paths[id]; dup {
		return fmt.Sprintf("revision %q is declared by both %s and %s", id, other, p)
	}
	g.parents[id] = parents
	g.paths[id] = p
	g.lines[id] = line
	return ""
}

// finish records the graph's members and divergence points, or why the graph
// cannot be read.
func (h *history) finish(g *graph, app string) {
	for id := range g.paths {
		h.members[id] = true
	}
	forks, why := g.forks()
	if why != "" {
		h.unknown = append(h.unknown, why)
		return
	}
	// Alembic refuses a branch label declared by two revisions.
	for _, label := range sortedKeys(keysOf(g.labels)) {
		ids := sortedKeys(setOf(g.labels[label]))
		if len(g.labels[label]) < 2 {
			continue
		}
		is := issue{Key: "label:" + label, Kind: kindLabel, Parent: label, Members: ids, Paths: map[string]string{}, Lines: map[string]int{}}
		for _, id := range ids {
			is.Paths[id] = g.paths[id]
			is.Lines[id] = g.lines[id]
		}
		h.issues = append(h.issues, is)
	}
	for _, f := range forks {
		f.App = app
		h.issues = append(h.issues, f)
	}
}

// forks returns each point where the history splits into lines that end in
// different heads. A split later joined by a merge revision is not a fork.
// Parent "" stands for the start of history (more than one root).
func (g *graph) forks() ([]issue, string) {
	ids := make([]string, 0, len(g.paths))
	for id := range g.paths {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	children := map[string][]string{}
	for _, id := range ids {
		if len(g.parents[id]) == 0 {
			children[""] = append(children[""], id)
		}
		for _, p := range g.parents[id] {
			if _, ok := g.paths[p]; !ok {
				return nil, fmt.Sprintf("%s: %q follows %q, which is not in this history", g.paths[id], id, p)
			}
			children[p] = append(children[p], id)
		}
	}
	reach := map[string][]string{}
	state := map[string]int{}
	var visit func(string) bool
	visit = func(id string) bool {
		switch state[id] {
		case 1:
			return false
		case 2:
			return true
		}
		state[id] = 1
		set := map[string]bool{}
		if len(children[id]) == 0 {
			set[id] = true
		}
		for _, c := range children[id] {
			if !visit(c) {
				return false
			}
			for _, h := range reach[c] {
				set[h] = true
			}
		}
		reach[id] = sortedKeys(set)
		state[id] = 2
		return true
	}
	for _, id := range ids {
		if !visit(id) {
			return nil, fmt.Sprintf("%s: the history contains a cycle through %q", g.paths[id], id)
		}
	}
	out := []issue{}
	for _, p := range append([]string{""}, ids...) {
		// A child that starts a named branch splits on purpose; only
		// unlabeled children continue the same branch.
		kids := []string{}
		for _, k := range children[p] {
			if !g.labeled[k] {
				kids = append(kids, k)
			}
		}
		if len(kids) < 2 {
			continue
		}
		heads := map[string]bool{}
		for _, k := range kids {
			for _, h := range reach[k] {
				heads[h] = true
			}
		}
		all := sortedKeys(heads)
		if len(all) < 2 {
			continue
		}
		split := true
		for _, k := range kids {
			if equal(reach[k], all) {
				split = false
			}
		}
		if !split {
			continue
		}
		is := issue{Key: "fork:" + p, Kind: kindFork, Parent: p, Members: kids, Paths: map[string]string{}, Lines: map[string]int{}, Heads: map[string][]string{}}
		for _, k := range kids {
			is.Paths[k] = g.paths[k]
			is.Lines[k] = g.lines[k]
			is.Heads[k] = reach[k]
		}
		out = append(out, is)
	}
	return out, ""
}

// djangoGraph builds one app's same-app dependency graph. The app label is
// taken from the dependencies that name migrations in this directory; the
// directory name is preferred when it is one of them.
func djangoGraph(l location, migrations map[string]djangoMigration, paths map[string]string) (string, *graph, string) {
	dirApp := path.Base(path.Dir(l.dir))
	labels := map[string]bool{}
	for name, m := range migrations {
		for _, list := range [][][2]string{m.dependencies, m.replaces, m.runBefore} {
			for _, pair := range list {
				// A migration never depends on itself, so a pair naming the
				// migration's own name belongs to another app.
				if _, local := migrations[pair[1]]; local && pair[1] != name {
					labels[pair[0]] = true
				}
			}
		}
	}
	app := dirApp
	switch {
	case labels[dirApp]:
	case len(labels) == 1:
		for a := range labels {
			app = a
		}
	case len(labels) > 1:
		return "", nil, fmt.Sprintf("%s: the app label is ambiguous (%s)", l.dir, strings.Join(sortedKeys(labels), ", "))
	case len(migrations) > 1:
		return "", nil, fmt.Sprintf("%s: no migration depends on another in this directory, so the app label is unknown", l.dir)
	}
	g := newGraph()
	names := make([]string, 0, len(migrations))
	for n := range migrations {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		m := migrations[name]
		for _, pair := range m.replaces {
			if _, present := migrations[pair[1]]; present && pair[0] == app {
				return "", nil, fmt.Sprintf("%s: squashed migration still has replaced migration %s present", paths[name], pair[1])
			}
		}
		for _, pair := range m.runBefore {
			if pair[0] == app {
				return "", nil, fmt.Sprintf("%s: run_before names a migration in the same app", paths[name])
			}
		}
		parents := []string{}
		for _, pair := range m.dependencies {
			if pair[0] != app {
				continue
			}
			if strings.HasPrefix(pair[1], "__") {
				return "", nil, fmt.Sprintf("%s: depends on %s of its own app", paths[name], pair[1])
			}
			parents = append(parents, pair[1])
		}
		g.add(name, parents, paths[name], m.line)
	}
	return app, g, ""
}

// versionIssues finds versions claimed by more than one Flyway or
// versioned-SQL migration. A versioned-SQL up/down pair is one migration,
// identified by its path without the direction.
func (h *history) versionIssues(l location, files []File) {
	h.versions = map[string]string{}
	h.contents = map[string]map[string]bool{}
	byVersion := map[string]map[string]bool{}
	shown := map[string]string{}
	for _, f := range files {
		name := path.Base(f.Path)
		version, id := "", f.Path
		if l.tool == toolFlyway {
			m := flywayName.FindStringSubmatch(name)
			if m == nil {
				continue
			}
			version = normalizeVersion(m[1])
		} else if l.tool == toolPrisma || l.tool == toolDiesel {
			m := timestampName.FindStringSubmatch(path.Base(path.Dir(f.Path)))
			if m == nil {
				h.unknown = append(h.unknown, f.Path+": nonliteral migration timestamp")
				continue
			}
			version = normalizeVersion(m[1])
			id = path.Dir(f.Path)
		} else if l.tool == toolSQLX {
			m := sqlxName.FindStringSubmatch(name)
			if m == nil {
				continue
			}
			version = normalizeVersion(m[1])
			id = strings.TrimSuffix(strings.TrimSuffix(f.Path, ".up.sql"), ".down.sql")
		} else {
			m := sqlName.FindStringSubmatch(name)
			if m == nil {
				continue
			}
			version, id = normalizeVersion(m[1]), strings.TrimSuffix(f.Path, "."+m[2]+".sql")
		}
		h.members[id] = true
		h.versions[f.Path] = version
		if byVersion[version] == nil {
			byVersion[version] = map[string]bool{}
		}
		byVersion[version][id] = true
		if h.contents[id] == nil {
			h.contents[id] = map[string]bool{}
		}
		h.contents[id][f.OID] = true
		if p, ok := shown[id]; !ok || strings.HasSuffix(f.Path, ".up.sql") || f.Path < p && !strings.HasSuffix(p, ".up.sql") {
			shown[id] = f.Path
		}
	}
	if l.tool == toolPrisma {
		return
	} // Prisma identifies migrations by the full directory name.
	for _, version := range sortedKeys(keysOf(byVersion)) {
		ids := sortedKeys(byVersion[version])
		if len(ids) < 2 {
			continue
		}
		is := issue{Key: "version:" + version, Kind: kindVersion, Parent: version, Members: ids, Paths: map[string]string{}, Lines: map[string]int{}}
		for _, id := range ids {
			is.Paths[id] = shown[id]
		}
		h.issues = append(h.issues, is)
	}
}

// normalizeVersion makes 1.01 and 1_1 and 1.1.0 the same Flyway version, the
// way version comparison treats them.
func normalizeVersion(v string) string {
	parts := strings.FieldsFunc(v, func(r rune) bool { return r == '.' || r == '_' })
	for i, p := range parts {
		if p = strings.TrimLeft(p, "0"); p == "" {
			p = "0"
		}
		parts[i] = p
	}
	for len(parts) > 1 && parts[len(parts)-1] == "0" {
		parts = parts[:len(parts)-1]
	}
	return strings.Join(parts, ".")
}

// compareVersions orders normalized versions numerically, part by part.
func compareVersions(a, b string) int {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(pa) || i < len(pb); i++ {
		x, y := new(big.Int), new(big.Int)
		if i < len(pa) {
			x.SetString(pa[i], 10)
		}
		if i < len(pb) {
			y.SetString(pb[i], 10)
		}
		if c := x.Cmp(y); c != 0 {
			return c
		}
	}
	return 0
}

func keysOf[V any](m map[string]V) map[string]bool {
	out := map[string]bool{}
	for k := range m {
		out[k] = true
	}
	return out
}

func setOf(items []string) map[string]bool {
	out := map[string]bool{}
	for _, x := range items {
		out[x] = true
	}
	return out
}

func sortedKeys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
