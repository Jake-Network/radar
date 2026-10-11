// Package onboarding installs project-local agent integrations without replacing
// unrelated configuration. All proposed changes are validated before writes.
package onboarding

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"unicode"

	gitrepo "github.com/Jake-Network/radar/internal/git"
	"github.com/Jake-Network/radar/internal/project"
)

//go:embed assets/*
var assets embed.FS

type Options struct {
	Agent          string
	Base, Branches string
	DryRun, Hook   bool
}
type Change struct {
	Path   string `json:"path"`
	Action string `json:"action"`
}
type Report struct {
	Root        string          `json:"root"`
	DryRun      bool            `json:"dry_run"`
	Changes     []Change        `json:"changes"`
	Tools       map[string]bool `json:"tools"`
	Diagnostics []string        `json:"diagnostics"`
}
type edit struct {
	path          string
	before, after []byte
	mode          os.FileMode
}

// Setup detects a containing Git checkout even before its first commit. Outside
// Git it uses the selected directory, allowing analysis-only projects.
func Setup(ctx context.Context, selected string, o Options) (Report, error) {
	r := Report{DryRun: o.DryRun, Changes: []Change{}, Tools: map[string]bool{}, Diagnostics: []string{}}
	if o.Agent != "codex" && o.Agent != "claude" && o.Agent != "both" {
		return r, fmt.Errorf("--agent must be codex, claude or both")
	}
	for _, value := range []string{o.Base, o.Branches} {
		if strings.ContainsFunc(value, unicode.IsControl) {
			return r, fmt.Errorf("hook base and branches must not contain control characters")
		}
	}
	if !o.Hook && (o.Base != "" || o.Branches != "") {
		return r, fmt.Errorf("--base and --branches require hooks; remove --hook=false")
	}
	root, err := project.Root(selected)
	if err != nil {
		return r, err
	}
	// Inspect sets Root before resolving HEAD, so an unborn but valid Git
	// repository still has a root. A stray .git directory is not a checkout.
	info, _ := gitrepo.Inspect(ctx, root)
	if info.Root != "" {
		root, err = project.Root(info.Root)
		if err != nil {
			return r, err
		}
	} else {
		r.Diagnostics = append(r.Diagnostics, "No Git checkout detected; checkpoint and merge analysis require Git history.")
	}
	r.Root = root
	for _, name := range []string{"radar", "git", "codex", "claude", "bash", "node", "python3", "go", "cargo", "java", "cmake"} {
		_, e := exec.LookPath(name)
		r.Tools[name] = e == nil
	}
	if !r.Tools["radar"] {
		r.Diagnostics = append(r.Diagnostics, "Put the radar binary on PATH before starting agent MCP servers or hooks.")
	}
	r.Diagnostics = append(r.Diagnostics, "Codex project MCP configuration requires a trusted project; Claude may prompt to approve the project MCP server. Restart the agent to load skills, MCP, and hooks.")
	edits := []edit{}
	add := func(path string, transform func([]byte) ([]byte, error), mode os.FileMode) error {
		full, e := project.SafePath(root, path)
		if e != nil {
			return e
		}
		before, e := os.ReadFile(full)
		if e != nil && !os.IsNotExist(e) {
			return e
		}
		if st, e := os.Stat(full); e == nil {
			mode = st.Mode().Perm()
		}
		after, e := transform(before)
		if e != nil {
			return fmt.Errorf("%s: %w", path, e)
		}
		action := "unchanged"
		if !bytes.Equal(before, after) {
			action = "update"
			if before == nil {
				action = "create"
			}
			edits = append(edits, edit{path, before, after, mode})
		}
		r.Changes = append(r.Changes, Change{path, action})
		return nil
	}
	config := func(b []byte) ([]byte, error) {
		if b != nil {
			_, e := project.Read(root)
			return b, e
		}
		return []byte("{\n  \"version\": 1,\n  \"no_telemetry\": true\n}\n"), nil
	}
	if err = add(".radar/config.json", config, 0600); err != nil {
		return r, err
	}
	install := func(path, asset string, mode os.FileMode) error {
		data, _ := assets.ReadFile("assets/" + asset)
		return add(path, func(old []byte) ([]byte, error) {
			if old != nil && !bytes.Equal(old, data) {
				return nil, fmt.Errorf("existing integration differs; inspect and merge it manually (not overwritten)")
			}
			return data, nil
		}, mode)
	}
	if o.Agent == "codex" || o.Agent == "both" {
		if err = install(".agents/skills/radar-architecture/SKILL.md", "codex.md", 0600); err != nil {
			return r, err
		}
		if err = add(".codex/config.toml", codexMCP, 0600); err != nil {
			return r, err
		}
		if o.Hook {
			if err = add(".codex/hooks.json", func(old []byte) ([]byte, error) { return agentHookCommand(old, "codex", hookCommand("codex", o)) }, 0600); err != nil {
				return r, err
			}
			r.Diagnostics = append(r.Diagnostics, "In Codex, open /hooks and trust the exact Radar hook definition before it can run; project trust alone does not approve hooks.")
		}
	}
	if o.Agent == "claude" || o.Agent == "both" {
		if err = install(".claude/skills/radar/SKILL.md", "claude.md", 0600); err != nil {
			return r, err
		}
		if err = add(".mcp.json", claudeMCP, 0600); err != nil {
			return r, err
		}
		if o.Hook {
			if err = add(".claude/settings.json", func(old []byte) ([]byte, error) { return agentHookCommand(old, "claude", hookCommand("claude", o)) }, 0600); err != nil {
				return r, err
			}
		}
	}
	if o.Hook {
		r.Diagnostics = append(r.Diagnostics, "Stop hooks run bounded static checks of the current branch and saved staged, unstaged, and untracked changes by default; --branches selects an explicit branch combination. They do not execute project tests. Unsaved editor buffers are excluded. Existing legacy plan hooks are preserved and run alongside the new hook; review them separately if you want to remove legacy plan verification.")
	}
	if o.DryRun {
		return r, nil
	}
	for _, change := range edits {
		if err = ctx.Err(); err != nil {
			return r, err
		}
		full, e := project.SafePath(root, change.path)
		if e != nil {
			return r, e
		}
		current, e := os.ReadFile(full)
		if e != nil && !os.IsNotExist(e) {
			return r, e
		}
		if !bytes.Equal(current, change.before) {
			return r, fmt.Errorf("%s changed during setup; retry after reviewing it", change.path)
		}
		if e = os.MkdirAll(filepath.Dir(full), 0700); e != nil {
			return r, e
		}
		f, e := os.CreateTemp(filepath.Dir(full), ".radar-setup-*")
		if e != nil {
			return r, e
		}
		name := f.Name()
		e = f.Chmod(change.mode)
		if e == nil {
			_, e = f.Write(change.after)
		}
		closeErr := f.Close()
		if e == nil {
			e = closeErr
		}
		if e == nil {
			e = os.Rename(name, full)
		}
		if e != nil {
			os.Remove(name)
			return r, e
		}
	}
	return r, nil
}

