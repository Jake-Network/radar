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
