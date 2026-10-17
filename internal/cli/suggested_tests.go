package cli

import (
	"fmt"
	"io"

	"github.com/Jake-Network/radar/internal/evidence"
	"github.com/Jake-Network/radar/internal/testselection"
)

// Derive the human preview from the execution selector without modifying the
// proposal or its serialized evidence. Optional package fallbacks stay visible
// as a count, rather than crowding out directly related tests.
func renderSuggestedTests(w io.Writer, proposal testselection.Proposal, changed []string, p palette) {
	selection, err := testselection.Plan(proposal, changed, testselection.ModeTargeted, len(proposal.Commands)+1)
	if err != nil {
		return
	} // The constant mode above is always supported.
	fallback := 0
	for _, omitted := range selection.Omitted {
		if omitted.Reason == "mode_excluded" {
			fallback++
		}
	}
	if len(selection.Commands) == 0 && fallback == 0 {
		return
	}
	fmt.Fprintf(w, "\n%s %s\n", p.bold("Suggested tests"), p.dim("(static relationships; not run)"))
	for i, c := range selection.Commands {
		if i == 5 {
			fmt.Fprintf(w, "  … %d more directly related commands\n", len(selection.Commands)-5)
			break
		}
		fmt.Fprintf(w, "  %s  %s\n", p.cyan(displayCommand(c.Command)), p.dim("(in "+c.CWD+")"))
	}
	if fallback > 0 {
		fmt.Fprintf(w, "  %s\n", p.dim(fmt.Sprintf("%s available; review with --run --suite balanced", plural(fallback, "package fallback", "package fallbacks"))))
	}
}

func renderRunnerPreparationErrors(w io.Writer, preparations []evidence.RunnerPreparation, indent string, p palette) {
	for _, preparation := range preparations {
		if preparation.Error != "" {
			fmt.Fprintf(w, "%s%s Runner setup: %s\n", indent, p.mark("?"), preparation.Error)
		}
	}
}
