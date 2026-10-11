package cli

import (
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/Jake-Network/radar/internal/integration"
	"github.com/Jake-Network/radar/internal/testselection"
)

// gateGap is one kind of missing test evidence, phrased for people: what
// was not verified, the commands or files involved, and the safest fix.
// It only regroups Selection.Blocking and Omitted; it decides nothing.
type gateGap struct {
	kind  string
	title string
	items []string
	fix   string
}

const (
	gapRunner      = "runner"
	gapBudget      = "budget"
	gapTimeout     = "timeout"
	gapInventory   = "inventory"
	gapUnsupported = "unsupported"
	gapNoTests     = "no_tests"
	gapDocs        = "documentation"
	gapOther       = "other"
)

// gateGaps groups the selection's blocking reasons by kind, merging the
// planning-time and execution-time entries for the same command.
func gateGaps(r integration.Report) []gateGap {
	s := r.Selection
	if s == nil {
		return nil
	}
	why := map[string]string{}
	for _, o := range s.Omitted {
		if o.Reason == "environment_unavailable" {
			reason, _, _ := strings.Cut(o.Explanation, "; Radar does not install")
			why[strings.Join(o.Command, " ")] = reason
		}
	}
	var gaps []gateGap
	if documentationOnly(r) {
		gaps = append(gaps, gateGap{kind: gapDocs, items: shorten(r.Changed, 5)})
	}
	add := func(kind, item string) {
		for i := range gaps {
			if gaps[i].kind == kind {
				if !slices.Contains(gaps[i].items, item) {
					gaps[i].items = append(gaps[i].items, item)
				}
				return
			}
		}
		gaps = append(gaps, gateGap{kind: kind, items: []string{item}})
	}
	for _, b := range s.Blocking {
		prefix, rest, _ := strings.Cut(b, ": ")
		switch prefix {
		case "required command runner unavailable or unverified", "environment_unavailable":
			add(gapRunner, rest)
		case "required command omitted by budget":
			add(gapBudget, rest)
		case "time_budget_exhausted":
			add(gapTimeout, rest)
		case "inventory incomplete":
			add(gapInventory, rest)
		case "inventoried test unsupported", "required verification omitted", "required recommendation omitted before full planning":
			add(gapUnsupported, rest)
		default:
			if b == "no runnable verification command was selected for changed code" {
				add(gapNoTests, "")
			} else {
				add(gapOther, b)
			}
		}
	}
	for i := range gaps {
		g := &gaps[i]
		n := len(g.items)
		switch g.kind {
		case gapRunner:
			g.title = fmt.Sprintf("runner unavailable: %s could not start in the private combined tree", plural(n, "test command", "test commands"))
			reasons := []string{}
			for j, item := range g.items {
				if w := why[item]; w != "" {
					g.items[j] = item + " — " + w
					reasons = append(reasons, w)
				}
			}
			switch {
			case notCopied(*g):
				g.fix = "Radar copies only committed files, so node_modules and virtualenvs are missing. Run these tests yourself on the combined branches, or declare a reviewed plan test_run with link (see docs/USAGE.md)."
			case len(reasons) > 0:
				g.fix = "Install the runner where you run radar (Radar never installs dependencies), then rerun."
			default:
				g.fix = "Radar could not confirm the runner before running; install it or check the command, then rerun."
			}
		case gapBudget:
			suggest := min(s.MaxCommands+n, 256)
			g.title = fmt.Sprintf("%s skipped: the budget allows %d", plural(n, "required test command was", "required test commands were"), s.MaxCommands)
			g.fix = fmt.Sprintf("Raise the budget with --max-commands %d.", suggest)
			if s.Mode != testselection.ModeTargeted {
				g.fix = fmt.Sprintf("Raise the budget with --max-commands %d, or select fewer tests with --suite targeted.", suggest)
			}
		case gapTimeout:
			g.title = fmt.Sprintf("%s did not start before the time budget ran out", plural(n, "test command", "test commands"))
			g.fix = "Raise --timeout (up to 30m), or select fewer tests with --suite targeted."
			if s.Mode == testselection.ModeTargeted {
				g.fix = "Raise --timeout (up to 30m)."
			}
		case gapInventory:
			g.title = fmt.Sprintf("inventory incomplete: test discovery skipped %s", plural(n, "file", "files"))
			g.fix = "Skipped files (oversized, symlinked or unreadable) may hold tests Radar cannot see. Fix or move them, or choose a limited --policy deliberately."
		case gapUnsupported:
			g.title = fmt.Sprintf("%s related to the change have no supported test command", plural(n, "test", "tests"))
			g.fix = "Radar cannot run these tests (unsupported layout or runner). Run them yourself, or declare a reviewed plan test_run."
		case gapDocs:
			g.title = "only documentation changed; Radar selects no test for documentation and cannot show that no test reads it"
			g.fix = "For a documentation-only change, the static result (radar gate) is what Radar can verify. A --policy without integration_execution would accept this, but a policy applies to every change, including untested code, so use one only for runs you know are documentation."
		case gapNoTests:
			g.title = "no test related to the changed code was found"
			g.items = nil
			g.fix = "Add a test that imports the changed code, try --suite full, or declare a reviewed plan test_run. Static relationships cover imports only."
		default:
			g.title = "required verification has other gaps"
			g.fix = "Inspect selection.blocking in --json."
		}
	}
	return gaps
}

