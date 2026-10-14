package migrations

import (
	"context"
	"fmt"
	"strings"
	"time"

	sitter "github.com/tree-sitter/go-tree-sitter"
	pyGrammar "github.com/tree-sitter/tree-sitter-python/bindings/go"
)

// pySource is one parsed Python file. Only literal values are read; anything
// computed is reported as a reason instead of being guessed.
type pySource struct {
	b    []byte
	root *sitter.Node
	tree *sitter.Tree
}

// parsePython parses a bounded source with the parser timeout and input
// callback used by the language adapters (ParseCtx races teardown upstream).
func parsePython(ctx context.Context, content []byte) (*pySource, string, error) {
	parseCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	parser := sitter.NewParser()
	defer parser.Close()
	if err := parser.SetLanguage(sitter.NewLanguage(pyGrammar.Language())); err != nil {
		return nil, "", err
	}
	parser.SetTimeoutMicros(5_000_000)
	tree := parser.ParseWithOptions(func(offset int, _ sitter.Point) []byte {
		if parseCtx.Err() != nil || offset >= len(content) {
			return nil
		}
		return content[offset:min(offset+4096, len(content))]
	}, nil, nil)
	if err := ctx.Err(); err != nil {
		if tree != nil {
			tree.Close()
		}
		return nil, "", err
	}
	if tree == nil || parseCtx.Err() != nil {
		if tree != nil {
			tree.Close()
		}
		return nil, "Python parse did not finish", nil
	}
	root := tree.RootNode()
	if root.HasError() {
		tree.Close()
		return nil, "Python syntax errors", nil
	}
	return &pySource{b: content, root: root, tree: tree}, "", nil
}

func (s *pySource) close() { s.tree.Close() }

func (s *pySource) text(n *sitter.Node) string { return n.Utf8Text(s.b) }

func line(n *sitter.Node) int { return int(n.StartPosition().Row) + 1 }

// assignments returns the simple `name = value` and `name: T = value`
// statements directly inside a module or class body, keyed by name. A name
// bound more than once, or also bound or changed anywhere else in scope
// (`+=`, a conditional or nested assignment, or with attrs any `x.name` or
// `name.append(...)`), maps to nil, which callers treat as not literal.
func (s *pySource) assignments(body, scope *sitter.Node, attrs bool, names ...string) map[string]*sitter.Node {
	want := map[string]bool{}
	for _, n := range names {
		want[n] = true
	}
	out := map[string]*sitter.Node{}
	top := map[uint]bool{}
	for i := uint(0); i < body.NamedChildCount(); i++ {
		stmt := body.NamedChild(i)
		if stmt.Kind() != "expression_statement" || stmt.NamedChildCount() != 1 {
			continue
		}
		a := stmt.NamedChild(0)
		if a.Kind() != "assignment" {
			continue
		}
		left := a.ChildByFieldName("left")
		if left == nil || left.Kind() != "identifier" || !want[s.text(left)] {
			continue
		}
		top[a.StartByte()] = true
		name := s.text(left)
		if _, seen := out[name]; seen {
			out[name] = nil
			continue
		}
		out[name] = a.ChildByFieldName("right")
	}
	// Inside a function only the migration's own attribute counts
	// (self.dependencies); t.dependencies there is a model field.
	type frame struct {
		n        *sitter.Node
		function bool
	}
	stack := []frame{{scope, false}}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		n, function := cur.n, cur.function
		stack = stack[:len(stack)-1]
		switch n.Kind() {
		case "assignment", "augmented_assignment":
			if left := n.ChildByFieldName("left"); left != nil && left.Kind() == "identifier" && want[s.text(left)] && !top[n.StartByte()] {
				out[s.text(left)] = nil
			}
		case "attribute":
			// Migration.dependencies = ..., self.dependencies.append(...),
			// dependencies.extend(...): any use beyond the literal.
			field, object := n.ChildByFieldName("attribute"), n.ChildByFieldName("object")
			own := object != nil && object.Kind() == "identifier" && (s.text(object) == "self" || s.text(object) == "cls" || s.text(object) == "Migration")
			if attrs && field != nil && want[s.text(field)] && (!function || own) {
				out[s.text(field)] = nil
			}
			if attrs && object != nil && object.Kind() == "identifier" && want[s.text(object)] {
				out[s.text(object)] = nil
			}
		}
		for i := uint(0); i < n.NamedChildCount(); i++ {
			stack = append(stack, frame{n.NamedChild(i), function || n.Kind() == "function_definition"})
		}
	}
	return out
}

// str reads a plain string literal: no f-string, bytes, escape, implicit
// concatenation or interpolation.
func (s *pySource) str(n *sitter.Node) (string, bool) {
	for n != nil && n.Kind() == "parenthesized_expression" && n.NamedChildCount() == 1 {
		n = n.NamedChild(0)
	}
	if n == nil || n.Kind() != "string" {
		return "", false
	}
	var b strings.Builder
	for i := uint(0); i < n.NamedChildCount(); i++ {
		c := n.NamedChild(i)
		switch c.Kind() {
		case "string_start":
			if strings.ContainsAny(strings.ToLower(s.text(c)), "fb") {
				return "", false
			}
		case "string_content":
			b.WriteString(s.text(c))
		case "string_end":
		default:
			return "", false
		}
	}
	v := b.String()
	if v == "" || strings.ContainsAny(v, "\\\n") {
		return "", false
	}
	return v, true
}

