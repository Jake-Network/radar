package cli

import (
	gitrepo "github.com/radar-engine/radar/internal/git"
	"github.com/radar-engine/radar/internal/languages"
	"os/exec"
	"path/filepath"
)

func (a *app) doctor() int {
	info, e := gitrepo.Inspect(a.ctx, a.root)
	tools := map[string]any{}
	for _, tool := range []string{"git", "node", "python3", "go", "cargo", "rust-analyzer", "scip-typescript", "scip-python"} {
		p, e := exec.LookPath(tool)
		tools[tool] = map[string]any{"available": e == nil, "path": p}
	}
	result := map[string]any{"version": Version, "root": a.root, "capabilities": languages.Capabilities(), "external_tools": tools, "semantic_indexing": "unavailable: external tool presence does not enable semantic analysis", "telemetry": false, "cloud_inference": false, "git": info}
	if e != nil {
		result["git_diagnostic"] = e.Error()
	}
	if info.Root != "" && filepath.Clean(info.Root) != a.root {
		result["git_diagnostic"] = "selected root is inside a larger Git repository; checkpoint analysis requires repository top-level root"
	}
	a.emit(result)
	return 0
}
