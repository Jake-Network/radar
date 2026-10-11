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
	if r.Decision != "block" || !strings.Contains(r.Reason, "backend.py") {
		t.Fatalf("missing combined conflict: %+v", r)
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
