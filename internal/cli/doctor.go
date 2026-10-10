package cli

import (
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"

	gitrepo "github.com/Jake-Network/radar/internal/git"
	"github.com/Jake-Network/radar/internal/languages"
	"github.com/Jake-Network/radar/internal/project"
)

func (a *app) doctor(_ options) int {
	info, e := gitrepo.Inspect(a.ctx, a.root)
	tools := map[string]any{}
	names := []string{"git", "node", "python3", "go", "cargo", "rust-analyzer", "scip-typescript", "scip-python"}
	for _, tool := range names {
		p, e := exec.LookPath(tool)
		tools[tool] = map[string]any{"available": e == nil, "path": p}
	}
	_, stateErr := project.Read(a.stateRoot)
	result := map[string]any{"version": Version, "root": a.root, "state_root": a.stateRoot, "initialized": stateErr == nil, "repository_identity": gitrepo.Identity(a.ctx, a.root), "capabilities": languages.Capabilities(), "external_tools": tools, "semantic_indexing": "unavailable: external tool presence does not enable semantic analysis", "import_resolution": "inferred from language path conventions (relative TS/JS, Python packages, go.mod modules, Rust crate/self/super paths)", "telemetry": false, "cloud_inference": false, "git": info}
	if e != nil {
		result["git_diagnostic"] = e.Error()
	}
	if info.Root != "" && filepath.Clean(info.Root) != a.root {
		result["git_diagnostic"] = "selected root is inside a larger Git repository; checkpoint analysis requires repository top-level root"
	}
	a.report(result, func(w io.Writer) {
		p := paletteOf(w)
		line := func(label, value string) {
			fmt.Fprintf(w, "  %s%s %s\n", p.bold(label), strings.Repeat(" ", max(0, 14-len(label))), value)
		}
		available := func(n string) bool { return tools[n].(map[string]any)["available"].(bool) }
		toolList := func(list []string) string {
			parts := []string{}
			for _, n := range list {
				if available(n) {
					parts = append(parts, p.mark("✓")+" "+n)
				} else {
					parts = append(parts, p.mark("✗")+" "+p.dim(n))
				}
			}
			return strings.Join(parts, "  ")
		}
		fmt.Fprintf(w, "%s %s\n\n", brand(p, "Radar doctor"), p.dim(Version))
		if info.Root != "" {
			state := "clean"
			if info.Dirty {
				state = p.yellow("uncommitted changes")
			}
			line("Repository", fmt.Sprintf("%s @ %s (%s)", p.cyan(info.Branch), short(info.Head), state))
		} else {
			line("Repository", p.mark("✗")+" not a Git repository")
		}
		if d, ok := result["git_diagnostic"]; ok {
			line("Git note", fmt.Sprintf("%s %v", p.mark("!"), d))
		}
		line("Root", a.root)
		if stateErr == nil {
			line("State", a.stateRoot+"/.radar")
		} else {
			line("State", "not initialized "+p.dim("(radar gate works without it; radar init enables plan, evidence and graph commands)"))
		}
		line("Languages", "TypeScript/JavaScript, Python, Go, Rust "+p.dim("(structural parsing; no semantic analysis)"))
		line("Git", toolList([]string{"git"}))
		line("Test runners", toolList([]string{"python3", "node", "go", "cargo"}))
		line("Index tools", toolList([]string{"rust-analyzer", "scip-typescript", "scip-python"})+" "+p.dim("(detected only; they do not enable semantic analysis)"))
		line("Privacy", "no telemetry, no cloud inference")
	})
	return 0
}
