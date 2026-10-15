package cli

import (
	"strings"
	"testing"
)

func TestAgentHookNewDirtyConflictBeforeLaterInputBlocks(t *testing.T) {
	root, agent := agentRepo(t, map[string]string{"backend.py": "VALUE = 1\n"})
	current := agent("current", nil)
	agent("later", map[string]string{"later.md": "later\n"})
	put(t, root, "backend.py", "VALUE = 2\n")
	gitTest(t, root, "add", "backend.py")
	gitTest(t, root, "commit", "-qm", "base advanced")
	put(t, current, "backend.py", "VALUE = 3\n")
	for i := 0; i < 2; i++ {
		r := hookInvoke(t, root, hookEvent(t, current), "--base", "main", "--branches", "current,later")
		if r.Decision != "block" || !strings.Contains(r.Reason, "backend.py") || !strings.Contains(r.Message, "later inputs remain unchecked") {
			t.Fatalf("run %d: new dirty conflict must block while later inputs remain unchecked: %+v", i, r)
		}
		if i == 1 && !strings.Contains(r.Message, "cache") {
			t.Fatalf("expected cached block on unchanged continuation: %+v", r)
		}
	}
}

func TestAgentHookInheritedConflictDoesNotHideDirtyMigrationFailure(t *testing.T) {
	files := map[string]string{"backend.py": "VALUE = 1\n", "db/env.py": "# alembic\n", "db/versions/r0.py": "revision = 'r0'\ndown_revision = None\n", "db/versions/r1.py": "revision = 'r1'\ndown_revision = 'r0'\n"}
	root, agent := agentRepo(t, files)
	current := agent("current", map[string]string{"backend.py": "VALUE = 2\n"})
	put(t, root, "backend.py", "VALUE = 3\n")
	gitTest(t, root, "add", "backend.py")
	gitTest(t, root, "commit", "-qm", "base advanced")
	put(t, current, "db/versions/r2.py", "revision = 'r2'\ndown_revision = 'r0'\n")
	r := hookInvoke(t, root, hookEvent(t, current), "--base", "main")
	if r.Decision != "block" || !strings.Contains(r.Reason, "migration_multiple_heads") || !strings.Contains(r.Message, "NOT VERIFIED") {
		t.Fatalf("inherited conflict hid independent dirty migration failure: %+v", r)
	}
}

func TestAgentHookInheritedConflictDoesNotHideDirtyContractFailure(t *testing.T) {
	root, agent := agentRepo(t, map[string]string{
		"backend.py":            "VALUE = 1\n",
		"schema.json":           `{"type":"object","properties":{"total":{"type":"integer"},"name":{"type":"string"}}}`,
		"other.json":            `{"type":"object","properties":{"id":{"type":"string"}}}`,
		"api.py":                "def summary(): return {'total': 1}\n",
		"consumer.ts":           "export const use = (x:any) => x.total;\n",
		"other.ts":              "export const id = (x:any) => x.id;\n",
		".radar/contracts.json": obligationManifest,
	})
	current := agent("current", map[string]string{"backend.py": "VALUE = 2\n"})
	put(t, root, "backend.py", "VALUE = 3\n")
	gitTest(t, root, "add", "backend.py")
	gitTest(t, root, "commit", "-qm", "base advanced")
	put(t, current, "schema.json", removedTotal)
	r := hookInvoke(t, root, hookEvent(t, current), "--base", "main")
	if r.Decision != "block" || !strings.Contains(r.Reason, "contract") || !strings.Contains(r.Message, "NOT VERIFIED") {
		t.Fatalf("inherited conflict hid independent dirty contract failure: %+v", r)
	}
}
