package cli

import (
	"encoding/json"
	"fmt"
	"strings"
)

// compactVerification preserves verdict/evidence distinctions while bounding
// agent context. Full details remain available with detail=true or CLI --json.
func compactVerification(raw string) string {
	var full map[string]any
	if json.Unmarshal([]byte(raw), &full) != nil {
		return raw
	}
	if _, ok := full["scope_summary"]; ok {
		return compactWorkspace(full)
	}
	out := map[string]any{"detail_hint": "Use detail=true or CLI --json for full provenance and inventory.", "suggested_repair_attempts": 2}
	for _, key := range []string{"gate", "status", "base", "base_ref", "head", "candidate_tree", "checks", "coverage", "feedback_digest", "limitations", "attribution", "skipped_branches", "next", "configuration_inputs", "worktree_inspection_error"} {
		if v, ok := full[key]; ok {
			out[key] = v
		}
	}
	if worktrees, ok := full["worktrees"].([]any); ok {
		compact := []any{}
		dirty := 0
		for _, raw := range worktrees {
			wt, _ := raw.(map[string]any)
			entry := map[string]any{}
			for _, k := range []string{"path", "branch", "commit", "detached", "commit_included", "uncommitted_included", "inspection_error"} {
				if v, ok := wt[k]; ok {
					entry[k] = v
				}
			}
			count := 0
			for _, k := range []string{"staged", "unstaged", "untracked"} {
				paths, _ := wt[k].([]any)
				entry[k+"_count"] = len(paths)
				count += len(paths)
			}
			if count > 0 {
				dirty++
			}
			if len(compact) < 8 {
				compact = append(compact, entry)
			}
		}
		out["worktrees"], out["worktree_count"], out["dirty_worktree_count"] = compact, len(worktrees), dirty
		out["worktree_scope"] = "Committed candidate only; uncommitted contents excluded. Use CLI --json for all paths."
	}
	if branches, ok := full["branches"].([]any); ok {
		compact := []any{}
		for _, raw := range branches {
			b, _ := raw.(map[string]any)
			changed, _ := b["changed"].([]any)
			compact = append(compact, map[string]any{"ref": b["ref"], "commit": b["commit"], "changed_count": len(changed)})
		}
		out["branches"] = compact
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
			compact := []any{}
			for _, c := range commands {
				compact = append(compact, compactCommand(c))
			}
			proposal["commands"] = compact
		}
		out["verification_proposal"] = proposal
	}
	out["agent_brief"] = agentBrief(full)
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

// compactCommand keeps the identity, argv, reason and size of a command but
// drops per-file provenance, which remains in the full report.
func compactCommand(raw any) map[string]any {
	c, _ := raw.(map[string]any)
	out := map[string]any{"id": c["id"], "cwd": c["cwd"], "framework": c["framework"], "tool_available": c["tool_available"]}
	if tier, ok := c["tier"]; ok {
		out["tier"] = tier
	}
	if argv, ok := c["command"].([]any); ok {
		out["argument_count"] = len(argv)
		if len(argv) > 8 {
			argv = append(append([]any{}, argv[:8]...), "…")
		}
		out["command"] = argv
	}
	if files, ok := c["test_files"].([]any); ok {
		out["test_file_count"] = len(files)
	}
	if reasons, ok := c["evidence_reasons"].([]any); ok && len(reasons) > 0 {
		codes := map[string]bool{}
		why := []any{}
		for _, r := range reasons {
			reason, _ := r.(map[string]any)
			code, _ := reason["code"].(string)
			if !codes[code] {
				codes[code] = true
				why = append(why, map[string]any{"code": code, "explanation": reason["explanation"]})
			}
		}
		out["why"] = why
	}
	return out
}

