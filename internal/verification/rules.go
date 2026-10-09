package verification

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	gitrepo "github.com/radar-engine/radar/internal/git"
	"github.com/radar-engine/radar/internal/model"
	"github.com/radar-engine/radar/internal/planning"
	"os"
	"strings"
)

func evaluate(ctx context.Context, id string, rule *planning.Rule, s model.Snapshot, root string) planning.Check {
	c := planning.Check{ID: id, Status: "unknown", Evidence: model.Unknown, Explanation: "No deterministic verification rule declared."}
	if rule == nil {
		return c
	}
	if err := planning.ValidateRule(*rule); err != nil {
		c.Explanation = err.Error()
		return c
	}
	if err := ctx.Err(); err != nil {
		c.Explanation = err.Error()
		return c
	}
	location := model.Provenance{Repository: s.Repository, Revision: s.Revision, Path: rule.Path, Method: "verification:" + rule.Kind, Evidence: model.VerifiedStatic}
	c.Location = &location
	if rule.Kind == "graph_entity" {
		for _, n := range s.Nodes {
			if n.ID == rule.Entity {
				c.Status = "passed"
				c.Evidence = n.Provenance.Evidence
				c.Location = &n.Provenance
				c.Explanation = "Entity observed in indexed graph."
				if c.Evidence != model.VerifiedStatic && c.Evidence != model.VerifiedTool {
					c.Status = "unknown"
					c.Explanation = "Entity has no verified indexing evidence."
				}
				return c
			}
		}
		c.Status = "failed"
		c.Evidence = model.VerifiedStatic
		c.Explanation = "Entity absent from indexed graph."
		if incompleteFor(s, rule.Path) {
			c.Status = "unknown"
			c.Evidence = model.Unknown
			c.Explanation = "Entity absent but indexing diagnostics prevent proving absence."
		}
		return c
	}
	readRevision := s.Revision
	if strings.HasPrefix(readRevision, "WORKTREE:") {
		readRevision = "WORKTREE"
	}
	data, err := gitrepo.ReadFile(ctx, root, readRevision, rule.Path)
	if err != nil {
		c.Explanation = "Unable to read bounded repository evidence: " + err.Error()
		if errors.Is(err, os.ErrNotExist) {
			c.Status = "failed"
			c.Evidence = model.VerifiedStatic
			c.Explanation = "Required file does not exist at the analyzed revision."
		}
		return c
	}
	c.Evidence = model.VerifiedStatic
	if rule.Kind == "file_exists" {
		c.Status = "passed"
		c.Explanation = "File exists and is readable at analyzed revision."
		return c
	}
	var value any
	if err = json.Unmarshal(data, &value); err != nil {
		c.Evidence = model.Unknown
		c.Explanation = "Malformed JSON evidence: " + err.Error()
		return c
	}
	if rule.Pointer != "" {
		for _, token := range strings.Split(strings.TrimPrefix(rule.Pointer, "/"), "/") {
			token = strings.ReplaceAll(strings.ReplaceAll(token, "~1", "/"), "~0", "~")
			obj, ok := value.(map[string]any)
			if !ok {
				c.Explanation = "JSON pointer crosses a non-object; unsupported evidence."
				c.Evidence = model.Unknown
				return c
			}
			value, ok = obj[token]
			if !ok {
				c.Status = "failed"
				c.Explanation = "JSON pointer does not exist: " + rule.Pointer
				return c
			}
		}
	}
	obj, ok := value.(map[string]any)
	if !ok {
		c.Evidence = model.Unknown
		c.Explanation = "JSON pointer target is not an object."
		return c
	}
	_, ok = obj[rule.Property]
	c.Status = "failed"
	c.Explanation = fmt.Sprintf("Required property %q is absent at %q.", rule.Property, rule.Pointer)
	if ok {
		c.Status = "passed"
		c.Explanation = fmt.Sprintf("Required property %q is present at %q.", rule.Property, rule.Pointer)
	}
	return c
}
