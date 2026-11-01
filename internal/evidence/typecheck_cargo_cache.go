package evidence

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"

	gitrepo "github.com/Jake-Network/radar/internal/git"
	"github.com/Jake-Network/radar/internal/pathutil"
)

// TypecheckOptions contains only Radar-owned, allowlisted execution settings.
// It intentionally cannot forward arbitrary caller or host environment values.
type TypecheckOptions struct{ CargoTargetDir string }

func (o TypecheckOptions) Environment(tool string) ([]string, error) {
	if o.CargoTargetDir == "" {
		return nil, nil
	}
	if tool != "cargo" || !filepath.IsAbs(o.CargoTargetDir) {
		return nil, errors.New("invalid Cargo typecheck cache setting")
	}
	info, err := os.Lstat(o.CargoTargetDir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("Cargo cache directory unavailable")
	}
	return []string{"CARGO_TARGET_DIR=" + o.CargoTargetDir}, nil
}

// PrepareCargoTarget uses the exact repository source tree as cache scope.
// Base and candidate cannot reuse artifacts for different source trees.
func PrepareCargoTarget(ctx context.Context, root string) (string, error) {
	identity, err := RepositoryIdentity(ctx, root)
	if err != nil {
		return "", err
	}
	tree, err := gitrepo.Tree(ctx, root, "HEAD")
	if err != nil {
		return "", err
	}
	state := os.Getenv("RADAR_STATE_DIR")
	if state == "" {
		cache, err := os.UserCacheDir()
		if err != nil {
			return "", err
		}
		state = filepath.Join(cache, "radar")
	}
	state, err = filepath.Abs(state)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(identity))
	dir, err := pathutil.StatePath(state, filepath.Join("typecheck-cache", "cargo", hex.EncodeToString(sum[:]), tree))
	if err != nil {
		return "", err
	}
	if err = os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	if _, err = pathutil.StatePath(state, filepath.Join("typecheck-cache", "cargo", hex.EncodeToString(sum[:]), tree)); err != nil {
		return "", err
	}
	return dir, nil
}

func CargoCompiledFiles(root, targetDir string) (map[string]bool, error) {
	if !filepath.IsAbs(targetDir) {
		return nil, errors.New("Cargo inventory requires an absolute owned target directory")
	}
	return compiledTypecheckFiles(root, targetDir, "cargo", nil)
}

// CargoArtifactFiles accepts dep-info only for artifacts reported by this exact
// invocation, so unused stale files in a warm target directory cannot add coverage.
func CargoArtifactFiles(root, targetDir string, artifacts []string) (map[string]bool, error) {
	allowed := map[string]bool{}
	for _, artifact := range artifacts {
		if !filepath.IsAbs(artifact) {
			continue
		}
		rel, err := filepath.Rel(targetDir, artifact)
		if err != nil {
			continue
		}
		safe, err := pathutil.StatePath(targetDir, rel)
		if err != nil {
			continue
		}
		stem := strings.TrimSuffix(safe, filepath.Ext(safe))
		allowed[stem+".d"] = true
		name := filepath.Base(stem)
		if strings.HasPrefix(name, "lib") {
			allowed[filepath.Join(filepath.Dir(stem), strings.TrimPrefix(name, "lib"))+".d"] = true
		}
	}
	if len(allowed) == 0 {
		return nil, errors.New("Cargo did not report artifacts for this invocation")
	}
	return compiledTypecheckFiles(root, targetDir, "cargo", allowed)
}
