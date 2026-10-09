package cli

import "flag"

func policyFlag(fs *flag.FlagSet, o *options) {
	fs.StringVar(&o.policy, "policy", "", "version 1 verification policy JSON; gates only its required check IDs")
}
