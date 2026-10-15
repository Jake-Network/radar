package composition

import (
	"context"
	"encoding/json"
	"github.com/Jake-Network/radar/internal/workspace"
	"os"
	"path/filepath"
	"testing"

	"github.com/Jake-Network/radar/internal/model"
)

func TestPairwiseDeclaredContractFailureAndRepair(t *testing.T) {
	f, producer, consumer, team, scope := teamFixture(t)
	f.agent(producer, "api/remove", map[string]string{"schema.json": `{"type":"object","properties":{"status":{"type":"string"}}}`})
	f.agent(consumer, "client/change", map[string]string{"consumer.ts": "export const version = 2;\n"})
	repos, err := Collect(context.Background(), Request{Scope: scope})
	if err != nil {
		t.Fatal(err)
	}
	before := git(t, producer, "status", "--porcelain")
	report, results, err := Pairwise(context.Background(), repos, team, 10, false)
	if err != nil || len(report.Pairs) != 1 || report.Status != model.StatusFailed || len(results) != 2 {
		t.Fatalf("%+v %v", report, err)
	}
	pair := report.Pairs[0]
	if pair.Inputs[0].Base == "" || pair.Inputs[1].Tree == "" || len(pair.Links) != 1 || pair.Links[0].Status != model.StatusFailed {
		t.Fatalf("unpinned pair %+v", pair)
	}
	if git(t, producer, "status", "--porcelain") != before {
		t.Fatal("source modified")
	}
	// A compatible producer addition is independently safe for the same consumer.
	f.agent(producer, "api/add", map[string]string{"schema.json": `{"type":"object","properties":{"total":{"type":"number"},"status":{"type":"string"},"other":{"type":"string"}}}`})
	repos, err = Collect(context.Background(), Request{Scope: scope, Named: map[string][]string{"orders": {"api/add"}, "payments": {"client/change"}}})
	if err != nil {
		t.Fatal(err)
	}
	report, _, err = Pairwise(context.Background(), repos, team, 10, false)
	if err != nil || report.Status != model.StatusPassed {
		t.Fatalf("compatible pair %+v %v", report, err)
	}
}

func TestPairwiseDirtyContractDriftIsStaticAndPinned(t *testing.T) {
	f, producer, consumer, team, scope := teamFixture(t)
	api := f.agent(producer, "api/work", map[string]string{"touch.txt": "api\n"})
	f.agent(consumer, "client/work", map[string]string{"touch.txt": "client\n"})
	repos, err := Collect(context.Background(), Request{Scope: scope})
	if err != nil {
		t.Fatal(err)
	}
	clean, _, err := Pairwise(context.Background(), repos, team, 10, false)
	if err != nil || clean.Status != model.StatusPassed {
		t.Fatalf("clean %+v %v", clean, err)
	}
	write(t, api, "schema.json", `{"type":"object","properties":{"status":{"type":"string"}}}`)
	before := git(t, api, "diff")
	dirty, _, err := Pairwise(context.Background(), repos, team, 10, true)
	if err != nil || dirty.Status != model.StatusFailed || dirty.Pairs[0].Inputs[0].DirtyDigest == "" {
		t.Fatalf("dirty %+v %v", dirty, err)
	}
	if git(t, api, "diff") != before {
		t.Fatal("dirty input modified")
	}
	if len(dirty.Pairs[0].Links[0].Cells) != 4 {
		t.Fatal("existing link matrix was not reused")
	}
}

func TestPairwiseUnknownBudgetAndMissingInputs(t *testing.T) {
	f, producer, consumer, team, scope := teamFixture(t)
	f.agent(producer, "a", map[string]string{"a.txt": "a"})
	f.agent(producer, "b", map[string]string{"b.txt": "b"})
	f.agent(consumer, "c", map[string]string{"c.txt": "c"})
	repos, err := Collect(context.Background(), Request{Scope: scope})
	if err != nil {
		t.Fatal(err)
	}
	report, _, err := Pairwise(context.Background(), repos, team, 1, false)
	if err != nil || report.Complete || len(report.Omitted) != 1 || report.Status != model.StatusIncomplete {
		t.Fatalf("budget %+v %v", report, err)
	}
	report, _, err = Pairwise(context.Background(), repos, nil, 10, false)
	if err != nil || report.Status != model.StatusIncomplete || report.Pairs[0].Status != model.StatusUnknown {
		t.Fatalf("undeclared %+v %v", report, err)
	}
	repos[1].ScopeRepo.Missing = "unregistered checkout"
	if _, _, err := Pairwise(context.Background(), repos, team, 10, false); err == nil {
		t.Fatal("missing repository passed")
	}
}

