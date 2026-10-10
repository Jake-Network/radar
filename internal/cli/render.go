package cli

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/Jake-Network/radar/internal/contracts"
	"github.com/Jake-Network/radar/internal/evidence"
	"github.com/Jake-Network/radar/internal/model"
	"github.com/Jake-Network/radar/internal/planning"
)

func mark(s model.Status) string {
	switch s {
	case model.StatusPassed:
		return "PASS"
	case model.StatusFailed:
		return "FAIL"
	case model.StatusWarning:
		return "WARN"
	case model.StatusBlocked:
		return "BLKD"
	case model.StatusTimeout:
		return "TIME"
	case model.StatusError:
		return "ERR "
	case model.StatusIncomplete:
		return "INCP"
	}
	return "UNKN"
}

func severityMark(s model.Severity) string {
	switch s {
	case model.SeverityError:
		return "error  "
	case model.SeverityWarning:
		return "warning"
	}
	return "info   "
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func renderNextSteps(w io.Writer, steps []string) {
	if len(steps) == 0 {
		return
	}
	p := paletteOf(w)
	fmt.Fprintln(w, "\n"+p.bold("Next steps:"))
	for i, s := range steps {
		fmt.Fprintf(w, "  %d. %s\n", i+1, p.next(s))
	}
}

func renderReport(w io.Writer, command string, r planning.Report) {
	p := paletteOf(w)
	fmt.Fprintf(w, "%s: %s  (revision %s, authoritative: %s)\n", p.bold(command), p.status(r.Status, strings.ToUpper(string(r.Status))), r.Revision, yesNo(r.Authoritative))
	if len(r.Checks) > 0 {
		fmt.Fprintln(w, "\n"+p.bold("Checks:"))
		for _, c := range r.Checks {
			fmt.Fprintf(w, "  %s  %s — %s\n", p.status(c.Status, mark(c.Status)), c.ID, c.Explanation)
		}
	}
	if len(r.Findings) > 0 {
		fmt.Fprintln(w, "\n"+p.bold("Findings:"))
		for _, f := range r.Findings {
			fmt.Fprintf(w, "  %s  %s: %s\n", p.severity(f.Severity, severityMark(f.Severity)), f.Code, f.Explanation)
		}
	}
	if len(r.Tasks) > 0 {
		fmt.Fprintln(w, "\n"+p.bold("Tasks:"))
		for _, t := range r.Tasks {
			line := fmt.Sprintf("  %s  %s — %s", p.status(t.Status, mark(t.Status)), t.ID, t.Explanation)
			if len(t.BlockedBy) > 0 {
				line += " (blocked by " + strings.Join(t.BlockedBy, ", ") + ")"
			}
			fmt.Fprintln(w, line)
		}
	}
	renderNextSteps(w, r.NextSteps)
}

func renderSchedule(w io.Writer, s planning.Schedule) {
	fmt.Fprintf(w, "Plan digest: %s\nOrder: %s\n", s.PlanDigest, strings.Join(s.Order, " → "))
	for i, group := range s.ParallelGroups {
		fmt.Fprintf(w, "  group %d (parallel): %s\n", i+1, strings.Join(group, ", "))
	}
	for _, packet := range s.Instructions {
		fmt.Fprintf(w, "\n[%s] %s\n", packet.Task.ID, packet.Task.Intent)
		if len(packet.Task.DependsOn) > 0 {
			fmt.Fprintf(w, "  depends on: %s\n", strings.Join(packet.Task.DependsOn, ", "))
		}
		if len(packet.Task.Components) > 0 {
			fmt.Fprintf(w, "  components: %s\n", strings.Join(packet.Task.Components, ", "))
		}
		if len(packet.Task.Contracts) > 0 {
			fmt.Fprintf(w, "  contracts:  %s\n", strings.Join(packet.Task.Contracts, ", "))
		}
		for _, c := range packet.Acceptance {
			rule := "no rule"
			if c.Rule != nil {
				rule = c.Rule.Kind
			}
			fmt.Fprintf(w, "  accept %s (%s): %s\n", c.ID, rule, c.Intent)
		}
	}
}

func renderContracts(w io.Writer, r contracts.Report) {
	p := paletteOf(w)
	fmt.Fprintf(w, "contracts: %s  (%s; %d/%d bindings analyzed; base %s → %s)\n", strings.ToUpper(string(r.Status)), r.Checkpoint, r.Analyzed, r.Bindings, short(r.Base), strings.Join(shortAll(r.Heads), ", "))
	for _, f := range r.Findings {
		fmt.Fprintf(w, "  %s  %s [%s] %s\n", p.severity(f.Severity, severityMark(f.Severity)), f.Code, f.Contract, f.Explanation)
		if f.Consumer != "" {
			fmt.Fprintf(w, "           producer %s → consumer %s (branches %s)\n", f.Producer, f.Consumer, strings.Join(shortAll(f.Branches), " / "))
		}
	}
	for _, d := range r.Diagnostics {
		fmt.Fprintf(w, "  %s  %s %s\n", p.severity(d.Severity, severityMark(d.Severity)), d.Path, d.Message)
	}
	for _, o := range r.Obligations {
		fmt.Fprintf(w, "  obligation %s at %s: %s — %s\n", o.Binding, short(o.Head), o.Kind, o.Explanation)
	}
	for _, u := range r.Unverified {
		fmt.Fprintln(w, "  unverified: "+u)
	}
	if r.Status == model.StatusIncomplete {
		fmt.Fprintln(w, "Some bindings were not analyzed; absence of findings is not a compatibility guarantee.")
	}
}

func renderLint(w io.Writer, r contracts.LintReport) {
	p := paletteOf(w)
	fmt.Fprintf(w, "contracts at %s: %s\n", r.Revision, p.status(r.Status, strings.ToUpper(string(r.Status))))
	if r.Error != "" {
		fmt.Fprintln(w, "  "+r.Error)
	}
	for _, b := range r.Bindings {
		fmt.Fprintf(w, "  %s  %s\n", p.status(b.Status, mark(b.Status)), b.ID)
		for _, i := range b.Issues {
			fmt.Fprintln(w, "        issue: "+i)
		}
		for _, n := range b.Notes {
			fmt.Fprintln(w, "        note:  "+n)
		}
	}
}

func renderRecord(w io.Writer, r evidence.Record) {
	p := paletteOf(w)
	fmt.Fprintf(w, "test: %s  exit %d, %d run, %d failed, %d skipped (harness %s)\n", p.status(r.Status, strings.ToUpper(string(r.Status))), r.ExitCode, r.TestsRun, r.TestsFailed, r.TestsSkipped, orDash(r.Harness))
	fmt.Fprintf(w, "evidence: %s\ncriteria: %s\n", r.ID, strings.Join(r.Criteria, ", "))
	switch r.Status {
	case model.StatusError:
		fmt.Fprintln(w, "The command failed before any recognized test result (build, dependency or setup error). It is not test evidence.")
	case model.StatusUnknown:
		fmt.Fprintln(w, "No recognized executed tests; use a supported harness or declare a `junit` report.")
	}
	if r.Status != model.StatusPassed && r.OutputTail != "" {
		fmt.Fprintf(w, "\n--- last output (not stored) ---\n%s\n", strings.TrimRight(r.OutputTail, "\n"))
	}
}

func renderFinding(w io.Writer, f model.Finding) {
	p := paletteOf(w)
	fmt.Fprintf(w, "%s %s (%s evidence)\n%s\n", p.severity(f.Severity, strings.ToUpper(string(f.Severity))), p.bold(f.Code), f.Evidence, f.Explanation)
	if f.Contract != "" {
		fmt.Fprintf(w, "contract: %s  producer: %s  consumer: %s\n", f.Contract, f.Producer, f.Consumer)
	}
	for _, l := range f.Locations {
		fmt.Fprintf(w, "  at %s:%d @ %s (%s)\n", l.Path, l.Line, short(l.Revision), l.Method)
	}
	if f.Remediation != "" {
		fmt.Fprintln(w, "remediation: "+f.Remediation)
	}
	if f.Verification != "" {
		fmt.Fprintln(w, "verify: "+f.Verification)
	}
}

func renderGraph(w io.Writer, s model.Snapshot) {
	fmt.Fprintf(w, "%d entities, %d relationships at %s\n", len(s.Nodes), len(s.Edges), s.Revision)
	for _, n := range s.Nodes {
		location := n.Provenance.Path
		if n.Provenance.Line > 0 {
			location = fmt.Sprintf("%s:%d", location, n.Provenance.Line)
		}
		fmt.Fprintf(w, "  %-12s %-56s %s\n", n.Kind, n.ID, location)
	}
	kinds := map[string]int{}
	for _, e := range s.Edges {
		kinds[e.Kind]++
	}
	names := make([]string, 0, len(kinds))
	for k := range kinds {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		fmt.Fprintf(w, "  %d %s edges\n", kinds[k], k)
	}
}

func renderAffected(w io.Writer, r affectedReport) {
	fmt.Fprintf(w, "affected: %d changed, %d dependent files (base %s → %s)\n", len(r.Changed), len(r.Affected), short(r.Base), short(r.Head))
	for _, p := range r.Changed {
		fmt.Fprintln(w, "  changed   "+p)
	}
	for _, f := range r.Affected {
		via := ""
		if f.Via != "" {
			via = " via " + f.Via
		}
		fmt.Fprintf(w, "  depth %-3d %s (imports %s%s)\n", f.Depth, f.Path, f.Changed, via)
	}
	for _, c := range r.Contracts {
		fmt.Fprintf(w, "  contract  %s — %s\n", c.ID, c.Reason)
	}
	for _, t := range r.Tasks {
		fmt.Fprintf(w, "  task      %s — %s\n", t.ID, strings.Join(t.Paths, ", "))
	}
	for _, d := range r.Diagnostics {
		fmt.Fprintf(w, "  %s  %s %s\n", paletteOf(w).severity(d.Severity, severityMark(d.Severity)), d.Path, d.Message)
	}
	if len(r.Contracts) > 0 {
		fmt.Fprintf(w, "Run `radar impact --base %s --head %s` to check the affected contracts.\n", short(r.Base), r.Head)
	}
}

func short(rev string) string {
	if len(rev) == 40 || len(rev) == 64 {
		return rev[:12]
	}
	return rev
}

func shortAll(revs []string) []string {
	out := make([]string, len(revs))
	for i, r := range revs {
		out[i] = short(r)
	}
	return out
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
