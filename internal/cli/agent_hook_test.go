package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func hookInvoke(t *testing.T, root, input string, flags ...string) agentHookResponse {
	t.Helper()
	var out bytes.Buffer
	a := &app{ctx: context.Background(), root: root, out: &out}
	if code := a.runAgentHook(append([]string{"stop", "--agent", "claude"}, flags...), strings.NewReader(input)); code != 0 {
		t.Fatalf("hook exit %d: %s", code, out.String())
	}
	var result agentHookResponse
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(err, out.String())
	}
	return result
}

func hookEvent(t *testing.T, cwd string) string {
	t.Helper()
	b, err := json.Marshal(agentHookEvent{CWD: cwd, Name: "Stop"})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestAgentHookSavedDirtyContractFailureWithoutMutation(t *testing.T) {
	root, base := obligationRepo(t)
	put(t, root, "schema.json", removedTotal)
	gitTest(t, root, "add", "schema.json")
	// Final saved content, rather than the staged intermediate content, is checked.
	put(t, root, "schema.json", `{"type":"object","properties":{"name":{"type":"integer"}}}`)
	before := watchSourceFingerprint(t, root)
	result := hookInvoke(t, root, hookEvent(t, filepath.Join(root, ".radar")), "--base", base)
	if result.Decision != "block" || !strings.Contains(result.Reason, "contract") {
		t.Fatalf("missing dirty contract failure: %+v", result)
	}
	if got := watchSourceFingerprint(t, root); got != before {
		t.Fatal("hook mutated source Git state")
	}
	// Restoring saved compatibility must stop blocking despite the failing staged version.
	put(t, root, "schema.json", `{"type":"object","properties":{"total":{"type":"integer"},"name":{"type":"string"}}}`)
	result = hookInvoke(t, root, hookEvent(t, root), "--base", base)
	if result.Decision != "" {
		t.Fatalf("staged content overrode saved content: %+v", result)
	}
}

func TestAgentHookExplicitCombinedConflictAndDefaultScope(t *testing.T) {
	root, agent := checkoutRepo(t)
	a := agent("a", map[string]string{"backend.py": "def price():\n    return 2\n"})
	agent("b", map[string]string{"backend.py": "def price():\n    return 3\n"})
	if r := hookInvoke(t, root, hookEvent(t, a), "--base", "main"); r.Decision != "" {
		t.Fatalf("default selected unrelated branches: %+v", r)
	}
	before := watchSourceFingerprint(t, root)
	r := hookInvoke(t, root, hookEvent(t, root), "--base", "main", "--branches", "a,b")
	if r.Decision != "" || !strings.Contains(r.Message, "backend.py") || !strings.Contains(r.Message, "restack") {
		t.Fatalf("committed conflict should warn: %+v", r)
	}
	if watchSourceFingerprint(t, root) != before {
		t.Fatal("combined hook mutated source")
	}
}

func TestAgentHookProtocolUnavailableAndRecursion(t *testing.T) {
	for _, input := range []string{"", "[]", "null", "{}{}", `{"cwd":9}`, strings.Repeat("x", 65537), `{"hook_event_name":"PostToolUse"}`} {
		r := hookInvoke(t, t.TempDir(), input)
		if r.Decision != "" || !strings.Contains(r.Message, "NOT VERIFIED") {
			t.Fatalf("invalid event fabricated result: %+v", r)
		}
	}
	if r := hookInvoke(t, t.TempDir(), `{"stop_hook_active":true}`); r != (agentHookResponse{}) {
		t.Fatal("recursive hook repeated", r)
	}
	root, _ := checkoutRepo(t)
	if r := hookInvoke(t, root, hookEvent(t, root), "--base", "main"); !strings.Contains(r.Message, "no selected changes") {
		t.Fatal("no changes claimed verification", r)
	}
	if r := hookInvoke(t, root, hookEvent(t, root), "--base", "main", "--timeout", "1ns"); r.Decision != "" || !strings.Contains(r.Message, "NOT VERIFIED") {
		t.Fatal("deadline claimed verification", r)
	}
	gitTest(t, root, "checkout", "--detach", "-q")
	put(t, root, "backend.py", "def price():\n    return 4\n")
	if r := hookInvoke(t, root, hookEvent(t, root), "--base", "main", "--agent", "codex"); r.Decision != "" || !strings.Contains(r.Message, "static preview complete") {
		t.Fatal("detached dirty preview unavailable", r)
	}
}

func TestAgentHookDoesNotExecuteRepositoryCode(t *testing.T) {
	root, _ := checkoutRepo(t)
	marker := filepath.Join(t.TempDir(), "executed")
	put(t, root, "test_checkout.py", "from pathlib import Path\nPath("+fmtPythonString(marker)+").write_text('executed')\n")
	put(t, root, "package.json", `{"scripts":{"test":"touch `+marker+`"}}`)
	gitTest(t, root, "config", "core.hooksPath", filepath.Join(root, "hooks"))
	put(t, root, "hooks/pre-commit", "#!/bin/sh\ntouch '"+marker+"'\n")
	if err := os.Chmod(filepath.Join(root, "hooks/pre-commit"), 0755); err != nil {
		t.Fatal(err)
	}
	r := hookInvoke(t, root, hookEvent(t, root), "--base", "main")
	if r.Decision != "" {
		t.Fatal(r)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("repository code or Git hook executed", err)
	}
}

func fmtPythonString(s string) string { b, _ := json.Marshal(s); return string(b) }

func TestAgentHookInputDeadline(t *testing.T) {
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	var out bytes.Buffer
	a := &app{ctx: context.Background(), root: t.TempDir(), out: &out}
	start := time.Now()
	if code := a.runAgentHook([]string{"stop", "--agent", "claude", "--timeout", "10ms"}, reader); code != 0 || !strings.Contains(out.String(), "NOT VERIFIED") {
		t.Fatal(code, out.String())
	}
	if time.Since(start) > time.Second {
		t.Fatal("hook waited beyond stdin deadline")
	}
}

func TestAgentHookUnbornAndUnsupportedDirtyState(t *testing.T) {
	unborn := gitRepo(t)
	if r := hookInvoke(t, unborn, hookEvent(t, unborn), "--base", "main"); r.Decision != "" || !strings.Contains(r.Message, "NOT VERIFIED") {
		t.Fatal("unborn repository claimed verification", r)
	}
	root, _ := checkoutRepo(t)
	if err := os.Symlink("backend.py", filepath.Join(root, "linked.py")); err != nil {
		t.Skip(err)
	}
	if r := hookInvoke(t, root, hookEvent(t, root), "--base", "main"); r.Decision != "" || !strings.Contains(r.Message, "NOT VERIFIED") {
		t.Fatal("unsupported dirty symlink claimed verification", r)
	}
}

func TestAgentHookActualDispatchRootOverride(t *testing.T) {
	root, _ := checkoutRepo(t)
	put(t, root, "backend.py", "def price():\n    return 4\n")
	file, err := os.CreateTemp(t.TempDir(), "event")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if _, err := file.WriteString(hookEvent(t, t.TempDir())); err != nil {
		t.Fatal(err)
	}
	if _, err := file.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	before := os.Stdin
	os.Stdin = file
	defer func() { os.Stdin = before }()
	var out, errs bytes.Buffer
	code := Run(context.Background(), []string{"--root", root, "hook", "stop", "--agent", "codex", "--base", "main"}, &out, &errs)
	if code != 0 || !strings.Contains(out.String(), "static preview complete") {
		t.Fatal(code, out.String(), errs.String())
	}
}

func TestAgentHookBlocksCombinedMigrationHeads(t *testing.T) {
	root, agent := checkoutRepo(t)
	shared := map[string]string{"db/env.py": "print('env')\n", "db/versions/r0.py": "revision = 'r0'\ndown_revision = None\n"}
	for name, revision := range map[string]string{"a": "a1", "b": "b1"} {
		files := map[string]string{"db/versions/" + revision + ".py": "revision = '" + revision + "'\ndown_revision = 'r0'\n"}
		for p, c := range shared {
			files[p] = c
		}
		agent(name, files)
	}
	if r := hookInvoke(t, root, hookEvent(t, root), "--base", "main", "--branches", "a"); r.Decision != "" {
		t.Fatalf("one branch alone has one head: %+v", r)
	}
	r := hookInvoke(t, root, hookEvent(t, root), "--base", "main", "--branches", "a,b")
	if r.Decision != "block" || !strings.Contains(r.Reason, "migration_multiple_heads") {
		t.Fatalf("missing combined migration failure: %+v", r)
	}
}

// An unreadable migration history is a static gap: the next step names it
// instead of suggesting --run, and the hook names the blocking check.
func TestUnknownMigrationOrderNamesTheGap(t *testing.T) {
	root, agent := checkoutRepo(t)
	agent("a", map[string]string{"db/env.py": "print('env')\n", "db/versions/r0.py": "revision = 'r0'\ndown_revision = None\n", "db/versions/a1.py": "revision = 'a1'\ndown_revision = 'r' + '0'\n"})
	code, out, _ := run(t, root, "gate", "a")
	if !strings.Contains(out, "NOT VERIFIED") || !strings.Contains(out, "Next: Migrations could not be checked") || strings.Contains(out, "Next: radar gate --run") {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	if _, out, _ = run(t, root, "gate", "a", "--run"); !strings.Contains(out, "Next: Migrations could not be checked") {
		t.Fatalf("--run next step must name the migration gap:\n%s", out)
	}
	r := hookInvoke(t, root, hookEvent(t, root), "--base", "main", "--branches", "a")
	if r.Decision != "" || !strings.Contains(r.Message, "required check migration_order is unknown") {
		t.Fatalf("hook %+v", r)
	}
}

// A policy that requires test evidence is not a static gap: the next step
// still runs the tests.
func TestStaticGateWithTestPolicySuggestsRun(t *testing.T) {
	root, agent := checkoutRepo(t)
	agent("a", map[string]string{"backend.py": "def price():\n    return 2\n"})
	for _, id := range []string{"test:x", "criterion:c1", "integration_tests"} {
		policy := filepath.Join(t.TempDir(), "policy.json")
		if err := os.WriteFile(policy, []byte(`{"version":1,"require":["textual_merge","`+id+`"]}`), 0o600); err != nil {
			t.Fatal(err)
		}
		_, out, _ := run(t, root, "gate", "a", "--policy", policy)
		if !strings.Contains(out, "Next: radar gate --run") {
			t.Fatalf("%s: next step should run tests:\n%s", id, out)
		}
	}
}

func TestAgentHookCacheInvalidation(t *testing.T) {
	root, _ := checkoutRepo(t)
	event := hookEvent(t, root)
	invoke := func() agentHookResponse { return hookInvoke(t, root, event, "--base", "main") }
	first := invoke()
	if strings.Contains(first.Message, "reused") {
		t.Fatal(first)
	}
	second := invoke()
	if !strings.Contains(second.Message, "reused") {
		t.Fatal("unchanged preview not cached", second)
	}
	put(t, root, "backend.py", "def price():\n    return 22\n")
	if r := invoke(); strings.Contains(r.Message, "reused") {
		t.Fatal("dirty bytes did not invalidate", r)
	}
	if r := invoke(); !strings.Contains(r.Message, "reused") {
		t.Fatal("dirty preview not cached", r)
	}
	put(t, root, "backend.py", "def price():\n    return 23\n")
	if r := invoke(); strings.Contains(r.Message, "reused") {
		t.Fatal("dirty edit did not invalidate", r)
	}
	gitTest(t, root, "add", "backend.py")
	gitTest(t, root, "commit", "-qm", "change HEAD")
	if r := invoke(); strings.Contains(r.Message, "reused") {
		t.Fatal("HEAD did not invalidate", r)
	}
	if r := hookInvoke(t, root, event, "--base", "HEAD^"); strings.Contains(r.Message, "reused") {
		t.Fatal("base did not invalidate", r)
	}
	if r := hookInvoke(t, root, event, "--base", "HEAD^", "--branches", "main"); strings.Contains(r.Message, "reused") {
		t.Fatal("selected branch did not invalidate", r)
	}
	savedVersion := Version
	Version = "different-version"
	defer func() { Version = savedVersion }()
	if r := invoke(); strings.Contains(r.Message, "reused") {
		t.Fatal("version did not invalidate", r)
	}
	path, err := hookCachePath(root)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var cached hookCacheRecord
	if err := json.Unmarshal(data, &cached); err != nil {
		t.Fatal(err)
	}
	cached.Response.Message = "unverified forged success"
	data, err = json.Marshal(cached)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if r := invoke(); strings.Contains(r.Message, "reused") {
		t.Fatal("corrupt cache reused", r)
	}
}

func TestAgentHookDirtyIntroducesTextualConflict(t *testing.T) {
	root, agent := checkoutRepo(t)
	a := agent("a", map[string]string{"backend.py": "def price():\n    return 2\n"})
	b := agent("b", map[string]string{"other.py": "def other():\n    return 3\n"})
	put(t, b, "backend.py", "def price():\n    return 3\n")
	r := hookInvoke(t, root, hookEvent(t, a), "--base", "main", "--branches", "a,b")
	if r.Decision != "block" || !strings.Contains(r.Reason, "backend.py") {
		t.Fatalf("new dirty conflict must block: %+v", r)
	}
}

func TestAgentHookDirtyCreatesMigrationHeads(t *testing.T) {
	root, agent := checkoutRepo(t)
	a := agent("a", map[string]string{"db/env.py": "# alembic\n", "db/versions/r0.py": "revision = 'r0'\ndown_revision = None\n", "db/versions/r1.py": "revision = 'r1'\ndown_revision = 'r0'\n"})
	put(t, a, "db/versions/r2.py", "revision = 'r2'\ndown_revision = 'r0'\n")
	r := hookInvoke(t, root, hookEvent(t, a), "--base", "main", "--branches", "a")
	if r.Decision != "block" || !strings.Contains(r.Reason, "migration_multiple_heads") {
		t.Fatal(r)
	}
}
