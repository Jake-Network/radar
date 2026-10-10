package composition

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Jake-Network/radar/internal/contracts"
	gitrepo "github.com/Jake-Network/radar/internal/git"
	"github.com/Jake-Network/radar/internal/integration"
	"github.com/Jake-Network/radar/internal/model"
	"github.com/Jake-Network/radar/internal/workspace"
)

func teamFixture(t *testing.T) (*fixture, string, string, *Team, workspace.Scope) {
	t.Helper()
	ctx := context.Background()
	f := newFixture(t)
	orders := f.repo("orders", map[string]string{"schema.json": `{"type":"object","properties":{"total":{"type":"number"},"status":{"type":"string"}}}`})
	payments := f.repo("payments", map[string]string{workspace.ConsumesPath: `{"version":1,"consumes":[{"contract":"order","fields":["total"],"source":"missing.ts"}]}`})
	file := workspace.TeamFile{Version: 1, Repos: []workspace.TeamRepo{{ID: "orders", Identity: gitrepo.Identity(ctx, orders)}, {ID: "payments", Identity: gitrepo.Identity(ctx, payments)}}, Links: []workspace.Link{{ID: "order", Producer: "orders:schema.json#", Consumer: "payments", Direction: "response"}}}
	data, _ := json.Marshal(file)
	write(t, orders, workspace.TeamPath, string(data))
	git(t, orders, "add", ".")
	git(t, orders, "commit", "-qm", "team")
	scope := scopeOf(t, orders, payments)
	team, err := LoadTeam(ctx, scope, "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	return f, orders, payments, team, scope
}

func TestTeamCommittedSnapshotAndReplay(t *testing.T) {
	_, orders, _, team, scope := teamFixture(t)
	ctx := context.Background()
	write(t, orders, workspace.TeamPath, `{"bad":"working tree"}`)
	dirty, err := LoadTeam(ctx, scope, "", nil, nil)
	if err != nil || !dirty.Uncommitted || dirty.Digest != team.Digest {
		t.Fatalf("dirty %+v %v", dirty, err)
	}
	git(t, orders, "add", ".")
	git(t, orders, "commit", "-qm", "broken current team")
	replay, err := LoadTeam(ctx, scope, "", nil, &Record{Team: team})
	if err != nil || replay.Blob != team.Blob {
		t.Fatalf("replay %+v %v", replay, err)
	}
	if _, err := LoadTeam(ctx, scope, "", nil, nil); err == nil {
		t.Fatal("current malformed team accepted")
	}
}

func TestTeamCacheLinksAndObligations(t *testing.T) {
	f, orders, _, team, scope := teamFixture(t)
	ctx := context.Background()
	f.agent(orders, "agent/remove", map[string]string{"schema.json": `{"type":"object","properties":{"status":{"type":"string"}}}`})
	repos, err := Collect(ctx, Request{Scope: scope})
	if err != nil {
		t.Fatal(err)
	}
	results := Build(ctx, repos, integration.Options{}, team)
	links := CheckLinks(results, team)
	if len(links) != 1 || links[0].Status != model.StatusFailed || links[0].Cells[0].Status != model.StatusPassed || len(links[0].Warnings) != 1 {
		t.Fatalf("links %+v", links)
	}
	if Digest(results) != Digest(results, nil) || Digest(results) == Digest(results, team) {
		t.Fatal("optional team digest changed stage1 or omitted team")
	}
	rec := NewRecord(scope, Selection{}, results, Digest(results, team), "FAIL", team)
	if rec.Team == nil || rec.Team.File != nil || rec.Team.Blob != team.Blob {
		t.Fatalf("record %+v", rec)
	}
	// Removing a declaration must retain its base obligation.
	candidate := *team.File
	candidate.Links = nil
	data, _ := json.Marshal(candidate)
	for i := range results {
		if results[i].ID == team.Home {
			value := results[i].Files[workspace.TeamPath]
			value.Candidate = data
			results[i].Files[workspace.TeamPath] = value
		}
	}
	links = CheckLinks(results, team)
	if links[0].Status != model.StatusIncomplete || len(links[0].Obligations) == 0 {
		t.Fatalf("removed declaration %+v", links)
	}
}

func TestTeamLinkOutsideScope(t *testing.T) {
	_, _, _, team, scope := teamFixture(t)
	ctx := context.Background()
	scope.Repos = scope.Repos[:1]
	repos, err := Collect(ctx, Request{Scope: scope})
	if err != nil {
		t.Fatal(err)
	}
	links := CheckLinks(Build(ctx, repos, integration.Options{}, team), team)
	if links[0].Status != model.StatusIncomplete || !strings.Contains(links[0].Cells[3].Reason, "outside") {
		t.Fatalf("links %+v", links)
	}
}

func TestTeamSemanticDigestRepoOrder(t *testing.T) {
	file := &workspace.TeamFile{Version: 1, Repos: []workspace.TeamRepo{{ID: "a"}, {ID: "b"}}, Links: []workspace.Link{{ID: "a"}, {ID: "b"}}, Retired: []contracts.Retirement{}}
	before := teamDigest(file)
	file.Repos[0], file.Repos[1] = file.Repos[1], file.Repos[0]
	file.Links[0], file.Links[1] = file.Links[1], file.Links[0]
	if before != teamDigest(file) {
		t.Fatal("ordering changed semantic digest")
	}
}

func TestTeamCandidateAddsLinkInAnotherRepo(t *testing.T) {
	f, orders, payments, team, scope := teamFixture(t)
	ctx := context.Background()
	// A newly declared reverse-direction link must read its producer document
	// from payments, although that path was absent from the base team inventory.
	write(t, payments, "invoice.json", `{"type":"object","properties":{"id":{"type":"integer"}}}`)
	git(t, payments, "add", ".")
	git(t, payments, "commit", "-qm", "invoice")
	added := *team.File
	added.Links = append(append([]workspace.Link{}, added.Links...), workspace.Link{ID: "invoice", Producer: "payments:invoice.json#", Consumer: "orders", Direction: "response"})
	data, _ := json.Marshal(added)
	f.agent(orders, "agent/link", map[string]string{workspace.TeamPath: string(data), workspace.ConsumesPath: `{"version":1,"consumes":[{"contract":"invoice","fields":["id"]}]}`})
	repos, err := Collect(ctx, Request{Scope: scope})
	if err != nil {
		t.Fatal(err)
	}
	links := CheckLinks(Build(ctx, repos, integration.Options{}, team), team)
	if len(links) != 2 || links[1].Status != model.StatusPassed {
		t.Fatalf("added link %+v", links)
	}
}

func TestTeamReplayOmittedHomeAndTeamBase(t *testing.T) {
	_, orders, _, team, scope := teamFixture(t)
	ctx := context.Background()
	rec := NewRecord(scope, Selection{}, nil, "", "", team)
	only := scope
	only.Repos = only.Repos[1:]
	loaded, err := LoadTeam(ctx, only, "", nil, &rec)
	if err != nil || loaded.Blob != team.Blob {
		t.Fatalf("omitted home replay %+v %v", loaded, err)
	}
	git(t, orders, "branch", "release")
	file := *team.File
	file.Repos = append([]workspace.TeamRepo(nil), file.Repos...)
	file.Repos[0].Base = "release"
	data, _ := json.Marshal(file)
	write(t, orders, workspace.TeamPath, string(data))
	git(t, orders, "add", ".")
	git(t, orders, "commit", "-qm", "declare release base")
	loaded, err = LoadTeam(ctx, scope, "", nil, nil)
	if err != nil || loaded.Base != team.Base {
		t.Fatalf("team home base %+v %v", loaded, err)
	}
	loaded, err = LoadTeam(ctx, scope, "", map[string]string{"orders": "main"}, nil)
	if err != nil || loaded.Base == team.Base {
		t.Fatalf("CLI base must win %+v %v", loaded, err)
	}
}

func TestStageOneDigestUnchangedWithoutTeam(t *testing.T) {
	const expected = "sha256:72e614447f7cd9c12cf51b294f934b2b547f7bed7f4aa1b0e7cd9f67fa17841c"
	if got := Digest(nil); got != expected {
		t.Fatalf("legacy digest changed: %s", got)
	}
	if got := Digest(nil, nil); got != expected {
		t.Fatalf("nil team changed legacy digest: %s", got)
	}
}

func TestTeamDeclaredBaseWithoutTeamFileIsAnError(t *testing.T) {
	_, orders, _, team, scope := teamFixture(t)
	ctx := context.Background()
	// main~1 is the baseline before the team file was committed.
	git(t, orders, "branch", "before-team", "main~1")
	file := *team.File
	file.Repos = append([]workspace.TeamRepo(nil), file.Repos...)
	file.Repos[0].Base = "before-team"
	data, _ := json.Marshal(file)
	write(t, orders, workspace.TeamPath, string(data))
	git(t, orders, "add", ".")
	git(t, orders, "commit", "-qm", "declare a base without the team file")
	if loaded, err := LoadTeam(ctx, scope, "", nil, nil); err == nil || !strings.Contains(err.Error(), "that base has no "+workspace.TeamPath) {
		t.Fatalf("declared links dropped silently: %+v %v", loaded, err)
	}
}

func TestTeamCommittedOnCheckedOutBranchIsNotUncommitted(t *testing.T) {
	_, orders, _, team, scope := teamFixture(t)
	ctx := context.Background()
	git(t, orders, "checkout", "-q", "-b", "agent/links")
	changed := *team.File
	changed.Links = nil
	data, _ := json.Marshal(changed)
	write(t, orders, workspace.TeamPath, string(data))
	git(t, orders, "add", ".")
	git(t, orders, "commit", "-qm", "edit team on a branch")
	loaded, err := LoadTeam(ctx, scope, "orders", map[string]string{"orders": "main"}, nil)
	if err != nil || loaded.Uncommitted {
		t.Fatalf("committed branch edit reported as uncommitted: %+v %v", loaded, err)
	}
}

func TestTeamMembershipIgnoresTheCheckedOutBranch(t *testing.T) {
	_, _, payments, team, scope := teamFixture(t)
	ctx := context.Background()
	git(t, payments, "checkout", "-q", "--orphan", "gh-pages")
	git(t, payments, "commit", "-qm", "pages")
	if gitrepo.Identity(ctx, payments) == team.File.Repos[1].Identity {
		t.Fatal("orphan branch kept the HEAD identity; the test checks nothing")
	}
	if _, err := workspace.ResolveTeamScope(scope, team.File, Member(ctx)); err != nil {
		t.Fatal("orphan branch changed team membership:", err)
	}
	other := workspace.TeamFile{Version: 1, Repos: []workspace.TeamRepo{team.File.Repos[0], {ID: "payments", Identity: team.File.Repos[0].Identity}}}
	if _, err := workspace.ResolveTeamScope(scope, &other, Member(ctx)); err == nil || !strings.Contains(err.Error(), "identity differs") {
		t.Fatal("another repository's identity accepted", err)
	}
}

func TestTeamHomeConflictLeavesOtherLinksIncomplete(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	orders := f.repo("orders", map[string]string{"schema.json": `{"type":"object","properties":{"total":{"type":"number"}}}`})
	payments := f.repo("payments", map[string]string{workspace.ConsumesPath: `{"version":1,"consumes":[{"contract":"order","fields":["total"]}]}`})
	catalog := f.repo("catalog", map[string]string{"c.txt": "0\n"})
	file := workspace.TeamFile{Version: 1, Repos: []workspace.TeamRepo{{ID: "catalog", Identity: gitrepo.Identity(ctx, catalog)}, {ID: "orders", Identity: gitrepo.Identity(ctx, orders)}, {ID: "payments", Identity: gitrepo.Identity(ctx, payments)}}, Links: []workspace.Link{{ID: "order", Producer: "orders:schema.json#", Consumer: "payments", Direction: "response"}}}
	data, _ := json.Marshal(file)
	write(t, catalog, workspace.TeamPath, string(data))
	git(t, catalog, "add", ".")
	git(t, catalog, "commit", "-qm", "team")
	f.agent(catalog, "agent/a", map[string]string{"c.txt": "a\n"})
	f.agent(catalog, "agent/b", map[string]string{"c.txt": "b\n"})
	scope := scopeOf(t, catalog, orders, payments)
	team, err := LoadTeam(ctx, scope, "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	repos, err := Collect(ctx, Request{Scope: scope})
	if err != nil {
		t.Fatal(err)
	}
	links := CheckLinks(Build(ctx, repos, integration.Options{}, team), team)
	if len(links) != 1 || links[0].Status != model.StatusIncomplete || links[0].Cells[3].Reason != "team home catalog has no candidate (conflict)" || len(links[0].Obligations) != 0 {
		t.Fatalf("home conflict reported as a removed declaration: %+v", links)
	}
}

func TestTeamBaseRetirementCoversLaterNarrowing(t *testing.T) {
	f, orders, payments, team, scope := teamFixture(t)
	ctx := context.Background()
	write(t, payments, workspace.ConsumesPath, `{"version":1,"consumes":[{"contract":"order","fields":["total","status"]}]}`)
	git(t, payments, "add", ".")
	git(t, payments, "commit", "-qm", "consume status")
	f.agent(payments, "agent/narrow", map[string]string{workspace.ConsumesPath: `{"version":1,"consumes":[{"contract":"order","fields":["total"]}]}`})
	check := func() Link {
		t.Helper()
		team, err := LoadTeam(ctx, scope, "", nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		repos, err := Collect(ctx, Request{Scope: scope})
		if err != nil {
			t.Fatal(err)
		}
		return CheckLinks(Build(ctx, repos, integration.Options{}, team), team)[0]
	}
	if l := check(); l.Status != model.StatusIncomplete || !strings.Contains(l.Cells[3].Reason, "without an explicit retirement record") {
		t.Fatalf("unretired narrowing %+v", l)
	}
	// The home PR that retires status merges first; the consumer merges later.
	retired := *team.File
	retired.Retired = []contracts.Retirement{{ID: "order", Fields: []string{"status"}, Reason: "payments stops reading status"}}
	data, _ := json.Marshal(retired)
	write(t, orders, workspace.TeamPath, string(data))
	git(t, orders, "add", ".")
	git(t, orders, "commit", "-qm", "retire status")
	if l := check(); l.Status != model.StatusPassed {
		t.Fatalf("base retirement ignored %+v", l)
	}
}
