package cli

import (
	"errors"
	"flag"
	"github.com/Jake-Network/radar/internal/gate"
	"github.com/Jake-Network/radar/internal/pathutil"
	"path/filepath"
)

func policyFlag(fs *flag.FlagSet, o *options) {
	fs.StringVar(&o.policy, "policy", "", "version 1 verification policy JSON; gates only its required check IDs")
}
func (a *app) loadPolicy(o options) (*gate.Policy, error) {
	if o.policy == "" {
		return nil, nil
	}
	if o.strict {
		return nil, errors.New("--policy and legacy --require-complete cannot be combined; select one gate model")
	}
	path := o.policy
	if !filepath.IsAbs(path) {
		resolved, e := pathutil.ResolveInside(a.root, path)
		if e != nil {
			return nil, e
		}
		path = resolved
	}
	p, e := gate.Load(path)
	if e != nil {
		return nil, e
	}
	return &p, nil
}
