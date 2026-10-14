package evidence

import (
	"context"
	"encoding/hex"
	"fmt"
	"os"
	"path"
	"strings"
	"time"

	gitrepo "github.com/Jake-Network/radar/internal/git"
	"github.com/Jake-Network/radar/internal/model"
	"github.com/Jake-Network/radar/internal/pathutil"
	"github.com/Jake-Network/radar/internal/runners"
)

type RunnerPreparation struct {
	Status        model.Status `json:"status"`
	CacheHit      bool         `json:"cache_hit"`
	CacheKey      string       `json:"cache_key,omitempty"`
	SetupCommands int          `json:"setup_commands"`
	OutputDigest  string       `json:"output_digest,omitempty"`
	CacheWarning  string       `json:"cache_warning,omitempty"`
	Artifacts     []string     `json:"artifacts"`
}

// PrepareRunner executes only explicitly declared setup, after the caller has
// obtained --run opt-in. Cached artifacts never count as test evidence.
func PrepareRunner(ctx context.Context, root, repository string, p runners.Profile, cache runners.Cache, timeout time.Duration) (RunnerPreparation, error) {
	return PrepareRunnerBudget(ctx, root, repository, p, cache, timeout, nil)
}

// PrepareRunnerBudget reserves command budget only for setup steps that
// actually execute. A cache hit consumes no command slots.
func PrepareRunnerBudget(ctx context.Context, root, repository string, p runners.Profile, cache runners.Cache, timeout time.Duration, take func() bool) (RunnerPreparation, error) {
	r := RunnerPreparation{Status: model.StatusPassed, Artifacts: []string{}}
	if err := runners.Validate(p); err != nil {
		return r, err
	}
	if timeout <= 0 || timeout > 30*time.Minute {
		return r, fmt.Errorf("invalid runner setup timeout")
	}
	dir, err := pathutil.ResolveInside(root, p.Root)
	if err != nil {
		return r, err
	}
	for _, a := range p.Artifacts {
		r.Artifacts = append(r.Artifacts, path.Join(p.Root, a))
	}
	head, err := gitrepo.Resolve(ctx, root, "HEAD")
	if err != nil {
		return r, fmt.Errorf("runner setup requires a committed private candidate: %w", err)
	}
	cp := CandidateCheckpoint{Repository: repository, Revision: head}
	cache.SourceRoot = root
	before, err := InspectCandidateSource(ctx, root, cp, r.Artifacts)
	if err != nil || !before.Matches {
		return r, fmt.Errorf("runner candidate source is not pristine")
	}
	lockfiles := map[string][]byte{}
	if len(p.Artifacts) > 0 {
		for _, f := range p.Lockfiles {
			file, err := pathutil.ResolveInside(dir, f)
			if err != nil {
				return r, err
			}
			info, err := os.Stat(file)
			if err != nil || !info.Mode().IsRegular() || info.Size() > runners.MaxBytes {
				return r, fmt.Errorf("unsafe runner lockfile")
			}
			b, err := os.ReadFile(file)
			if err != nil {
				return r, err
			}
			lockfiles[f] = b
		}
		cacheIdentity := repository
		for _, artifact := range p.Artifacts {
			if path.Base(artifact) == "target" {
				tree, treeErr := gitrepo.Tree(ctx, root, head)
				if treeErr != nil {
					return r, treeErr
				}
				cacheIdentity += "\x00compiled-source-tree:" + tree
				break
			}
		}
		r.CacheKey, err = runners.CacheKey(p, cacheIdentity, lockfiles)
		if err != nil {
			return r, err
		}
		hit, e := cache.Restore(ctx, r.CacheKey, dir, p)
		if e != nil {
			r.CacheWarning = "dependency cache unavailable; declared setup will run"
		}
		if hit {
			r.CacheHit = true
			return r, nil
		}
	}
	home, err := os.MkdirTemp("", "radar-runner-home-")
	if err != nil {
		return r, err
	}
	defer os.RemoveAll(home)
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var out output
	for _, argv := range p.Setup {
		if take != nil && !take() {
			r.Status = model.StatusIncomplete
			return r, nil
		}
		if err := checkExecutable(argv); err != nil {
			return r, err
		}
		if len(argv[0]) > 0 && (argv[0][0] == '.' || containsSlash(argv[0])) {
			if _, err := pathutil.ResolveInside(dir, argv[0]); err != nil {
				return r, err
			}
		}
		state, err := execute(runCtx, argv, dir, profileSetupEnvironment(home), &out)
		r.SetupCommands++
		r.OutputDigest = hex.EncodeToString(out.sum())
		if err != nil {
			return r, fmt.Errorf("runner setup executable unavailable: %w", err)
		}
		if runCtx.Err() != nil {
			r.Status = model.StatusTimeout
			return r, nil
		}
		if state.ExitCode() != 0 || out.exceeded {
			r.Status = model.StatusError
			return r, nil
		}
	}
	after, err := InspectCandidateSource(ctx, root, cp, r.Artifacts)
	if err != nil {
		return r, err
	}
	if !after.Matches {
		r.Status = model.StatusIncomplete
		r.CacheWarning = "setup changed candidate source; dependency cache was not published"
		return r, nil
	}
	if len(p.Artifacts) > 0 {
		if err := cache.Store(ctx, r.CacheKey, dir, p); err != nil {
			r.CacheWarning = "dependency artifacts were not cached (unsafe, missing or over budget)"
		}
	}
	return r, nil
}

// Explicit profile setup may retrieve its declared dependencies. Start from
// the same private, scrubbed environment as tests and remove only Radar's
// offline dependency switches; host secrets and startup variables stay absent.
// Normal verification and legacy plan setup retain their existing environment.
func profileSetupEnvironment(home string) []string {
	base := environment(home, nil)
	out := make([]string, 0, len(base))
	for _, value := range base {
		name, _, _ := strings.Cut(value, "=")
		switch name {
		case "PIP_NO_INDEX", "CARGO_NET_OFFLINE", "npm_config_offline", "GOPROXY", "GOSUMDB":
			continue
		}
		out = append(out, value)
	}
	return out
}
func containsSlash(s string) bool {
	for _, r := range s {
		if r == '/' {
			return true
		}
	}
	return false
}
