package contracts

import (
	"context"
	"testing"

	"github.com/Jake-Network/radar/internal/model"
)

func TestDeletedManifestKeepsBaselineObligation(t *testing.T) {
	root, base := fixture(t)
	write(t, root, "openapi.json", `{"openapi":"3.1.0","components":{"schemas":{"Export":{"type":"object","properties":{}}}}}`)
	gitRun(t, root, "rm", "-q", ".radar/contracts.json")
	commit(t, root, "drop field and manifest")
	r, err := Impact(context.Background(), root, base, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != model.StatusFailed || len(r.Findings) != 1 || r.Findings[0].Code != "contract_obligation_removed" || r.Findings[0].Severity != model.SeverityError {
		t.Fatalf("%+v", r)
	}
	if len(r.Obligations) != 1 || r.Obligations[0].Kind != "removed" || len(r.Unestablished()) == 0 {
		t.Fatalf("obligation loss not recorded: %+v", r)
	}
	// An approved plan's removal delta is an explicit, reviewed retirement.
	r, err = Impact(context.Background(), root, base, "HEAD", Retirement{ID: "export", Reason: "approved plan removes export"})
	if err != nil {
		t.Fatal(err)
	}
	if r.Status == model.StatusFailed || len(r.Findings) != 1 || r.Findings[0].Code != "contract_obligation_retired" || r.Findings[0].Severity != model.SeverityWarning || len(r.Unestablished()) != 0 {
		t.Fatalf("approved retirement not honored: %+v", r)
	}
}

func TestPreexistingRetirementDoesNotRetireAgain(t *testing.T) {
	root, _ := fixture(t)
	// A stale retirement already present at base must not excuse a later removal.
	write(t, root, ".radar/contracts.json", `{"version":1,"bindings":[{"id":"export","schema":"openapi.json","pointer":"/components/schemas/Export","producer":"backend.py","consumer":"consumer.ts","fields":["total"],"direction":"response"}],"retired":[{"id":"export","reason":"old"}]}`)
	commit(t, root, "stale retirement")
	base := gitRun(t, root, "rev-parse", "HEAD")
	write(t, root, "openapi.json", `{"openapi":"3.1.0","components":{"schemas":{"Export":{"type":"object","properties":{}}}}}`)
	write(t, root, ".radar/contracts.json", `{"version":1,"bindings":[],"retired":[{"id":"export","reason":"old"}]}`)
	commit(t, root, "remove")
	r, err := Impact(context.Background(), root, base, "HEAD")
	if err != nil || r.Status != model.StatusFailed {
		t.Fatalf("%+v %v", r, err)
	}
}

func TestManifestRetirementValidation(t *testing.T) {
	root, _ := fixture(t)
	for _, bad := range []string{
		`{"version":1,"bindings":[],"retired":[{"id":"x"}]}`,
		`{"version":1,"bindings":[],"retired":[{"id":"","reason":"r"}]}`,
		`{"version":1,"bindings":[],"retired":[{"id":"x","reason":"r"},{"id":"x","reason":"s"}]}`,
	} {
		write(t, root, ".radar/contracts.json", bad)
		if _, err := LoadManifest(context.Background(), root, "WORKTREE"); err == nil {
			t.Errorf("accepted %s", bad)
		}
	}
}
