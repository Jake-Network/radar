package workspace

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

func teamFixture() *TeamFile {
	return &TeamFile{Version: 1, Repos: []TeamRepo{{ID: "orders", Identity: "git:" + strings.Repeat("a", 40), Base: "main"}, {ID: "payments", Identity: "git:" + strings.Repeat("b", 40)}}, Links: []Link{{ID: "order-response", Producer: "orders:openapi.json#/components/schemas/Order", Consumer: "payments", Direction: "response"}}}
}

func TestParseProducer(t *testing.T) {
	for _, v := range []string{"orders:openapi.json#/Order", "orders:api/schema.yaml", "orders:api/./schema.json#"} {
		if _, err := ParseProducer(v); err != nil {
			t.Errorf("%s: %v", v, err)
		}
	}
	for _, v := range []string{"schema.json", ":x#/a", "a:../x#/a", "a:/x#/a", "a:a\\b#/a", "a:x#scalar", "a:x#/~2", "a:.", "a:C:/x#"} {
		if _, err := ParseProducer(v); err == nil {
			t.Errorf("accepted %q", v)
		}
	}
}

func TestParseTeamStrict(t *testing.T) {
	b, _ := json.Marshal(teamFixture())
	parsed, err := ParseTeam(b)
	if err != nil || len(parsed.Links) != 1 {
		t.Fatal(parsed, err)
	}
	tests := map[string]func(*TeamFile){
		"version":          func(f *TeamFile) { f.Version = 2 },
		"duplicate repo":   func(f *TeamFile) { f.Repos = append(f.Repos, f.Repos[0]) },
		"identity":         func(f *TeamFile) { f.Repos[0].Identity = "remote:https://example.com" },
		"missing producer": func(f *TeamFile) { f.Links[0].Producer = "other:x" },
		"missing consumer": func(f *TeamFile) { f.Links[0].Consumer = "other" },
		"duplicate link":   func(f *TeamFile) { f.Links = append(f.Links, f.Links[0]) },
		"direction":        func(f *TeamFile) { f.Links[0].Direction = "both" },
		"base":             func(f *TeamFile) { f.Repos[0].Base = "--help" },
	}
	for name, change := range tests {
		t.Run(name, func(t *testing.T) {
			f := teamFixture()
			change(f)
			b, _ := json.Marshal(f)
			if _, err := ParseTeam(b); err == nil {
				t.Fatal("accepted invalid team")
			}
		})
	}
	for _, v := range []string{string(b) + "{}", `{"version":1,"repos":[],"scenarios":[]}`, `{"version":1,"repos":[{"id":"a","identity":"git:aaaa","extra":1}],"links":[]}`, `null`} {
		if _, err := ParseTeam([]byte(v)); err == nil {
			t.Fatalf("accepted %s", v)
		}
	}
	f := teamFixture()
	f.Links[0].Direction = ""
	b, _ = json.Marshal(f)
	if _, err := ParseTeam(b); err != nil {
		t.Fatal("legacy direction should remain optional", err)
	}
}

func TestParseConsumesStrict(t *testing.T) {
	good := `{"version":1,"consumes":[{"contract":"order-response","fields":["total","items[].id"],"source":"src/client.ts"}]}`
	if _, err := ParseConsumes([]byte(good)); err != nil {
		t.Fatal(err)
	}
	for _, v := range []string{
		`{"version":2,"consumes":[]}`,
		`{"version":1,"consumes":[{"contract":"a","fields":[]}]}`,
		`{"version":1,"consumes":[{"contract":"a","fields":["a","a"]}]}`,
		`{"version":1,"consumes":[{"contract":"a","fields":["a..b"]}]}`,
		`{"version":1,"consumes":[{"contract":"a","fields":["a"],"source":"../secret"}]}`,
		`{"version":1,"consumes":[{"contract":"a","fields":["a"],"unknown":true}]}`,
		`{"version":1,"consumes":[{"contract":"a","fields":["a"]},{"contract":"a","fields":["b"]}]}`,
	} {
		if _, err := ParseConsumes([]byte(v)); err == nil {
			t.Fatalf("accepted %s", v)
		}
	}
}

