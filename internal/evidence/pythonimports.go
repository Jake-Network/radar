package evidence

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var pythonExecutableName = regexp.MustCompile(`^python(?:[23](?:\.[0-9]+)?)?(?:\.exe)?$`)
var pythonModuleName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// pythonImportCheck runs only after execution opt-in. It probes the same
// interpreter (including a declared uv prefix), working directory and environment
// as the test. No path is injected to conceal an externally editable package.
func pythonImportCheck(ctx context.Context, root, dir string, argv, env []string) *Diagnosis {
	unwrapped := runnerArgv(argv)
	if len(unwrapped) == 0 {
		return nil
	}
	tool := strings.ToLower(filepath.Base(unwrapped[0]))
	python := pythonExecutableName.MatchString(tool)
	console := tool == "pytest" || tool == "py.test" || tool == "pytest.exe" || tool == "py.test.exe"
	uv := strings.TrimSuffix(strings.ToLower(filepath.Base(argv[0])), ".exe") == "uv"
	if !python && !console {
		// Unknown wrappers around a recognised Python harness cannot establish
		// its interpreter. Keep first-party provenance unverified.
		if !uv && !uses(argv, "pytest") && !uses(argv, "py.test") {
			return nil
		}
		names, err := firstPartyPythonContext(ctx, root)
		if err != nil || len(names) > 0 {
			return pythonImportDiagnosis("python", false)
		}
		return nil
	}
	names, err := firstPartyPythonForCommandContext(ctx, root, dir, unwrapped)
	if err != nil {
		return pythonImportDiagnosis("python", false)
	}
	if len(names) == 0 {
		return nil
	}
	// Preserve the actual launcher context. uv resolves the console interpreter
	// inside its own run environment, never through the parent's PATH Python.
	offset := len(argv) - len(unwrapped)
	probe := []string{}
	consolePath := ""
	if console {
		if uv {
			probe = append(probe, argv[:offset]...)
			probe = append(probe, "python", "-c", pythonConsoleProbe, unwrapped[0])
		} else {
			var e error
			consolePath, e = pythonConsoleInterpreter(dir, unwrapped[0], &probe)
			if e != nil {
				return pythonImportDiagnosis("python", false)
			}
		}
	} else if python {
		probe = append(probe, argv[:offset+1]...)
		for _, flag := range unwrapped[1:] {
			if flag == "-m" || flag == "-c" || !strings.HasPrefix(flag, "-") {
				break
			}
			switch flag {
			case "-I", "-S", "-s", "-E", "-B", "-P", "-u", "-O", "-OO":
				probe = append(probe, flag)
			default:
				return pythonImportDiagnosis("python", false)
			}
		}
	} else {
		return pythonImportDiagnosis("python", false)
	}
	payload, _ := json.Marshal(names)
	if uv && console {
		probe = append(probe, pythonImportProbe, string(payload), root)
	} else {
		probe = append(probe, "-c", pythonImportProbe, string(payload), root, consolePath)
	}
	var out output
	state, err := execute(ctx, probe, dir, env, &out)
	if err != nil || state.ExitCode() != 0 || out.exceeded {
		return pythonImportDiagnosis("python", false)
	}
	var result struct {
		Outside string `json:"outside"`
		Checked bool   `json:"checked"`
	}
	if json.Unmarshal(out.b.Bytes(), &result) != nil || !result.Checked {
		return pythonImportDiagnosis("python", false)
	}
	if result.Outside != "" {
		if !pythonModuleName.MatchString(result.Outside) {
			return pythonImportDiagnosis("python", false)
		}
		return pythonImportDiagnosis(result.Outside, true)
	}
	return nil
}

func pythonImportDiagnosis(name string, outside bool) *Diagnosis {
	if !safeName.MatchString(name) {
		name = "python"
	}
	message := fmt.Sprintf("Python could not verify candidate-local imports for %s", name)
	if outside {
		message = fmt.Sprintf("Python resolves first-party package %s outside the private candidate", name)
	}
	return &Diagnosis{Kind: "python_import_unverified", Name: name, Message: message}
}

// Discover conventional flat and src layouts at every pyproject boundary,
// including uv workspace members. Conventional flat and src namespace packages
// are included; tooling directories and nested project containers are not packages.
func firstPartyPython(root string) ([]string, error) {
	return firstPartyPythonContext(context.Background(), root)
}

