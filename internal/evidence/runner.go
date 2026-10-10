// Package evidence runs explicitly authorized verification commands in private commit snapshots.
// This isolates checkout state, not malicious code: execution is not an OS sandbox.
package evidence

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	gitrepo "github.com/Jake-Network/radar/internal/git"
	"github.com/Jake-Network/radar/internal/model"
	"github.com/Jake-Network/radar/internal/pathutil"
	"github.com/Jake-Network/radar/internal/planning"
)

// execution is the part of a test_run rule that shapes how the command runs.
type execution struct {
	CWD   string
	Setup [][]string
	Env   []string
	Link  []string
	JUnit string
}

// declared finds every criterion whose test_run rule declares argv, and the
// single execution environment those rules agree on.
func declared(p planning.Plan, argv []string) ([]string, execution, error) {
	var criteria []string
	var spec *execution
	consider := func(id string, rule *planning.Rule) error {
		if rule == nil || rule.Kind != "test_run" || !reflect.DeepEqual(rule.Command, argv) {
			return nil
		}
		if err := planning.ValidateRule(*rule); err != nil {
			return fmt.Errorf("criterion %s: %w", id, err)
		}
		e := execution{CWD: normalizeCWD(rule.CWD), Setup: rule.Setup, Env: rule.Env, Link: rule.Link, JUnit: rule.JUnit}
		if len(e.Setup) == 0 {
			e.Setup = nil
		}
		if len(e.Env) == 0 {
			e.Env = nil
		}
		if len(e.Link) == 0 {
			e.Link = nil
		}
		if spec != nil && !reflect.DeepEqual(*spec, e) {
			return errors.New("criteria declaring this command disagree on cwd, setup, env, link or junit")
		}
		spec = &e
		criteria = append(criteria, id)
		return nil
	}
	for _, c := range p.Acceptance {
		if err := consider(c.ID, c.Rule); err != nil {
			return nil, execution{}, err
		}
	}
	for _, c := range p.Constraints {
		if err := consider("constraint:"+c.ID, c.Rule); err != nil {
			return nil, execution{}, err
		}
	}
	if len(criteria) == 0 {
		return nil, execution{}, errors.New("command is not declared by a plan test_run criterion")
	}
	sort.Strings(criteria)
	return criteria, *spec, nil
}

func checkExecutable(argv []string) error {
	if len(argv) == 0 || argv[0] == "" {
		return errors.New("command required")
	}
	for _, arg := range argv {
		if strings.ContainsRune(arg, 0) {
			return errors.New("NUL in command")
		}
	}
	if filepath.IsAbs(argv[0]) || strings.Contains(argv[0], "\\") || strings.HasPrefix(filepath.Clean(argv[0]), "..") {
		return errors.New("command executable must be a PATH tool or snapshot-relative path")
	}
	return nil
}

// Run requires callers to obtain explicit execution opt-in before invoking it.
// Only commands declared by acceptance test_run rules can produce evidence.
func Run(ctx context.Context, root, ref string, p planning.Plan, argv []string, timeout time.Duration) (Record, error) {
	var r Record
	if timeout <= 0 || timeout > 30*time.Minute {
		return r, errors.New("command and timeout between zero and 30 minutes required")
	}
	if err := checkExecutable(argv); err != nil {
		return r, err
	}
	criteria, spec, err := declared(p, argv)
	if err != nil {
		return r, err
	}
	for _, step := range spec.Setup {
		if err := checkExecutable(step); err != nil {
			return r, fmt.Errorf("setup: %w", err)
		}
	}
	r.Criteria = criteria
	r.Repository, err = RepositoryIdentity(ctx, root)
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
	if err = linkDependencies(root, dest, spec.Link); err != nil {
		return r, err
	}
	privateHome, err := os.MkdirTemp("", "radar-evidence-home-")
	if err != nil {
		return r, err
	}
	defer os.RemoveAll(privateHome)
	env := environment(privateHome, spec.Env)
	testDir, err := executionDirectory(dest, spec.CWD)
	if err != nil {
		return r, err
	}
	r.CWD = spec.CWD
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	r.SchemaVersion = "1"
	r.PlanDigest = planning.Digest(p)
	r.Command = append([]string{}, argv...)
	r.Env = append([]string(nil), spec.Env...)
	r.StartedAt = time.Now().UTC().Format(time.RFC3339Nano)
	var setupOut output
	for _, step := range spec.Setup {
		state, err := execute(runCtx, step, testDir, env, &setupOut)
		if err != nil {
			return Record{}, fmt.Errorf("could not start setup command %v: %w", step, err)
		}
		if runCtx.Err() != nil || state.ExitCode() != 0 {
			r.FinishedAt = time.Now().UTC().Format(time.RFC3339Nano)
			r.ExitCode = state.ExitCode()
			r.Phase = "setup"
			r.Status = model.StatusError
			if runCtx.Err() != nil {
				r.Status = model.StatusTimeout
			} else if len(harnessCounts(step, setupOut.b.Bytes(), testDir).BuildErrors) > 0 {
				// The setup build stopped on compiler errors in repository source.
				r.Status = model.StatusFailed
			}
			r.OutputDigest = hex.EncodeToString(setupOut.sum())
			r.OutputTail = string(setupOut.tailBytes())
			r.IntegrityDigest = integrity(r)
			r.ID = "evidence:" + r.IntegrityDigest
			return r, nil
		}
	}
	var out output
	state, err := execute(runCtx, argv, testDir, env, &out)
	if err != nil {
		return Record{}, fmt.Errorf("could not start verification command: %w", err)
	}
	r.FinishedAt = time.Now().UTC().Format(time.RFC3339Nano)
	r.ExitCode = state.ExitCode()
	content := out.b.Bytes()
	r.OutputDigest = hex.EncodeToString(out.sum())
	r.OutputTail = string(append(setupOut.tailBytes(), out.tailBytes()...))
	result := harnessCounts(argv, content, testDir)
	if spec.JUnit != "" {
		if junit, err := readJUnit(testDir, spec.JUnit); err == nil {
			result = junit
		}
	}
	r.TestsRun, r.TestsFailed, r.TestsSkipped, r.Harness = result.Run, result.Failed, result.Skipped, result.Harness
	r.Status = outcome(runCtx.Err() != nil, r.ExitCode, result, out.exceeded, argv, content)
	r.IntegrityDigest = integrity(r)
	r.ID = "evidence:" + r.IntegrityDigest
	return r, nil
}

