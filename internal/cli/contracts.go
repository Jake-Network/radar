package cli

import (
	"errors"
	"github.com/radar-engine/radar/internal/contracts"
	"strings"
)

func (a *app) contractCommand(command string, o options) int {
	if o.base == "" {
		return a.fail(errors.New("--base is required"))
	}
	if e := a.repository(); e != nil {
		return a.fail(e)
	}
	var r contracts.Report
	var e error
	if command == "impact" {
		if o.head == "" {
			return a.fail(errors.New("--head is required (commit ref or WORKTREE)"))
		}
		r, e = contracts.Impact(a.ctx, a.root, o.base, o.head)
	} else {
		if o.branches == "" {
			return a.fail(errors.New("--branches requires comma-separated refs"))
		}
		refs := strings.Split(o.branches, ",")
		for i := range refs {
			refs[i] = strings.TrimSpace(refs[i])
		}
		r, e = contracts.Scan(a.ctx, a.root, o.base, refs)
	}
	if e != nil {
		return a.fail(e)
	}
	if e = a.store.SaveFindings(a.ctx, r.Findings); e != nil {
		return a.fail(e)
	}
	a.emit(r)
	if r.Checkpoint == "authoritative" {
		for _, f := range r.Findings {
			if f.Severity == "error" {
				return 1
			}
		}
	}
	return 0
}
func (a *app) explain(o options) int {
	if len(o.args) != 1 {
		return a.fail(errors.New("explain requires a finding ID"))
	}
	f, e := a.store.Finding(a.ctx, o.args[0])
	if e != nil {
		return a.fail(e)
	}
	a.emit(f)
	return 0
}
