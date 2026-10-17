package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestLandingRefusalExplainsPublicationAndImportedObjects(t *testing.T) {
	root, agent := checkoutRepo(t)
	agent("feature", map[string]string{"backend.py": "def price():\n    return 2\n"})
	put(t, root, ".radar/landing-policy.json", `{"version":1,"require":["textual_merge","integration_execution"]}`)
	result := invoke(t, root, 1, "land", "feature", "--base", "main", "--apply", "--policy", ".radar/landing-policy.json")
	outcome, ok := result["landing_apply"].(map[string]any)
	if !ok || outcome["published"] != false || outcome["objects_imported"] != true || outcome["reason"] == "" || result["integration_ref"] != nil {
		t.Fatalf("refusal metadata: %v", result)
	}
	if refs := gitTest(t, root, "for-each-ref", "--format=%(refname)", "refs/radar/integration/"); refs != "" {
		t.Fatal("blocked landing published", refs)
	}
	var out, errs bytes.Buffer
	code := Run(context.Background(), []string{"--root", root, "land", "feature", "--base", "main", "--apply", "--policy", ".radar/landing-policy.json"}, &out, &errs)
	if code != 1 || !strings.Contains(out.String(), "Integration ref not published:") || !strings.Contains(out.String(), "Completed candidate object import: false") {
		t.Fatalf("refusal hidden: code=%d output=%s errors=%s", code, out.String(), errs.String())
	}
}

func TestConflictBriefReproductionReturnsBrief(t *testing.T) {
	root, agent := checkoutRepo(t)
	agent("a", map[string]string{"backend.py": "def price():\n    return 2\n"})
	agent("b", map[string]string{"backend.py": "def price():\n    return 3\n"})
	result := invoke(t, root, 1, "land", "a", "b", "--base", "main", "--conflict-brief")
	brief := result["conflict_brief"].(map[string]any)
	var args []string
	for _, arg := range brief["reproduction"].([]any) {
		args = append(args, arg.(string))
	}
	if len(args) == 0 || args[0] != "radar" {
		t.Fatalf("invalid reproduction: %v", args)
	}
	replayed := invoke(t, root, 1, args[1:]...)
	if replayed["conflict_brief"] == nil {
		t.Fatalf("reproduction returned no brief: %v", replayed)
	}
	files := replayed["conflict_brief"].(map[string]any)["files"].([]any)
	if len(files) != 1 || files[0].(map[string]any)["path"] != "backend.py" {
		t.Fatalf("reproduction changed brief: %v", replayed)
	}
}
