package cli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	gitrepo "github.com/Jake-Network/radar/internal/git"
)

func TestAgentHookReportsPeerOmissionScope(t *testing.T) {
	for _, kind := range []string{"no_overlap", "missing", "detached", "assume_unchanged", "skip_worktree", "unmerged", "behind_base", "shared_commit", "exec_bit"} {
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
			case "assume_unchanged", "skip_worktree":
				// Git diff cannot see these saved edits, so an empty changed-path
				// list must not be read as a peer identical to base.
				flag := "--assume-unchanged"
				if kind == "skip_worktree" {
					flag = "--skip-worktree"
				}
				gitTest(t, peer, "update-index", flag, "own.md")
				put(t, peer, "own.md", "hidden conflicting saved content\n")
				reason, message = "hidden_index_entries", "sparse, assume-unchanged or unmerged index entries hide saved content"
			case "unmerged":
				// A conflicted index at base HEAD whose saved file equals base content.
				blob := strings.TrimSpace(gitTest(t, peer, "rev-parse", "HEAD:peer.md"))
				sideFile := filepath.Join(t.TempDir(), "side")
				if err := os.WriteFile(sideFile, []byte("side\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				side := strings.TrimSpace(gitTest(t, peer, "hash-object", "-w", sideFile))
				zero := strings.Repeat("0", len(blob))
				index := exec.Command("git", "-C", peer, "update-index", "--index-info")
				index.Stdin = strings.NewReader("0 " + zero + "\tpeer.md\n100644 " + blob + " 1\tpeer.md\n100644 " + blob + " 2\tpeer.md\n100644 " + side + " 3\tpeer.md\n")
				if out, err := index.CombinedOutput(); err != nil {
					t.Fatalf("create unmerged index: %v %s", err, out)
				}
				reason, message = "hidden_index_entries", "sparse, assume-unchanged or unmerged index entries hide saved content"
			case "exec_bit":
				// Git diff ignores modes under core.fileMode=false, but the pair's
				// saved snapshot records the exec bit and can conflict on it.
				if runtime.GOOS == "windows" {
					t.Skip("exec bits are not represented in Windows saved snapshots")
				}
				gitTest(t, root, "config", "core.fileMode", "false")
				if err := os.Chmod(filepath.Join(peer, "peer.md"), 0o755); err != nil {
					t.Fatal(err)
				}
				reason, message = "saved_snapshot_differs", "saved snapshot differs from base"
			case "behind_base", "shared_commit":
				// The saved tree equals base, but the pair merges from the peer's
				// own HEAD: its merge-base differs, so the peer carries a real diff.
				if kind == "behind_base" {
					put(t, root, "peer.md", "advanced base\n")
					gitTest(t, root, "commit", "-qam", "advance base")
				} else {
					put(t, peer, "peer.md", "shared work\n")
					gitTest(t, peer, "commit", "-qam", "shared work")
				}
				gitTest(t, peer, "restore", "--source=main", "--worktree", "--staged", ".")
				reason, message = "head_not_base", "Git diff reports no change from base but HEAD differs"
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

func TestAgentHookUnchangedPeerIsNotUnverifiedScope(t *testing.T) {
	root, agent := agentRepo(t, map[string]string{"own.md": "base\n", "peer.md": "base\n"})
	agent("idle", nil)
	agent("fresh", nil)
	put(t, root, "own.md", "current saved content\n")
	for _, detailed := range []bool{true, false} {
		flags := []string{"--base", "main"}
		if detailed {
			flags = append(flags, "--json")
		}
		result := hookInvoke(t, root, hookEvent(t, root), flags...)
		if result.Decision != "" || strings.Contains(result.Message, "NOT VERIFIED") {
			t.Fatalf("peers identical to base were reported as unverified: %+v", result)
		}
		if !detailed {
			continue
		}
		d := result.Radar
		if d == nil || d.Self != "complete" && d.Self != "reused" || d.PeersTotal != 2 || d.Unchanged != 2 || d.Omitted != 0 || d.Candidates != 0 || len(d.OmissionReasons) != 0 {
			t.Fatalf("incorrect unchanged peer scope: %+v", d)
		}
	}
}

func TestAgentHookUnchangedConfirmationDoesNotStarvePairs(t *testing.T) {
	root, agent := agentRepo(t, map[string]string{"own.md": "base\n", "peer.md": "base\n"})
	for _, name := range []string{"idle-a", "idle-b", "idle-c", "idle-d", "idle-e"} {
		agent(name, nil)
	}
	// Sorted last so that a selection-time confirmation runs before its pair.
	conflict := agent("zz-conflict", nil)
	put(t, root, "own.md", "current saved content\n")
	put(t, conflict, "own.md", "conflicting peer saved content\n")
	// Model large checkouts: each base-identical confirmation consumes the
	// remaining peer budget. The related pair must still be checked first.
	confirmed := hookConfirmUnchanged
	t.Cleanup(func() { hookConfirmUnchanged = confirmed })
	hookConfirmUnchanged = func(ctx context.Context, base string, w gitrepo.WorktreeStatus) string {
		<-ctx.Done()
		return confirmed(ctx, base, w)
	}
	result := hookInvoke(t, root, hookEvent(t, root), "--base", "main", "--json", "--timeout", "5s")
	if result.Decision != "block" {
		t.Fatalf("unchanged confirmation starved the conflicting pair: %+v", result)
	}
	d := result.Radar
	if d == nil || d.PeersTotal != 6 || d.Checked+d.Reused != 1 || d.Unchanged != 0 || d.Omitted != 5 || d.OmissionReasons["inspection_incomplete"] != 5 || d.OmissionReasons["inspection_error"] != 0 {
		t.Fatalf("unconfirmed peers were not reported as incomplete: %+v", d)
	}
	if !strings.Contains(result.Message, "NOT VERIFIED") {
		t.Fatalf("unconfirmed peers were hidden: %+v", result)
	}
}

func TestAgentHookPeerScopeSurvivesLongPairNotes(t *testing.T) {
	notes := "Radar peer worktree drift (informational; your saved changes did not introduce it):\n" + strings.Repeat("src/module/file.go:12\n", 178)
	for _, self := range []string{"Radar static preview complete.", strings.Repeat("long self finding ", 70)} {
		got := hookPeerMessage(self, map[string]int{"inspection_incomplete": 5}, notes)
		if !strings.Contains(got, "NOT VERIFIED: 5 peers were not checked") || len(got) > 4096 || !strings.Contains(got, "src/module/file.go:12") {
			t.Fatalf("truncation removed unverified peer scope or all notes (%d bytes): %q", len(got), got[max(0, len(got)-200):])
		}
	}
}
