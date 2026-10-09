package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/radar-engine/radar/internal/evidence"
	"strings"
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
	a.emit(record)
	if record.Status == "failed" {
		return 1
	}
	return 0
}
func (a *app) records(ids string) ([]evidence.Record, error) {
	out := []evidence.Record{}
	if ids == "" {
		return out, nil
	}
	seen := map[string]bool{}
	for _, id := range strings.Split(ids, ",") {
		id = strings.TrimSpace(id)
		if seen[id] {
			continue
		}
		seen[id] = true
		kind, raw, e := a.store.Artifact(a.ctx, strings.TrimSpace(id))
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
