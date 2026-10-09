package verification

import (
	"context"
	"fmt"
	"github.com/radar-engine/radar/internal/evidence"
	"github.com/radar-engine/radar/internal/model"
	"github.com/radar-engine/radar/internal/planning"
	"time"
)

func evaluateWithEvidence(ctx context.Context, id string, rule *planning.Rule, s model.Snapshot, root string, p planning.Plan, records []evidence.Record) planning.Check {
	if rule == nil || rule.Kind != "test_run" {
		return evaluate(ctx, id, rule, s, root)
	}
	c := planning.Check{ID: id, Status: "unknown", Evidence: model.Unknown, Explanation: "No matching observed test evidence for this plan, commit, criterion and command."}
	if e := ctx.Err(); e != nil {
		c.Explanation = e.Error()
		return c
	}
	if e := planning.ValidateRule(*rule); e != nil {
		c.Explanation = e.Error()
		return c
	}
	var chosen *evidence.Record
	for i := range records {
		r := &records[i]
		if rule.EvidenceID != "" && r.ID != rule.EvidenceID {
			continue
		}
		if e := evidence.Validate(*r, root, s.Revision, p, id, rule.Command); e != nil {
			continue
		}
		finished, _ := time.Parse(time.RFC3339Nano, r.FinishedAt)
		previous := time.Time{}
		if chosen != nil {
			previous, _ = time.Parse(time.RFC3339Nano, chosen.FinishedAt)
		}
		if chosen == nil || finished.After(previous) || (finished.Equal(previous) && r.ID > chosen.ID) {
			chosen = r
		}
	}
	if chosen == nil {
		return c
	}
	c.Location = &model.Provenance{Repository: chosen.Repository, Revision: chosen.Revision, Method: "observed_test:" + chosen.ID, Evidence: model.ObservedTest, Timestamp: chosen.FinishedAt}
	c.Evidence = model.ObservedTest
	switch chosen.Status {
	case "passed":
		c.Status = "passed"
		c.Explanation = fmt.Sprintf("%d executed test cases passed at the exact implementation checkpoint (evidence %s).", chosen.TestsRun, chosen.ID)
	case "failed":
		c.Status = "failed"
		c.Explanation = fmt.Sprintf("Declared test command failed with exit code %d (evidence %s).", chosen.ExitCode, chosen.ID)
	case "timeout":
		c.Explanation = "Test execution timed out; criterion is not verified."
	case "unknown":
		c.Explanation = "Test runner did not establish successful execution of non-skipped test cases."
	}
	return c
}
