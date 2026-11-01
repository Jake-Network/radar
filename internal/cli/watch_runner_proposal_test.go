package cli

import "testing"

func TestSupersetRunnerProposalNeverRunsShell(t *testing.T) {
	root := t.TempDir()
	if got := supersetSetupProposal(root); got != nil {
		t.Fatal(got)
	}
	put(t, root, ".superset/config.json", `{"setup":[["uv","sync","--frozen"]]}`)
	got := supersetSetupProposal(root)
	if got == nil || len(got.Setup) != 1 || got.Setup[0][0] != "uv" {
		t.Fatal(got)
	}
	put(t, root, ".superset/config.json", `{"scripts":{"setup":"touch should-not-run"}}`)
	got = supersetSetupProposal(root)
	if got == nil || len(got.Setup) != 0 {
		t.Fatal(got)
	}
}
