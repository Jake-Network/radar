package integration

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Jake-Network/radar/internal/gate"
	"github.com/Jake-Network/radar/internal/indexer"
	"github.com/Jake-Network/radar/internal/model"
	"github.com/Jake-Network/radar/internal/runners"
)

func TestVerificationMetadataDeadlineRecordsNoExecution(t *testing.T) {
	root := fixture(t)
	head := gitTest(t, root, "rev-parse", "HEAD")
	for _, suite := range []string{"", "balanced"} {
		t.Run("suite="+suite, func(t *testing.T) {
			ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
			defer cancel()
			r := Report{CandidateCommit: head}
			o := opts()
			o.Suite = suite
			o.Command = []string{"must-not-execute"}
			o.Progress = func(step Step) {
				if step.Stage == StageTest {
					t.Fatal("test started after metadata deadline")
				}
			}
			if err := runVerification(ctx, root, &r, o); err != nil {
				t.Fatal("deadline escaped as raw metadata error", err)
			}
			checkStatus(t, r, "integration_execution", model.StatusTimeout)
			if suite != "" {
				checkStatus(t, r, "test_selection", model.StatusIncomplete)
			}
			if r.Execution != nil || len(r.Executions) != 0 || len(r.Selected) != 0 || r.Selection != nil || len(r.Findings) != 0 || len(r.Attributions) != 0 {
				t.Fatal("metadata timeout fabricated execution or branch lead", r)
			}
			for _, check := range r.Checks {
				if check.Evidence != model.Unknown {
					t.Fatal("unexecuted metadata timeout claimed evidence", check)
				}
			}
			applyGate(&r, o)
			if r.Gate.Verdict == gate.Pass {
				t.Fatal("metadata timeout passed candidate gate")
			}
		})
	}
}

type verificationProfileProvider struct {
	entries func(context.Context) ([]indexer.Entry, error)
	read    func(context.Context) ([]byte, error)
}

func (p verificationProfileProvider) Method() string { return "test" }
func (p verificationProfileProvider) Entries(ctx context.Context) ([]indexer.Entry, error) {
	return p.entries(ctx)
}
func (p verificationProfileProvider) Read(ctx context.Context, _ indexer.Entry, _ int64) ([]byte, error) {
	return p.read(ctx)
}

func TestVerificationProfilesDistinguishesMetadataDeadlineFromOtherErrors(t *testing.T) {
	raw := errors.New("git metadata unavailable")
	for _, kind := range []string{"entries deadline", "read deadline", "malformed after deadline", "success after deadline", "entries error", "read error", "canceled"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			if kind == "entries deadline" || kind == "read deadline" || kind == "malformed after deadline" || kind == "success after deadline" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithDeadline(ctx, time.Now().Add(-time.Second))
				defer cancel()
			} else if kind == "canceled" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			provider := verificationProfileProvider{
				entries: func(context.Context) ([]indexer.Entry, error) {
					if kind == "success after deadline" {
						return nil, nil
					}
					if kind == "entries deadline" || kind == "entries error" || kind == "canceled" {
						return nil, raw
					}
					return []indexer.Entry{{Path: runners.Manifest, Mode: indexer.ModeRegular, Size: 1}}, nil
				},
				read: func(context.Context) ([]byte, error) {
					if kind == "read deadline" || kind == "read error" {
						return nil, raw
					}
					return []byte("{"), nil
				},
			}
			_, err := verificationProfiles(ctx, provider)
			if err == nil {
				t.Fatal("metadata error disappeared")
			}
			deadline := kind == "entries deadline" || kind == "read deadline" || kind == "success after deadline"
			if errors.Is(err, context.DeadlineExceeded) != deadline {
				t.Fatal("metadata error misclassified", kind, err)
			}
			if !deadline && kind != "malformed after deadline" && !errors.Is(err, raw) {
				t.Fatal("original metadata error lost", err)
			}
		})
	}
}

func TestVerificationMetadataCancellationAndInvalidGitRemainErrors(t *testing.T) {
	root := fixture(t)
	head := gitTest(t, root, "rev-parse", "HEAD")
	for _, kind := range []string{"canceled", "invalid commit", "malformed profile"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			commit := head
			if kind == "canceled" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			} else if kind == "invalid commit" {
				commit = "missing-commit"
			} else {
				put(t, root, runners.Manifest, "{")
				gitTest(t, root, "add", runners.Manifest)
				gitTest(t, root, "commit", "-qm", "malformed runner profile")
				commit = gitTest(t, root, "rev-parse", "HEAD")
			}
			r := Report{CandidateCommit: commit}
			if err := runVerification(ctx, root, &r, opts()); err == nil {
				t.Fatal("nondeadline metadata error suppressed", kind)
			}
			if len(r.Checks) != 0 || r.Execution != nil || len(r.Executions) != 0 {
				t.Fatal("metadata failure fabricated execution", r)
			}
		})
	}
}

func TestVerificationMetadataDeadlinePreviewCannotInventTestEvidence(t *testing.T) {
	root := fixture(t)
	for _, suite := range []string{"", "balanced"} {
		t.Run("suite="+suite, func(t *testing.T) {
			o := opts("backend")
			o.Timeout = time.Nanosecond
			o.AttributionRuns = 1
			o.Suite = suite
			o.Command = []string{"must-not-execute"}
			if suite != "" {
				o.Command = nil
			}
			r, err := Preview(context.Background(), root, o)
			if err != nil {
				t.Fatal("metadata budget escaped public Preview as raw error", err)
			}
			checkStatus(t, r, "integration_execution", model.StatusTimeout)
			if r.Execution != nil || len(r.Executions) != 0 || len(r.Selected) != 0 || len(r.Attributions) != 0 || r.Gate.Verdict == gate.Pass {
				t.Fatal("unstarted verification manufactured test evidence or candidate PASS", r)
			}
			for _, finding := range r.Findings {
				if finding.Evidence == model.ObservedTest || len(finding.Branches) > 0 {
					t.Fatal("unstarted verification manufactured observed branch lead", finding)
				}
			}
		})
	}
}
