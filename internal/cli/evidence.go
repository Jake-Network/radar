package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/Jake-Network/radar/internal/evidence"
	"github.com/Jake-Network/radar/internal/model"
)

func (a *app) test(o options) int {
	if !o.allow {
		return a.fail(errors.New("test runs repository code; explicitly opt in with --allow-execution"))
	}
	if o.ref == "" || o.ref == "WORKTREE" {
		return a.fail(errors.New("test requires --ref for an immutable committed checkpoint"))
	}
	if e := a.repository(); e != nil {
		return a.fail(e)
	}
	p, e := a.loadPlan(o.plan)
	if e != nil {
		return a.fail(e)
	}
	fmt.Fprintln(a.errout, "Radar will execute the explicitly declared repository test command in a private committed snapshot. This is not an OS sandbox.")
	record, e := evidence.Run(a.ctx, a.root, o.ref, p, o.args, o.timeout)
	if e != nil {
		return a.fail(e)
	}
	if e = a.store.SaveEvidence(a.ctx, record.ID, record); e != nil {
		return a.fail(e)
	}
	// The output tail is displayed, never persisted.
	a.report(struct {
		evidence.Record
		OutputTail string `json:"output_tail,omitempty"`
	}{record, record.OutputTail}, func(w io.Writer) { renderRecord(w, record) })
	if record.Status == model.StatusFailed {
		return 1
	}
	return 0
}
func (a *app) records(ids []string) ([]evidence.Record, error) {
	out := []evidence.Record{}
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		kind, raw, e := a.store.Artifact(a.ctx, id)
		if e != nil {
			return nil, e
		}
		if kind != "test_evidence" {
			return nil, fmt.Errorf("artifact %s is not observed test evidence", id)
		}
		var r evidence.Record
		if e = json.Unmarshal(raw, &r); e != nil {
			return nil, e
		}
		out = append(out, r)
	}
	return out, nil
}