func TestPairwiseRejectsMovedPins(t *testing.T) {
	f, producer, consumer, team, scope := teamFixture(t)
	f.agent(producer, "a", map[string]string{"a.txt": "a"})
	f.agent(consumer, "b", map[string]string{"b.txt": "b"})
	repos, err := Collect(context.Background(), Request{Scope: scope})
	if err != nil {
		t.Fatal(err)
	}
	git(t, producer, "update-ref", "refs/heads/a", repos[0].Base)
	if _, _, err := Pairwise(context.Background(), repos, team, 10, false); err == nil {
		t.Fatal("moved branch accepted")
	}
}

func TestPairwiseIDLFormatsReuseDeclaredLinkCells(t *testing.T) {
	for _, tc := range []struct{ name, path, before, after, field string }{
		{"protobuf", "wire.proto", `message Item { string total = 1; }`, `message Item { string other = 2; }`, "Item.total"},
		{"graphql", "wire.graphql", `type Query { total: String }`, `type Query { other: String }`, "Query.total"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, producer, consumer, team, scope := teamFixture(t)
			team.File.Links[0].Producer = "orders:" + tc.path + "#"
			declaration, _ := json.Marshal(team.File)
			write(t, producer, workspace.TeamPath, string(declaration))
			write(t, producer, tc.path, tc.before)
			git(t, producer, "add", ".")
			git(t, producer, "commit", "-qm", "protocol baseline")
			write(t, consumer, workspace.ConsumesPath, `{"version":1,"consumes":[{"contract":"order","fields":["`+tc.field+`"]}]}`)
			git(t, consumer, "add", ".")
			git(t, consumer, "commit", "-qm", "declared protocol fields")
			team, err := LoadTeam(context.Background(), scope, "", nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			f.agent(producer, "api/remove", map[string]string{tc.path: tc.after})
			f.agent(consumer, "client/work", map[string]string{"client.txt": "changed"})
			repos, err := Collect(context.Background(), Request{Scope: scope})
			if err != nil {
				t.Fatal(err)
			}
			report, _, err := Pairwise(context.Background(), repos, team, 10, false)
			if err != nil || report.Status != model.StatusFailed || len(report.Pairs[0].Links[0].Cells) != 4 {
				t.Fatalf("%+v %v", report, err)
			}
		})
	}
}

func TestPairwiseRejectsUnboundedOrAmbiguousInputs(t *testing.T) {
	for _, budget := range []int{-1, 2017} {
		if _, _, err := Pairwise(context.Background(), nil, nil, budget, false); err == nil {
			t.Fatal("unbounded budget accepted")
		}
	}
	repos := []Repo{{ScopeRepo: workspace.ScopeRepo{ID: "same"}, Base: "a"}, {ScopeRepo: workspace.ScopeRepo{ID: "same"}, Base: "a"}}
	if _, _, err := Pairwise(context.Background(), repos, nil, 10, false); err == nil {
		t.Fatal("duplicate repository IDs accepted")
	}
	repos = repos[:1]
	repos[0].Branches = make([]Branch, 65)
	if _, _, err := Pairwise(context.Background(), repos, nil, 10, false); err == nil {
		t.Fatal("too many branches accepted")
	}
}

