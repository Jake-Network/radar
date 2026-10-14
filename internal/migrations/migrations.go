// Package migrations finds database migration histories that diverge only
// once branches are combined: two Alembic heads, two Django leaves, or two
// files claiming one Flyway or versioned-SQL version. It reads committed
// files as data and never runs a migration tool or repository code.
package migrations

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	gitrepo "github.com/Jake-Network/radar/internal/git"
	"github.com/Jake-Network/radar/internal/model"
)

// CheckID names the gate check this package produces.
const CheckID = "migration_order"

const (
	method = "migration_history"
	// maxFileBytes bounds one migration source file; larger files are not
	// parsed and make their history unknown.
	maxFileBytes = 1 << 20
	// maxFiles bounds the migration files read from one revision.
	maxFiles = 20000
)

const (
	toolAlembic   = "alembic"
	toolDjango    = "django"
	toolFlyway    = "flyway"
	toolSQL       = "sql"
	toolPrisma    = "prisma"
	toolLiquibase = "liquibase"
	toolSQLX      = "sqlx"
	toolDiesel    = "diesel"
)

// Result is the migration_order outcome. A Result that is not Applicable has
// no check: no changed file belongs to a recognized migration history.
type Result struct {
	Applicable  bool
	Status      model.Status
	Evidence    model.Evidence
	Explanation string
	Findings    []model.Finding
	Diagnostics []model.Diagnostic
}

// File is one regular file in a committed tree.
type File struct {
	Path string
	OID  string
	Size int64
}

// Tree is one revision taking part in the comparison. Trees are ordered: the
// base, each input merged alone onto the base in merge order, then the
// combined candidate. Unavailable explains a tree that could not be built.
type Tree struct {
	Label       string
	Revision    string
	Files       []File
	Unavailable string
}

// Reader returns a blob's content, or an error wrapping gitrepo.ErrBlobTooLarge.
type Reader func(oid string) ([]byte, error)

// Analyze compares the migration histories of the base, each input merged
// alone onto the base, and the combined candidate in the private repository
// root. Each single-branch tree is what that branch's own pull request would
// merge, so a stale branch is not blamed for history the base moved past.
func Analyze(ctx context.Context, root, base string, inputs []string, combined string, changed []string) (Result, error) {
	if !plausible(changed) {
		if info, e := os.Lstat(filepath.Join(root, ".radar", "migrations.json")); e != nil || !info.Mode().IsRegular() {
			return Result{}, nil
		}
	}
	trees, e := LoadTrees(ctx, root, base, inputs, combined)
	if e != nil {
		return Result{}, e
	}
	blobs, e := gitrepo.OpenBlobReader(ctx, root)
	if e != nil {
		return Result{}, e
	}
	defer blobs.Close()
	return Evaluate(ctx, trees, changed, func(oid string) ([]byte, error) { return blobs.Read(oid, maxFileBytes) })
}

// LoadTrees lists the base, each private single-branch merge, and combined tree.
// It writes Git objects only in the caller-provided private repository.
func LoadTrees(ctx context.Context, root, base string, inputs []string, combined string) ([]Tree, error) {
	load := func(label, revision string) (Tree, error) {
		entries, e := gitrepo.Entries(ctx, root, revision)
		if e != nil {
			return Tree{}, e
		}
		t := Tree{Label: label, Revision: revision}
		for _, entry := range entries {
			if entry.Type == "blob" && (entry.Mode == "100644" || entry.Mode == "100755") {
				t.Files = append(t.Files, File{Path: entry.Path, OID: entry.OID, Size: entry.Size})
			}
		}
		return t, nil
	}
	baseTree, e := load("the base", base)
	if e != nil {
		return nil, e
	}
	trees := []Tree{baseTree}
	for i, sha := range inputs {
		label := fmt.Sprintf("branch %d (%s) merged alone onto the base", i+1, short(sha))
		t := Tree{Label: label, Revision: sha}
		if single, why := mergeOnto(ctx, root, base, sha); why != "" {
			t.Unavailable = why
		} else if t, e = load(label, single); e != nil {
			return nil, e
		}
		if e = ctx.Err(); e != nil {
			return nil, e
		}
		trees = append(trees, t)
	}
	combinedTree, e := load("the combined tree", combined)
	if e != nil {
		return nil, e
	}
	trees = append(trees, combinedTree)
	return trees, nil
}

