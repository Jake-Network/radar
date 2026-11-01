package runners

import (
	"reflect"
	"testing"
)

func TestProfilesAllowSetupOnlyAndTypedTypechecks(t *testing.T) {
	c, err := Parse([]byte(`{"version":1,"profiles":[{"root":".","setup":[["node","prepare.js"]],"typecheck":[{"tool":"tsc","command":["node","node_modules/typescript/bin/tsc","--noEmit"],"files":["src/**/*.ts"]}]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Profiles) != 1 || len(c.Profiles[0].Typecheck) != 1 || c.Profiles[0].Typecheck[0].Tool != "tsc" {
		t.Fatalf("profiles=%+v", c.Profiles)
	}
	if got := c.Profiles[0].Rewrite([]string{"node", "--test"}); !reflect.DeepEqual(got, []string{"node", "--test"}) {
		t.Fatalf("empty prefix changed command: %v", got)
	}
}

func TestTypedTypechecksRejectUnsafeOrAmbiguousDeclarations(t *testing.T) {
	for _, decl := range []string{
		`{"tool":"unknown","command":["true"]}`,
		`{"tool":"tsc","command":[]}`,
		`{"tool":"tsc","command":["/bin/sh"]}`,
		`{"tool":"tsc","command":["node"],"files":["../outside.ts"]}`,
		`{"tool":"tsc","command":["node"],"files":["/outside.ts"]}`,
		`{"tool":"tsc","command":["node"],"files":["src/[bad.ts"]}`,
		`{"tool":"tsc","command":["node"],"unknown":true}`,
	} {
		if _, err := Parse([]byte(`{"version":1,"profiles":[{"root":".","typecheck":[` + decl + `]}]}`)); err == nil {
			t.Errorf("accepted %s", decl)
		}
	}
	if _, err := Parse([]byte(`{"version":1,"profiles":[{"root":".","typecheck":[{"tool":"tsc","command":["node"]},{"tool":"tsc","command":["node"]}]}]}`)); err == nil {
		t.Fatal("duplicate tool accepted")
	}
}

func TestTypecheckPrefixDoesNotAppendTestExclusions(t *testing.T) {
	p := Profile{Root: ".", Prefix: []string{"uv", "run", "--frozen"}, Exclude: []string{"--ignore=tests/e2e"}}
	got := p.RewriteTypecheck([]string{"python3", "-m", "mypy", "."})
	want := []string{"uv", "run", "--frozen", "python3", "-m", "mypy", "."}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("rewritten=%v want=%v", got, want)
	}
}
