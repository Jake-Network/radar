package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Jake-Network/radar/internal/workspace"
)

const connectSchema = `{"type":"object","properties":{"total":{"type":"number"},"status":{"type":"string"}}}`

func connectArgs(id string) []string {
	return []string{"workspace", "connect", "orders:openapi.json#", "payments", "--fields", "total,status", "--direction", "response", "--id", id, "--source", "client.py"}
}

func TestWorkspaceConnectPreservesAndUpdates(t *testing.T) {
	s := newShop(t)
	s.register()
	put(t, s.orders, "openapi.json", connectSchema)
	beforeOrders := gitTest(t, s.orders, "rev-parse", "HEAD")
	beforePayments := gitTest(t, s.payments, "rev-parse", "HEAD")
	out := connectText(t, s.orders, connectArgs("order-response")...)
	if !strings.Contains(out, "Next: commit both declarations, then: radar gate --again") || !strings.Contains(out, "git -C") {
		t.Fatal(out)
	}
	dir, _ := workspace.ConfigDir()
	reg, e := workspace.Load(dir)
	if e != nil || reg.Workspaces[0].Home != "orders" {
		t.Fatalf("home: %+v %v", reg, e)
	}
	invoke(t, s.orders, 0, connectArgs("another-response")...)
	args := connectArgs("order-response")
	for i := range args {
		if args[i] == "total,status" {
			args[i] = "total"
		}
	}
	out = connectText(t, s.orders, args...)
	if !strings.Contains(out, "consumes:") || !strings.Contains(out, " → ") {
		t.Fatal(out)
	}
	b, e := os.ReadFile(filepath.Join(s.orders, workspace.TeamPath))
	if e != nil {
		t.Fatal(e)
	}
	team, e := workspace.ParseTeam(b)
	if e != nil || len(team.Links) != 2 {
		t.Fatalf("team %+v %v", team, e)
	}
	b, e = os.ReadFile(filepath.Join(s.payments, workspace.ConsumesPath))
	if e != nil {
		t.Fatal(e)
	}
	cons, e := workspace.ParseConsumes(b)
	if e != nil || len(cons.Consumes) != 2 || len(cons.Consumes[0].Fields) != 1 {
		t.Fatalf("consumes %+v %v", cons, e)
	}
	if gitTest(t, s.orders, "rev-parse", "HEAD") != beforeOrders || gitTest(t, s.payments, "rev-parse", "HEAD") != beforePayments {
		t.Fatal("connect changed HEAD")
	}
	if gitTest(t, s.orders, "diff", "--cached", "--name-only") != "" || gitTest(t, s.payments, "diff", "--cached", "--name-only") != "" {
		t.Fatal("connect changed index")
	}
}

func TestWorkspaceConnectIntoAndRejectsInvalidInputs(t *testing.T) {
	s := newShop(t)
	s.register()
	put(t, s.orders, "openapi.json", connectSchema)
	for _, args := range [][]string{
		{"workspace", "connect", "orders:openapi.json#/missing", "payments", "--fields", "total", "--direction", "response"},
		{"workspace", "connect", "orders:openapi.json#", "payments", "--fields", "missing", "--direction", "response"},
		{"workspace", "connect", "orders:openapi.json#", "payments", "--fields", "total"},
		{"workspace", "connect", "orders:openapi.json#", "payments", "--fields", "total", "--direction", "response", "--into", "payments:" + s.orders},
	} {
		invoke(t, s.orders, 2, args...)
		if _, e := os.Stat(filepath.Join(s.orders, workspace.TeamPath)); !os.IsNotExist(e) {
			t.Fatalf("invalid connect wrote team: %v", e)
		}
	}
	wtOrders := s.wt(s.orders, "agent/api")
	wtPayments := s.wt(s.payments, "agent/client")
	put(t, wtOrders, "openapi.json", connectSchema)
	args := append(connectArgs("order-response"), "--into", "orders:"+wtOrders, "--into", "payments:"+wtPayments)
	invoke(t, s.orders, 0, args...)
	for _, r := range []string{s.orders, s.payments} {
		if _, e := os.Stat(filepath.Join(r, ".radar")); !os.IsNotExist(e) {
			t.Fatalf("wrote main checkout %s", r)
		}
	}
	for _, p := range []string{filepath.Join(wtOrders, workspace.TeamPath), filepath.Join(wtPayments, workspace.ConsumesPath)} {
		if _, e := os.Stat(p); e != nil {
			t.Fatal(e)
		}
	}
}

func TestWorkspaceConnectConcurrent(t *testing.T) {
	s := newShop(t)
	s.register()
	put(t, s.orders, "openapi.json", connectSchema)
	var wg sync.WaitGroup
	errs := make(chan string, 2)
	for _, id := range []string{"first-response", "second-response"} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			var out, err bytes.Buffer
			args := append([]string{"--root", s.orders}, connectArgs(id)...)
			if code := Run(context.Background(), args, &out, &err); code != 0 {
				errs <- out.String() + err.String()
			}
		}(id)
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Error(e)
	}
	for _, p := range []string{filepath.Join(s.orders, workspace.TeamPath), filepath.Join(s.payments, workspace.ConsumesPath)} {
		b, e := os.ReadFile(p)
		if e != nil {
			t.Fatal(e)
		}
		var v map[string]json.RawMessage
		if e = json.Unmarshal(b, &v); e != nil {
			t.Fatal(e)
		}
		key := "links"
		if strings.HasSuffix(p, "consumes.json") {
			key = "consumes"
		}
		var entries []any
		if e = json.Unmarshal(v[key], &entries); e != nil || len(entries) != 2 {
			t.Fatalf("lost concurrent write %s: %s %v", p, b, e)
		}
	}
}

