// Package planning validates declared intent against evidence, without inventing a design.
package planning

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/Jake-Network/radar/internal/jsonptr"
	"github.com/Jake-Network/radar/internal/pathutil"
)

var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func validArgv(argv []string, what string) error {
	if len(argv) == 0 || strings.TrimSpace(argv[0]) == "" {
		return fmt.Errorf("%s requires exact command argv", what)
	}
	for _, arg := range argv {
		if strings.ContainsRune(arg, 0) {
			return fmt.Errorf("command argument contains NUL")
		}
	}
	return nil
}

// ValidateRule rejects unsupported verification requests rather than executing arbitrary commands.
func ValidateRule(r Rule) error {
	if r.Kind != "test_run" && (len(r.Setup) > 0 || len(r.Env) > 0 || len(r.Link) > 0 || r.JUnit != "") {
		return fmt.Errorf("setup, env, link and junit apply only to test_run rules")
	}
	switch r.Kind {
	case "file_exists", "json_property":
		if _, err := pathutil.RepoRelative(r.Path); err != nil {
			return fmt.Errorf("rule path: %w", err)
		}
		if r.Kind == "json_property" && r.Property == "" {
			return fmt.Errorf("json_property requires property and an RFC 6901 pointer")
		}
		if err := jsonptr.Validate(r.Pointer); err != nil {
			return err
		}
	case "test_run":
		if err := validArgv(r.Command, "test_run"); err != nil {
			return err
		}
		for _, step := range r.Setup {
			if err := validArgv(step, "setup step"); err != nil {
				return err
			}
		}
		for _, name := range r.Env {
			if !envName.MatchString(name) {
				return fmt.Errorf("env entry %q must be an environment variable name", name)
			}
		}
		for _, link := range r.Link {
			if _, err := pathutil.RepoRelative(link); err != nil {
				return fmt.Errorf("link %q: %w", link, err)
			}
		}
		if r.JUnit != "" {
			if _, err := pathutil.RepoRelative(r.JUnit); err != nil {
				return fmt.Errorf("junit report path: %w", err)
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
