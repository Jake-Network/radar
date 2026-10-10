package cli

import (
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/Jake-Network/radar/internal/composition"
	"github.com/Jake-Network/radar/internal/gate"
	"github.com/Jake-Network/radar/internal/model"
	"github.com/Jake-Network/radar/internal/workspace"
)

// scopeSummary counts a link as checked when its candidate+candidate cell
// reached a result: passed, failed, or passed with warnings.
func scopeSummary(s workspace.Scope, replayOf string, links []composition.Link) string {
	parts := []string{"per-repo checks only", "0 cross-repo links checked"}
	if len(links) > 0 {
		checked := 0
		for _, l := range links {
			if linkChecked(l.Status) {
				checked++
			}
		}
		parts = []string{fmt.Sprintf("%d/%d cross-repo links checked", checked, len(links))}
	}
	if s.Partial() {
		parts = append(parts, fmt.Sprintf("partial workspace (%d/%d repos)", len(s.Repos), len(s.All)))
	}
	if s.OneOff {
		parts = append(parts, "one-off scope (--with)")
	}
	if replayOf != "" {
		parts = append(parts, "replay of "+replayOf)
	}
	return strings.Join(parts, " · ")
}

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

var workspaceLabels = map[string][2]string{
	"no_breaking_contracts": {"no breaking contract change", "breaking contract change"},
	"integration_execution": {"selected tests pass", "tests on the combined tree did not pass"},
	"test_selection":        {"test selection complete", "test selection incomplete"},
}

// repoLine is the ✓/✗/! summary of one repository.
func repoLine(r workspaceRepo) (string, string) {
	if r.Error != "" {
		return "✗", "error — " + r.Error
	}
	rep := r.Report
	combined := plural(len(r.Branches), "branch combined", "branches combined")
	if len(r.Branches) == 0 {
		combined = "base only"
	}
	switch r.Verdict {
	case gate.Pass:
		parts := []string{combined}
		for _, c := range rep.Gate.Required {
			if l, ok := workspaceLabels[c.ID]; ok {
				parts = append(parts, l[0])
			} else if c.ID != "textual_merge" {
				parts = append(parts, c.ID+" passed")
			}
		}
		return "✓", strings.Join(parts, " · ")
	case gate.Fail:
		for _, c := range rep.Gate.Required {
			if c.Status != model.StatusFailed {
				continue
			}
			if c.ID == "textual_merge" {
				return "✗", "branches conflict: " + strings.Join(shorten(rep.Conflicts, 4), ", ")
			}
			if l, ok := workspaceLabels[c.ID]; ok {
				return "✗", l[1]
			}
			return "✗", c.ID + " failed"
		}
		return "✗", "failed"
	}
	for _, c := range rep.Gate.Required {
		if c.Status == model.StatusPassed {
			continue
		}
		if r.Verdict == gate.Error {
			return "✗", "error — " + c.ID + ": " + firstSentence(c.Explanation)
		}
		return "!", "not verified — " + c.ID + ": " + firstSentence(c.Explanation)
	}
	return "!", "not verified — " + rep.Gate.Explanation
}

func chain(r workspaceRepo) string {
	if r.Base == "" {
		if r.BaseRef != "" {
			return r.BaseRef
		}
		return "—"
	}
	parts := []string{r.BaseRef + "@" + short(r.Base)}
	for _, b := range r.Branches {
		parts = append(parts, b.Ref+"@"+short(b.Commit))
	}
	return strings.Join(parts, " + ")
}