// agentBrief answers, in bounded form, what changed, what must be verified,
// what was verified and what the agent must repair. It never decides repairs
// or retries; Radar only reports deterministic evidence and policy state.
func agentBrief(full map[string]any) map[string]any {
	list := func(v any) []any { l, _ := v.([]any); return l }
	bound := func(l []any, n int) []any {
		if len(l) > n {
			return l[:n]
		}
		return l
	}
	brief := map[string]any{}
	changed := list(full["changed"])
	if impact, ok := full["impact"].(map[string]any); ok {
		changed = list(impact["changed"])
	}
	brief["changed_files"] = bound(changed, 15)
	brief["changed_file_count"] = len(changed)

	contracts := []any{}
	repair := []any{}
	for _, raw := range list(full["findings"]) {
		f, _ := raw.(map[string]any)
		if f["contract"] != nil && f["contract"] != "" {
			contracts = append(contracts, map[string]any{"contract": f["contract"], "code": f["code"], "severity": f["severity"], "producer": f["producer"], "consumer": f["consumer"]})
		}
		if f["severity"] == "error" {
			repair = append(repair, map[string]any{"code": f["code"], "explanation": f["explanation"], "remediation": f["remediation"], "verification": f["verification"]})
		}
	}
	brief["contract_impact"] = bound(contracts, 10)
	obligations := list(full["contract_obligations"])
	if declared, ok := full["declared_contracts"].(map[string]any); ok {
		obligations = list(declared["obligation_changes"])
		if u := list(declared["unverified_obligations"]); len(u) > 0 {
			brief["unverified_contract_obligations"] = bound(u, 10)
		}
	}
	if len(obligations) > 0 {
		brief["contract_obligation_changes"] = bound(obligations, 10)
	}
	brief["must_repair"] = bound(repair, 10)
	brief["must_repair_count"] = len(repair)

	executed, failed, blocked := []any{}, []any{}, []any{}
	for _, raw := range list(full["checks"]) {
		c, _ := raw.(map[string]any)
		id, _ := c["id"].(string)
		status, _ := c["status"].(string)
		if strings.HasPrefix(id, "test:") {
			entry := map[string]any{"id": strings.TrimPrefix(id, "test:"), "status": status}
			switch status {
			case "passed":
				executed = append(executed, entry)
			case "failed", "error", "timeout":
				executed = append(executed, entry)
				failed = append(failed, entry)
			default:
				blocked = append(blocked, map[string]any{"id": entry["id"], "status": status, "explanation": c["explanation"]})
			}
		}
	}
	tests := map[string]any{"executed": bound(executed, 16), "executed_count": len(executed), "failed": bound(failed, 10), "not_run": bound(blocked, 10)}
	if s, ok := full["selection"].(map[string]any); ok {
		essential, optional := []any{}, []any{}
		for _, raw := range list(s["commands"]) {
			c := compactCommand(raw)
			if c["tier"] == "optional" {
				optional = append(optional, c)
			} else {
				essential = append(essential, c)
			}
		}
		omitted := []any{}
		for _, raw := range list(s["omitted"]) {
			o, _ := raw.(map[string]any)
			omitted = append(omitted, map[string]any{"id": o["id"], "tier": o["tier"], "reason": o["reason"], "test_file_count": len(list(o["test_files"]))})
		}
		tests["mode"] = s["mode"]
		tests["essential"] = bound(essential, 8)
		tests["essential_count"] = len(essential)
		tests["optional"] = bound(optional, 4)
		tests["optional_count"] = len(optional)
		tests["omitted"] = bound(omitted, 10)
		tests["omitted_count"] = len(omitted)
		tests["uncovered_changes"] = bound(list(s["uncovered_changes"]), 10)
		tests["blocking"] = bound(list(s["blocking"]), 10)
	}
	brief["tests"] = tests

	next := []any{}
	if g, ok := full["gate"].(map[string]any); ok {
		for _, raw := range list(g["required_checks"]) {
			c, _ := raw.(map[string]any)
			if c["status"] != "passed" {
				next = append(next, fmt.Sprintf("required check %v is %v: %v", c["id"], c["status"], c["explanation"]))
			}
		}
	}
	if len(repair) > 0 {
		next = append(next, "After repairing, rerun the same Radar command on the new commits; prior evidence does not transfer to a new candidate.")
	}
	brief["verify_next"] = bound(next, 8)
	return brief
}

// compactWorkspace bounds a workspace gate report: the scope, each
// repository's verdict and selection, what must be repaired, and the next
// command. Full reports stay available with detail=true or CLI --json.
func compactWorkspace(full map[string]any) string {
	out := map[string]any{"detail_hint": "Use detail=true or CLI --json for each repository's full report.", "suggested_repair_attempts": 2}
	for _, key := range []string{"version", "workspace", "scope_source", "scope_summary", "one_off", "only", "repo_count", "branch_count", "cross_repo", "cross_repo_execution", "again", "replay_of", "digest", "run_id", "record_error", "verdict", "next", "configuration_inputs", "team_file", "excluded_repos"} {
		if v, ok := full[key]; ok {
			out[key] = v
		}
	}
	repos := []any{}
	list := func(v any) []any { l, _ := v.([]any); return l }
	for _, raw := range list(full["repos"]) {
		r, _ := raw.(map[string]any)
		entry := map[string]any{}
		for _, key := range []string{"id", "base_ref", "base", "base_source", "selection_source", "verdict", "error", "next", "attribution", "skipped_branches", "detached"} {
			if v, ok := r[key]; ok {
				entry[key] = v
			}
		}
		branches := []any{}
		for _, b := range list(r["branches"]) {
			branch, _ := b.(map[string]any)
			branches = append(branches, map[string]any{"ref": branch["ref"], "commit": branch["commit"], "source": branch["source"], "changed_count": len(list(branch["changed"]))})
		}
		entry["branches"] = branches
		dirty := []any{}
		for _, d := range list(r["dirty"]) {
			wt, _ := d.(map[string]any)
			count := len(list(wt["staged"])) + len(list(wt["unstaged"])) + len(list(wt["untracked"]))
			dirty = append(dirty, map[string]any{"path": wt["path"], "branch": wt["branch"], "selected": wt["selected"], "uncommitted_count": count})
		}
		entry["dirty"] = dirty
		if report, ok := r["report"].(map[string]any); ok {
			brief := agentBrief(report)
			entry["must_repair"] = brief["must_repair"]
			entry["verify_next"] = brief["verify_next"]
			entry["tests"] = brief["tests"]
			if g, ok := report["gate"].(map[string]any); ok {
				entry["required_checks"] = g["required_checks"]
			}
			entry["conflicts"] = report["conflicts"]
			entry["candidate_tree"] = report["candidate_tree"]
		}
		repos = append(repos, entry)
	}
	out["repos"] = repos
	if raw, exists := full["links"]; exists {
		links := []any{}
		for _, item := range list(raw) {
			l, _ := item.(map[string]any)
			entry := map[string]any{"id": l["id"], "producer": l["producer"], "consumer": l["consumer"], "status": l["status"]}
			cells := []any{}
			for _, item := range list(l["cells"]) {
				c, _ := item.(map[string]any)
				if c["status"] != "passed" {
					cells = append(cells, c)
				}
			}
			entry["unverified_cells"] = cells
			links = append(links, entry)
		}
		out["links"] = links
	}
	if raw, exists := full["suggested_links"]; exists {
		suggestions := list(raw)
		if len(suggestions) > 3 {
			suggestions = suggestions[:3]
		}
		out["suggested_links"] = suggestions
	}
	data, err := json.Marshal(out)
	if err != nil {
		return "{}"
	}
	return string(data)
}