func TestPairwiseNamedDirtyScopeExcludesUnselectedWorktree(t *testing.T) {
	f, producer, consumer, team, scope := teamFixture(t)
	selected := f.agent(producer, "api/selected", map[string]string{"selected.txt": "api"})
	unrelated := f.agent(producer, "api/unrelated", map[string]string{"other.txt": "other"})
	f.agent(consumer, "client/selected", map[string]string{"selected.txt": "client"})
	// This break must stay outside the explicitly named selection.
	write(t, unrelated, "schema.json", `{"type":"object","properties":{"status":{"type":"string"}}}`)
	write(t, selected, "selected.txt", "saved but compatible")
	repos, err := Collect(context.Background(), Request{Scope: scope, Named: map[string][]string{"orders": {"refs/heads/api/selected"}, "payments": {"client/selected"}}})
	if err != nil {
		t.Fatal(err)
	}
	report, results, err := Pairwise(context.Background(), repos, team, 10, true)
	if err != nil || report.Status != model.StatusPassed || len(report.Pairs) != 1 {
		t.Fatalf("named scope %+v %v", report, err)
	}
	for _, pair := range report.Pairs {
		for _, input := range pair.Inputs {
			if input.Ref == "api/unrelated" {
				t.Fatal("unselected dirty branch included")
			}
		}
	}
	if pairLinkStatus(CheckLinks(results, team)) != model.StatusPassed {
		t.Fatal("unselected dirty branch entered full candidate")
	}
}

func TestPairwiseSameHeadDirtyBranchesKeepDistinctIdentity(t *testing.T) {
	f, producer, consumer, team, scope := teamFixture(t)
	first := f.agent(producer, "api/first", nil)
	second := f.agent(producer, "api/second", nil)
	f.agent(consumer, "client/selected", map[string]string{"client.txt": "selected"})
	if git(t, first, "rev-parse", "HEAD") != git(t, second, "rev-parse", "HEAD") {
		t.Fatal("fixture heads differ")
	}
	write(t, first, "first.txt", "first saved tree")
	write(t, second, "second.txt", "second saved tree")
	repos, err := Collect(context.Background(), Request{Scope: scope, Named: map[string][]string{"orders": {"api/first", "api/second"}, "payments": {"client/selected"}}})
	if err != nil {
		t.Fatal(err)
	}
	report, results, err := Pairwise(context.Background(), repos, team, 10, true)
	if err != nil || report.Status != model.StatusPassed || len(report.Pairs) != 2 {
		t.Fatalf("twins %+v %v", report, err)
	}
	seen := map[string]PairInput{}
	for _, pair := range report.Pairs {
		for _, input := range pair.Inputs {
			if input.Repo == "orders" {
				seen[input.Ref] = input
			}
		}
	}
	if len(seen) != 2 || seen["api/first"].Tree == seen["api/second"].Tree || seen["api/first"].DirtyDigest == seen["api/second"].DirtyDigest {
		t.Fatalf("distinct saved inputs collapsed: %+v", seen)
	}
	for _, r := range results {
		if r.ID == "orders" {
			if r.Report == nil || len(r.Report.Inputs) != 2 || r.Report.Inputs[0] == r.Report.Inputs[1] {
				t.Fatalf("original snapshots collapsed: %+v", r.Report)
			}
		}
	}
}

func TestPairwiseUnavailableDirtyRepoKeepsUnrelatedPairEvidence(t *testing.T) {
	f, producer, consumer, team, _ := teamFixture(t)
	f.agent(producer, "api/work", map[string]string{"api.txt": "work"})
	f.agent(consumer, "client/work", map[string]string{"client.txt": "work"})
	third := f.repo("unavailable", map[string]string{"base.txt": "baseline"})
	dirty := f.agent(third, "third/work", map[string]string{"third.txt": "work"})
	if err := os.Symlink("outside", filepath.Join(dirty, "unsupported-link")); err != nil {
		t.Skip(err)
	}
	scope := scopeOf(t, producer, consumer, third)
	repos, err := Collect(context.Background(), Request{Scope: scope})
	if err != nil {
		t.Fatal(err)
	}
	report, results, err := Pairwise(context.Background(), repos, team, 10, true)
	if err != nil || report.Status != model.StatusIncomplete || report.Complete || len(report.Pairs) != 3 {
		t.Fatalf("scoped failure %+v %v", report, err)
	}
	good := 0
	unavailable := 0
	for _, pair := range report.Pairs {
		if pair.Status == model.StatusPassed {
			good++
		} else if pair.Status == model.StatusIncomplete {
			unavailable++
		}
	}
	if good != 1 || unavailable != 2 {
		t.Fatalf("unrelated pair evidence lost: %+v", report.Pairs)
	}
	for _, result := range results {
		if result.ID == "unavailable" && (result.Error == "" || result.Report != nil) {
			t.Fatalf("missing scoped error: %+v", result)
		}
	}
}
