package verification

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/radar-engine/radar/internal/contracts"
	gitrepo "github.com/radar-engine/radar/internal/git"
	"github.com/radar-engine/radar/internal/jsonptr"
	"github.com/radar-engine/radar/internal/model"
	"github.com/radar-engine/radar/internal/planning"
)

func evaluate(ctx context.Context, id string, rule *planning.Rule, s model.Snapshot, root string) planning.Check {
	c := planning.Check{ID: id, Status: model.StatusUnknown, Evidence: model.Unknown, Explanation: "No deterministic verification rule declared."}
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
				c.Status = model.StatusPassed
				c.Evidence = n.Provenance.Evidence
				c.Location = &n.Provenance
				c.Explanation = "Entity observed in indexed graph."
				if c.Evidence != model.VerifiedStatic && c.Evidence != model.VerifiedTool {
					c.Status = model.StatusUnknown
					c.Explanation = "Entity has no verified indexing evidence."
				}
				return c
			}
		}
		c.Status = model.StatusFailed
		c.Evidence = model.VerifiedStatic
		c.Explanation = "Entity absent from indexed graph."
		if incompleteFor(s, model.PathFromID(rule.Entity)) {
			c.Status = model.StatusUnknown
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
			c.Status = model.StatusFailed
			c.Evidence = model.VerifiedStatic
			c.Explanation = "Required file does not exist at the analyzed revision."
		}
		return c
	}
	c.Evidence = model.VerifiedStatic
	if rule.Kind == "file_exists" {
		c.Status = model.StatusPassed
		c.Explanation = "File exists and is readable at analyzed revision."
		return c
	}
	doc, err := contracts.DecodeDocumentAt(rule.Path, data)
	if err != nil {
		c.Evidence = model.Unknown
		c.Explanation = "Malformed JSON/YAML evidence: " + err.Error()
		return c
	}
	value, found, err := jsonptr.Lookup(doc, rule.Pointer)
	if err != nil {
		c.Evidence = model.Unknown
		c.Explanation = "JSON pointer crosses a non-object; unsupported evidence."
		return c
	}
	if !found {
		c.Status = model.StatusFailed
		c.Explanation = "JSON pointer does not exist: " + rule.Pointer
		return c
	}
	obj, ok := value.(map[string]any)
	if !ok {
		c.Evidence = model.Unknown
		c.Explanation = "JSON pointer target is not an object."
		return c
	}
	_, ok = obj[rule.Property]
	c.Status = model.StatusFailed
	c.Explanation = fmt.Sprintf("Required property %q is absent at %q.", rule.Property, rule.Pointer)
	if ok {
		c.Status = model.StatusPassed
		c.Explanation = fmt.Sprintf("Required property %q is present at %q.", rule.Property, rule.Pointer)
	}
	return c
}
