package evidence

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var pythonModuleName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// pythonImportCheck runs only after execution opt-in. It probes the same
// interpreter (including a declared uv prefix), working directory and environment
// as the test. No path is injected to conceal an externally editable package.
func pythonImportCheck(ctx context.Context, root, dir string, argv, env []string) *Diagnosis {
	unwrapped := runnerArgv(argv)
	if len(unwrapped) == 0 {
		return nil
	}
	tool := filepath.Base(unwrapped[0])
	if tool != "python" && tool != "python3" {
		return nil
	}
	names, err := firstPartyPython(root)
	if err != nil {
		return pythonImportDiagnosis("python", false)
	}
	if len(names) == 0 {
		return nil
	}
	// Preserve interpreter flags before the module/script argument.
	offset := len(argv) - len(unwrapped)
	probe := append([]string{}, argv[:offset+1]...)
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
	payload, _ := json.Marshal(names)
	probe = append(probe, "-c", pythonImportProbe, string(payload), root)
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
// including uv workspace members. Namespace packages under src are included.
func firstPartyPython(root string) ([]string, error) {
	names := map[string]bool{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
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
				if entry.IsDir() && pythonModuleName.MatchString(name) {
					_, initErr := os.Stat(filepath.Join(layout, name, "__init__.py"))
					if initErr != nil && !os.IsNotExist(initErr) {
						return initErr
					}
					isPackage := initErr == nil
					if !isPackage && layout != project {
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
					if pythonModuleName.MatchString(name) && name != "conftest" && name != "setup" {
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
func containsPython(root string) (bool, error) {
	found := false
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
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
