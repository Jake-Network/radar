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
	if rec.Team == nil || rec.Team.File != nil || rec.Team.Blob != team.Blob || len(rec.Links) != 1 {
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
