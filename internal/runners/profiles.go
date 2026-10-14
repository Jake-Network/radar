// Package runners reads explicit execution declarations without running them.
package runners

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"runtime"
	"strings"

	"github.com/Jake-Network/radar/internal/indexer"
	"github.com/Jake-Network/radar/internal/pathutil"
)

const Manifest = ".radar/runners.json"
const MaxBytes = 1 << 20

type Profile struct {
	Root      string     `json:"root"`
	Prefix    []string   `json:"prefix"`
	Setup     [][]string `json:"setup,omitempty"`
	Exclude   []string   `json:"exclude,omitempty"`
	Lockfiles []string   `json:"lockfiles,omitempty"`
	Artifacts []string   `json:"artifacts,omitempty"`
	Coverage  *Coverage  `json:"coverage,omitempty"`
}

// Coverage declares a fresh tool-generated report; Radar never runs undeclared
// instrumentation commands or assumes an existing report belongs to this run.
type Coverage struct {
	Format string `json:"format"`
	Report string `json:"report"`
}
type Config struct {
	Version       int       `json:"version"`
	Profiles      []Profile `json:"profiles"`
	MaxCacheBytes int64     `json:"max_cache_bytes,omitempty"`
}

func Parse(data []byte) (Config, error) {
	var c Config
	if len(data) > MaxBytes {
		return c, fmt.Errorf("runner manifest exceeds size limit")
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(&c); err != nil {
		return c, err
	}
	if d.Decode(new(any)) != io.EOF {
		return c, fmt.Errorf("trailing runner manifest data")
	}
	if c.Version != 1 || c.Profiles == nil || len(c.Profiles) > 128 {
		return c, fmt.Errorf("unsupported runner version or profile count")
	}
	if c.MaxCacheBytes == 0 {
		c.MaxCacheBytes = 512 << 20
	}
	if c.MaxCacheBytes < 1 || c.MaxCacheBytes > 10<<30 {
		return c, fmt.Errorf("cache budget must be between 1 byte and 10 GiB")
	}
	seen := map[string]bool{}
	for i := range c.Profiles {
		p := &c.Profiles[i]
		if err := Validate(*p); err != nil {
			return c, err
		}
		if seen[p.Root] {
			return c, fmt.Errorf("duplicate runner root %q", p.Root)
		}
		seen[p.Root] = true
	}
	return c, nil
}

func Validate(p Profile) error {
	clean, err := pathutil.RepoRelative(p.Root)
	if err != nil || clean != p.Root {
		return fmt.Errorf("runner root must be a clean repository-relative path")
	}
	if len(p.Prefix) == 0 {
		return fmt.Errorf("runner prefix required")
	}
	if err := ValidateArgv(p.Prefix); err != nil {
		return err
	}
	if len(p.Setup) > 16 || len(p.Lockfiles) > 32 || len(p.Artifacts) > 8 || len(p.Exclude) > 256 {
		return fmt.Errorf("runner declaration budget exceeded")
	}
	for _, argv := range p.Setup {
		if err := ValidateArgv(argv); err != nil {
			return err
		}
	}
	for _, arg := range p.Exclude {
		if len(arg) > 8192 || strings.ContainsAny(arg, "\x00\r\n") {
			return fmt.Errorf("invalid exclude argument")
		}
	}
	for _, f := range append(append([]string{}, p.Lockfiles...), p.Artifacts...) {
		clean, err := pathutil.RepoRelative(f)
		if err != nil || clean != f || clean == "." {
			return fmt.Errorf("invalid runner artifact or lockfile path %q", f)
		}
	}
	for _, f := range p.Artifacts {
		switch path.Base(f) {
		case ".venv", "node_modules", "target":
		default:
			return fmt.Errorf("only .venv, node_modules and target dependency artifacts may be cached")
		}
	}
	if len(p.Artifacts) > 0 && len(p.Lockfiles) == 0 {
		return fmt.Errorf("cached artifacts require explicit lockfiles")
	}
	if p.Coverage != nil {
		switch p.Coverage.Format {
		case "python", "go", "jacoco", "llvm", "c8":
		default:
			return fmt.Errorf("unsupported coverage report format")
		}
		clean, err := pathutil.RepoRelative(p.Coverage.Report)
		if err != nil || clean != p.Coverage.Report || clean == "." {
			return fmt.Errorf("invalid coverage report path")
		}
	}
	return nil
}

func ValidateArgv(argv []string) error {
	if len(argv) == 0 || len(argv) > 256 || argv[0] == "" {
		return fmt.Errorf("bounded nonempty argv required")
	}
	for _, arg := range argv {
		if len(arg) > 8192 || strings.ContainsAny(arg, "\x00\r\n") {
			return fmt.Errorf("invalid runner argument")
		}
	}
	if strings.Contains(argv[0], "/") {
		if _, err := pathutil.RepoRelative(argv[0]); err != nil {
			return err
		}
	} else if strings.Contains(argv[0], "\\") {
		return fmt.Errorf("invalid runner executable")
	}
	return nil
}

// Load only reads the immutable provider's manifest; absence is an empty config.
func Load(ctx context.Context, p indexer.Provider) (Config, error) {
	entries, err := p.Entries(ctx)
	if err != nil {
		return Config{}, err
	}
	for _, e := range entries {
		if e.Path != Manifest {
			continue
		}
		if e.Mode != indexer.ModeRegular || e.Size > MaxBytes {
			return Config{}, fmt.Errorf("unsafe runner manifest")
		}
		b, err := p.Read(ctx, e, MaxBytes)
		if err != nil {
			return Config{}, err
		}
		return Parse(b)
	}
	return Config{Version: 1, MaxCacheBytes: 512 << 20}, nil
}

func (c Config) Match(cwd string) *Profile {
	if cwd == "" {
		cwd = "."
	}
	var best *Profile
	for i := range c.Profiles {
		p := &c.Profiles[i]
		if cwd == p.Root || p.Root == "." || strings.HasPrefix(cwd, p.Root+"/") {
			if best == nil || best.Root == "." && p.Root != "." || len(p.Root) > len(best.Root) {
				best = p
			}
		}
	}
	return best
}

// Rewrite preserves argv boundaries. Known package-manager entry points are
// replaced with their declared prefix; generic prefixes wrap the full runner.
func (p Profile) Rewrite(argv []string) []string {
	if len(argv) == 0 {
		return nil
	}
	tail := append([]string{}, argv...)
	first := path.Base(p.Prefix[0])
	original := path.Base(argv[0])
	if first == original || (first == "python" || first == "python3") && (original == "python" || original == "python3") {
		tail = tail[1:]
	} else if (first == "pnpm" || first == "npm" || first == "yarn") && strings.Contains(argv[0], "node_modules/.bin/") {
		tail[0] = original
	}
	out := append(append([]string{}, p.Prefix...), tail...)
	return append(out, p.Exclude...)
}

// CacheKey binds artifacts to declaration, platform, repository identity and
// every declared lockfile. Empty/missing inputs never produce a reusable key.
func CacheKey(p Profile, repository string, lockfiles map[string][]byte) (string, error) {
	if err := Validate(p); err != nil {
		return "", err
	}
	if repository == "" || len(p.Lockfiles) == 0 {
		return "", fmt.Errorf("cache identity and lockfiles required")
	}
	h := sha256.New()
	declaration, _ := json.Marshal(p)
	h.Write(declaration)
	for _, s := range []string{runtime.GOOS, runtime.GOARCH, repository} {
		h.Write([]byte{0})
		h.Write([]byte(s))
	}
	for _, f := range p.Lockfiles {
		b, ok := lockfiles[f]
		if !ok || len(b) == 0 {
			return "", fmt.Errorf("missing or empty lockfile %q", f)
		}
		h.Write([]byte{0})
		h.Write([]byte(f))
		h.Write([]byte{0})
		h.Write(b)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