func renderWorkspaceGate(w io.Writer, g workspaceReport) {
	verdict := verdictMark[g.Verdict]
	if !g.ran && g.Verdict == gate.Pass {
		verdict = "PASS (static)"
	}
	if g.ran && g.Verdict == gate.Pass {
		verdict = "PASS (selected policy; bounded verification)"
	}
	head := []string{}
	if g.Workspace != "" {
		head = append(head, fmt.Sprintf("workspace %q", g.Workspace))
	}
	head = append(head, plural(g.RepoCount, "repo", "repos"), plural(g.BranchCount, "branch", "branches"), g.ScopeSummary)
	fmt.Fprintf(w, "Radar gate: %s — %s\n\n", verdict, strings.Join(head, " · "))

	idWidth, chainWidth := 0, 0
	for _, r := range g.Repos {
		idWidth = max(idWidth, len(r.ID))
		chainWidth = max(chainWidth, len(chain(r)))
	}
	for _, r := range g.Repos {
		source := r.SelectionSource
		if r.Error != "" && r.Base == "" {
			source = "error"
		}
		fmt.Fprintf(w, "  %-*s  %-*s   (%s)\n", idWidth, r.ID, chainWidth, chain(r), source)
	}
	fmt.Fprintln(w)
	for _, r := range g.Repos {
		mark, text := repoLine(r)
		fmt.Fprintf(w, "  %s %-*s  %s\n", mark, idWidth, r.ID, text)
		if r.Next != "" {
			fmt.Fprintf(w, "      next: %s\n", r.Next)
		}
		if r.Report == nil {
			continue
		}
		warnings := 0
		for _, f := range r.Report.Findings {
			switch f.Severity {
			case model.SeverityError:
				fmt.Fprintf(w, "      ✗ %s: %s\n", f.Code, f.Explanation)
				if f.Remediation != "" {
					fmt.Fprintf(w, "        fix: %s\n", f.Remediation)
				}
			case model.SeverityWarning:
				warnings++
			}
		}
		if warnings > 0 {
			fmt.Fprintf(w, "      · %s; see --json\n", plural(warnings, "warning", "warnings"))
		}
		if s := r.Report.Selection; g.ran && s != nil {
			fmt.Fprintf(w, "      tests (%s): ran %d of %d selected command(s)\n", s.Mode, len(r.Report.Executions), len(s.Commands))
			for _, ev := range r.Report.Executions {
				fmt.Fprintf(w, "        %s  %s  (in %s; %d recognized test(s))\n", gateMark(ev.Status), strings.Join(shorten(ev.Command, 6), " "), ev.CWD, ev.Observation.TestsRun)
			}
			for _, b := range s.Blocking {
				fmt.Fprintf(w, "        not run: %s\n", b)
			}
			if len(s.Uncovered) > 0 {
				fmt.Fprintf(w, "        uncovered changes: %s\n", strings.Join(shorten(s.Uncovered, 5), ", "))
			}
		}
	}
	if len(g.Links) == 0 {
		fmt.Fprintf(w, "  ! cross-repo links: not checked — %s\n", g.CrossRepo.Reason)
	} else {
		renderWorkspaceLinks(w, g)
	}
	for _, suggestion := range g.SuggestedLinks[:min(3, len(g.SuggestedLinks))] {
		fmt.Fprintf(w, "  · proposed link  %s → %s: %s (not checked)\n    %s\n", suggestion.Producer, suggestion.Consumer, suggestion.Candidate.Endpoint, suggestion.Command)
	}
	for _, r := range g.ExcludedRepos {
		fmt.Fprintf(w, "  · %s  registered but not in the team file\n", r.ID)
	}
	if g.TeamFile != nil && g.TeamFile.Uncommitted {
		fmt.Fprintf(w, "  ! %s:%s  uncommitted team file excluded; using the committed base declaration\n", g.TeamFile.Home, g.TeamFile.Path)
	}
	if g.CrossRepoExecution != nil {
		fmt.Fprintf(w, "  ! cross-repo execution: not checked — %s\n", g.CrossRepoExecution.Reason)
	}
	for _, r := range g.Repos {
		for _, d := range r.Dirty {
			if d.Selected {
				fmt.Fprintf(w, "  · %s:%s  committed changes only (%d uncommitted excluded)\n", r.ID, d.Branch, d.Count())
			}
		}
		for _, s := range r.Skipped {
			fmt.Fprintf(w, "  · %s:%s\n", r.ID, strings.Replace(s, " (", "  skipped (", 1))
		}
		for _, d := range r.Detached {
			fmt.Fprintf(w, "  ! %s  detached worktree %s @ %s not selected — name it: radar gate %s:%s\n", r.ID, d.Path, short(d.Commit), r.ID, d.Commit)
		}
		if r.WorktreeInspectionError != "" {
			fmt.Fprintf(w, "  ! %s  worktree inspection unavailable: %s\n", r.ID, r.WorktreeInspectionError)
		}
	}
	if a := g.Again; a != nil {
		changes := []string{}
		for _, k := range a.Added {
			changes = append(changes, "+ "+k)
		}
		for _, k := range a.Removed {
			changes = append(changes, "− "+k)
		}
		for _, k := range a.Moved {
			changes = append(changes, k+" has new commits")
		}
		if len(a.Added)+len(a.Removed) > 0 {
			fmt.Fprintf(w, "  ! again: branches changed since run %s: %s\n", a.PreviousRun, strings.Join(changes, " · "))
		} else if len(a.Moved) > 0 {
			fmt.Fprintf(w, "  · again: same branches as run %s; %s\n", a.PreviousRun, strings.Join(changes, " · "))
		} else {
			fmt.Fprintf(w, "  · again: same branches and commits as run %s\n", a.PreviousRun)
		}
	}
	leads := []string{}
	for _, r := range g.Repos {
		for _, attribution := range sortedAttributions(r.Attribution) {
			for _, l := range attribution.Leads {
				line := fmt.Sprintf("  %s:%s  %s", r.ID, l.Branch, strings.Join(shorten(l.Files, 3), ", "))
				if !slices.Contains(leads, line) {
					leads = append(leads, line)
				}
			}
		}
	}
	leads = append(leads, workspaceLinkLeads(g)...)
	if len(leads) > 0 {
		fmt.Fprintln(w, "\nRepair leads (where to look, not proof of cause)")
		for _, l := range leads {
			fmt.Fprintln(w, l)
		}
	}
	switch {
	case g.ReplayOf != "":
		fmt.Fprintf(w, "\n  · replayed run %s · digest %s\n", g.ReplayOf, shortDigest(g.Digest))
	case g.RecordError != "":
		fmt.Fprintf(w, "\n  ! run not recorded: %s (--again and --replay cannot use this run)\n", g.RecordError)
	default:
		fmt.Fprintf(w, "\n  · run %s · digest %s\n", g.RunID, shortDigest(g.Digest))
	}
	fmt.Fprintf(w, "\nNext: %s\n", g.Next)
}