// mergeOnto merges one input onto the base without a worktree and records
// the result as a commit in the private repository, so it can be listed like
// any revision. It returns why the merge is unavailable instead of failing:
// a conflict or an older Git only weakens attribution.
func mergeOnto(ctx context.Context, root, base, input string) (string, string) {
	if base == input {
		return base, ""
	}
	out, e := gitrepo.RunPrivate(ctx, root, "merge-tree", "--write-tree", "--no-messages", base, input)
	tree := strings.TrimSpace(strings.SplitN(out, "\n", 2)[0])
	// merge-tree exits 1 for a conflicted merge and prints the tree first;
	// any other failure, such as Git before 2.38, prints no tree.
	var exit *exec.ExitError
	if errors.As(e, &exit) && exit.ExitCode() == 1 && objectID.MatchString(tree) {
		return "", "it conflicts with the base"
	}
	if e != nil || !objectID.MatchString(tree) {
		return "", "Git could not merge it onto the base in private state (merge-tree --write-tree, Git 2.38 or later)"
	}
	commit, e := gitrepo.RunPrivate(ctx, root, "commit-tree", tree, "-p", base, "-p", input, "-m", "radar migration preview")
	if e != nil || !objectID.MatchString(commit) {
		return "", "Git could not record the single-branch merge"
	}
	return commit, ""
}

var objectID = regexp.MustCompile(`^[0-9a-f]{40}([0-9a-f]{24})?$`)

// plausible reports whether a changed path could belong to a migration
// history, so repositories without one skip listing whole trees.
func plausible(changed []string) bool {
	for _, p := range changed {
		name, dir := path.Base(p), path.Dir(p)
		if p == ".radar/migrations.json" || (name == "migration.sql" || name == "up.sql" || name == "down.sql") && timestampName.MatchString(path.Base(dir)) {
			return true
		}
		switch {
		case strings.HasSuffix(name, ".py") && (path.Base(dir) == "migrations" || strings.Contains("/"+dir+"/", "/versions/")):
			return true
		case flywayName.MatchString(name) || sqlName.MatchString(name):
			return true
		}
	}
	return false
}

// location is one migration history: an Alembic versions directory, a
// Django migrations package, or one directory of Flyway or versioned-SQL
// files. Flyway subdirectories are separate locations because a common
// layout keeps one folder per database vendor and loads only one of them.
type location struct{ tool, dir string }

func (l location) contains(p string) bool {
	if l.tool == toolAlembic || l.tool == toolPrisma || l.tool == toolLiquibase || l.tool == toolDiesel {
		return l.dir == "." || strings.HasPrefix(p, l.dir+"/")
	}
	return l.dir == "." || p == l.dir || strings.HasPrefix(p, l.dir+"/")
}

func (l location) String() string {
	switch l.tool {
	case toolAlembic:
		return "Alembic history " + l.dir
	case toolDjango:
		return "Django migrations " + l.dir
	case toolFlyway:
		return "Flyway migrations " + l.dir
	}
	return "versioned SQL migrations " + l.dir
}

var (
	flywayName = regexp.MustCompile(`^V([0-9]+(?:[._][0-9]+)*)__[^/]+\.sql$`)
	sqlName    = regexp.MustCompile(`^([0-9]+)_[^/]+\.(up|down)\.sql$`)
	djangoName = regexp.MustCompile(`^[0-9]+_`)
)

// filesByDir indexes a tree's file names by directory.
func filesByDir(t Tree) map[string]map[string]bool {
	out := map[string]map[string]bool{}
	for _, f := range t.Files {
		d := path.Dir(f.Path)
		if out[d] == nil {
			out[d] = map[string]bool{}
		}
		out[d][path.Base(f.Path)] = true
	}
	return out
}

