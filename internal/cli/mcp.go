package cli

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"strconv"
	"strings"
	"sync"
)

// MCP exposes read-mostly Radar commands as tools for coding agents over
// newline-delimited JSON-RPC on stdin/stdout. `test` (runs repository code)
// and `approve` (declares a human review) are deliberately not exposed.

// maxToolOutput keeps tool results within typical agent context limits.
const maxToolOutput = 40000

var mcpProtocolVersions = []string{"2025-06-18", "2025-03-26", "2024-11-05"}

type toolParam struct {
	name, flag, kind, description string
	required                      bool
}

type toolSpec struct {
	name, command, description string
	params                     []toolParam
}

func p(name, flag, kind, description string, required bool) toolParam {
	return toolParam{name: name, flag: flag, kind: kind, description: description, required: required}
}

var mcpTools = []toolSpec{
	{"radar_check", "check", "Analyze changed files, dependency impact, contracts and optional plan. Missing coverage is not a pass; does not execute repository commands.", []toolParam{p("base", "base", "string", "baseline ref", true), p("head", "head", "string", "head revision (default working tree)", false), p("plan", "plan", "string", "optional plan path", false), p("require_complete", "require-complete", "boolean", "legacy whole-analysis strictness", false), p("policy", "policy", "string", "optional required-check policy path", false), p("suggest_tests", "suggest-tests", "boolean", "recommend tests without execution", false), p("detail", "__detail", "boolean", "include full report instead of bounded summary", false)}},
	{"radar_merge_check", "merge-check", "Preview the combined branches in temporary Git state without executing repository commands; individual branch evidence is not integration proof.", []toolParam{p("base", "base", "string", "baseline ref", true), p("branches", "branches", "string", "comma-separated refs", true), p("plan", "plan", "string", "optional reviewed plan path", false), p("policy", "policy", "string", "optional required-check policy path", false), p("suggest_tests", "suggest-tests", "boolean", "recommend candidate tests without execution", false), p("detail", "__detail", "boolean", "include full report instead of bounded summary", false)}},
	{"radar_contracts_discover", "discover", "Discover proposed contract candidates with static evidence; does not accept or overwrite authoritative bindings.", []toolParam{p("ref", "ref", "string", "checkpoint (default working tree)", false)}},
	{"radar_doctor", "doctor", "Report Radar capabilities, repository identity and state location.", nil},
	{"radar_init", "init", "Create .radar state in the repository (required by legacy stateful tools).", nil},
	{"radar_index", "index", "Index source declarations, inferred file dependencies and explicit contracts; returns counts and diagnostics (query entities with radar_resolve/radar_graph). Omit ref for the working tree.", []toolParam{p("ref", "ref", "string", "Git commit/branch to index", false)}},
	{"radar_resolve", "resolve", "Find entity IDs for a name, file path or path#Qualified.Name. Use the IDs in plan components and graph queries.", []toolParam{p("query", "", "string", "name, path or path#Qualified.Name", true), p("kind", "kind", "string", "restrict to a node kind (file, function, class, type, ...)", false), p("limit", "limit", "integer", "maximum results", false)}},
	{"radar_graph", "graph", "Query the indexed graph. Use from+edge=DEPENDS_ON+reverse=true to find files that import a file.", []toolParam{p("kind", "kind", "string", "node kind", false), p("name", "name", "string", "name substring", false), p("from", "from", "string", "start entity ID", false), p("edge", "edge", "string", "edge kind (DEFINES, IMPORTS, DEPENDS_ON, CONSUMES, EXPOSES)", false), p("reverse", "reverse", "boolean", "follow edges backwards", false), p("depth", "depth", "integer", "maximum traversal depth", false), p("ref", "ref", "string", "Git checkpoint", false), p("plan", "plan", "string", "overlay a plan's intent graph", false)}},
	{"radar_plan", "plan", "Create a grounded, incomplete plan bundle for a feature. Fill the plan file afterwards; it is not a design.", []toolParam{p("intent", "", "string", "feature description", true), p("ref", "ref", "string", "baseline commit", false), p("output", "output", "string", "new repository-relative plan path", false)}},
	{"radar_preflight", "preflight", "Validate a plan against the indexed baseline; returns findings and next_steps.", []toolParam{p("plan", "plan", "string", "plan path", true), p("ref", "ref", "string", "baseline commit", false)}},
	{"radar_tasks", "tasks", "Return the task DAG, parallel groups and per-task instruction packets.", []toolParam{p("plan", "plan", "string", "plan path", true)}},
	{"radar_verify", "verify", "Verify an implementation against a plan. Working-tree results are informational; pass ref for a commit.", []toolParam{p("plan", "plan", "string", "plan path", true), p("ref", "ref", "string", "implementation commit", false), p("evidence", "evidence", "string", "comma-separated evidence IDs from radar test", false)}},
	{"radar_impact", "impact", "Compare declared contracts between base and head (commit or WORKTREE).", []toolParam{p("base", "base", "string", "base ref", true), p("head", "head", "string", "head ref or WORKTREE", true)}},
	{"radar_scan", "scan", "Find contract conflicts across concurrent branches.", []toolParam{p("base", "base", "string", "base ref", true), p("branches", "branches", "string", "comma-separated refs", true)}},
	{"radar_affected", "affected", "List files, contracts and plan tasks affected by changes between base and head through import dependencies.", []toolParam{p("base", "base", "string", "base ref", true), p("head", "head", "string", "head ref or WORKTREE (default)", false), p("plan", "plan", "string", "plan path to map affected tasks", false), p("depth", "depth", "integer", "maximum dependency depth", false)}},
	{"radar_contracts", "contracts", "Lint .radar/contracts.json: schemas, pointers, declared fields and stale consumer declarations.", []toolParam{p("ref", "ref", "string", "checkpoint (default working tree)", false)}},
	{"radar_explain", "explain", "Show a persisted finding with its evidence.", []toolParam{p("finding_id", "", "string", "finding ID", true)}},
}