func TestResolveTeamScope(t *testing.T) {
	f := teamFixture()
	s := Scope{Workspace: "shop", Key: "shop", Current: "orders", Source: SourceRegistry, Repos: []ScopeRepo{{ID: "orders", Origin: SourceRegistry}, {ID: "payments", Origin: SourceRegistry}, {ID: "extra", Origin: SourceRegistry}, {ID: "adhoc", Origin: SourceWith}}, Only: []string{"orders"}, OneOff: true}
	identities := map[string]string{"orders": f.Repos[0].Identity, "payments": f.Repos[1].Identity}
	member := func(r ScopeRepo, identity string) bool { return identities[r.ID] == identity }
	out, err := ResolveTeamScope(s, f, member)
	if err != nil {
		t.Fatal(err)
	}
	if out.Source != SourceTeamFile || len(out.Repos) != 1 || out.Repos[0].TeamBase != "main" || len(out.All) != 3 || len(out.Excluded) != 1 || out.Excluded[0].ID != "extra" || !out.Partial() || !out.OneOff {
		t.Fatalf("wrong scope %+v", out)
	}
	if len(s.Repos) != 4 {
		t.Fatal("mutated input scope")
	}
	delete(identities, "payments")
	if _, err := ResolveTeamScope(s, f, member); err == nil {
		t.Fatal("identity mismatch hidden by --only")
	}
	// A team repo that is unregistered or moved is reported as missing, and
	// --only can leave it out, instead of stopping the run.
	s.Repos = s.Repos[:1]
	s.Only = []string{"orders", "orders"}
	out, err = ResolveTeamScope(s, f, member)
	if err != nil || len(out.Repos) != 1 || len(out.All) != 2 || !slices.Equal(out.Only, []string{"orders"}) {
		t.Fatalf("unregistered team repo excluded by --only: %+v %v", out, err)
	}
	s.Only = nil
	if out, err = ResolveTeamScope(s, f, member); err != nil || len(out.Repos) != 2 || out.Repos[1].ID != "payments" || out.Repos[1].Missing == "" {
		t.Fatalf("unregistered team repo: %+v %v", out, err)
	}
	identities["payments"] = f.Repos[1].Identity
	s.Repos = append(s.Repos, ScopeRepo{ID: "payments", Origin: SourceRegistry, Missing: "path /gone is not available"})
	if out, err = ResolveTeamScope(s, f, member); err != nil || out.Repos[1].Missing != "path /gone is not available" {
		t.Fatalf("moved team repo: %+v %v", out, err)
	}
	s.Repos[1].Missing = ""
	s.Only = []string{"extra"}
	if _, err := ResolveTeamScope(s, f, member); err == nil {
		t.Fatal("--only selected excluded registry member")
	}
}

func TestRegistryHome(t *testing.T) {
	w := Workspace{Name: "shop", Home: "orders", Repos: []Repo{{ID: "orders", Path: "/orders", CommonDir: "/orders/.git"}, {ID: "payments", Path: "/payments", CommonDir: "/payments/.git"}}}
	r := Registry{Version: 1, Workspaces: []Workspace{w}}
	if err := r.Validate(); err != nil {
		t.Fatal(err)
	}
	r.Workspaces[0].Home = "missing"
	if err := r.Validate(); err == nil {
		t.Fatal("accepted unregistered home")
	}
	r.Workspaces[0].Home = "orders"
	if _, _, err := r.Remove(Location{CommonDir: "/orders/.git"}, "orders"); err != nil {
		t.Fatal(err)
	}
	if r.Workspaces[0].Home != "" {
		t.Fatal("removing home left stale ID")
	}
	r.Workspaces[0].Home = ""
	b, _ := json.Marshal(r)
	if strings.Contains(string(b), `"home"`) {
		t.Fatal("old registry format changed")
	}
}

func TestResolveTeamScopeWithoutTeamPreservesOnly(t *testing.T) {
	s := Scope{All: []string{"orders", "payments"}, Repos: []ScopeRepo{{ID: "orders"}, {ID: "payments"}}, Only: []string{"payments"}, Source: SourceRegistry}
	out, err := ResolveTeamScope(s, nil, nil)
	if err != nil || len(out.Repos) != 1 || out.Repos[0].ID != "payments" || out.Source != SourceRegistry {
		t.Fatal(out, err)
	}
}
