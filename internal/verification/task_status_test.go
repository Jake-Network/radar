package verification

import (
	"github.com/Jake-Network/radar/internal/model"
	"github.com/Jake-Network/radar/internal/planning"
	"testing"
)

func TestExecutionProblemsNeverCompleteTasks(t *testing.T) {
	p := planning.Plan{Tasks: []planning.Task{{ID: "producer", Acceptance: []string{"tests"}}, {ID: "consumer", DependsOn: []string{"producer"}, Acceptance: []string{"client"}}}}
	for _, status := range []model.Status{model.StatusError, model.StatusTimeout, model.StatusIncomplete} {
		r := planning.Report{Checks: []planning.Check{{ID: "tests", Status: status}, {ID: "client", Status: model.StatusPassed}}}
		ApplyTaskResults(p, &r)
		if len(r.Tasks) != 2 || r.Tasks[0].Status == model.StatusPassed || r.Tasks[1].Status != model.StatusBlocked || len(r.Feedback) != 2 {
			t.Fatalf("execution problem hidden: %+v", r)
		}
	}
}