func TestWorkspaceConnectRefusesSymlinkState(t *testing.T) {
	s := newShop(t)
	s.register()
	put(t, s.orders, "openapi.json", connectSchema)
	outside := t.TempDir()
	if e := os.Symlink(outside, filepath.Join(s.payments, ".radar")); e != nil {
		t.Skip(e)
	}
	invoke(t, s.orders, 2, connectArgs("order-response")...)
	if _, e := os.Stat(filepath.Join(outside, "consumes.json")); !os.IsNotExist(e) {
		t.Fatal("escaped consumer repository")
	}
	if _, e := os.Stat(filepath.Join(s.orders, workspace.TeamPath)); !os.IsNotExist(e) {
		t.Fatal("team file written before validation")
	}
}

func TestWorkspaceConnectIgnoresUnavailableRegistryOnlyRepo(t *testing.T) {
	s := newShop(t)
	s.register()
	put(t, s.orders, "openapi.json", connectSchema)
	invoke(t, s.orders, 0, connectArgs("first-response")...)
	dir, err := workspace.ConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(t.TempDir(), "missing-repo")
	if err = workspace.Update(dir, func(reg *workspace.Registry) error {
		reg.Workspaces[0].Repos = append(reg.Workspaces[0].Repos, workspace.Repo{ID: "unused", Path: missing, CommonDir: filepath.Join(missing, ".git")})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	invoke(t, s.orders, 0, connectArgs("second-response")...)
	b, err := os.ReadFile(filepath.Join(s.orders, workspace.TeamPath))
	if err != nil {
		t.Fatal(err)
	}
	team, err := workspace.ParseTeam(b)
	if err != nil || len(team.Repos) != 2 || len(team.Links) != 2 {
		t.Fatalf("connect changed team membership or lost links: %+v %v", team, err)
	}
	reg, err := workspace.Load(dir)
	if err != nil || len(reg.Workspaces[0].Repos) != 3 {
		t.Fatalf("connect changed registry-only membership: %+v %v", reg, err)
	}
}

func TestWorkspaceConnectBoundsDefaultIDForLongRepoIDs(t *testing.T) {
	s := newShop(t)
	s.register()
	put(t, s.orders, "openapi.json", connectSchema)
	producerID := "orders-" + strings.Repeat("a", workspace.MaxIDLength-len("orders-"))
	consumerID := "payments-" + strings.Repeat("b", workspace.MaxIDLength-len("payments-"))
	dir, err := workspace.ConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	if err = workspace.Update(dir, func(reg *workspace.Registry) error {
		for i := range reg.Workspaces[0].Repos {
			r := &reg.Workspaces[0].Repos[i]
			if r.ID == "orders" {
				r.ID = producerID
			} else if r.ID == "payments" {
				r.ID = consumerID
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	args := []string{"workspace", "connect", producerID + ":openapi.json#", consumerID, "--fields", "total", "--direction", "response"}
	invoke(t, s.orders, 0, args...)
	invoke(t, s.orders, 0, args...) // Same generated ID updates, rather than adding a duplicate.
	args[len(args)-1] = "request"
	invoke(t, s.orders, 0, args...) // Distinct long inputs must retain distinct IDs.
	b, err := os.ReadFile(filepath.Join(s.orders, workspace.TeamPath))
	if err != nil {
		t.Fatal(err)
	}
	team, err := workspace.ParseTeam(b)
	if err != nil || len(team.Links) != 2 {
		t.Fatalf("default IDs were unstable or collided: %+v %v", team, err)
	}
	for _, link := range team.Links {
		if err := workspace.ValidID(link.ID); err != nil {
			t.Fatal(err)
		}
		if link.Producer != producerID+":openapi.json#" || link.Consumer != consumerID {
			t.Fatalf("bounded ID changed link endpoints: %+v", link)
		}
	}
	b, err = os.ReadFile(filepath.Join(s.payments, workspace.ConsumesPath))
	if err != nil {
		t.Fatal(err)
	}
	consumes, err := workspace.ParseConsumes(b)
	if err != nil || len(consumes.Consumes) != 2 {
		t.Fatalf("consumer IDs were unstable or collided: %+v %v", consumes, err)
	}
	for _, consume := range consumes.Consumes {
		if consume.Contract != team.Links[0].ID && consume.Contract != team.Links[1].ID {
			t.Fatalf("consumer expectation has mismatched generated ID: %+v", consume)
		}
	}
}

func connectText(t *testing.T, root string, args ...string) string {
	t.Helper()
	var out, errs bytes.Buffer
	if code := Run(context.Background(), append([]string{"--root", root}, args...), &out, &errs); code != 0 {
		t.Fatalf("connect exit %d: %s %s", code, &out, &errs)
	}
	return out.String()
}

func TestWorkspaceConnectDefaultIDSeparatesProducerDocuments(t *testing.T) {
	s := newShop(t)
	s.register()
	put(t, s.orders, "openapi.json", connectSchema)
	put(t, s.orders, "other.json", connectSchema)
	first := []string{"workspace", "connect", "orders:openapi.json#", "payments", "--fields", "total", "--direction", "response"}
	second := append([]string(nil), first...)
	second[2] = "orders:other.json#"
	a := invoke(t, s.orders, 0, first...)
	b := invoke(t, s.orders, 0, second...)
	if a["id"] == b["id"] {
		t.Fatal("different documents share default link id", a, b)
	}
	data, e := os.ReadFile(filepath.Join(s.orders, workspace.TeamPath))
	if e != nil {
		t.Fatal(e)
	}
	team, e := workspace.ParseTeam(data)
	if e != nil || len(team.Links) != 2 {
		t.Fatalf("links overwritten: %+v %v", team, e)
	}
}
