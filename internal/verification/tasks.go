package verification

import "github.com/radar-engine/radar/internal/planning"

// ApplyTaskResults derives task completion from acceptance evidence and DAG
// prerequisites. Owning code or finishing an agent session is not completion.
func ApplyTaskResults(p planning.Plan, r *planning.Report) {
	r.Tasks = []planning.TaskStatus{}
	r.Feedback = []planning.AgentFeedback{}
	schedule, err := planning.Tasks(p)
	if err != nil {
		return
	}
	byID := map[string]planning.Task{}
	for _, t := range p.Tasks {
		byID[t.ID] = t
	}
	checks := map[string]planning.Check{}
	for _, c := range r.Checks {
		checks[c.ID] = c
	}
	results := map[string]planning.TaskStatus{}
	for _, id := range schedule.Order {
		t := byID[id]
		result := planning.TaskStatus{ID: id, Status: "passed", Explanation: "All declared acceptance checks passed and prerequisites are complete.", Checks: append([]string(nil), t.Acceptance...), BlockedBy: []string{}}
		if len(t.Acceptance) == 0 {
			result.Status = "unknown"
			result.Explanation = "Task declares no acceptance evidence."
		}
		for _, contractID := range t.Contracts {
			for _, check := range r.Checks {
				if check.ID == "contract:"+contractID || check.ID == "contract:"+contractID+":binding" {
					result.Checks = append(result.Checks, check.ID)
				}
			}
		}
		for _, cid := range result.Checks {
			c, ok := checks[cid]
			if !ok || c.Status == "unknown" || c.Status == "warning" {
				if result.Status != "failed" {
					result.Status = "unknown"
					result.Explanation = "Acceptance evidence is missing or unverified."
				}
			} else if c.Status == "failed" {
				result.Status = "failed"
				result.Explanation = "Declared acceptance evidence failed."
			}
		}
		for _, dep := range t.DependsOn {
			if results[dep].Status != "passed" {
				result.BlockedBy = append(result.BlockedBy, dep)
			}
		}
		if len(result.BlockedBy) > 0 && result.Status != "failed" {
			result.Status = "blocked"
			result.Explanation = "Prerequisite tasks are not verified complete."
		}
		results[id] = result
		r.Tasks = append(r.Tasks, result)
		if result.Status != "passed" {
			r.Feedback = append(r.Feedback, planning.AgentFeedback{TaskID: id, Status: result.Status, Message: result.Explanation, CheckIDs: append([]string(nil), result.Checks...)})
		}
	}
}