// renderGaps prints each gap with at most a few of its items.
func renderGaps(w io.Writer, gaps []gateGap, p palette) {
	for _, g := range gaps {
		fmt.Fprintf(w, "  %s not verified: %s\n", p.mark("?"), g.title)
		for i, item := range g.items {
			if i == 3 {
				fmt.Fprintf(w, "      … %d more (see --json)\n", len(g.items)-3)
				break
			}
			fmt.Fprintf(w, "      %s\n", p.dim(item))
		}
		fmt.Fprintf(w, "    fix: %s\n", g.fix)
	}
}

// notCopied reports runner gaps caused by dependency directories that the
// private copy never contains.
func notCopied(g gateGap) bool {
	return slices.ContainsFunc(g.items, func(item string) bool { return strings.Contains(item, "node_modules/") })
}

// missingPrograms names runners that were not on PATH, from the execution
// diagnoses and the pre-run availability check.
func missingPrograms(r integration.Report, g gateGap) string {
	names := []string{}
	if found := missingRunner(r.Executions); found != "" {
		names = append(names, strings.Split(found, ", ")...)
	}
	for _, item := range g.items {
		_, why, _ := strings.Cut(item, " — runner ")
		if program, ok := strings.CutSuffix(why, " is not on PATH"); ok && !slices.Contains(names, program) {
			names = append(names, program)
		}
	}
	return strings.Join(names, ", ")
}

// blockedNext is the single next step for a blocked --run, chosen from the
// gaps only when one kind explains the whole block. again renders the rerun
// command after adjusting a copy of the invocation's options.
func blockedNext(r integration.Report, gaps []gateGap, again func(func(*options)) string, o options) string {
	same := again(func(*options) {})
	uncovered := r.Selection != nil && len(r.Selection.Uncovered) > 0
	if len(gaps) == 1 && !uncovered {
		switch gaps[0].kind {
		case gapBudget:
			if r.Selection.MaxCommands >= 256 {
				break
			}
			return "raise the command budget: " + again(func(o *options) { o.maxCommands = min(r.Selection.MaxCommands+len(gaps[0].items), 256) })
		case gapTimeout:
			if o.timeout >= 30*time.Minute {
				break
			}
			return "raise the time budget: " + again(func(o *options) { o.timeout = 30 * time.Minute })
		case gapDocs:
			if !docsOnlyBlock(r) {
				break
			}
			return "documentation changes have no test evidence; the static check is what Radar verifies: " + strings.Replace(same, "radar gate --run", "radar gate", 1)
		case gapRunner:
			if names := missingPrograms(r, gaps[0]); names != "" {
				return "install " + names + " where you run radar, then rerun: " + same
			}
			if notCopied(gaps[0]) {
				// Rerunning cannot help: dependency directories are never copied.
				return "these tests cannot run in Radar's private copy; run them yourself on the combined branches, or declare a reviewed plan test_run with link (docs/USAGE.md)"
			}
		}
	}
	if len(gaps) == 0 && uncovered {
		// --suite full runs whole suites but does not show they cover the
		// files, so it is not offered as the fix.
		return "add or name tests that cover the uncovered files, commit, then rerun: " + same
	}
	return "resolve the items marked ? above, then rerun: " + same
}