const codexBlock = "[mcp_servers.radar]\ncommand = \"radar\"\nargs = [\"mcp\", \"--root\", \".\"]\n"

var radarTable = regexp.MustCompile(`(?m)^\s*\[\s*mcp_servers\s*\.\s*(?:radar|"radar"|'radar')(?:\s*\.|\s*\])`)
var mcpParent = regexp.MustCompile(`^\[\s*mcp_servers\s*\]\s*(?:#.*)?$`)
var quotedTopLevel = regexp.MustCompile(`^(?:\[\s*|)["']`)

func codexMCP(old []byte) ([]byte, error) {
	// Quoted top-level names can also encode mcp_servers through TOML escapes.
	// Without a TOML parser we cannot safely distinguish those declarations;
	// preserve their bytes and request a manual merge before appending a table.
	for _, line := range strings.Split(string(old), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#") {
			continue
		}
		if quotedTopLevel.MatchString(line) || (strings.Contains(line, "mcp_servers") && strings.ContainsAny(line, "\"'")) {
			return nil, fmt.Errorf("quoted MCP or top-level TOML declaration requires manual merging")
		}
	}
	if radarTable.Match(old) {
		// Only our exact section is accepted. Refuse edits or duplicate tables.
		s := string(old)
		matches := radarTable.FindAllIndex(old, -1)
		if len(matches) != 1 {
			return nil, fmt.Errorf("existing Radar MCP configuration differs; merge manually")
		}
		i := matches[0][0]
		for i < len(s) && (s[i] == '\n' || s[i] == '\r' || s[i] == ' ' || s[i] == '\t') {
			i++
		}
		if !strings.HasPrefix(s[i:], codexBlock) {
			return nil, fmt.Errorf("existing Radar MCP configuration differs; merge manually")
		}
		end := i + len(codexBlock)
		tail := strings.TrimSpace(s[end:])
		if tail != "" && !strings.HasPrefix(tail, "[") {
			return nil, fmt.Errorf("existing Radar MCP section has extra settings; merge manually")
		}
		return old, nil
	}
	// Inline/quoted TOML forms are preserved rather than guessed at.
	inParent := false
	for _, line := range strings.Split(string(old), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") {
			inParent = mcpParent.MatchString(line)
		}
		if inParent && strings.HasPrefix(line, "radar") && strings.Contains(line, "=") {
			return nil, fmt.Errorf("unrecognized Radar MCP declaration; merge manually")
		}
		if strings.HasPrefix(line, "mcp_servers") && strings.Contains(line, "=") {
			return nil, fmt.Errorf("inline MCP configuration requires manual merging")
		}
		if !strings.HasPrefix(line, "#") && strings.Contains(line, "mcp_servers") && strings.Contains(line, "radar") {
			return nil, fmt.Errorf("unrecognized Radar MCP declaration; merge manually")
		}
	}
	return append(append(append([]byte{}, old...), '\n'), []byte(codexBlock)...), nil
}

