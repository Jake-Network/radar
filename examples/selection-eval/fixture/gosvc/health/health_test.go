package health

import "testing"

func TestStatus(t *testing.T) {
	for _, tc := range []struct {
		up, total int
		want      string
	}{{2, 2, "ok"}, {1, 2, "degraded"}, {0, 2, "down"}} {
		if got := Status(tc.up, tc.total); got != tc.want {
			t.Fatal(tc, got)
		}
	}
}
