package cli

import (
	"os"
	"strings"
	"testing"
)

func TestAgentHookReportsPeerOmissionScope(t *testing.T) {
	for _, kind := range []string{"no_overlap", "missing", "detached", "unchanged"} {
		t.Run(kind, func(t *testing.T) {
			root, agent := agentRepo(t, map[string]string{"own.md": "base\n", "peer.md": "base\n"})
			peer := agent("peer", nil)
			put(t, root, "own.md", "current saved content\n")
			reason, message := "no_overlapping_paths", "skipped: no overlapping paths"
			switch kind {
			case "no_overlap":
				put(t, peer, "peer.md", "unrelated saved content\n")
			case "missing":
				if err := os.RemoveAll(peer); err != nil {
					t.Fatal(err)
				}
				reason, message = "inspection_error", "inspection unavailable"
			case "detached":
				gitTest(t, peer, "checkout", "--detach", "-q")
				put(t, peer, "own.md", "conflicting detached saved content\n")
				reason, message = "detached", "detached peers excluded by default"
			case "unchanged":
				reason, message = "no_changed_paths", "skipped: no changed paths"
			}
			before := watchSourceFingerprint(t, root)
			peerBefore := ""
			if kind != "missing" {
				peerBefore = watchSourceFingerprint(t, peer)
			}
			for _, detailed := range []bool{true, false} {
				flags := []string{"--base", "main"}
				if detailed {
					flags = append(flags, "--json")
				}
				result := hookInvoke(t, root, hookEvent(t, root), flags...)
				if result.Decision != "" || !strings.Contains(result.Message, "NOT VERIFIED") || !strings.Contains(result.Message, message) {
					t.Fatalf("omitted peer was hidden or misclassified: %+v", result)
				}
				if !detailed {
					if result.Radar != nil {
						t.Fatal("native protocol exposed optional diagnostics")
					}
					continue
				}
				d := result.Radar
				if d == nil || d.Self != "complete" || d.PeersTotal != 1 || d.Omitted != 1 || d.Candidates != 0 || d.Checked != 0 || d.Reused != 0 || len(d.OmissionReasons) != 1 || d.OmissionReasons[reason] != 1 {
					t.Fatalf("incorrect total or omission cause: %+v", d)
				}
			}
			if watchSourceFingerprint(t, root) != before {
				t.Fatal("peer omission mutated current source")
			}
			if kind != "missing" && watchSourceFingerprint(t, peer) != peerBefore {
				t.Fatal("peer omission mutated peer source")
			}
		})
	}
}
