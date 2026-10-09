package cli

import (
	"flag"
	"fmt"
	"io"

	"github.com/Jake-Network/radar/internal/discovery"
)

func discoveryCommand() command {
	return command{name: "discover", usage: "radar discover [--ref REF] [--json]", summary: "Discover proposed contract relationships with evidence; never modify accepted bindings.", flags: func(fs *flag.FlagSet, o *options) { refFlag(fs, o, refUsage) }, run: func(a *app, o options) int {
		ref := o.ref
		if ref == "" {
			ref = "WORKTREE"
		}
		r, e := discovery.Discover(a.ctx, a.root, ref)
		if e != nil {
			return a.fail(e)
		}
		a.report(r, func(w io.Writer) {
			fmt.Fprintf(w, "Contract discovery: %s (%d schemas, %d proposed candidates)\n", r.Status, len(r.Schemas), len(r.Candidates))
			for _, c := range r.Candidates {
				fmt.Fprintf(w, "  %s → %s: %s [%s] fields=%v\n", c.Producer, c.Consumer, c.Contract, c.Evidence, c.Fields)
				for _, ambiguity := range c.Ambiguities {
					fmt.Fprintf(w, "    %s\n", ambiguity)
				}
			}
			for _, d := range r.Diagnostics {
				fmt.Fprintf(w, "  %s: %s\n", d.Path, d.Message)
			}
			fmt.Fprintln(w, "Use --json to inspect proposed_manifest. Accept bindings manually after review; discovery does not verify runtime compatibility.")
		})
		return 0
	}}
}