func (t toolSpec) schema() map[string]any {
	properties := map[string]any{}
	required := []string{}
	for _, param := range t.params {
		properties[param.name] = map[string]any{"type": param.kind, "description": param.description}
		if param.required {
			required = append(required, param.name)
		}
	}
	return map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false}
}

// args converts tool arguments into a command line.
func (t toolSpec) args(arguments map[string]any) ([]string, error) {
	args := []string{t.command}
	positional := []string{}
	for _, param := range t.params {
		value, ok := arguments[param.name]
		if !ok || value == nil {
			if param.required {
				return nil, fmt.Errorf("missing required argument %s", param.name)
			}
			continue
		}
		var text string
		switch v := value.(type) {
		case string:
			if param.kind != "string" {
				return nil, fmt.Errorf("argument %s must be a %s", param.name, param.kind)
			}
			text = v
		case bool:
			if param.kind != "boolean" {
				return nil, fmt.Errorf("argument %s must be a %s", param.name, param.kind)
			}
			if param.flag == "__detail" {
				continue
			}
			if v {
				args = append(args, "--"+param.flag)
			}
			continue
		case float64:
			if param.kind != "integer" || math.Trunc(v) != v || math.IsInf(v, 0) || math.IsNaN(v) {
				return nil, fmt.Errorf("argument %s must be an integer", param.name)
			}
			text = strconv.FormatFloat(v, 'f', -1, 64)
		default:
			return nil, fmt.Errorf("argument %s has unsupported type", param.name)
		}
		if param.flag == "" {
			positional = append(positional, text)
		} else {
			args = append(args, "--"+param.flag+"="+text)
		}
	}
	for name := range arguments {
		known := false
		for _, param := range t.params {
			known = known || param.name == name
		}
		if !known {
			return nil, fmt.Errorf("unknown argument %s", name)
		}
	}
	if len(positional) > 0 {
		args = append(args, "--")
		args = append(args, positional...)
	}
	return args, nil
}

type rpcRequest struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (a *app) mcp(_ options) int {
	return serveMCP(a, os.Stdin, a.out)
}

func serveMCP(a *app, in io.Reader, out io.Writer) int {
	var mu sync.Mutex
	send := func(id json.RawMessage, result any, err *rpcError) {
		message := map[string]any{"jsonrpc": "2.0", "id": id}
		if err != nil {
			message["error"] = err
		} else {
			message["result"] = result
		}
		b, _ := json.Marshal(message)
		mu.Lock()
		defer mu.Unlock()
		out.Write(append(b, '\n'))
	}
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 64<<10), 16<<20)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var req rpcRequest
		if err := json.Unmarshal(line, &req); err != nil {
			send(json.RawMessage("null"), nil, &rpcError{-32700, "parse error"})
			continue
		}
		if len(req.ID) == 0 {
			continue // notifications need no response
		}
		switch req.Method {
		case "initialize":
			var params struct {
				ProtocolVersion string `json:"protocolVersion"`
			}
			json.Unmarshal(req.Params, &params)
			version := mcpProtocolVersions[0]
			for _, v := range mcpProtocolVersions {
				if v == params.ProtocolVersion {
					version = v
				}
			}
			send(req.ID, map[string]any{
				"protocolVersion": version,
				"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
				"serverInfo":      map[string]any{"name": "radar", "version": Version},
				"instructions":    "Radar grounds plans in indexed repository evidence. Call radar_doctor first; radar_init once if uninitialized; radar_index before graph queries; radar_resolve to find entity IDs. Results distinguish verified, inferred, proposed and unknown evidence: never report unknown as passed. Test execution and plan approval require a human and are not available as tools.",
			}, nil)
		case "ping":
			send(req.ID, map[string]any{}, nil)
		case "tools/list":
			tools := []map[string]any{}
			for _, t := range mcpTools {
				tools = append(tools, map[string]any{"name": t.name, "description": t.description, "inputSchema": t.schema()})
			}
			send(req.ID, map[string]any{"tools": tools}, nil)
		case "tools/call":
			var params struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			}
			if err := json.Unmarshal(req.Params, &params); err != nil {
				send(req.ID, nil, &rpcError{-32602, "invalid params"})
				continue
			}
			send(req.ID, a.callTool(params.Name, params.Arguments), nil)
		default:
			send(req.ID, nil, &rpcError{-32601, "method not found: " + req.Method})
		}
	}
	return 0
}

