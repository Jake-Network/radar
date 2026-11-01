package typecheck

import (
	"encoding/json"
	"path"
	"sort"
	"strings"

	"github.com/Jake-Network/radar/internal/indexer"
	"github.com/Jake-Network/radar/internal/model"
)

// ToolTSC identifies TypeScript compiler diagnostics, rather than test output.
const ToolTSC = "tsc"

func typeScriptSource(file string) bool {
	switch path.Ext(file) {
	case ".ts", ".tsx", ".mts", ".cts":
		return true
	}
	return false
}

func typeScriptConfigName(file string) bool {
	name := path.Base(file)
	return name == "jsconfig.json" || strings.HasPrefix(name, "tsconfig") && strings.HasSuffix(name, ".json")
}

// selectTypeScript chooses whole configured projects, so an unchanged caller
// of a renamed declaration is checked as well. The compiler's actual input
// inventory is verified at execution time; nearest configuration alone is
// not evidence that its include/exclude settings cover a changed file.
func selectTypeScript(byPath map[string]indexer.Entry, entries []indexer.Entry, read func(string) ([]byte, bool), changed []string, plan *Plan) {
	exists := func(file string) bool { _, ok := byPath[file]; return ok }
	selected := map[string]*Target{}
	selectFile := func(file string) {
		if indexer.ExcludedPath(file) {
			if exists(file) {
				plan.Gaps = append(plan.Gaps, Gap{Path: file, Reason: "inside a dependency or generated directory, which Radar does not type-check"})
			}
			return
		}
		config, ok := indexer.NearestTypeScriptConfig(file, exists)
		if !ok {
			if exists(file) {
				plan.Gaps = append(plan.Gaps, Gap{Path: file, Reason: "no tsconfig.json or jsconfig.json encloses this TypeScript file"})
			}
			return
		}
		if reason := typeScriptConfigProblem(config, read, map[string]bool{}); reason != "" {
			plan.Gaps = append(plan.Gaps, Gap{Path: file, Reason: reason})
			return
		}
		target := selected[config]
		if target == nil {
			dir := path.Dir(config)
			target = &Target{ID: model.StableID("typecheck", ToolTSC, config), Tool: ToolTSC, Root: dir, CWD: dir, Command: []string{"node", "node_modules/typescript/bin/tsc", "--noEmit", "--pretty", "false", "-p", path.Base(config)}}
			selected[config] = target
		}
		if exists(file) && !contains(target.Changed, file) {
			target.Changed = append(target.Changed, file)
		}
	}
	for _, file := range changed {
		if typeScriptSource(file) {
			selectFile(file)
			continue
		}
		if !typeScriptConfigName(file) || indexer.ExcludedPath(file) {
			continue
		}
		if !exists(file) {
			plan.Gaps = append(plan.Gaps, Gap{Path: file, Reason: "deleted TypeScript configuration; its previous project inputs are unavailable"})
		}
		for _, entry := range entries {
			if !typeScriptSource(entry.Path) || indexer.ExcludedPath(entry.Path) {
				continue
			}
			config, ok := indexer.NearestTypeScriptConfig(entry.Path, exists)
			if ok && typeScriptConfigUses(config, file, read, map[string]bool{}) {
				selectFile(entry.Path)
			}
		}
	}
	configs := make([]string, 0, len(selected))
	for config := range selected {
		configs = append(configs, config)
	}
	sort.Slice(configs, func(i, j int) bool {
		a, b := path.Dir(configs[i]), path.Dir(configs[j])
		return a < b || a == b && configs[i] < configs[j]
	})
	for _, config := range configs {
		target := selected[config]
		sort.Strings(target.Changed)
		plan.Targets = append(plan.Targets, *target)
	}
}

type typeScriptConfig struct {
	Extends         json.RawMessage `json:"extends"`
	References      json.RawMessage `json:"references"`
	CompilerOptions struct {
		Composite bool `json:"composite"`
	} `json:"compilerOptions"`
}

func typeScriptExtends(config string, raw json.RawMessage, read func(string) ([]byte, bool)) []string {
	var one string
	var many []string
	if json.Unmarshal(raw, &one) == nil && one != "" {
		many = []string{one}
	} else {
		_ = json.Unmarshal(raw, &many)
	}
	var configs []string
	for _, parent := range many {
		if !strings.HasPrefix(parent, "./") && !strings.HasPrefix(parent, "../") {
			continue
		}
		parent = path.Join(path.Dir(config), parent)
		if parent == ".." || strings.HasPrefix(parent, "../") {
			continue
		}
		if _, ok := read(parent); !ok {
			if _, ok := read(parent + ".json"); ok {
				parent += ".json"
			} else {
				parent = path.Join(parent, "tsconfig.json")
			}
		}
		configs = append(configs, parent)
	}
	return configs
}

func typeScriptConfigProblem(config string, read func(string) ([]byte, bool), seen map[string]bool) string {
	if seen[config] || len(seen) >= 16 {
		return "TypeScript configuration inheritance is cyclic or exceeds the analysis limit"
	}
	seen[config] = true
	defer delete(seen, config)
	content, ok := read(config)
	var settings typeScriptConfig
	if !ok || indexer.UnmarshalJSONC(content, &settings) != nil {
		return "TypeScript configuration could not be read"
	}
	var refs []json.RawMessage
	if len(settings.References) > 0 && json.Unmarshal(settings.References, &refs) != nil {
		return "TypeScript project references could not be read"
	}
	if len(refs) > 0 || settings.CompilerOptions.Composite {
		return "TypeScript project references and composite projects are not supported"
	}
	for _, parent := range typeScriptExtends(config, settings.Extends, read) {
		if reason := typeScriptConfigProblem(parent, read, seen); reason != "" {
			return reason
		}
	}
	return ""
}

func typeScriptConfigUses(config, changed string, read func(string) ([]byte, bool), seen map[string]bool) bool {
	if config == changed {
		return true
	}
	if seen[config] || len(seen) >= 16 {
		return false
	}
	seen[config] = true
	content, ok := read(config)
	var settings typeScriptConfig
	if !ok || indexer.UnmarshalJSONC(content, &settings) != nil {
		return false
	}
	for _, parent := range typeScriptExtends(config, settings.Extends, read) {
		if typeScriptConfigUses(parent, changed, read, seen) {
			return true
		}
	}
	return false
}
