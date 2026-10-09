package cli

import (
	"fmt"
	"github.com/Jake-Network/radar/internal/testselection"
	"io"
	"strings"
)

func renderProposal(w io.Writer, p *testselection.Proposal) {
	if p == nil {
		return
	}
	fmt.Fprintf(w, "Recommended verification: %d commands (not complete coverage)\n", len(p.Commands))
	for _, c := range p.Commands {
		availability := "runner available; dependencies unchecked"
		if !c.ToolAvailable {
			availability = "runner availability unconfirmed"
		}
		fmt.Fprintf(w, "  [%d] %s (cwd %s; %s)\n", c.Priority, strings.Join(c.Command, " "), c.CWD, availability)
		if len(c.EvidenceReasons) > 0 {
			fmt.Fprintf(w, "    Why: %s\n", c.EvidenceReasons[0].Explanation)
		}
	}
}

// renderSelection summarizes a bounded execution plan without listing every
// grouped test file; the JSON report retains the complete plan.
func renderSelection(w io.Writer, s *testselection.Selection) {
	if s == nil {
		return
	}
	fmt.Fprintf(w, "Selection (%s): %d of %d candidate commands, %d of %d inventoried test files, budget %d commands\n", s.Mode, len(s.Commands), s.Candidates, s.TestFiles, s.Inventory, s.MaxCommands)
	for _, c := range s.Commands {
		fmt.Fprintf(w, "  %-8s %s (cwd %s; %d test files)\n", c.Tier, strings.Join(shorten(c.Command, 6), " "), c.CWD, len(c.TestFiles))
	}
	for _, o := range s.Omitted {
		fmt.Fprintf(w, "  omitted  %s [%s, %s]: %s\n", strings.Join(shorten(o.Command, 6), " "), o.Tier, o.Reason, o.Explanation)
	}
	if len(s.Uncovered) > 0 {
		fmt.Fprintf(w, "  uncovered changes (no direct test relationship): %s\n", strings.Join(s.Uncovered, ", "))
	}
	for _, b := range s.Blocking {
		fmt.Fprintln(w, "  blocking: "+b)
	}
	fmt.Fprintln(w, "  "+s.Explanation)
}

func shorten(argv []string, n int) []string {
	if len(argv) <= n {
		return argv
	}
	return append(append([]string(nil), argv[:n]...), fmt.Sprintf("… (+%d paths)", len(argv)-n))
}
