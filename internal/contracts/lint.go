package contracts

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	gitrepo "github.com/Jake-Network/radar/internal/git"
	"github.com/Jake-Network/radar/internal/model"
)

// BindingCheck reports whether one binding can be analyzed and still matches
// the files it declares.
type BindingCheck struct {
	ID     string       `json:"id"`
	Status model.Status `json:"status"`
	Issues []string     `json:"issues"`
	Notes  []string     `json:"notes,omitempty"`
}

// LintReport summarizes `radar contracts`.
type LintReport struct {
	Status   model.Status   `json:"status"`
	Revision string         `json:"revision"`
	Bindings []BindingCheck `json:"bindings"`
	Error    string         `json:"error,omitempty"`
}

// MissingConsumerFields returns declared fields whose final name does not
// occur as an identifier in the consumer source. This lexical check flags
// declarations that may be stale; presence does not prove runtime use.
func MissingConsumerFields(content []byte, fields []string) []string {
	var missing []string
	for _, field := range fields {
		parts := strings.Split(strings.ReplaceAll(field, "[]", ""), ".")
		name := parts[len(parts)-1]
		if name == "" {
			continue
		}
		pattern := regexp.MustCompile(`(^|[^A-Za-z0-9_$])` + regexp.QuoteMeta(name) + `($|[^A-Za-z0-9_$])`)
		if !pattern.Match(content) {
			missing = append(missing, field)
		}
	}
	return missing
}

// Lint checks the manifest at ref: schema analyzability, pointers, declared
// files, declared fields against the schema, and lexical consumer drift.
func Lint(ctx context.Context, root, ref string) LintReport {
	r := LintReport{Status: model.StatusPassed, Revision: ref, Bindings: []BindingCheck{}}
	m, err := LoadManifest(ctx, root, ref)
	if err != nil {
		r.Status = model.StatusFailed
		r.Error = err.Error()
		return r
	}
	for _, b := range m.Bindings {
		c := BindingCheck{ID: b.ID, Status: model.StatusPassed, Issues: []string{}}
		issue := func(format string, args ...any) { c.Issues = append(c.Issues, fmt.Sprintf(format, args...)) }
		doc, err := ReadSchema(ctx, root, ref, b.Schema)
		var schema *Schema
		if err == nil {
			schema, err = Analyzable(doc, b.Pointer)
		}
		if err != nil {
			issue("schema %s: %v", b.Schema, err)
		} else {
			c.Notes = schema.Notes("")
			for _, field := range b.Fields {
				if _, ok := schema.Field(field); !ok {
					issue("declared field %q is not a property of %s%s", field, b.Schema, b.Pointer)
				}
			}
		}
		if b.Direction == "" {
			c.Notes = append(c.Notes, "direction is unset; incompatible changes are reported as risks, not failures")
		}
		for _, link := range []struct{ role, path string }{{"producer", b.Producer}, {"consumer", b.Consumer}} {
			if link.path == "" {
				continue
			}
			content, err := gitrepo.ReadFile(ctx, root, ref, link.path)
			if err != nil {
				issue("%s %s: %v", link.role, link.path, err)
				continue
			}
			if link.role == "consumer" {
				for _, field := range MissingConsumerFields(content, b.Fields) {
					issue("declared field %q does not appear in consumer %s; the declaration may be stale (lexical check)", field, link.path)
				}
			}
		}
		if len(c.Issues) > 0 {
			c.Status = model.StatusWarning
			r.Status = model.StatusWarning
		}
		r.Bindings = append(r.Bindings, c)
	}
	return r
}
