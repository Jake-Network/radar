package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
	"unicode"

	"github.com/Jake-Network/radar/internal/gate"
	gitrepo "github.com/Jake-Network/radar/internal/git"
	"github.com/Jake-Network/radar/internal/integration"
	"github.com/Jake-Network/radar/internal/model"
	"github.com/Jake-Network/radar/internal/project"
)

func hookCommand() command {
	return command{name: "hook", usage: "radar hook stop --agent claude|codex [--base REF] [--branches A,B] [--timeout 20s]", summary: "Handle an agent Stop event with a private static saved-change preview.", advanced: true, run: (*app).agentHook}
}

type agentHookEvent struct {
	CWD    string `json:"cwd"`
	Active bool   `json:"stop_hook_active"`
	Name   string `json:"hook_event_name"`
}

type agentHookResponse struct {
	Decision string `json:"decision,omitempty"`
	Reason   string `json:"reason,omitempty"`
	Message  string `json:"systemMessage,omitempty"`
}

func (a *app) agentHook(o options) int { return a.runAgentHook(o.args, os.Stdin) }

// The protocol always exits zero: a supported failure is a JSON block decision,
// while missing evidence remains informational rather than an agent retry loop.
func (a *app) runAgentHook(args []string, input io.Reader) int {
	respond := func(r agentHookResponse) int {
		if err := json.NewEncoder(a.out).Encode(r); err != nil {
			return 2
		}
		return 0
	}
	unavailable := func(reason string) int {
		return respond(agentHookResponse{Message: "Radar NOT VERIFIED: " + hookText(reason)})
	}
	if len(args) == 0 || args[0] != "stop" {
		return unavailable("expected hook stop")
	}
	fs := flag.NewFlagSet("hook stop", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var agent, base, branches, cwd string
	var timeout time.Duration
	fs.StringVar(&agent, "agent", "", "agent protocol")
	fs.StringVar(&base, "base", "", "integration base")
	fs.StringVar(&branches, "branches", "", "explicit branch list")
	fs.DurationVar(&timeout, "timeout", 20*time.Second, "static analysis deadline")
	if err := fs.Parse(args[1:]); err != nil {
		return unavailable(err.Error())
	}
	if len(fs.Args()) != 0 || agent != "claude" && agent != "codex" || timeout <= 0 || timeout > 2*time.Minute {
		return unavailable("invalid hook options; agent must be claude or codex and timeout must be 0..2m")
	}
	ctx, cancel := context.WithTimeout(a.ctx, timeout)
	defer cancel()
	// Stop hooks normally close stdin after the event. Bound the wait as well
	// as its size, so a broken host cannot keep the agent waiting indefinitely.
	type readResult struct {
		data []byte
		err  error
	}
	read := make(chan readResult, 1)
	go func() { data, err := io.ReadAll(io.LimitReader(input, 65537)); read <- readResult{data, err} }()
	var data []byte
	var err error
	select {
	case result := <-read:
		data, err = result.data, result.err
	case <-ctx.Done():
		return unavailable("hook input deadline exceeded")
	}
	if err != nil || len(data) > 65536 {
		return unavailable("hook input unreadable or exceeds 64 KiB")
	}
	var event agentHookEvent
	if err := json.Unmarshal(data, &event); err != nil || !bytes.HasPrefix(bytes.TrimSpace(data), []byte("{")) {
		return unavailable("expected one JSON hook event object")
	}
	if event.Name != "" && event.Name != "Stop" {
		return unavailable("only Stop events are supported")
	}
	if event.Active {
		return respond(agentHookResponse{})
	}
	cwd = event.CWD
	if a.rootExplicit {
		cwd = a.root
	}
	if cwd == "" {
		cwd = a.root
	}
	root, err := project.Root(cwd)
	if err != nil {
		return unavailable("event cwd is unavailable")
	}
	root, err = gitrepo.TopLevel(ctx, root)
	if err != nil {
		return unavailable("event cwd is not an available Git worktree")
	}
	b := *a
	b.ctx, b.root = ctx, root
	refs := []string{"HEAD"}
	if branches != "" {
		refs = nil
		for _, ref := range strings.Split(branches, ",") {
			if ref = strings.TrimSpace(ref); ref == "" {
				return unavailable("empty selected branch")
			}
			refs = append(refs, ref)
		}
	}
	o := options{base: base, args: refs, includeDirty: true}
	state, err := b.observeWatch(ctx, o)
	if err != nil {
		return unavailable(err.Error())
	}
	in := state.Value.(watchInput)
	cacheKey := hookCacheKey(ctx, root, state.Key)
	if cached, ok := loadHookCache(root, cacheKey); ok {
		latest, err := b.observeWatch(ctx, o)
		if err == nil && latest.Key == state.Key && ctx.Err() == nil {
			cached.Message = strings.TrimSpace(cached.Message + " Radar reused the unchanged static preview cache; tests were not run.")
			return respond(cached)
		}
		return unavailable("inputs changed while reading cached preview")
	}
	finish := func(r agentHookResponse) int {
		latest, err := b.observeWatch(ctx, o)
		if err != nil || latest.Key != state.Key {
			return unavailable("inputs changed during analysis; rerun after saving changes")
		}
		if ctx.Err() != nil {
			return unavailable("static analysis deadline exceeded")
		}
		saveHookCache(root, cacheKey, r)
		return respond(r)
	}
	var candidate *integration.Candidate
	if len(in.Snapshots) == 0 {
		candidate, err = integration.BuildCandidate(ctx, root, in.Base, in.SHAs)
	} else {
		candidate, err = integration.BuildDirtyCandidate(ctx, root, in.Base, in.SHAs, in.Snapshots)
	}
	if candidate != nil {
		defer candidate.Close()
	}
	if err != nil {
		return unavailable(err.Error())
	}
	report, err := integration.Analyze(ctx, candidate, integration.Options{Base: in.Base, Branches: in.SHAs, Policy: in.config.Policy})
	if err != nil {
		return unavailable(err.Error())
	}
	in.config.applyIntegration(&report)
	// Committed merge conflicts need restacking, not another agent turn. When
	// dirty content is present, compare the committed candidate so only newly
	// introduced conflict paths request a repair. Other supported failures
	// (including committed migration heads) remain actionable.
	if len(report.Conflicts) > 0 {
		committedConflicts := report.Conflicts
		if len(in.Snapshots) > 0 {
			committed, err := integration.BuildCandidate(ctx, root, in.Base, in.SHAs)
			if committed != nil {
				defer committed.Close()
			}
			if err != nil {
				return unavailable("committed conflict comparison unavailable: " + err.Error())
			}
			committedConflicts = committed.Conflicts
		}
		prior := map[string]bool{}
		for _, path := range committedConflicts {
			prior[path] = true
		}
		newConflict := false
		for _, path := range report.Conflicts {
			if !prior[path] {
				newConflict = true
			}
		}
		if !newConflict {
			return finish(agentHookResponse{Message: hookText("Radar NOT VERIFIED: committed integration has textual conflicts; restack or reconcile these branches before landing: " + strings.Join(report.Conflicts, ", ") + ". Saved changes introduced no new conflicted paths. No tests were run.")})
		}
	}
	if report.Gate.Verdict == gate.Fail {
		lines := []string{"Radar found a supported static failure in the selected saved-change candidate. Fix the findings before completing, then rerun Radar. No tests or repository commands were executed."}
		for _, conflict := range report.Conflicts {
			lines = append(lines, "Merge conflict: "+conflict)
			if len(lines) >= 8 {
				break
			}
		}
		for _, finding := range report.Findings {
			if len(lines) >= 8 {
				break
			}
			lines = append(lines, finding.Code+": "+finding.Explanation)
		}
		return finish(agentHookResponse{Decision: "block", Reason: hookText(strings.Join(lines, "\n"))})
	}
	if report.Gate.Verdict != gate.Pass {
		for _, c := range report.Gate.Required {
			if c.Status != model.StatusPassed {
				return finish(agentHookResponse{Message: "Radar NOT VERIFIED: " + hookText(string(report.Gate.Verdict)+": required check "+c.ID+" is "+string(c.Status)+": "+c.Explanation)})
			}
		}
		return finish(agentHookResponse{Message: "Radar NOT VERIFIED: " + hookText(string(report.Gate.Verdict)+": "+report.Gate.Explanation)})
	}
	if len(report.Changed) == 0 {
		return finish(agentHookResponse{Message: "Radar NOT VERIFIED: no selected changes relative to base; no development verification was performed"})
	}
	return finish(agentHookResponse{Message: fmt.Sprintf("Radar static preview complete: %d changed files in selected %s; supported static checks only. Tests were not run; undeclared contracts and runtime compatibility remain unverified.", len(report.Changed), hookText(strings.Join(refs, ",")))})
}

func hookText(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' {
			return ' '
		}
		return r
	}, s)
	if len(s) > 4096 {
		s = string([]rune(s)[:min(1024, len([]rune(s)))]) + "…"
	}
	return s
}
