package verification

import (
	"context"
	"github.com/Jake-Network/radar/internal/evidence"
	"github.com/Jake-Network/radar/internal/model"
	"github.com/Jake-Network/radar/internal/planning"
)

// VerifyCandidateWithEvidence excludes all ordinary branch evidence. It runs
// while the private candidate object database still exists, before destruction.
func VerifyCandidateWithEvidence(ctx context.Context, p planning.Plan, s model.Snapshot, root string, c evidence.CandidateCheckpoint, records []evidence.Record, sourceUnchanged bool) planning.Report {
	var eligible []evidence.Record
	if sourceUnchanged {
		for _, record := range records {
			valid := true
			for _, criterion := range record.Criteria {
				if e := evidence.ValidateCandidate(record, root, c, p, criterion, record.Command); e != nil {
					valid = false
					break
				}
			}
			if valid && len(record.Criteria) > 0 {
				eligible = append(eligible, record)
			}
		}
	}
	r := VerifyWithEvidence(ctx, p, s, root, eligible)
	if !sourceUnchanged {
		r.Authoritative = false
		r.Checks = append(r.Checks, planning.Check{ID: "candidate_source_integrity", Status: model.StatusUnknown, Evidence: model.Unknown, Explanation: "Candidate source changed during the selected suite; all candidate test records were excluded."})
		if r.Status == model.StatusPassed {
			r.Status = model.StatusUnknown
		}
		f := model.NewFinding("candidate_source_changed", "Candidate source changed; repair and rerun the entire selected suite on a fresh preview.", model.ObservedTest)
		f.Remediation = "Rerun every selected command against a new unchanged candidate after repairing the mutating setup or test."
		r.Findings = append(r.Findings, f)
		ApplyTaskResults(p, &r)
	}
	return r
}
