package typecheck

import (
	"context"
	"reflect"
	"testing"

	"github.com/Jake-Network/radar/internal/indexer"
)

func tsEntryMap(entries []indexer.Entry) map[string]indexer.Entry {
	byPath := map[string]indexer.Entry{}
	for _, entry := range entries {
		byPath[entry.Path] = entry
	}
	return byPath
}

func TestSelectTypeScriptNearestConfiguration(t *testing.T) {
	files := tree{
		"tsconfig.json": "{}", "app/a.ts": "", "app/b.tsx": "", "app/types.d.ts": "",
		"pkg/tsconfig.json": "{}", "pkg/lib.mts": "", "pkg/lib.cts": "",
		"node_modules/dependency/index.ts": "",
	}
	entries, _ := files.Entries(context.Background())
	byPath := tsEntryMap(entries)
	read := func(name string) ([]byte, bool) { b, ok := files[name]; return []byte(b), ok }
	plan := Plan{}
	selectTypeScript(byPath, entries, read, []string{"app/b.tsx", "pkg/lib.cts", "app/a.ts", "pkg/lib.mts", "app/gone.ts", "node_modules/dependency/index.ts"}, &plan)
	if len(plan.Targets) != 2 {
		t.Fatalf("targets: %+v", plan.Targets)
	}
	if plan.Targets[0].Tool != ToolTSC || plan.Targets[0].CWD != "." || !reflect.DeepEqual(plan.Targets[0].Changed, []string{"app/a.ts", "app/b.tsx"}) || !reflect.DeepEqual(plan.Targets[1].Changed, []string{"pkg/lib.cts", "pkg/lib.mts"}) {
		t.Fatalf("targets: %+v", plan.Targets)
	}
	if len(plan.Gaps) != 1 || plan.Gaps[0].Path != "node_modules/dependency/index.ts" {
		t.Fatalf("gaps: %+v", plan.Gaps)
	}
	if !reflect.DeepEqual(plan.Targets[0].Command, []string{"node", "node_modules/typescript/bin/tsc", "--noEmit", "--pretty", "false", "-p", "tsconfig.json"}) {
		t.Fatalf("command: %q", plan.Targets[0].Command)
	}
}

func TestSelectTypeScriptMissingAndUnsupportedConfigurations(t *testing.T) {
	cases := []struct{ name, config string }{
		{"project references", `{"references":[{"path":"./lib"}]}`},
		{"composite", `{"compilerOptions":{"composite":true}}`},
		{"malformed", `{`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			files := tree{"tsconfig.json": tc.config, "a.ts": ""}
			entries, _ := files.Entries(context.Background())
			plan := Plan{}
			selectTypeScript(tsEntryMap(entries), entries, func(name string) ([]byte, bool) { b, ok := files[name]; return []byte(b), ok }, []string{"a.ts"}, &plan)
			if len(plan.Gaps) != 1 || plan.Gaps[0].Path != "a.ts" || len(plan.Targets) != 0 {
				t.Fatalf("plan: %+v", plan)
			}
		})
	}
	files := tree{"a.ts": ""}
	entries, _ := files.Entries(context.Background())
	plan := Plan{}
	selectTypeScript(tsEntryMap(entries), entries, func(string) ([]byte, bool) { return nil, false }, []string{"a.ts"}, &plan)
	if len(plan.Gaps) != 1 || len(plan.Targets) != 0 {
		t.Fatalf("unconfigured TypeScript: %+v", plan)
	}
}

func TestSelectTypeScriptConfigurationChangeChecksOwnedSources(t *testing.T) {
	files := tree{"tsconfig.json": `{// comment
"compilerOptions":{"strict":true,},}`, "a.ts": "", "pkg/tsconfig.json": "{}", "pkg/p.ts": ""}
	entries, _ := files.Entries(context.Background())
	plan := Plan{}
	selectTypeScript(tsEntryMap(entries), entries, func(name string) ([]byte, bool) { b, ok := files[name]; return []byte(b), ok }, []string{"tsconfig.json"}, &plan)
	if len(plan.Targets) != 1 || !reflect.DeepEqual(plan.Targets[0].Changed, []string{"a.ts"}) {
		t.Fatalf("plan: %+v", plan)
	}
}