// classify assigns a file to the migration history it belongs to, by
// convention only. Configured locations elsewhere are not recognized.
// A Django candidate is confirmed only when a Migration class is parsed.
func classify(p string, dirs map[string]map[string]bool) (location, bool) {
	name, dir := path.Base(p), path.Dir(p)
	if strings.HasSuffix(name, ".py") {
		// Alembic: SCRIPT/versions/**.py next to SCRIPT/env.py.
		for d := dir; d != "." && d != "/"; d = path.Dir(d) {
			if path.Base(d) == "versions" && dirs[path.Dir(d)]["env.py"] {
				return location{toolAlembic, d}, true
			}
		}
		// Django: APP/migrations/NAME.py, excluding an Alembic script
		// directory that happens to be named migrations (Flask-Migrate).
		if path.Base(dir) == "migrations" && name != "__init__.py" && !dirs[dir]["env.py"] {
			return location{toolDjango, dir}, true
		}
		return location{}, false
	}
	if flywayName.MatchString(name) && strings.Contains("/"+dir+"/", "/db/migration/") {
		return location{toolFlyway, dir}, true
	}
	if sqlName.MatchString(name) {
		return location{toolSQL, dir}, true
	}
	return location{}, false
}

// issue is one problem in one tree's history. Key identifies the problem
// across trees; Members are the revisions or files that take part in it.
type issue struct {
	Key     string
	Kind    string
	Members []string
	Paths   map[string]string // member -> file path
	Lines   map[string]int
	// Parent is the revision every member follows ("" for the start of
	// history), or the duplicated version.
	Parent string
	// Heads lists the heads each member's line ends in.
	Heads map[string][]string
	// App is the Django app label.
	App string
}

const (
	kindFork    = "fork"
	kindVersion = "version"
	kindLabel   = "label"
)

// history is one location's analyzed state in one tree.
type history struct {
	issues  []issue
	unknown []string
	// members are the revisions (or version files) present.
	members map[string]bool
	// versions maps each Flyway or versioned-SQL file to its version, and
	// contents each migration to the blob IDs of its files.
	versions map[string]string
	contents map[string]map[string]bool
	// confirmed is false for a Django-like directory without any Migration
	// class: another tool's migrations, which are not judged.
	confirmed bool
}

// Evaluate classifies every migration problem in the combined tree as
// introduced by the combination, already present with one branch merged
// alone, or already on the base. trees must be the base, each input merged
// alone onto the base, then the combined tree. Only histories a changed path
// belongs to are judged; a Result that is not Applicable has no check.
func Evaluate(ctx context.Context, trees []Tree, changed []string, read Reader) (Result, error) {
	if len(trees) < 2 {
		return Result{}, errors.New("migration comparison needs a base and a combined tree")
	}
	last := len(trees) - 1
	configs := make([][]configuredLocation, len(trees))
	for i, t := range trees {
		c, present, e := readConfig(t, read)
		if e != nil {
			return Result{Applicable: true, Status: model.StatusUnknown, Evidence: model.Unknown, Explanation: e.Error(), Findings: []model.Finding{}, Diagnostics: []model.Diagnostic{}}, nil
		}
		configs[i] = c
		if i == last && !present && len(configs[0]) > 0 {
			return Result{Applicable: true, Status: model.StatusUnknown, Evidence: model.Unknown, Explanation: "Base migration configuration was removed; its obligations cannot be discarded."}, nil
		}
	}
	allConfig, e := combinedConfig(configs[0], configs[last])
	if e != nil {
		return Result{Applicable: true, Status: model.StatusUnknown, Evidence: model.Unknown, Explanation: e.Error()}, nil
	}
	for i := range configs {
		configs[i] = allConfig
	}
	targets := map[location]bool{}
	for _, t := range []Tree{trees[0], trees[last]} {
		dirs := filesByDir(t)
		for _, f := range t.Files {
			if l, ok := classifyConfigured(f.Path, dirs, allConfig); ok && !targets[l] {
				for _, p := range changed {
					if l.contains(p) || p == ".radar/migrations.json" {
						targets[l] = true
						break
					}
				}
			}
		}
	}
	if len(targets) == 0 {
		return Result{}, nil
	}
	parsed := map[string]parsedFile{}
	histories := make([]map[location]*history, len(trees))
	for i, t := range trees {
		histories[i] = map[location]*history{}
		if t.Unavailable != "" {
			continue
		}
		dirs := filesByDir(t)
		groups, files := map[location][]File{}, 0
		for _, f := range t.Files {
			if l, ok := classifyConfigured(f.Path, dirs, configs[i]); ok && targets[l] {
				groups[l] = append(groups[l], f)
				files++
			}
		}
		if files > maxFiles {
			return Result{Applicable: true, Status: model.StatusUnknown, Evidence: model.Unknown, Explanation: fmt.Sprintf("Migration order was not checked: %s has more than %d migration files.", t.Label, maxFiles), Findings: []model.Finding{}, Diagnostics: []model.Diagnostic{}}, nil
		}
		for l, fs := range groups {
			if e := ctx.Err(); e != nil {
				return Result{}, e
			}
			h, e := analyzeLocation(ctx, l, fs, read, parsed)
			if e != nil {
				return Result{}, e
			}
			histories[i][l] = h
		}
	}
	// A directory named migrations that holds no Django Migration class at
	// the base or in the combined tree belongs to another tool.
	for l := range targets {
		b, c := histories[0][l], histories[last][l]
		if (b == nil || !b.confirmed) && (c == nil || !c.confirmed) {
			delete(targets, l)
		}
	}
	if len(targets) == 0 {
		return Result{}, nil
	}
	return compare(trees, histories, targets), nil
}

