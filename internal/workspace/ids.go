// Package workspace groups repositories so one radar gate checks them
// together. It owns the local registry, repository identity, repo IDs and the
// pure rules that turn an invocation into a scope, targets and bases.
package workspace

import (
	"fmt"
	"regexp"
	"strings"
)

var idPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]*$`)

// MaxIDLength bounds repo IDs and workspace names, which also name run
// directories.
const MaxIDLength = 64

// ValidID checks a repo ID or workspace name: a letter, then letters, digits,
// '-' or '_'.
func ValidID(id string) error {
	if len(id) > MaxIDLength || !idPattern.MatchString(id) {
		return fmt.Errorf("invalid ID %q: start with a letter and use only letters, digits, '-' and '_' (at most %d characters)", id, MaxIDLength)
	}
	return nil
}

// DefaultID derives a valid ID from a folder name: invalid characters become
// '-', and a name that does not start with a letter is prefixed with "repo-".
func DefaultID(folder string) string {
	var b strings.Builder
	dash := false
	for _, r := range folder {
		ok := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-'
		if !ok {
			if !dash && b.Len() > 0 {
				b.WriteByte('-')
			}
			dash = true
			continue
		}
		b.WriteRune(r)
		dash = false
	}
	id := strings.Trim(b.String(), "-_")
	if id == "" {
		return "repo"
	}
	if c := id[0]; !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z') {
		id = "repo-" + id
	}
	if len(id) > MaxIDLength {
		id = strings.TrimRight(id[:MaxIDLength], "-_")
	}
	return id
}