func (a *app) callTool(name string, arguments map[string]any) map[string]any {
	failure := func(message string) map[string]any {
		return map[string]any{"content": []map[string]any{{"type": "text", "text": message}}, "isError": true}
	}
	var spec *toolSpec
	for i := range mcpTools {
		if mcpTools[i].name == name {
			spec = &mcpTools[i]
		}
	}
	if spec == nil {
		return failure("unknown tool " + name)
	}
	args, err := spec.args(arguments)
	if err != nil {
		return failure(err.Error())
	}
	if spec.command == "index" {
		args = append(args, "--summary")
	}
	// Flags must precede "--" positional arguments.
	if i := indexOf(args, "--"); i >= 0 {
		args = append(append(append([]string{}, args[:i]...), "--root", a.root, "--json"), args[i:]...)
	} else {
		args = append(args, "--root", a.root, "--json")
	}
	var stdout, stderr bytes.Buffer
	code := Run(a.ctx, args, &stdout, &stderr)
	text := stdout.String()
	if (spec.command == "check" || spec.command == "merge-check") && arguments["detail"] != true {
		text = compactVerification(text)
	}
	if len(text) > maxToolOutput {
		text = text[:maxToolOutput] + fmt.Sprintf("\n... output truncated at %d of %d bytes; narrow the query (kind, name, from, depth, limit) or run the CLI with --json.", maxToolOutput, stdout.Len())
	}
	content := []map[string]any{{"type": "text", "text": text}}
	if text := strings.TrimSpace(stderr.String()); text != "" {
		content = append(content, map[string]any{"type": "text", "text": "stderr: " + text})
	}
	if code == 1 {
		content = append(content, map[string]any{"type": "text", "text": "radar exit code 1: a check failed or required evidence is missing; inspect gate, findings and next steps."})
	}
	return map[string]any{"content": content, "isError": code == 2}
}

func indexOf(list []string, value string) int {
	for i, v := range list {
		if v == value {
			return i
		}
	}
	return -1
}

// compactVerification preserves verdict/evidence distinctions while bounding
// agent context. Full details remain available with detail=true or CLI --json.
func compactVerification(raw string) string {
	var full map[string]any
	if json.Unmarshal([]byte(raw), &full) != nil {
		return raw
	}
	out := map[string]any{"detail_hint": "Use detail=true or CLI --json for full provenance and inventory.", "suggested_repair_attempts": 2}
	for _, key := range []string{"gate", "status", "base", "head", "candidate_tree", "checks", "coverage", "feedback_digest", "limitations"} {
		if v, ok := full[key]; ok {
			out[key] = v
		}
	}
	for _, key := range []string{"findings", "diagnostics", "changed", "affected"} {
		if values, ok := full[key].([]any); ok {
			out[key+"_count"] = len(values)
			if len(values) > 10 {
				values = values[:10]
			}
			out[key] = values
		}
	}
	if impact, ok := full["impact"].(map[string]any); ok {
		for _, key := range []string{"changed", "affected"} {
			if values, ok := impact[key].([]any); ok {
				out[key+"_count"] = len(values)
			}
		}
	}
	if p, ok := full["verification_proposal"].(map[string]any); ok {
		proposal := map[string]any{"status": p["status"], "limitations": p["limitations"]}
		if commands, ok := p["commands"].([]any); ok {
			proposal["recommended_count"] = len(commands)
			if len(commands) > 8 {
				commands = commands[:8]
			}
			proposal["commands"] = commands
		}
		out["verification_proposal"] = proposal
	}
	for key, limit := range map[string]int{"checks": 20, "coverage": 12, "limitations": 10} {
		if values, ok := out[key].([]any); ok && len(values) > limit {
			out[key+"_count"] = len(values)
			out[key] = values[:limit]
		}
	}
	if original, ok := out["gate"].(map[string]any); ok {
		gateSummary := map[string]any{}
		for k, v := range original {
			gateSummary[k] = v
		}
		if required, ok := gateSummary["required_checks"].([]any); ok && len(required) > 20 {
			gateSummary["required_check_count"] = len(required)
			gateSummary["required_checks"] = required[:20]
		}
		out["gate"] = gateSummary
	}
	data, err := json.Marshal(out)
	if err != nil {
		return raw
	}
	return string(data)
}