// compare applies the combination differential to every judged location.
func compare(trees []Tree, histories []map[location]*history, targets map[location]bool) Result {
	res := Result{Applicable: true, Status: model.StatusPassed, Evidence: model.VerifiedStatic, Findings: []model.Finding{}, Diagnostics: []model.Diagnostic{}}
	last := len(trees) - 1
	locations := make([]location, 0, len(targets))
	for l := range targets {
		locations = append(locations, l)
	}
	sort.Slice(locations, func(i, j int) bool {
		return locations[i].dir+"\x00"+locations[i].tool < locations[j].dir+"\x00"+locations[j].tool
	})
	unknown, failed := crossDirectory(trees, targets), 0
	for _, l := range locations {
		// Only the combined tree decides whether a conflict exists. The base
		// and each single-branch merge only attribute it, so an unreadable
		// single-branch tree weakens attribution, not the result.
		readable := make([]bool, len(trees))
		where := map[string][]string{}
		reasons := []string{}
		for i, t := range trees {
			h := histories[i][l]
			readable[i] = t.Unavailable == "" && (h == nil || len(h.unknown) == 0)
			if h == nil {
				continue
			}
			for _, r := range h.unknown {
				if where[r] == nil {
					reasons = append(reasons, r)
				}
				where[r] = append(where[r], t.Label)
			}
		}
		for _, r := range reasons {
			res.Diagnostics = append(res.Diagnostics, model.Diagnostic{Path: l.dir, Message: l.String() + ": " + r + " in " + strings.Join(where[r], ", "), Severity: model.SeverityWarning})
		}
		combined := histories[last][l]
		if combined == nil {
			continue // the branches removed this history
		}
		if !readable[last] {
			unknown = append(unknown, l.String()+": "+combined.unknown[0]+" in "+trees[last].Label)
			continue
		}
		if !readable[0] && len(combined.issues) > 0 {
			unknown = append(unknown, l.String()+": the combined tree has a conflict, but the base cannot be read to tell whether it was already there ("+histories[0][l].unknown[0]+")")
			continue
		}
		for _, is := range combined.issues {
			f := finding(trees, histories, readable, l, is)
			if f.Severity == model.SeverityError {
				failed++
			}
			res.Findings = append(res.Findings, f)
		}
		res.Findings = append(res.Findings, outOfOrder(trees, histories, l)...)
	}
	switch {
	case failed > 0:
		res.Status = model.StatusFailed
		res.Explanation = fmt.Sprintf("%s in the combined migration history.", plural(failed, "conflict", "conflicts"))
	case len(unknown) > 0:
		res.Status, res.Evidence = model.StatusUnknown, model.Unknown
		res.Explanation = "Migration order could not be established: " + strings.Join(bounded(unknown, 3), "; ") + "."
	default:
		res.Explanation = fmt.Sprintf("Compared %s changed by the branches at the base, each branch merged alone and the combined tree; no new conflict.", plural(len(locations), "migration history", "migration histories"))
	}
	return res
}

