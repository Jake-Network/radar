package cli

import "flag"

func policyFlag(fs *flag.FlagSet, o *options) {
	fs.StringVar(&o.policy, "policy", "", "version 1 verification policy JSON at `PATH`; gates only its required check IDs")
}