// outcome separates test failures from commands that failed before any test
// result was recognized (missing dependencies, build or setup errors).
func outcome(timedOut bool, exit int, result harnessResult, exceeded bool, argv []string, content []byte) model.Status {
	switch {
	case result.Failed > 0:
		return model.StatusFailed
	case timedOut:
		return model.StatusTimeout
	case exit != 0 && len(result.BuildErrors) > 0:
		// The combined source does not compile: an observed failure of the
		// source, not of the environment.
		return model.StatusFailed
	case exit == 0 && result.Failed > 0:
		return model.StatusFailed
	case exit == 0 && result.Run > 0 && !exceeded:
		return model.StatusPassed
	case exit == 0:
		return model.StatusUnknown
	case isUnittest(argv) && exit == 5 && strings.Contains(string(content), "Ran 0 tests"):
		return model.StatusUnknown
	case result.Run > 0 || result.Failed > 0:
		return model.StatusFailed
	}
	return model.StatusError
}

func execute(ctx context.Context, argv []string, dir string, env []string, out *output) (*os.ProcessState, error) {
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = dir
	cmd.Env = env
	configureProcess(cmd)
	cmd.WaitDelay = time.Second
	cmd.Stdout = out
	cmd.Stderr = out
	err := cmd.Run()
	if err != nil && cmd.ProcessState == nil {
		return nil, err
	}
	return cmd.ProcessState, nil
}