func sortedAttributions(m map[string]gateAttribution) []gateAttribution {
	keys := []string{}
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	out := []gateAttribution{}
	for _, k := range keys {
		out = append(out, m[k])
	}
	return out
}

// firstSentence keeps summary lines short; --json has the full explanation.
func firstSentence(s string) string {
	if i := strings.Index(s, ". "); i >= 0 {
		return s[:i+1]
	}
	return s
}

func shortDigest(d string) string {
	if len(d) > len("sha256:")+16 {
		return d[:len("sha256:")+16]
	}
	return d
}

func linkDetail(l composition.Link) string {
	c := l.Cells[3]
	if c.Reason != "" {
		return c.Reason
	}
	for _, f := range c.Findings {
		if f.Severity == model.SeverityError {
			return f.Explanation
		}
	}
	if len(c.Findings) > 0 {
		return c.Findings[0].Explanation
	}
	return "declared fields are compatible"
}

func renderWorkspaceLinks(w io.Writer, g workspaceReport) {
	for _, l := range g.Links {
		switch l.Status {
		case model.StatusPassed:
			fmt.Fprintf(w, "  ✓ link %s  %s → %s\n", l.ID, l.Producer, l.Consumer)
		case model.StatusWarning:
			fmt.Fprintf(w, "  ✓ link %s  %s → %s (warning: %s)\n", l.ID, l.Producer, l.Consumer, linkDetail(l))
		case model.StatusFailed:
			fmt.Fprintf(w, "  ✗ link %s  %s → %s: %s (candidate+candidate)\n", l.ID, l.Producer, l.Consumer, linkDetail(l))
		default:
			fmt.Fprintf(w, "  ! link %s  not checked — %s\n", l.ID, linkDetail(l))
		}
		for _, warning := range l.Warnings {
			fmt.Fprintf(w, "  · link %s  %s\n", l.ID, warning)
		}
		for _, i := range []int{1, 2} {
			c := l.Cells[i]
			if c.Status != model.StatusFailed {
				continue
			}
			order := "producer first fails"
			if i == 2 {
				order = "consumer first fails"
			}
			reason := c.Reason
			if reason == "" && len(c.Findings) > 0 {
				reason = c.Findings[0].Explanation
			}
			fmt.Fprintf(w, "  ! link %s  %s → %s: %s — %s (%s producer + %s consumer; excluded from verdict)\n", l.ID, l.Producer, l.Consumer, order, reason, c.Producer, c.Consumer)
		}
	}
}

func workspaceLinkLeads(g workspaceReport) []string {
	leads := []string{}
	for _, l := range g.Links {
		if l.Status != model.StatusFailed {
			continue
		}
		for _, r := range g.Repos {
			file, detail := "", ""
			if r.ID == l.Producer {
				file = l.ProducerPath
				detail = linkDetail(l)
			}
			if r.ID == l.Consumer {
				file = workspace.ConsumesPath
				detail = "declares consumed fields"
			}
			if file == "" {
				continue
			}
			matched := false
			for _, b := range r.Branches {
				if slices.Contains(b.Changed, file) {
					leads = append(leads, fmt.Sprintf("  %s:%s  %s  %s", r.ID, b.Ref, file, detail))
					matched = true
				}
			}
			if !matched {
				leads = append(leads, fmt.Sprintf("  %s:%s  %s  %s", r.ID, r.BaseRef, file, detail))
			}
		}
	}
	return leads
}