// Reject duplicate object keys before decoding configuration into maps, where
// a duplicate could otherwise hide a user-owned entry that setup overwrites.
func uniqueJSONKeys(data []byte) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	var value func() error
	value = func() error {
		token, err := d.Token()
		if err != nil {
			return err
		}
		switch token {
		case json.Delim('{'):
			keys := map[string]bool{}
			for d.More() {
				key, err := d.Token()
				if err != nil {
					return err
				}
				name, ok := key.(string)
				if !ok || keys[name] {
					return fmt.Errorf("configuration contains duplicate object key %q", name)
				}
				keys[name] = true
				if err := value(); err != nil {
					return err
				}
			}
			_, err = d.Token()
		case json.Delim('['):
			for d.More() {
				if err := value(); err != nil {
					return err
				}
			}
			_, err = d.Token()
		}
		return err
	}
	return value()
}

func jsonObject(old []byte) (map[string]any, error) {
	m := map[string]any{}
	if old != nil {
		if err := uniqueJSONKeys(old); err != nil {
			return nil, err
		}
		d := json.NewDecoder(bytes.NewReader(old))
		d.UseNumber()
		if e := d.Decode(&m); e != nil {
			return nil, e
		}
		if e := d.Decode(new(any)); e != io.EOF {
			return nil, fmt.Errorf("configuration contains trailing JSON")
		}
		if m == nil {
			return nil, fmt.Errorf("configuration must be an object")
		}
	}
	return m, nil
}
func objectAt(m map[string]any, key string) (map[string]any, error) {
	v, ok := m[key]
	if !ok {
		n := map[string]any{}
		m[key] = n
		return n, nil
	}
	n, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s must be an object", key)
	}
	return n, nil
}
func encode(m map[string]any) ([]byte, error) {
	b, e := json.MarshalIndent(m, "", "  ")
	return append(b, '\n'), e
}
func claudeMCP(old []byte) ([]byte, error) {
	m, e := jsonObject(old)
	if e != nil {
		return nil, e
	}
	servers, e := objectAt(m, "mcpServers")
	if e != nil {
		return nil, e
	}
	want := map[string]any{"command": "radar", "args": []any{"mcp", "--root", "."}}
	if v, ok := servers["radar"]; ok {
		if !reflect.DeepEqual(v, want) {
			return nil, fmt.Errorf("existing Radar MCP configuration differs; merge manually")
		}
		return old, nil
	}
	servers["radar"] = want
	return encode(m)
}

