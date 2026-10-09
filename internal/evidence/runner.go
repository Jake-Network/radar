package evidence

// Package evidence runs explicitly authorized verification commands in private commit snapshots.
// This isolates checkout state, not malicious code: execution is not an OS sandbox.
import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	gitrepo "github.com/radar-engine/radar/internal/git"
	"github.com/radar-engine/radar/internal/planning"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"
)

// Run requires callers to obtain explicit execution opt-in before invoking it.
// Only commands declared by acceptance test_run rules can produce evidence.
func Run(ctx context.Context, root, ref string, p planning.Plan, argv []string, timeout time.Duration) (Record, error) {
	var r Record
	if len(argv) == 0 || argv[0] == "" || timeout <= 0 || timeout > 30*time.Minute {
		return r, errors.New("command and timeout between zero and 30 minutes required")
	}
	for _, arg := range argv {
		if strings.ContainsRune(arg, 0) {
			return r, errors.New("NUL in command")
		}
	}
	if filepath.IsAbs(argv[0]) || strings.Contains(argv[0], "\\") || strings.HasPrefix(filepath.Clean(argv[0]), "..") {
		return r, errors.New("command executable must be a PATH tool or snapshot-relative path")
	}
	for _, c := range p.Acceptance {
		if c.Rule != nil && c.Rule.Kind == "test_run" && reflect.DeepEqual(c.Rule.Command, argv) {
			r.Criteria = append(r.Criteria, c.ID)
		}
	}
	for _, c := range p.Constraints {
		if c.Rule != nil && c.Rule.Kind == "test_run" && reflect.DeepEqual(c.Rule.Command, argv) {
			r.Criteria = append(r.Criteria, "constraint:"+c.ID)
		}
	}
	if len(r.Criteria) == 0 {
		return r, errors.New("command is not declared by a plan test_run criterion")
	}
	sort.Strings(r.Criteria)
	var err error
	r.Repository, err = Repository(root)
	if err != nil {
		return r, err
	}
	r.Revision, err = gitrepo.Resolve(ctx, root, ref)
	if err != nil {
		return r, err
	}
	dest, err := os.MkdirTemp("", "radar-evidence-")
	if err != nil {
		return r, err
	}
	defer os.RemoveAll(dest)
	if err = snapshot(ctx, root, r.Revision, dest); err != nil {
		return r, err
	}
	privateHome, err := os.MkdirTemp("", "radar-evidence-home-")
	if err != nil {
		return r, err
	}
	defer os.RemoveAll(privateHome)
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(runCtx, argv[0], argv[1:]...)
	cmd.Dir = dest
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + privateHome, "USERPROFILE=" + privateHome, "TMPDIR=" + privateHome, "TMP=" + privateHome, "TEMP=" + privateHome, "LANG=C", "GOPROXY=off", "GOSUMDB=off", "GOTOOLCHAIN=local", "GOCACHE=" + filepath.Join(privateHome, "go-cache"), "GOMODCACHE=" + filepath.Join(privateHome, "go-mod"), "PIP_NO_INDEX=1", "PYTHONDONTWRITEBYTECODE=1", "CARGO_NET_OFFLINE=true", "npm_config_offline=true", "GIT_TERMINAL_PROMPT=0"}
	configureProcess(cmd)
	cmd.WaitDelay = time.Second
	var out output
	cmd.Stdout = &out
	cmd.Stderr = &out
	r.SchemaVersion = "1"
	r.PlanDigest = planning.Digest(p)
	r.Command = append([]string{}, argv...)
	r.StartedAt = time.Now().UTC().Format(time.RFC3339Nano)
	err = cmd.Run()
	r.FinishedAt = time.Now().UTC().Format(time.RFC3339Nano)
	r.ExitCode = -1
	if cmd.ProcessState != nil {
		r.ExitCode = cmd.ProcessState.ExitCode()
	}
	r.Status = "passed"
	if err != nil {
		r.Status = "failed"
	}
	if runCtx.Err() != nil {
		r.Status = "timeout"
	}
	if err != nil && cmd.ProcessState == nil {
		return Record{}, fmt.Errorf("could not start verification command: %w", err)
	}
	content := out.b.Bytes()
	r.OutputDigest = hex.EncodeToString(out.sum())
	r.TestsRun = counts(argv, content)
	if r.Status == "failed" && r.ExitCode == 5 && len(argv) > 2 && (argv[0] == "python3" || argv[0] == "python") && argv[1] == "-m" && argv[2] == "unittest" && strings.Contains(string(content), "Ran 0 tests") {
		r.Status = "unknown"
	}
	if r.Status == "passed" && (out.exceeded || r.TestsRun == 0) {
		r.Status = "unknown"
	}
	r.IntegrityDigest = integrity(r)
	r.ID = "evidence:" + r.IntegrityDigest
	return r, nil
}