// same returns the members of the issue with key in history h, or nil.
func same(h *history, key string) []string {
	if h == nil {
		return nil
	}
	for _, b := range h.issues {
		if b.Key == key {
			return b.Members
		}
	}
	return nil
}

func finding(trees []Tree, histories []map[location]*history, readable []bool, l location, is issue) model.Finding {
	last := len(trees) - 1
	members := make([]string, 0, len(is.Members))
	locations := []model.Provenance{}
	for _, m := range is.Members {
		p := is.Paths[m]
		if l.tool == toolFlyway || l.tool == toolSQL {
			members = append(members, p)
		} else {
			members = append(members, fmt.Sprintf("%s (%s)", display(l, m), p))
		}
		locations = append(locations, model.Provenance{Repository: model.RepositoryID, Revision: trees[last].Revision, Path: p, Line: is.Lines[m], Method: method + ":" + l.tool, Evidence: model.VerifiedStatic})
	}
	var code, text string
	switch {
	case is.Kind == kindLabel:
		code = "migration_duplicate_branch_label"
		text = fmt.Sprintf("%s declares branch label %q more than once: %s. Alembic refuses a reused branch label.", l, is.Parent, strings.Join(members, ", "))
	case l.tool == toolLiquibase:
		code = "migration_duplicate_changeset"
		text = fmt.Sprintf("%s repeats changeSet (id, author) %q at %s.", l, is.Parent, strings.Join(members, ", "))
	case l.tool == toolAlembic:
		code = "migration_multiple_heads"
		text = fmt.Sprintf("%s has more than one head: revisions %s all revise %s. `alembic upgrade head` refuses multiple heads.", l, strings.Join(members, ", "), parentName(l, is.Parent))
	case l.tool == toolDjango:
		code = "migration_multiple_leaves"
		text = fmt.Sprintf("%s for app %s have more than one leaf: %s all depend on %s. `manage.py migrate` reports conflicting migrations.", l, is.App, strings.Join(members, ", "), parentName(l, is.Parent))
	default:
		code = "migration_duplicate_version"
		text = fmt.Sprintf("%s use version %s more than once: %s.", l, is.Parent, strings.Join(members, ", "))
	}
	// The base already has the problem when the combined tree only keeps,
	// drops or renames its parts: every member is a base member, or for
	// versions has the content of one (a renamed file).
	base := same(histories[0][l], is.Key)
	if len(base) > 0 && onBase(is, base, histories[0][l], histories[len(trees)-1][l]) {
		f := model.NewFinding(code+"_on_base", text+" The base already has this; combining did not introduce it.", model.VerifiedStatic)
		f.Locations = locations
		return f
	}
	// Attribute to a single branch only when merging that branch alone onto
	// the base produces exactly this problem.
	alone, partial, unreadable := -1, []string{}, false
	for i := 1; i < last; i++ {
		got := same(histories[i][l], is.Key)
		switch {
		case !readable[i]:
			unreadable = true
		case equal(got, is.Members):
			if alone < 0 {
				alone = i
			}
		case len(got) >= 2:
			partial = append(partial, trees[i].Label)
		}
	}
	if len(base) >= 2 {
		text += " The base already has part of it (" + strings.Join(base, ", ") + ")."
	}
	switch {
	case alone > 0:
		text += " " + capitalize(trees[alone].Label) + " already has this; combining branches did not cause it."
	case len(partial) > 0:
		text += " Part of it already appears with " + strings.Join(partial, " and ") + "; combining adds the rest."
	case unreadable:
		text += " The base does not have this; at least one branch could not be merged or read alone, so Radar does not say whether combining caused it."
	default:
		text += " Neither the base nor any branch merged alone onto it has this; it appears only when the branches are combined."
	}
	f := model.NewFinding(code, text, model.VerifiedStatic)
	f.Locations = locations
	f.Severity = model.SeverityError
	f.Remediation = fix(trees, histories, l, is)
	if l.tool == toolLiquibase {
		f.Remediation = "Give each loaded changeSet a unique id and author pair."
	}
	if is.Kind == kindLabel {
		f.Remediation = fmt.Sprintf("Give one of the revisions a different branch label than %q.", is.Parent)
	}
	return f
}

