package integration

import (
	"strings"
	"testing"

	"github.com/Jake-Network/radar/internal/testselection"
)

// An absent repository-relative runner is an environment gap either way;
// the reason names node_modules only when that is the cause.
func TestUnavailableNamesTheReasonARunnerIsAbsent(t *testing.T) {
	root := t.TempDir()
	for _, tc := range []struct{ tool, want string }{
		{"./node_modules/.bin/jest", "node_modules are not copied"},
		{"./gradlew", "not committed, or removed by a branch"},
	} {
		got := unavailable(root, testselection.Command{Command: []string{tc.tool}, CWD: "."})
		if !strings.Contains(got, "is absent from the private candidate") || !strings.Contains(got, tc.want) {
			t.Fatalf("%s: %q", tc.tool, got)
		}
	}
}
