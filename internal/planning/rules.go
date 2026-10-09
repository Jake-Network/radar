// Package planning validates declared intent against evidence, without inventing a design.
package planning

import (
	"fmt"
	"strings"
)

// ValidateRule rejects unsupported verification requests rather than executing arbitrary commands.
func ValidateRule(r Rule) error {
	switch r.Kind {
	case "file_exists", "json_property":
		if r.Path == "" || strings.HasPrefix(r.Path, "/") || strings.Contains(r.Path, "\\") {
			return fmt.Errorf("rule path must be repository relative")
		}
		for _, part := range strings.Split(r.Path, "/") {
			if part == ".." {
				return fmt.Errorf("rule path escapes repository")
			}
		}
		if r.Kind == "json_property" && (r.Property == "" || (r.Pointer != "" && !strings.HasPrefix(r.Pointer, "/"))) {
			return fmt.Errorf("json_property requires property and an RFC 6901 pointer")
		}
		for i := 0; i < len(r.Pointer); i++ {
			if r.Pointer[i] == '~' {
				if i+1 >= len(r.Pointer) || (r.Pointer[i+1] != '0' && r.Pointer[i+1] != '1') {
					return fmt.Errorf("invalid RFC 6901 pointer escape")
				}
				i++
			}
		}
	case "test_run":
		if len(r.Command) == 0 || strings.TrimSpace(r.Command[0]) == "" {
			return fmt.Errorf("test_run requires exact command argv")
		}
		for _, arg := range r.Command {
			if strings.ContainsRune(arg, 0) {
				return fmt.Errorf("command argument contains NUL")
			}
		}
	case "graph_entity":
		if r.Entity == "" {
			return fmt.Errorf("graph_entity requires entity ID")
		}
	default:
		return fmt.Errorf("unsupported verification rule %q", r.Kind)
	}
	return nil
}