func onBase(is issue, base []string, before, after *history) bool {
	if is.Kind != kindVersion {
		return subset(is.Members, base)
	}
	// A renamed migration can inherit a base member only once. Reusing one
	// base blob for several new migrations must not hide a newly added duplicate.
	if len(is.Members) > len(base) {
		return false
	}
	assigned := map[string]string{}
	var match func(string, map[string]bool) bool
	match = func(member string, visited map[string]bool) bool {
		for _, old := range base {
			if visited[old] {
				continue
			}
			compatible := member == old
			for oid := range after.contents[member] {
				compatible = compatible || before.contents[old][oid]
			}
			if !compatible {
				continue
			}
			visited[old] = true
			if assigned[old] == "" || match(assigned[old], visited) {
				assigned[old] = member
				return true
			}
		}
		return false
	}
	for _, member := range is.Members {
		if !match(member, map[string]bool{}) {
			return false
		}
	}
	return true
}

// crossDirectory reports, as reasons the check cannot decide, a Flyway
// version that a changed directory shares with another directory under the
// same db/migration root. Whether both load depends on configured locations
// Radar does not read; the default location includes subdirectories.
func crossDirectory(trees []Tree, targets map[location]bool) []string {
	seen := func(t Tree) map[string]map[string]bool {
		out := map[string]map[string]bool{} // root + version -> directories
		for _, f := range t.Files {
			m := flywayName.FindStringSubmatch(path.Base(f.Path))
			dir := path.Dir(f.Path)
			i := strings.Index("/"+dir+"/", "/db/migration/")
			if m == nil || i < 0 {
				continue
			}
			key := ("/" + dir + "/")[:i] + "\x00" + normalizeVersion(m[1])
			if out[key] == nil {
				out[key] = map[string]bool{}
			}
			out[key][dir] = true
		}
		return out
	}
	base, combined := seen(trees[0]), seen(trees[len(trees)-1])
	// A version the base already repeats across the same directories is an
	// established layout, such as one folder per database vendor.
	layouts := [][]string{}
	for _, dirs := range base {
		if len(dirs) >= 2 {
			layouts = append(layouts, sortedKeys(dirs))
		}
	}
	reasons := []string{}
	for _, key := range sortedKeys(keysOf(combined)) {
		dirs := sortedKeys(combined[key])
		if len(dirs) < 2 || slices.ContainsFunc(layouts, func(b []string) bool { return subset(dirs, b) }) {
			continue
		}
		changed := false
		for _, d := range dirs {
			changed = changed || targets[location{toolFlyway, d}]
		}
		if changed {
			version := key[strings.Index(key, "\x00")+1:]
			reasons = append(reasons, fmt.Sprintf("Flyway version %s appears in %s; whether both load depends on configured locations", version, strings.Join(dirs, " and ")))
		}
	}
	return reasons
}

func subset(a, b []string) bool {
	set := map[string]bool{}
	for _, x := range b {
		set[x] = true
	}
	for _, x := range a {
		if !set[x] {
			return false
		}
	}
	return true
}

