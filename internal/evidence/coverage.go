package evidence

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Jake-Network/radar/internal/coverage"
	"github.com/Jake-Network/radar/internal/pathutil"
	"github.com/Jake-Network/radar/internal/runners"
)

// ObserveCandidateCoverage additionally observes Python read-only file opens,
// including runtime data files omitted by line coverage. Other tools can use
// their explicit bounded reports via coverage.Parse; no tracing is invented.
func ObserveCandidateCoverage(ctx context.Context, root, cwd string, argv []string, report string, timeout time.Duration) (CandidateObservation, []string, error) {
	var files []string
	r, err := observeCandidateReport(ctx, root, cwd, argv, report, timeout, &files)
	return r, files, err
}

// ObserveCandidateDeclaredCoverage consumes only a fresh explicitly declared
// framework report from this command. Its argv remains the reviewed argv.
func ObserveCandidateDeclaredCoverage(ctx context.Context, root, cwd string, argv []string, junit string, config runners.Coverage, timeout time.Duration) (CandidateObservation, []string, error) {
	r := CandidateObservation{}
	if cwd == "" {
		cwd = "."
	}
	dir, err := pathutil.ResolveInside(root, cwd)
	if err != nil {
		return r, nil, err
	}
	filename, err := pathutil.StatePath(dir, config.Report)
	if err != nil {
		return r, nil, err
	}
	if _, err := os.Lstat(filename); err == nil {
		return r, nil, fmt.Errorf("stale coverage report exists before candidate execution")
	} else if !os.IsNotExist(err) {
		return r, nil, err
	}
	r, err = ObserveCandidateReport(ctx, root, cwd, argv, junit, timeout)
	if err != nil {
		return r, nil, err
	}
	filename, err = pathutil.StatePath(dir, config.Report)
	if err != nil {
		return r, nil, err
	}
	info, err := os.Lstat(filename)
	if err != nil {
		return r, nil, fmt.Errorf("declared coverage report was not produced")
	}
	if !info.Mode().IsRegular() || info.Size() > coverage.MaxBytes {
		return r, nil, fmt.Errorf("unsafe or oversized coverage report")
	}
	data, err := os.ReadFile(filename)
	if err != nil {
		return r, nil, err
	}
	files, err := coverage.ParseAt(config.Format, data, root, cwd)
	if err != nil {
		return r, nil, err
	}
	return r, files, nil
}

func prepareFileObservation(root, home string, argv, env []string) ([]string, error) {
	unwrapped := runnerArgv(argv)
	if len(unwrapped) == 0 {
		return nil, fmt.Errorf("coverage runner missing")
	}
	base := filepath.Base(unwrapped[0])
	if base != "python" && base != "python3" {
		return nil, fmt.Errorf("automatic runtime file observation supports Python; declare coverage reports for other frameworks")
	}
	for _, arg := range unwrapped[1:] {
		if arg == "-I" || arg == "-S" {
			return nil, fmt.Errorf("isolated Python disables runtime file observation")
		}
	}
	// Hook is installed in a private home, never in candidate source. Values are
	// argv/environment data, not interpolated Python source.
	if err := os.WriteFile(filepath.Join(home, "sitecustomize.py"), []byte(pythonReadHook), 0600); err != nil {
		return nil, err
	}
	// Clear any inherited Python path before injecting our sole startup hook.
	out := []string{}
	for _, e := range env {
		if !strings.HasPrefix(e, "PYTHONPATH=") {
			out = append(out, e)
		}
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	return append(out, "PYTHONPATH="+home, "RADAR_OBSERVE_ROOT="+abs, "RADAR_OBSERVE_REPORT="+filepath.Join(home, "observed-files.jsonl")), nil
}
func readFileObservation(root, home string) ([]string, error) {
	filename := filepath.Join(home, "observed-files.jsonl")
	info, err := os.Lstat(filename)
	if err != nil {
		return nil, fmt.Errorf("runtime file observation did not produce a report")
	}
	if !info.Mode().IsRegular() || info.Size() > coverage.MaxBytes {
		return nil, fmt.Errorf("unsafe or oversized runtime observation")
	}
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, err
	}
	return coverage.Parse("files", data, root)
}

const pythonReadHook = `import sys, os, json
_root = os.path.realpath(os.environ["RADAR_OBSERVE_ROOT"])
_report = os.environ["RADAR_OBSERVE_REPORT"]
_seen = set()
_writing = False
def _radar_read(event, args):
    global _writing
    if event != "open" or _writing or len(_seen) >= 50000:
        return
    try:
        name, mode, flags = args
        if not isinstance(name, (str, bytes)) or flags & (os.O_WRONLY | os.O_RDWR | os.O_CREAT | os.O_TRUNC):
            return
        name = os.path.realpath(os.fsdecode(name))
        if os.path.commonpath((_root, name)) != _root or not os.path.isfile(name):
            return
        rel = os.path.relpath(name, _root).replace(os.sep, "/")
        if rel in _seen or any(p in (".git", ".venv", "node_modules", "__pycache__") for p in rel.split("/")):
            return
        _seen.add(rel)
        _writing = True
        with open(_report, "a", encoding="utf-8") as output:
            output.write(json.dumps(rel) + "\n")
    except (OSError, ValueError, TypeError):
        pass
    finally:
        _writing = False
sys.addaudithook(_radar_read)
with open(_report, "a", encoding="utf-8") as output:
    pass
`
