package cli

import (
	"flag"
	"fmt"
	"strings"
)

// gateFlagArgs moves recognized flags before branches for flag.FlagSet.
// Values are kept verbatim, never evaluated by a shell. -- ends flag parsing.
func gateFlagArgs(fs *flag.FlagSet, args []string) ([]string, error) {
	var flags, refs []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			refs = append(refs, args[i+1:]...)
			break
		}
		if arg == "-" || !strings.HasPrefix(arg, "-") {
			refs = append(refs, arg)
			continue
		}
		name, _, hasValue := strings.Cut(strings.TrimPrefix(strings.TrimPrefix(arg, "-"), "-"), "=")
		f := fs.Lookup(name)
		if f == nil {
			if name == "h" || name == "help" {
				flags = append(flags, arg)
				continue
			}
			return nil, fmt.Errorf("unknown gate option %q; use -- before literal positional references", arg)
		}
		flags = append(flags, arg)
		boolean, ok := f.Value.(interface{ IsBoolFlag() bool })
		if !hasValue && !(ok && boolean.IsBoolFlag()) {
			if i+1 == len(args) || strings.HasPrefix(args[i+1], "--") {
				return nil, fmt.Errorf("%s requires a value", arg)
			}
			i++
			flags = append(flags, args[i])
		}
	}
	return append(append(flags, "--"), refs...), nil
}