func firstPartyPythonContext(ctx context.Context, root string) ([]string, error) {
	names := map[string]bool{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if e := ctx.Err(); e != nil {
			return e
		}
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", ".venv", "venv", "node_modules", "__pycache__":
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() != "pyproject.toml" {
			return nil
		}
		project := filepath.Dir(p)
		for _, layout := range []string{project, filepath.Join(project, "src")} {
			entries, e := os.ReadDir(layout)
			if os.IsNotExist(e) {
				continue
			}
			if e != nil {
				return e
			}
			for _, entry := range entries {
				name := entry.Name()
				if entry.Type()&os.ModeSymlink != 0 {
					continue
				}
				if entry.IsDir() && pythonModuleName.MatchString(name) && flatNamespaceDirectory(name) {
					_, initErr := os.Stat(filepath.Join(layout, name, "__init__.py"))
					if initErr != nil && !os.IsNotExist(initErr) {
						return initErr
					}
					isPackage := initErr == nil
					if !isPackage && (layout != project || flatNamespaceDirectory(name)) {
						var err error
						isPackage, err = containsPython(filepath.Join(layout, name))
						if err != nil {
							return err
						}
					}
					if isPackage {
						names[name] = true
					}
				} else if !entry.IsDir() && strings.HasSuffix(name, ".py") {
					name = strings.TrimSuffix(name, ".py")
					if pythonModuleName.MatchString(name) && name != "conftest" && name != "setup" && !strings.HasPrefix(name, "test_") && !strings.HasSuffix(name, "_test") {
						names[name] = true
					}
				}
			}
		}
		return nil
	})
	result := make([]string, 0, len(names))
	for name := range names {
		result = append(result, name)
	}
	sort.Strings(result)
	return result, err
}

// These conventional flat-layout directories contain tooling rather than package
// roots. An explicit __init__.py still identifies a regular package above.
func flatNamespaceDirectory(name string) bool {
	switch name {
	case "src", "test", "tests", "doc", "docs", "script", "scripts", "build", "dist", "venv", "node_modules", "__pycache__":
		return false
	}
	return true
}

func containsPython(root string) (bool, error) {
	found := false
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.IsDir() {
			// A nested pyproject is inspected independently. Its ancestors (for
			// example uv workspace apps/ and libs/) are not importable package roots.
			_, projectErr := os.Stat(filepath.Join(p, "pyproject.toml"))
			if projectErr == nil {
				return filepath.SkipDir
			}
			if !os.IsNotExist(projectErr) {
				return projectErr
			}
			if p != root && !flatNamespaceDirectory(d.Name()) {
				return filepath.SkipDir
			}
		}
		if !d.IsDir() && strings.HasSuffix(d.Name(), ".py") {
			found = true
			return fs.SkipAll
		}
		return nil
	})
	return found, err
}

const pythonImportProbe = `import importlib.util, json, os, sys
root = os.path.realpath(sys.argv[2])
if len(sys.argv) > 3 and sys.argv[3]:
    sys.path[0] = os.path.dirname(sys.argv[3])
outside = ""
for name in json.loads(sys.argv[1]):
    spec = importlib.util.find_spec(name)
    if spec is None:
        continue
    if spec.origin in ("built-in", "frozen"):
        outside = name
        break
    paths = list(spec.submodule_search_locations or [])
    if spec.origin:
        paths.append(spec.origin)
    if not paths:
        print(json.dumps({"checked": False}))
        sys.exit(0)
    for path in paths:
        if os.path.commonpath([root, os.path.realpath(path)]) != root:
            outside = name
            break
    if outside:
        break
print(json.dumps({"checked": True, "outside": outside}))
`

// Console scripts must declare a recognisable Python interpreter. Shell/native
// wrappers cannot establish provenance and are deliberately unverified.
func pythonConsoleInterpreter(dir, command string, argv *[]string) (string, error) {
	name := command
	if !filepath.IsAbs(name) && strings.ContainsAny(name, "/\\") {
		name = filepath.Join(dir, name)
	} else if !filepath.IsAbs(name) {
		var err error
		name, err = exec.LookPath(name)
		if err != nil {
			return "", err
		}
	}
	f, err := os.Open(name)
	if err != nil {
		return "", err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, 4096))
	if err != nil {
		return "", err
	}
	line := strings.SplitN(string(b), "\n", 2)[0]
	if !strings.HasPrefix(line, "#!") {
		return "", fmt.Errorf("console interpreter unavailable")
	}
	parts := strings.Fields(strings.TrimSpace(strings.TrimPrefix(line, "#!")))
	if len(parts) == 2 && filepath.Base(parts[0]) == "env" {
		parts = parts[1:]
		resolved, e := exec.LookPath(parts[0])
		if e != nil {
			return "", e
		}
		parts[0] = resolved
	}
	if len(parts) != 1 || !pythonExecutableName.MatchString(strings.ToLower(filepath.Base(parts[0]))) {
		return "", fmt.Errorf("unsupported console interpreter")
	}
	*argv = append(*argv, parts[0])
	return name, nil
}

const pythonConsoleProbe = `import os, shutil, sys
command = shutil.which(sys.argv[1])
if not command:
    sys.exit(3)
with open(command, "rb") as stream:
    line = stream.readline(4096).decode("utf-8").strip()
parts = line[2:].split() if line.startswith("#!") else []
if len(parts) == 2 and os.path.basename(parts[0]) == "env":
    parts = [shutil.which(parts[1])]
if len(parts) != 1 or not parts[0]:
    sys.exit(3)
import re
if not re.fullmatch(r"python(?:[23](?:\.[0-9]+)?)?(?:\.exe)?", os.path.basename(parts[0]).lower()):
    sys.exit(3)
os.execv(parts[0], [parts[0], "-c", sys.argv[2], sys.argv[3], sys.argv[4], command])
`