// fix proposes one edit: move the member the latest input introduced after
// the other line, or renumber it. It is a suggestion, not verified.
func fix(trees []Tree, histories []map[location]*history, l location, is issue) string {
	later, rank := is.Members[len(is.Members)-1], -1
	for _, m := range is.Members {
		for i := range trees {
			if h := histories[i][l]; h != nil && h.members[m] {
				if i >= rank {
					later, rank = m, i
				}
				break
			}
		}
	}
	// Members whose lines end in the same heads are one line already
	// joined by a merge; the other line is the one later does not belong to.
	others := map[string]bool{}
	for _, m := range is.Members {
		if !equal(is.Heads[m], is.Heads[later]) {
			others[strings.Join(is.Heads[m], "\x00")] = true
		}
	}
	head := ""
	if len(others) == 1 {
		for k := range others {
			if !strings.Contains(k, "\x00") {
				head = k
			}
		}
	}
	switch l.tool {
	case toolAlembic:
		if head != "" {
			return fmt.Sprintf("In %s, set down_revision = %q so it follows the other line, or add a merge revision with `alembic merge heads`.", is.Paths[later], head)
		}
		return "Add a merge revision with `alembic merge heads`, or chain the lines one after another."
	case toolDjango:
		if head != "" {
			instead := ""
			if is.Parent != "" {
				instead = fmt.Sprintf(" instead of (%q, %q)", is.App, is.Parent)
			}
			return fmt.Sprintf("In %s, depend on (%q, %q)%s, or run `python manage.py makemigrations --merge %s`.", is.Paths[later], is.App, head, instead, is.App)
		}
		return fmt.Sprintf("Run `python manage.py makemigrations --merge %s`.", is.App)
	}
	newest := ""
	for _, v := range histories[len(trees)-1][l].versions {
		if newest == "" || compareVersions(v, newest) > 0 {
			newest = v
		}
	}
	return fmt.Sprintf("Renumber %s (and its matching files) to a version above %s.", is.Paths[later], newest)
}

// outOfOrder warns once per new Flyway or versioned-SQL version that sorts
// below the newest version on the base: databases already past it skip or
// reject it unless out-of-order migration is allowed. A renamed or edited
// file keeping a base version is not new.
func outOfOrder(trees []Tree, histories []map[location]*history, l location) []model.Finding {
	base, combined := histories[0][l], histories[len(trees)-1][l]
	if base == nil || len(base.versions) == 0 || combined.versions == nil {
		return nil
	}
	newest, known := "", map[string]bool{}
	for _, v := range base.versions {
		known[v] = true
		if newest == "" || compareVersions(v, newest) > 0 {
			newest = v
		}
	}
	paths := map[string][]string{}
	for p, v := range combined.versions {
		if !known[v] && compareVersions(v, newest) < 0 {
			paths[v] = append(paths[v], p)
		}
	}
	versions := make([]string, 0, len(paths))
	for v := range paths {
		versions = append(versions, v)
	}
	sort.Slice(versions, func(i, j int) bool { return compareVersions(versions[i], versions[j]) < 0 })
	out := []model.Finding{}
	for _, v := range versions {
		ps := paths[v]
		sort.Strings(ps)
		f := model.NewFinding("migration_out_of_order", fmt.Sprintf("%s adds version %s (%s), below version %s already on the base. Databases already migrated past %s skip or reject it unless out-of-order migration is enabled.", l, v, strings.Join(ps, ", "), newest, newest), model.VerifiedStatic)
		if l.tool == toolPrisma {
			f.Explanation = fmt.Sprintf("%s adds timestamp %s (%s), earlier than %s already on the base. Fresh databases and databases with applied migrations may observe different chronological order.", l, v, strings.Join(ps, ", "), newest)
		}
		for _, p := range ps {
			f.Locations = append(f.Locations, model.Provenance{Repository: model.RepositoryID, Revision: trees[len(trees)-1].Revision, Path: p, Method: method + ":" + l.tool, Evidence: model.VerifiedStatic})
		}
		f.Remediation = fmt.Sprintf("Renumber version %s above %s if it must run on existing databases.", v, newest)
		out = append(out, f)
	}
	return out
}

func display(l location, member string) string {
	if l.tool == toolAlembic {
		return fmt.Sprintf("%q", member)
	}
	return member
}

func parentName(l location, parent string) string {
	if parent == "" {
		if l.tool == toolAlembic {
			return "no earlier revision (multiple roots)"
		}
		return "no earlier migration (multiple initial migrations)"
	}
	if l.tool == toolAlembic {
		return fmt.Sprintf("%q", parent)
	}
	return parent
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func short(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

func bounded(items []string, n int) []string {
	if len(items) > n {
		return append(append([]string{}, items[:n]...), fmt.Sprintf("%d more", len(items)-n))
	}
	return items
}

func equal(a, b []string) bool {
	if a == nil || b == nil || len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
