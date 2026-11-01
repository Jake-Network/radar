package evidence

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/Jake-Network/radar/internal/pathutil"
)

// GoTypecheckFiles uses the same recognized launcher as a canonical go vet
// command. Other commands cannot borrow the inventory of go list ./....
func GoTypecheckFiles(ctx context.Context, root, cwd string, argv []string, timeout time.Duration) (map[string]bool, error) {
	u := runnerArgv(argv)
	if len(u) != 4 || strings.TrimSuffix(strings.ToLower(filepath.Base(u[0])), ".exe") != "go" || u[1] != "vet" || u[2] != "-buildtag" || u[3] != "./..." {
		return nil, errors.New("source inventory requires a recognized go vet -buildtag ./... command")
	}
	probe := append([]string(nil), argv[:len(argv)-len(u)+1]...)
	probe = append(probe, "list", "-e", "-json=Dir,GoFiles,CgoFiles,TestGoFiles,XTestGoFiles", "./...")
	data, err := typecheckInventory(ctx, root, cwd, probe, timeout)
	if err != nil {
		return nil, err
	}
	files := map[string]bool{}
	decoder := json.NewDecoder(bytes.NewReader(data))
	for {
		var pkg struct {
			Dir                                          string
			GoFiles, CgoFiles, TestGoFiles, XTestGoFiles []string
		}
		if err := decoder.Decode(&pkg); err == io.EOF {
			break
		} else if err != nil {
			return nil, errors.New("go source inventory could not be decoded")
		}
		rel, ok := relativeDir(root, pkg.Dir)
		if !ok {
			continue
		}
		for _, list := range [][]string{pkg.GoFiles, pkg.CgoFiles, pkg.TestGoFiles, pkg.XTestGoFiles} {
			for _, file := range list {
				files[path.Join(rel, file)] = true
			}
		}
	}
	if len(files) == 0 {
		return nil, errors.New("go source inventory selected no repository files")
	}
	return files, nil
}

// RuffCheckedFiles asks the recognized ruff check invocation which files its
// selectors include. Trigger globs alone never establish this inventory.
func RuffCheckedFiles(ctx context.Context, root, cwd string, argv []string, timeout time.Duration) (map[string]bool, error) {
	u := runnerArgv(argv)
	if len(u) < 2 || strings.TrimSuffix(strings.ToLower(filepath.Base(u[0])), ".exe") != "ruff" || u[1] != "check" {
		return nil, errors.New("source inventory requires a recognized ruff check command")
	}
	probe := append([]string(nil), argv[:len(argv)-len(u)+2]...)
	probe = append(probe, "--show-files")
	for i := 2; i < len(u); i++ {
		arg := u[i]
		if arg == "--" {
			probe = append(probe, u[i:]...)
			break
		}
		switch arg {
		case "--show-files", "--show-settings", "--help", "-h", "--version", "-V":
			return nil, errors.New("non-checking ruff output modes have no supported source inventory")
		}
		if arg == "--output-format" {
			i++
			continue
		}
		if strings.HasPrefix(arg, "--output-format=") {
			continue
		}
		if arg == "--fix" || arg == "--fix-only" || arg == "--watch" || arg == "--diff" {
			return nil, errors.New("mutating or watch ruff commands have no supported source inventory")
		}
		probe = append(probe, arg)
	}
	data, err := typecheckInventory(ctx, root, cwd, probe, timeout)
	if err != nil {
		return nil, err
	}
	dir, err := pathutil.ResolveInside(root, cwd)
	if err != nil {
		return nil, err
	}
	files := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		full := line
		if !filepath.IsAbs(full) {
			full = filepath.Join(dir, filepath.FromSlash(full))
		}
		rel, ok := insidePath(root, full)
		if !ok {
			return nil, errors.New("ruff source inventory contains a path outside the candidate")
		}
		file, err := pathutil.ResolveInside(root, rel)
		if err != nil {
			return nil, errors.New("ruff source inventory contains an unsafe path")
		}
		if info, err := os.Stat(file); err != nil || !info.Mode().IsRegular() {
			return nil, errors.New("ruff source inventory contains a missing file")
		}
		files[rel] = true
	}
	if len(files) == 0 {
		return nil, errors.New("ruff source inventory selected no repository files")
	}
	return files, nil
}

func typecheckInventory(ctx context.Context, root, cwd string, argv []string, timeout time.Duration) ([]byte, error) {
	if timeout <= 0 || timeout > 30*time.Minute {
		return nil, errors.New("invalid typecheck inventory timeout")
	}
	if err := checkExecutable(argv); err != nil {
		return nil, err
	}
	dir, err := pathutil.ResolveInside(root, cwd)
	if err != nil {
		return nil, err
	}
	home, err := os.MkdirTemp("", "radar-typecheck-inventory-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(home)
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(runCtx, argv[0], argv[1:]...)
	cmd.Dir = dir
	cmd.Env = environment(home, nil)
	configureProcess(cmd)
	cmd.WaitDelay = time.Second
	var out limitedBuffer
	out.limit = maxGoListBytes
	cmd.Stdout = &out
	cmd.Stderr = io.Discard
	runErr := cmd.Run()
	if runCtx.Err() != nil {
		return nil, errors.New("typecheck source inventory timed out")
	}
	if out.exceeded {
		return nil, errors.New("typecheck source inventory exceeds output limit")
	}
	if runErr != nil {
		return nil, errors.New("typecheck source inventory command failed")
	}
	return out.b.Bytes(), nil
}