var radarHookCommand = regexp.MustCompile(`(?:^|[\s/\\"'])radar(?:\.exe)?["']?\s+hook(?:\s|$)`)

func claudeHook(old []byte) ([]byte, error) { return agentHook(old, "claude") }
func codexHook(old []byte) ([]byte, error)  { return agentHook(old, "codex") }

func hookCommand(agent string, o Options) string {
	command := "radar hook stop --agent " + agent
	for _, arg := range []struct{ flag, value string }{{"--base", o.Base}, {"--branches", o.Branches}} {
		if arg.value != "" {
			// Native agent command hooks invoke a shell. Always quote user values,
			// including single quotes, so refs cannot become shell instructions.
			command += " " + arg.flag + " '" + strings.ReplaceAll(arg.value, "'", "'\"'\"'") + "'"
		}
	}
	return command
}

func agentHook(old []byte, agent string) ([]byte, error) {
	return agentHookCommand(old, agent, hookCommand(agent, Options{}))
}

// agentHookCommand preserves unrelated entries and exact legacy plan hooks.
// Modified or duplicate Radar hooks require deliberate manual merging.
func agentHookCommand(old []byte, agent, commandText string) ([]byte, error) {
	m, e := jsonObject(old)
	if e != nil {
		return nil, e
	}
	hooks, e := objectAt(m, "hooks")
	if e != nil {
		return nil, e
	}
	stops := []any{}
	if v, ok := hooks["Stop"]; ok {
		var valid bool
		stops, valid = v.([]any)
		if !valid {
			return nil, fmt.Errorf("hooks.Stop must be an array")
		}
	}
	want := map[string]any{"hooks": []any{map[string]any{"type": "command", "command": commandText, "timeout": json.Number("30")}}}
	found, legacy := 0, 0
	for _, v := range stops {
		entry, ok := v.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("hooks.Stop entries must be objects")
		}
		commands, ok := entry["hooks"].([]any)
		if !ok {
			return nil, fmt.Errorf("hooks.Stop entry hooks must be an array")
		}
		for _, raw := range commands {
			command, ok := raw.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("hooks.Stop hooks must be objects")
			}
			kind, ok := command["type"].(string)
			if !ok || kind == "" {
				return nil, fmt.Errorf("hooks.Stop hook type must be a nonempty string")
			}
			if kind != "command" {
				continue
			}
			text, ok := command["command"].(string)
			if !ok || strings.TrimSpace(text) == "" {
				return nil, fmt.Errorf("hooks.Stop command must be a nonempty string")
			}
			if strings.Contains(text, "radar-verify-stop") {
				legacy++
				oldCommand := `bash "$CLAUDE_PROJECT_DIR"/.claude/hooks/radar-verify-stop.sh`
				oldDirect := `"$CLAUDE_PROJECT_DIR"/.claude/hooks/radar-verify-stop.sh`
				if agent != "claude" || (text != oldCommand && text != oldDirect) || len(command) != 3 || command["timeout"] != json.Number("120") {
					return nil, fmt.Errorf("existing legacy Radar Stop hook differs; merge manually")
				}
				continue
			}
			if radarHookCommand.MatchString(text) {
				if !reflect.DeepEqual(v, want) {
					return nil, fmt.Errorf("existing Radar Stop hook differs; merge manually")
				}
				found++
			}
		}
	}
	if found > 1 || legacy > 1 {
		return nil, fmt.Errorf("duplicate Radar Stop hooks; merge manually")
	}
	if found == 1 {
		return old, nil
	}
	hooks["Stop"] = append(stops, want)
	return encode(m)
}