// strList reads a list or tuple whose elements are all plain strings.
func (s *pySource) strList(n *sitter.Node) ([]string, bool) {
	if n == nil || (n.Kind() != "list" && n.Kind() != "tuple") {
		return nil, false
	}
	out := []string{}
	for i := uint(0); i < n.NamedChildCount(); i++ {
		c := n.NamedChild(i)
		if c.Kind() == "comment" {
			continue
		}
		v, ok := s.str(c)
		if !ok {
			return nil, false
		}
		out = append(out, v)
	}
	return out, true
}

// alembicRevision is one Alembic revision file's identity.
type alembicRevision struct {
	id      string
	parents []string
	line    int
	// reason explains why the file cannot be used; empty when it can.
	reason string
	// skip marks a module without a revision, such as a helper.
	skip bool
	// labels name the branch this revision starts (branch_labels): Alembic
	// histories with labels keep one head per branch on purpose.
	labels []string
}

func parseAlembic(ctx context.Context, content []byte) (alembicRevision, error) {
	src, reason, err := parsePython(ctx, content)
	if err != nil || reason != "" {
		return alembicRevision{reason: reason}, err
	}
	defer src.close()
	a := src.assignments(src.root, src.root, false, "revision", "down_revision", "branch_labels")
	rev, hasRev := a["revision"]
	down, hasDown := a["down_revision"]
	if !hasRev && !hasDown {
		return alembicRevision{skip: true}, nil
	}
	r := alembicRevision{}
	var ok bool
	if r.id, ok = src.str(rev); !ok {
		return alembicRevision{reason: "revision is not a plain string literal"}, nil
	}
	r.line = line(rev)
	switch {
	case !hasDown || down == nil:
		return alembicRevision{reason: "down_revision is missing, reassigned or changed outside its literal"}, nil
	case down.Kind() == "none":
	default:
		if one, ok := src.str(down); ok {
			r.parents = []string{one}
		} else if many, ok := src.strList(down); ok && len(many) > 0 {
			r.parents = many
		} else {
			return alembicRevision{reason: "down_revision is not a string, tuple of strings or None"}, nil
		}
	}
	if labels, present := a["branch_labels"]; present {
		single, one := src.str(labels)
		many, list := src.strList(labels)
		switch {
		case labels == nil:
			return alembicRevision{reason: "branch_labels is reassigned or changed outside its literal"}, nil
		case labels.Kind() == "none" || (list && len(many) == 0):
		case one:
			r.labels = []string{single}
		case list:
			r.labels = many
		default:
			return alembicRevision{reason: "branch_labels is not a string, tuple of strings or None"}, nil
		}
	}
	return r, nil
}

// djangoMigration is one Django migration module's same-file facts. Pairs
// are (app_label, migration_name).
type djangoMigration struct {
	dependencies [][2]string
	replaces     [][2]string
	runBefore    [][2]string
	line         int
	reason       string
	skip         bool
}

func parseDjango(ctx context.Context, content []byte) (djangoMigration, error) {
	src, reason, err := parsePython(ctx, content)
	if err != nil || reason != "" {
		return djangoMigration{reason: reason}, err
	}
	defer src.close()
	var class *sitter.Node
	for i := uint(0); i < src.root.NamedChildCount(); i++ {
		n := src.root.NamedChild(i)
		if n.Kind() != "class_definition" || src.text(n.ChildByFieldName("name")) != "Migration" {
			continue
		}
		if class != nil {
			return djangoMigration{reason: "more than one Migration class"}, nil
		}
		class = n
	}
	if class == nil {
		return djangoMigration{skip: true}, nil
	}
	if bases := class.ChildByFieldName("superclasses"); bases == nil || !strings.HasSuffix(strings.TrimRight(src.text(bases), ") \t\n,"), "Migration") {
		return djangoMigration{reason: "Migration class does not extend a Migration base"}, nil
	}
	body := class.ChildByFieldName("body")
	if body == nil {
		return djangoMigration{reason: "Migration class has no body"}, nil
	}
	m := djangoMigration{line: line(class)}
	a := src.assignments(body, src.root, true, "dependencies", "replaces", "run_before")
	for _, field := range []struct {
		name string
		into *[][2]string
	}{{"dependencies", &m.dependencies}, {"replaces", &m.replaces}, {"run_before", &m.runBefore}} {
		value, present := a[field.name]
		if !present {
			continue
		}
		pairs, why := src.pairs(value)
		if why != "" {
			return djangoMigration{reason: field.name + " " + why}, nil
		}
		*field.into = pairs
	}
	return m, nil
}

// pairs reads a list of ("app", "name") tuples. swappable_dependency(...)
// names a configurable model in another app and is skipped.
func (s *pySource) pairs(n *sitter.Node) ([][2]string, string) {
	if n == nil {
		return nil, "is reassigned or changed outside its literal"
	}
	if n.Kind() != "list" && n.Kind() != "tuple" {
		return nil, "is not a literal list"
	}
	out := [][2]string{}
	for i := uint(0); i < n.NamedChildCount(); i++ {
		c := n.NamedChild(i)
		switch c.Kind() {
		case "comment":
			continue
		case "call":
			if fn := s.text(c.ChildByFieldName("function")); fn == "swappable_dependency" || fn == "migrations.swappable_dependency" {
				continue
			}
			return nil, "contains a computed entry"
		}
		values, ok := s.strList(c)
		if !ok || len(values) != 2 {
			return nil, fmt.Sprintf("entry %d is not an (app, migration) pair of strings", i+1)
		}
		out = append(out, [2]string{values[0], values[1]})
	}
	return out, ""
}
