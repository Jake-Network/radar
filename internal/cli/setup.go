package cli

import (
	"flag"
	"fmt"
	"io"

	"github.com/Jake-Network/radar/internal/onboarding"
)

func setupCommand() command {
	return command{name: "setup", usage: "radar setup --agent codex|claude|both [--dry-run] [--hook]", summary: "Initialize Radar and install safe project-local agent skills and MCP configuration.", flags: func(fs *flag.FlagSet, o *options) {
		fs.StringVar(&o.agent, "agent", "codex", "coding agent: codex, claude or both")
		fs.BoolVar(&o.dryRun, "dry-run", false, "show proposed changes without writing files")
		fs.BoolVar(&o.hook, "hook", false, "install optional Claude Stop verification hook (requires bash)")
	}, run: func(a *app, o options) int {
		if len(o.args) > 0 {
			return a.fail(fmt.Errorf("setup does not accept positional arguments"))
		}
		r, e := onboarding.Setup(a.ctx, a.root, onboarding.Options{Agent: o.agent, DryRun: o.dryRun, Hook: o.hook})
		if e != nil {
			return a.fail(e)
		}
		a.report(r, func(w io.Writer) {
			fmt.Fprintf(w, "Radar setup: %s (dry run: %v)\n", r.Root, r.DryRun)
			for _, c := range r.Changes {
				fmt.Fprintf(w, "  %s %s\n", c.Action, c.Path)
			}
			for _, d := range r.Diagnostics {
				fmt.Fprintln(w, d)
			}
			fmt.Fprintln(w, "Next: radar check --base main; no API credentials required.")
		})
		return 0
	}}
}