// environment removes inherited secrets and language startup settings while
// reusing local, read-mostly dependency caches so offline builds can resolve
// already-downloaded modules. Rule-declared variable names pass through.
func environment(privateHome string, passthrough []string) []string {
	home, _ := os.UserHomeDir()
	existing := func(candidates ...string) string {
		for _, c := range candidates {
			if c == "" {
				continue
			}
			if _, err := os.Stat(c); err == nil {
				return c
			}
		}
		return ""
	}
	join := func(base string, parts ...string) string {
		if base == "" {
			return ""
		}
		return filepath.Join(append([]string{base}, parts...)...)
	}
	gopath := strings.Split(os.Getenv("GOPATH"), string(os.PathListSeparator))[0]
	userCache, _ := os.UserCacheDir()
	env := []string{
		"PATH=" + os.Getenv("PATH"), "HOME=" + privateHome, "USERPROFILE=" + privateHome,
		"TMPDIR=" + privateHome, "TMP=" + privateHome, "TEMP=" + privateHome, "LANG=C",
		"GOPROXY=off", "GOSUMDB=off", "GOTOOLCHAIN=local", "GOPATH=" + filepath.Join(privateHome, "go"),
		"PIP_NO_INDEX=1", "PYTHONDONTWRITEBYTECODE=1", "CARGO_NET_OFFLINE=true", "npm_config_offline=true",
		"GIT_TERMINAL_PROMPT=0",
	}
	shared := map[string]string{
		"GOMODCACHE":       existing(os.Getenv("GOMODCACHE"), join(gopath, "pkg", "mod"), join(home, "go", "pkg", "mod")),
		"GOCACHE":          existing(os.Getenv("GOCACHE"), join(userCache, "go-build")),
		"CARGO_HOME":       existing(os.Getenv("CARGO_HOME"), join(home, ".cargo")),
		"RUSTUP_HOME":      existing(os.Getenv("RUSTUP_HOME"), join(home, ".rustup")),
		"npm_config_cache": existing(os.Getenv("npm_config_cache"), join(home, ".npm")),
		// Gradle and the Maven wrapper keep distributions and dependencies
		// here (Maven itself resolves ~/.m2 from the account, not $HOME).
		"GRADLE_USER_HOME": existing(os.Getenv("GRADLE_USER_HOME"), join(home, ".gradle")),
		"MAVEN_USER_HOME":  existing(os.Getenv("MAVEN_USER_HOME"), join(home, ".m2")),
		"JAVA_HOME":        existing(os.Getenv("JAVA_HOME")),
		"PYTHONUSERBASE":   existing(os.Getenv("PYTHONUSERBASE"), join(home, ".local")),
		"VIRTUAL_ENV":      existing(os.Getenv("VIRTUAL_ENV")),
	}
	defaults := map[string]string{"GOMODCACHE": filepath.Join(privateHome, "go-mod"), "GOCACHE": filepath.Join(privateHome, "go-cache")}
	keys := make([]string, 0, len(shared))
	for k := range shared {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		value := shared[k]
		if value == "" {
			value = defaults[k]
		}
		if value != "" {
			env = append(env, k+"="+value)
		}
	}
	for _, name := range passthrough {
		if value, ok := os.LookupEnv(name); ok {
			env = append(env, name+"="+value) // later entries win in exec.Cmd
		}
	}
	return env
}

// linkDependencies exposes declared untracked dependency directories (for
// example node_modules or .venv) from the checkout inside the snapshot.
func linkDependencies(root, dest string, links []string) error {
	for _, link := range links {
		source, err := pathutil.ResolveInside(root, link)
		if err != nil {
			return fmt.Errorf("link %s: %w", link, err)
		}
		target := filepath.Join(dest, filepath.FromSlash(link))
		if _, err := os.Lstat(target); err == nil {
			return fmt.Errorf("link %s: path exists in the committed snapshot", link)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			return err
		}
		if err := os.Symlink(source, target); err != nil {
			return fmt.Errorf("link %s: %w", link, err)
		}
	}
	return nil
}

// WrapperUnavailable explains why a Gradle or Maven wrapper in dir would
// have to download its pinned distribution, which Radar never lets a run do:
// the wrapper fetches it before the build tool's offline mode applies. It
// reads the committed wrapper properties and the shared user homes as data
// and returns "" when the distribution is already cached.
func WrapperUnavailable(dir string, argv []string) string {
	if len(argv) == 0 {
		return ""
	}
	var properties, homeVar, homeDefault string
	switch filepath.Base(argv[0]) {
	case "gradlew":
		properties, homeVar, homeDefault = filepath.Join("gradle", "wrapper", "gradle-wrapper.properties"), "GRADLE_USER_HOME", ".gradle"
	case "mvnw":
		properties, homeVar, homeDefault = filepath.Join(".mvn", "wrapper", "maven-wrapper.properties"), "MAVEN_USER_HOME", ".m2"
	default:
		return ""
	}
	full, err := pathutil.ResolveInside(dir, filepath.ToSlash(properties))
	if err != nil {
		return "wrapper properties " + filepath.ToSlash(properties) + " are absent"
	}
	data, err := os.ReadFile(full)
	if err != nil || len(data) > 64<<10 {
		return "wrapper properties " + filepath.ToSlash(properties) + " are unreadable"
	}
	var url string
	for _, line := range strings.Split(string(data), "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "distributionUrl="); ok {
			url = strings.ReplaceAll(strings.TrimSpace(v), `\:`, ":")
		}
	}
	name := strings.TrimSuffix(strings.TrimSuffix(url[strings.LastIndex(url, "/")+1:], ".zip"), ".tar.gz")
	if name == "" || !safeName.MatchString(name) {
		return "wrapper distributionUrl is missing or not a plain archive name"
	}
	home := os.Getenv(homeVar)
	if home == "" {
		if user, err := os.UserHomeDir(); err == nil {
			home = filepath.Join(user, homeDefault)
		}
	}
	if entries, err := os.ReadDir(filepath.Join(home, "wrapper", "dists", name)); err != nil || len(entries) == 0 {
		return "wrapper distribution " + name + " is not cached in " + homeVar + " and Radar does not download it"
	}
	return ""
}
