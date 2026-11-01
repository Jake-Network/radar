package evidence

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Jake-Network/radar/internal/pathutil"
)

// mypy prints "FILE:LINE:COL: error: MESSAGE  [code]" with
// --show-column-numbers --show-error-codes; notes follow their error.
var (
	mypyLine      = regexp.MustCompile(`^(.+?\.pyi?):([0-9]+):(?:[0-9]+:)? (error|note|warning): (.+?)(?:  \[([a-z0-9-]{1,60})\])?$`)
	mypyMissing   = regexp.MustCompile(`^Cannot find implementation or library stub for module named "([A-Za-z0-9_.]{1,100})"`)
	mypyUntyped   = regexp.MustCompile(`^(?:Library stubs not installed for|Skipping analyzing) "([A-Za-z0-9_.]{1,100})"`)
	mypyName      = regexp.MustCompile(`^Name "([A-Za-z_][A-Za-z0-9_]{0,99})" is not defined`)
	mypyAttribute = regexp.MustCompile(`^(?:Module )?"([A-Za-z_][A-Za-z0-9_.]{0,99})" has no attribute "([A-Za-z_][A-Za-z0-9_]{0,99})"`)
)

func parseMypy(text string, n *typecheckNormalizer) typecheckParse {
	var p typecheckParse
	for _, line := range strings.Split(text, "\n") {
		m := mypyLine.FindStringSubmatch(strings.TrimRight(line, "\r"))
		if m == nil || m[3] != "error" {
			continue
		}
		message, code := m[4], m[5]
		if strings.HasPrefix(message, "INTERNAL ERROR") {
			// mypy crashed; the location is where, not what is wrong.
			p.diagnostics, p.crashed = nil, true
			return p
		}
		if u := mypyUntyped.FindStringSubmatch(message); u != nil || code == "import-untyped" {
			// An installed package without type information, or its stubs
			// missing: types from it are unknown to the run.
			name := "python"
			if u != nil {
				name = u[1]
			}
			p.diagnostics = nil
			p.environment = &Diagnosis{Kind: "module_missing", Name: name, Message: fmt.Sprintf("mypy has no type information for %s (the package or its stubs are missing from this Python environment); the combined source was not type-checked", name)}
			return p
		}
		path, located := n.path(m[1])
		if missing := mypyMissing.FindStringSubmatch(message); missing != nil || code == "import-not-found" {
			module := ""
			if missing != nil {
				module = missing[1]
			}
			if module == "" || !n.localPythonModule(module) || !located {
				name := module
				if name == "" {
					name = "python"
				}
				p.diagnostics = nil
				p.environment = &Diagnosis{Kind: "module_missing", Name: name, Message: fmt.Sprintf("mypy could not find module %s in this Python environment (a dependency is not installed, or mypy is not configured to ignore it); the combined source was not type-checked", name)}
				return p
			}
			// A module of the project itself is missing: a branch removed
			// or moved what another still imports.
			number, _ := strconv.Atoi(m[2])
			p.diagnostics = append(p.diagnostics, n.diagnostic(path, number, "missing repository module "+module, ""))
			continue
		}
		if !located {
			continue
		}
		number, _ := strconv.Atoi(m[2])
		symbol := ""
		if u := mypyName.FindStringSubmatch(message); u != nil {
			symbol = u[1]
		} else if a := mypyAttribute.FindStringSubmatch(message); a != nil && code == "attr-defined" {
			symbol = a[1] + "." + a[2]
		}
		if code != "" {
			message += " [" + code + "]"
		}
		p.diagnostics = append(p.diagnostics, n.diagnostic(path, number, message, symbol))
	}
	return p
}

// ruff prints "FILE:LINE:COL: CODE MESSAGE" with --output-format concise;
// syntax errors use "SyntaxError:" or "invalid-syntax:" in place of a code.
var (
	ruffLine      = regexp.MustCompile(`^(.+?\.pyi?):([0-9]+):[0-9]+: ([A-Z]{1,4}[0-9]{1,4}|SyntaxError|invalid-syntax):? (?:\[\*\] )?(.+)$`)
	ruffUndefined = regexp.MustCompile("^Undefined name `([A-Za-z_][A-Za-z0-9_]{0,99})`")
	ruffNoFiles   = regexp.MustCompile(`No Python files found`)
)

func parseRuff(text string, n *typecheckNormalizer) typecheckParse {
	var p typecheckParse
	for _, line := range strings.Split(text, "\n") {
		if ruffNoFiles.MatchString(line) {
			p.noInputs = true
			continue
		}
		m := ruffLine.FindStringSubmatch(strings.TrimRight(line, "\r"))
		if m == nil {
			continue
		}
		if m[3] == "SyntaxError" || m[3] == "invalid-syntax" {
			// ruff does not check the rest of a file it cannot parse.
			p.partial = true
		}
		path, ok := n.path(m[1])
		if !ok {
			continue
		}
		number, _ := strconv.Atoi(m[2])
		symbol := ""
		if u := ruffUndefined.FindStringSubmatch(m[4]); u != nil {
			symbol = u[1]
		}
		p.diagnostics = append(p.diagnostics, n.diagnostic(path, number, m[3]+" "+m[4], symbol))
	}
	return p
}

// mypySourcesProbe asks mypy which files it would check for the given
// arguments, using its own option and configuration handling. It imports
// only mypy, never project code, and prints one JSON list.
const mypySourcesProbe = `import json, sys
from mypy.main import process_options
sources, _ = process_options(sys.argv[1:])
print(json.dumps([s.path for s in sources if s.path]))
`

// MypySources returns the repository paths of the files mypy would check
// when run with args in cwd, with the interpreter, environment and
// configuration the type-check command uses. Like GoCompiledFiles it runs
// only after execution opt-in; an error leaves every changed file unchecked.
func MypySources(ctx context.Context, root, cwd string, args []string, timeout time.Duration) (map[string]bool, error) {
	if timeout <= 0 || timeout > 30*time.Minute {
		return nil, errors.New("invalid mypy source probe timeout")
	}
	dir := root
	if cwd != "" && cwd != "." {
		var err error
		if dir, err = pathutil.ResolveInside(root, cwd); err != nil {
			return nil, err
		}
	}
	home, err := os.MkdirTemp("", "radar-mypy-sources-home-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(home)
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(runCtx, "python3", append([]string{"-c", mypySourcesProbe}, args...)...)
	cmd.Dir = dir
	cmd.Env = environment(home, nil)
	configureProcess(cmd)
	cmd.WaitDelay = time.Second
	var out limitedBuffer
	out.limit = maxGoListBytes
	cmd.Stdout = &out
	cmd.Stderr = io.Discard
	runErr := cmd.Run()
	switch {
	case runCtx.Err() != nil:
		return nil, errors.New("mypy source probe timed out")
	case out.exceeded:
		return nil, errors.New("mypy source probe output exceeds limit")
	case runErr != nil:
		return nil, fmt.Errorf("mypy source probe: %w", runErr)
	}
	var paths []string
	lines := strings.Split(strings.TrimSpace(out.b.String()), "\n")
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &paths); err != nil {
		return nil, fmt.Errorf("mypy source probe output: %w", err)
	}
	files := map[string]bool{}
	for _, p := range paths {
		if rel, ok := sourcePath(dir, p); ok {
			if cwd != "" && cwd != "." {
				rel = filepath.ToSlash(filepath.Join(cwd, rel))
			}
			if clean, err := pathutil.RepoRelative(rel); err == nil {
				files[clean] = true
			}
		}
	}
	return files, nil
}
