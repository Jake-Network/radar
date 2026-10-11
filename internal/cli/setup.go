package cli

import (
	"flag"
	"fmt"
	"io"

	"github.com/Jake-Network/radar/internal/onboarding"
)

func setupCommand() command {
	return command{name: "setup", usage: "radar setup [claude|codex|both] [--dry-run] [--hook=false] [--base REF] [--branches A,B]", summary: "Initialize Radar and install project-local agent hooks, skills and MCP configuration.", flags: func(fs *flag.FlagSet, o *options) {
		fs.StringVar(&o.agent, "agent", "", "coding agent: codex (default), claude or both; same as the positional name")
		fs.BoolVar(&o.dryRun, "dry-run", false, "show proposed changes without writing files")
		fs.StringVar(&o.base, "base", "", "persist an explicit base ref in installed Stop hooks")
		fs.StringVar(&o.branches, "branches", "", "persist comma-separated participating branch refs in installed Stop hooks")
		fs.BoolVar(&o.hook, "hook", true, "install native Stop verification hooks; --hook=false skips installation (does not remove existing hooks)")
	}, run: func(a *app, o options) int {
		// `radar setup claude` and `radar setup --agent claude` are the same.
		switch {
		case len(o.args) > 1:
			return a.fail(fmt.Errorf("setup takes one agent name (claude, codex or both), got %d", len(o.args)))
		case len(o.args) == 1 && o.agent != "" && o.agent != o.args[0]:
			return a.fail(fmt.Errorf("setup got two agents: %q and --agent %q; give one", o.args[0], o.agent))
		case len(o.args) == 1 && o.args[0] != "claude" && o.args[0] != "codex" && o.args[0] != "both":
			return a.fail(fmt.Errorf("unknown agent %q: use radar setup claude, codex or both", o.args[0]))
		case len(o.args) == 1:
			o.agent = o.args[0]
		case o.agent == "":
			o.agent = "codex"
		}
		r, e := onboarding.Setup(a.ctx, a.root, onboarding.Options{Agent: o.agent, DryRun: o.dryRun, Hook: o.hook, Base: o.base, Branches: o.branches})
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
			p := paletteOf(w)
			fmt.Fprintf(w, "%s %s; no API credentials required.\n", p.bold("Next:"), p.command("radar gate"))
		})
		return 0
	}}
}
