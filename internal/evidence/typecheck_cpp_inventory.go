package evidence

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Jake-Network/radar/internal/pathutil"
)

// CPPCompiledFiles asks the same compiler/preprocessor for its actual local
// dependency set. It is an execution probe, only used after execution opt-in.
func CPPCompiledFiles(ctx context.Context, root, cwd string, argv []string, timeout time.Duration) (map[string]bool, error) {
	if err := checkExecutable(argv); err != nil {
		return nil, err
	}
	if timeout <= 0 || timeout > 30*time.Minute {
		return nil, errors.New("invalid compiler inventory timeout")
	}
	dir, err := pathutil.ResolveInside(root, cwd)
	if err != nil {
		return nil, err
	}
	for i := 1; i < len(argv); i++ {
		for _, prefix := range []string{"-I", "-iquote", "-include", "-isystem"} {
			if !strings.HasPrefix(argv[i], prefix) {
				continue
			}
			value := strings.TrimPrefix(argv[i], prefix)
			if value == "" {
				if i+1 >= len(argv) {
					return nil, errors.New("compiler include option has no path")
				}
				value = argv[i+1]
			}
			full := value
			if !filepath.IsAbs(full) {
				full = filepath.Join(dir, value)
			}
			rel, relErr := filepath.Rel(root, full)
			if relErr != nil {
				return nil, errors.New("compiler include path escapes candidate")
			}
			if _, pathErr := pathutil.ResolveInside(root, filepath.ToSlash(rel)); pathErr != nil && !os.IsNotExist(pathErr) {
				return nil, errors.New("compiler include search path provenance could not be verified")
			}
			break
		}
	}
	home, err := os.MkdirTemp("", "radar-cpp-inventory-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(home)
	args := make([]string, 0, len(argv)+3)
	for _, a := range argv {
		if a != "-fsyntax-only" {
			args = append(args, a)
		}
	}
	args = append(args, "-MM", "-MF", "-")
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(runCtx, args[0], args[1:]...)
	cmd.Dir = dir
	cmd.Env = environment(home, nil)
	configureProcess(cmd)
	cmd.WaitDelay = time.Second
	var output limitedBuffer
	output.limit = 4 << 20
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Run(); err != nil || runCtx.Err() != nil || output.exceeded {
		return nil, errors.New("compiler dependency probe did not complete")
	}
	text := strings.ReplaceAll(output.b.String(), "\\\r\n", "")
	text = strings.ReplaceAll(text, "\\\n", "")
	out := map[string]bool{}
	for _, line := range strings.Split(text, "\n") {
		_, deps, ok := strings.Cut(line, ": ")
		if !ok {
			continue
		}
		names, ok := makeDependencyWords(deps)
		if !ok {
			return nil, errors.New("compiler dependency inventory has unsupported escaping")
		}
		for _, name := range names {
			full := name
			if !filepath.IsAbs(full) {
				full = filepath.Join(dir, name)
			}
			rel, err := filepath.Rel(root, full)
			if err != nil {
				return nil, errors.New("compiler reads a source dependency outside the candidate")
			}
			clean, err := pathutil.RepoRelative(filepath.ToSlash(rel))
			if err != nil {
				return nil, errors.New("compiler reads a source dependency outside the candidate")
			}
			if _, err = pathutil.ResolveInside(root, clean); err == nil {
				out[clean] = true
			} else {
				return nil, errors.New("compiler dependency provenance could not be verified")
			}
		}
	}
	if len(out) == 0 {
		return nil, errors.New("compiler reported no local inputs")
	}
	return out, nil
}

func makeDependencyWords(s string) ([]string, bool) {
	var out []string
	var word strings.Builder
	escaped := false
	flush := func() {
		if word.Len() > 0 {
			out = append(out, word.String())
			word.Reset()
		}
	}
	for _, c := range s {
		if escaped {
			word.WriteRune(c)
			escaped = false
			continue
		}
		switch c {
		case '\\':
			escaped = true
		case ' ', '\t':
			flush()
		case '$', '#':
			return nil, false
		default:
			word.WriteRune(c)
		}
	}
	if escaped {
		return nil, false
	}
	flush()
	return out, true
}
