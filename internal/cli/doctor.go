package cli

import (
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"sort"

	gitrepo "github.com/radar-engine/radar/internal/git"
	"github.com/radar-engine/radar/internal/languages"
	"github.com/radar-engine/radar/internal/project"
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
		fmt.Fprintf(w, "Radar %s\nRoot:        %s\nState:       %s (initialized: %v)\n", Version, a.root, a.stateRoot, stateErr == nil)
		if info.Root != "" {
			fmt.Fprintf(w, "Git:         %s @ %s (dirty: %v)\n", info.Branch, info.Head, info.Dirty)
		}
		if d, ok := result["git_diagnostic"]; ok {
			fmt.Fprintf(w, "Git note:    %v\n", d)
		}
		fmt.Fprintln(w, "Languages:   TypeScript/JavaScript, Python, Go, Rust (structural; semantic analysis unavailable)")
		sort.Strings(names)
		fmt.Fprint(w, "Tools:      ")
		for _, n := range names {
			mark := "-"
			if tools[n].(map[string]any)["available"].(bool) {
				mark = "+"
			}
			fmt.Fprintf(w, " %s%s", mark, n)
		}
		fmt.Fprintln(w, "\nTelemetry:   none; cloud inference: none")
	})
	return 0
}
