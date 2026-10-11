package onboarding

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// realTempDir resolves t.TempDir symlinks (macOS /var is /private/var) so
// paths compare equal to the canonical roots Radar and Git report.
func realTempDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func fixtureRoot(t *testing.T) string {
	t.Helper()
	root := realTempDir(t)
	if b, e := exec.Command("git", "init", "-q", root).CombinedOutput(); e != nil {
		t.Fatalf("git init: %v %s", e, b)
	}
	return root
}

func put(t *testing.T, root, path, body string) {
	t.Helper()
	p := filepath.Join(root, path)
	if e := os.MkdirAll(filepath.Dir(p), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(p, []byte(body), 0600); e != nil {
		t.Fatal(e)
	}
}
func TestSetupBothIdempotentAndPreservesSettings(t *testing.T) {
	r := fixtureRoot(t)
	put(t, r, ".codex/config.toml", "model = \"local\"\n")
	put(t, r, ".mcp.json", `{"other":true,"mcpServers":{"other":{"command":"foo"}}}`)
	put(t, r, ".claude/settings.json", `{"permissions":{"allow":["Read"]}}`)
	if e := os.Mkdir(filepath.Join(r, "sub"), 0700); e != nil {
		t.Fatal(e)
	}
	op := Options{Agent: "both", Hook: true}
	a, e := Setup(context.Background(), filepath.Join(r, "sub"), op)
	if e != nil {
		t.Fatal(e)
	}
	if a.Root != r {
		t.Fatal(a.Root)
	}
	b, e := Setup(context.Background(), r, op)
	if e != nil {
		t.Fatal(e)
	}
	for _, c := range b.Changes {
		if c.Action != "unchanged" {
			t.Fatal(c)
		}
	}
	data, _ := os.ReadFile(filepath.Join(r, ".codex/config.toml"))
	if !strings.Contains(string(data), `model = "local"`) {
		t.Fatal(string(data))
	}
	data, _ = os.ReadFile(filepath.Join(r, ".mcp.json"))
	if !strings.Contains(string(data), `"other"`) {
		t.Fatal(string(data))
	}
}
func TestDryRunAndConflictDoNotWrite(t *testing.T) {
	r := fixtureRoot(t)
	a, e := Setup(context.Background(), r, Options{Agent: "codex", DryRun: true})
	if e != nil || len(a.Changes) != 3 {
		t.Fatalf("%+v %v", a, e)
	}
	if _, e = os.Stat(filepath.Join(r, ".radar")); !os.IsNotExist(e) {
		t.Fatal(e)
	}
	put(t, r, ".mcp.json", `{"mcpServers":{"radar":{"command":"different"}}}`)
	_, e = Setup(context.Background(), r, Options{Agent: "both"})
	if e == nil {
		t.Fatal("conflict accepted")
	}
	if _, e = os.Stat(filepath.Join(r, ".radar")); !os.IsNotExist(e) {
		t.Fatal("partial write", e)
	}
}
func TestRejectSymlinkAndModifiedSkill(t *testing.T) {
	r := fixtureRoot(t)
	if e := os.Symlink(realTempDir(t), filepath.Join(r, ".agents")); e != nil {
		t.Skip(e)
	}
	if _, e := Setup(context.Background(), r, Options{Agent: "codex"}); e == nil {
		t.Fatal("symlink accepted")
	}
	r = fixtureRoot(t)
	put(t, r, ".agents/skills/radar-architecture/SKILL.md", "custom")
	if _, e := Setup(context.Background(), r, Options{Agent: "codex"}); e == nil {
		t.Fatal("overwrote custom skill")
	}
}
func TestMCPAndHookConflictMerging(t *testing.T) {
	if _, e := codexMCP([]byte("[mcp_servers.\"radar\"]\ncommand = \"evil\"\n")); e == nil {
		t.Fatal("quoted table accepted")
	}
	if _, e := codexMCP([]byte(codexBlock + "enabled = false\n")); e == nil {
		t.Fatal("extra settings accepted")
	}
	if _, e := codexMCP([]byte("[mcp_servers]\nradar = { command = \"custom\" }\n")); e == nil {
		t.Fatal("parent table inline entry accepted")
	}
	bignum, e := claudeMCP([]byte(`{"setting":9007199254740993}`))
	if e != nil || !strings.Contains(string(bignum), "9007199254740993") {
		t.Fatal("lost number precision", e, string(bignum))
	}
	old := []byte(`{"permissions":{"allow":["Read"]},"hooks":{"Stop":[{"hooks":[{"type":"command","command":"echo ok"}]}]}}`)
	b, e := claudeHook(old)
	if e != nil {
		t.Fatal(e)
	}
	again, e := claudeHook(b)
	if e != nil || string(again) != string(b) {
		t.Fatal("hook not idempotent", e)
	}
	if !strings.Contains(string(b), "echo ok") || !strings.Contains(string(b), "permissions") {
		t.Fatal(string(b))
	}
}
func TestInvalidInputs(t *testing.T) {
	for _, o := range []Options{{Agent: "unknown"}, {Agent: "codex", Hook: true, Base: "main\n"}, {Agent: "codex", Base: "main"}} {
		if _, e := Setup(context.Background(), realTempDir(t), o); e == nil {
			t.Fatal(o)
		}
	}
}

func TestMalformedAncestorGitMarkerDoesNotRedirectSetup(t *testing.T) {
	parent := realTempDir(t)
	if e := os.Mkdir(filepath.Join(parent, ".git"), 0700); e != nil {
		t.Fatal(e)
	}
	selected := filepath.Join(parent, "project")
	if e := os.Mkdir(selected, 0700); e != nil {
		t.Fatal(e)
	}
	r, e := Setup(context.Background(), selected, Options{Agent: "codex", DryRun: true})
	if e != nil {
		t.Fatal(e)
	}
	if r.Root != selected {
		t.Fatalf("malformed ancestor redirected setup: %s", r.Root)
	}
}

func TestQuotedCodexMCPDeclarationsPreserved(t *testing.T) {
	for _, config := range []string{
		"[\"mcp_servers\"]\n\"radar\" = { command = \"existing-agent-server\", args = [] }\n",
		"['mcp_servers']\n'radar' = { command = 'existing-agent-server', args = [] }\n",
		"[mcp_servers]\n\"radar\" = { command = \"existing\" }\n",
		"[mcp_servers] # existing configuration\n'radar' = { command = 'existing' }\n",
		"\"mcp_servers\" = { radar = { command = \"existing\" } }\n",
		"'mcp_servers' = { radar = { command = 'existing' } }\n",
		"[\"\\u006dcp_servers\"]\nradar = { command = \"existing\" }\n",
	} {
		t.Run(config, func(t *testing.T) {
			root := fixtureRoot(t)
			put(t, root, ".codex/config.toml", config)
			if _, e := Setup(context.Background(), root, Options{Agent: "codex"}); e == nil {
				t.Fatal("quoted MCP declaration accepted")
			}
			data, e := os.ReadFile(filepath.Join(root, ".codex/config.toml"))
			if e != nil || string(data) != config {
				t.Fatalf("configuration changed: %s %v", data, e)
			}
			if _, e := os.Stat(filepath.Join(root, ".radar")); !os.IsNotExist(e) {
				t.Fatal("wrote partial setup", e)
			}
		})
	}
}

func TestNativeHooksPreserveLegacyAndUnrelatedConfiguration(t *testing.T) {
	root := fixtureRoot(t)
	legacy := `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"bash \"$CLAUDE_PROJECT_DIR\"/.claude/hooks/radar-verify-stop.sh","timeout":120}]},{"hooks":[{"type":"command","command":"echo radar docs"}]}]},"permissions":{"allow":["Read"]}}`
	put(t, root, ".claude/settings.json", legacy)
	put(t, root, ".codex/hooks.json", `{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"echo hello"}]}]}}`)
	op := Options{Agent: "both", Hook: true, Base: "main", Branches: "agent/a,agent/b"}
	if _, err := Setup(context.Background(), root, op); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{".claude/settings.json", ".codex/hooks.json"} {
		data, err := os.ReadFile(filepath.Join(root, path))
		if err != nil || !strings.Contains(string(data), "radar hook stop --agent") || !strings.Contains(string(data), "--base 'main' --branches 'agent/a,agent/b'") {
			t.Fatalf("%s: %s %v", path, data, err)
		}
		if path == ".claude/settings.json" && (!strings.Contains(string(data), "radar-verify-stop.sh") || !strings.Contains(string(data), "echo radar docs") || !strings.Contains(string(data), "permissions")) {
			t.Fatalf("lost legacy/unrelated settings: %s", data)
		}
		if path == ".codex/hooks.json" && !strings.Contains(string(data), "SessionStart") {
			t.Fatalf("lost unrelated hook: %s", data)
		}
	}
	if _, err := os.Stat(filepath.Join(root, ".claude/hooks/radar-verify-stop.sh")); !os.IsNotExist(err) {
		t.Fatalf("new install unexpectedly creates bash wrapper: %v", err)
	}
	r, err := Setup(context.Background(), root, op)
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range r.Changes {
		if change.Action != "unchanged" {
			t.Fatal(change)
		}
	}
}

func TestNativeHookConflictValidationBeforeWrites(t *testing.T) {
	for _, config := range []string{
		`{"hooks":{"Stop":{}}}`,
		`{"hooks":{"Stop":[null]}}`,
		`{"hooks":{"Stop":[{"hooks":{}}]}}`,
		`{"hooks":{"Stop":[{"hooks":[{"type":"command","command":7}]}]}}`,
		`{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"radar hook stop --agent codex --timeout 2s","timeout":30}]}]}}`,
		`{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"radar hook stop --agent codex","timeout":30}]},{"hooks":[{"type":"command","command":"radar hook stop --agent codex","timeout":30}]}]}}`,
		`{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"custom radar-verify-stop.sh","timeout":120}]}]}}`,
		`{"hooks":null}`,
		`{} {}`,
		`{"hooks":{"Stop":[]},"hooks":{"Stop":[]}}`,
		`{"hooks":{"Stop":[],"Stop":[]}}`,
	} {
		t.Run(config, func(t *testing.T) {
			root := fixtureRoot(t)
			put(t, root, ".codex/hooks.json", config)
			if _, err := Setup(context.Background(), root, Options{Agent: "both", Hook: true}); err == nil {
				t.Fatal("invalid or conflicting hook accepted")
			}
			if _, err := os.Stat(filepath.Join(root, ".radar")); !os.IsNotExist(err) {
				t.Fatalf("partial writes: %v", err)
			}
			data, _ := os.ReadFile(filepath.Join(root, ".codex/hooks.json"))
			if string(data) != config {
				t.Fatal("conflict overwritten")
			}
		})
	}
}

func TestHookPathsRejectSymlinks(t *testing.T) {
	for _, agent := range []string{"claude", "codex"} {
		root := fixtureRoot(t)
		put(t, root, "."+agent+"/placeholder", "")
		path := ".claude/settings.json"
		if agent == "codex" {
			path = ".codex/hooks.json"
		}
		target := filepath.Join(realTempDir(t), "target.json")
		if err := os.WriteFile(target, []byte("{}"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, filepath.Join(root, path)); err != nil {
			t.Skip(err)
		}
		if _, err := Setup(context.Background(), root, Options{Agent: agent, Hook: true}); err == nil {
			t.Fatal("symlink hook configuration accepted")
		}
		if _, err := os.Stat(filepath.Join(root, ".radar")); !os.IsNotExist(err) {
			t.Fatal("partial writes", err)
		}
	}
}

func TestHookScopeShellQuoting(t *testing.T) {
	value := "main'; printf unsafe; #"
	command := hookCommand("claude", Options{Base: value})
	// Exercise the actual shell parsing path with a stand-in radar executable.
	root := realTempDir(t)
	put(t, root, "radar", "#!/bin/sh\nprintf '%s\\n' \"$@\"\n")
	if err := os.Chmod(filepath.Join(root, "radar"), 0700); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", "-c", command)
	cmd.Env = append(os.Environ(), "PATH="+root)
	data, err := cmd.CombinedOutput()
	if err != nil || string(data) != "hook\nstop\n--agent\nclaude\n--base\n"+value+"\n" {
		t.Fatalf("unsafe shell parsing: %s %v", data, err)
	}
}
