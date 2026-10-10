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

func connectText(t *testing.T, root string, args ...string) string {
	t.Helper()
	var out, errs bytes.Buffer
	if code := Run(context.Background(), append([]string{"--root", root}, args...), &out, &errs); code != 0 {
		t.Fatalf("connect exit %d: %s %s", code, &out, &errs)
	}
	return out.String()
}
