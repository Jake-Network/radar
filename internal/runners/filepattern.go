package runners

import (
	"fmt"
	"path"
	"strings"

	"github.com/Jake-Network/radar/internal/pathutil"
)

// ValidateFilePattern accepts bounded profile-relative globs. A full ** path
// segment matches zero or more directories; other segments use path.Match.
func ValidateFilePattern(pattern string) error {
	clean, err := pathutil.RepoRelative(pattern)
	if err != nil || clean != pattern || len(pattern) > 8192 || strings.ContainsAny(pattern, "\\\x00\r\n") {
		return fmt.Errorf("typecheck files must be clean profile-relative globs")
	}
	parts := strings.Split(pattern, "/")
	if len(parts) > 64 {
		return fmt.Errorf("typecheck file selector is too deep")
	}
	for _, part := range parts {
		if _, err := path.Match(part, ""); err != nil {
			return fmt.Errorf("invalid typecheck file selector")
		}
	}
	return nil
}

// MatchFile reports whether a validated selector matches a profile-relative
// repository path, without consulting the filesystem or following links.
func MatchFile(pattern, name string) bool {
	if ValidateFilePattern(pattern) != nil {
		return false
	}
	patterns, names := strings.Split(pattern, "/"), strings.Split(name, "/")
	type key struct{ p, n int }
	seen := map[key]bool{}
	var match func(int, int) bool
	match = func(p, n int) bool {
		k := key{p, n}
		if seen[k] {
			return false
		}
		seen[k] = true
		if p == len(patterns) {
			return n == len(names)
		}
		if patterns[p] == "**" {
			return match(p+1, n) || n < len(names) && match(p, n+1)
		}
		if n == len(names) {
			return false
		}
		ok, _ := path.Match(patterns[p], names[n])
		return ok && match(p+1, n+1)
	}
	return match(0, 0)
}
