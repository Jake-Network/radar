package evidence

import (
	"bytes"
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

// mypyProbeScript asks mypy, through its own option, configuration and
// search-path handling, what a command would check and where it would look
// for imports. It imports only mypy, never project code, reads one JSON
// object ({"root", "deleted"}) on stdin and prints one JSON object.
//
// hidden lists the modules of deleted files that mypy would not report as
// missing: no search base still provides them, and either the module's
// options ignore missing imports or import-not-found is disabled for the
// run or for a checked module.
const mypyProbeScript = `import ast, io, json, os, sys, tokenize
from mypy.build import default_data_dir
from mypy.config_parser import parse_mypy_comments
from mypy.main import process_options
from mypy.modulefinder import compute_search_paths, FindModuleCache

data = json.loads(sys.stdin.read())
root = os.path.realpath(data["root"])

def within(path, base):
    try:
        return os.path.commonpath([base, path]) == base
    except ValueError:
        return False

sources, options = process_options(sys.argv[1:])
paths = compute_search_paths(sources, options, default_data_dir())
finder = FindModuleCache(paths, None, options)
search = []
for entry in list(paths.python_path) + list(paths.mypy_path):
    entry = os.path.realpath(os.path.abspath(entry))
    if entry not in search:
        search.append(entry)
bases = [entry for entry in search if within(entry, root)]
outside = [entry for entry in search if not within(entry, root)]
silenced = {"import", "import-not-found"}

def disabled(opts):
    return bool(silenced & (set(opts.disable_error_code) - set(opts.enable_error_code)))

def provided(parts):
    rel = os.path.join(*parts)
    return any(os.path.isdir(os.path.join(base, rel)) or
               os.path.isfile(os.path.join(base, rel) + ".py") or
               os.path.isfile(os.path.join(base, rel) + ".pyi") for base in bases)

missing = set()
for deleted in data["deleted"]:
    deleted = os.path.join(os.path.realpath(os.path.dirname(os.path.abspath(deleted))), os.path.basename(deleted))
    for base in bases:
        if not within(deleted, base) or deleted == base:
            continue
        stem = os.path.relpath(deleted, base)
        stem = stem[:-4] if stem.endswith(".pyi") else stem[:-3] if stem.endswith(".py") else ""
        parts = stem.split(os.sep) if stem else []
        if parts and parts[-1] == "__init__":
            parts = parts[:-1]
        if parts and all(part.isidentifier() for part in parts) and not provided(parts):
            missing.add(".".join(parts))

# Resolve -m/--module sources with mypy's own module finder too. Follow only
# repository-local imports; package and typeshed code is never read here.
queue = [(s.path or finder.find_module(s.module), s.module or "") for s in sources]
checked = {}
visited = set()
hidden = set()
imports = set()
total = 0
while queue:
    filename, module = queue.pop()
    if not isinstance(filename, str):
        continue
    filename = os.path.realpath(filename)
    if not within(filename, root) or filename in visited:
        continue
    visited.add(filename)
    if len(visited) > 5000:
        raise RuntimeError("source inventory exceeds limit")
    size = os.path.getsize(filename)
    total += size
    if size > 1048576 or total > 67108864:
        raise RuntimeError("import scan exceeds limit")
    with tokenize.open(filename) as stream:
        source = stream.read()
    importer_options = options.clone_for_module(module)
    configured_ignore = importer_options.ignore_errors
    # Inline options are real, unindented comment tokens, not lookalike
    # text inside a docstring or trailing a statement. Let mypy interpret
    # their values and precedence over global/per-module configuration.
    flags = []
    known = True
    try:
        for token in tokenize.generate_tokens(io.StringIO(source).readline):
            if token.type == tokenize.COMMENT and token.start[1] == 0 and token.string.startswith("# mypy: "):
                flags.append((token.start[0], token.string[len("# mypy: "):]))
    except (tokenize.TokenError, IndentationError, SyntaxError):
        # Still run mypy so it can observe a source syntax failure. An
        # undecodable source must not become coverage if it exits zero.
        known = False
    if flags:
        changes, inline_errors = parse_mypy_comments(flags, importer_options)
        importer_options = importer_options.apply_changes(changes)
        known = known and not inline_errors
    # A configured ignore_errors also silences the build's error sink;
    # parsing an inline False is not proof the tool re-enabled diagnostics.
    if known and not configured_ignore and not importer_options.ignore_errors:
        checked[filename] = module
    if not missing:
        continue
    try:
        tree = ast.parse(source, filename)
    except SyntaxError:
        # The real checker reports supported syntax errors. An incomplete
        # import inventory must never authorize a pass.
        raise RuntimeError("import scan syntax is unavailable") from None
    package = module if os.path.basename(filename) in ("__init__.py", "__init__.pyi") else module.rpartition(".")[0]
    for node in ast.walk(tree):
        names = []
        if isinstance(node, ast.Import):
            names = [alias.name for alias in node.names]
        elif isinstance(node, ast.ImportFrom):
            base = node.module or ""
            if node.level:
                parts = package.split(".") if package else []
                if node.level > len(parts):
                    continue
                base = ".".join(parts[:len(parts)-node.level+1]) + ("." + base if base else "")
            if base:
                names = [base] + [base + "." + alias.name for alias in node.names if alias.name != "*"]
        for name in names:
            for gone in missing:
                if name == gone or name.startswith(gone + "."):
                    imports.add((filename, node.lineno, gone))
                    if disabled(importer_options) or options.clone_for_module(gone).ignore_missing_imports:
                        hidden.add(gone)
            found = finder.find_module(name)
            if isinstance(found, str) and within(os.path.realpath(found), root):
                target_options = options.clone_for_module(name)
                if target_options.follow_imports != "skip":
                    queue.append((found, name))
for gone in missing:
    if disabled(options) or options.clone_for_module(gone).ignore_missing_imports:
        hidden.add(gone)
print(json.dumps({
    "sources": sorted(checked.items()),
    "bases": bases,
    "outside": outside,
    "packages": [os.path.abspath(entry) for entry in paths.package_path],
    "hidden": sorted(hidden),
    "hidden_imports": sorted(imports),
}))
`

// MypyProbe is what mypy reports about one command before it runs, with
// the interpreter, environment and configuration the command uses.
// Sources maps each checked repository path to its module name. Bases are
// the repository directories mypy resolves project imports from (the
// working directory, package bases and mypy_path). Outside lists search
// directories outside the candidate: mypy would read code the combined
// tree does not have. PackageDirs are the installed-package directories
// mypy reads. Hidden lists deleted project modules whose missing import
// mypy is configured not to report.
type MypyProbe struct {
	Sources       map[string]string
	Bases         []string
	Outside       []string
	PackageDirs   []string
	Hidden        []string
	HiddenImports []MypyMissingImport
}

// MypyMissingImport is a syntactic reference to a deleted local module.
// It is inferred input coverage, separate from tool diagnostics.
type MypyMissingImport struct {
	Path   string `json:"path"`
	Line   int    `json:"line"`
	Module string `json:"module"`
}

// RunMypyProbe asks mypy what the command with args in cwd checks and where
// it resolves imports. deleted are repository paths of Python files the
// combined tree no longer has. Like GoCompiledFiles it runs only after
// execution opt-in; an error leaves every changed file unchecked.
func RunMypyProbe(ctx context.Context, root, cwd string, args, deleted []string, timeout time.Duration) (MypyProbe, error) {
	return RunMypyProbeCommand(ctx, root, cwd, append([]string{"python3", "-m", "mypy"}, args...), deleted, timeout)
}

// RunMypyProbeCommand preserves the interpreter and supported runner prefix of
// the actual mypy command, so inventory and execution use the same environment.
func RunMypyProbeCommand(ctx context.Context, root, cwd string, argv, deleted []string, timeout time.Duration) (MypyProbe, error) {
	var probe MypyProbe
	if timeout <= 0 || timeout > 30*time.Minute {
		return probe, errors.New("invalid mypy probe timeout")
	}
	dir := root
	if cwd != "" && cwd != "." {
		var err error
		if dir, err = pathutil.ResolveInside(root, cwd); err != nil {
			return probe, err
		}
	}
	input := struct {
		Root    string   `json:"root"`
		Deleted []string `json:"deleted"`
	}{Root: root, Deleted: []string{}}
	for _, d := range deleted {
		clean, err := pathutil.RepoRelative(d)
		if err != nil {
			continue
		}
		input.Deleted = append(input.Deleted, filepath.Join(root, filepath.FromSlash(clean)))
	}
	payload, err := json.Marshal(input)
	if err != nil {
		return probe, err
	}
	home, err := os.MkdirTemp("", "radar-mypy-probe-home-")
	if err != nil {
		return probe, err
	}
	defer os.RemoveAll(home)
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	unwrapped := runnerArgv(argv)
	moduleFlag := -1
	for i := 1; i+1 < len(unwrapped); i++ {
		if unwrapped[i] == "-m" && unwrapped[i+1] == "mypy" {
			moduleFlag = i
			break
		}
	}
	if moduleFlag < 0 || len(unwrapped) == 0 || !pythonExecutableName.MatchString(strings.ToLower(filepath.Base(unwrapped[0]))) {
		return probe, errors.New("mypy probe requires a supported Python interpreter command")
	}
	probeArgv := append([]string(nil), argv[:len(argv)-len(unwrapped)+moduleFlag]...)
	probeArgv = append(probeArgv, "-c", mypyProbeScript)
	probeArgv = append(probeArgv, unwrapped[moduleFlag+2:]...)
	cmd := exec.CommandContext(runCtx, probeArgv[0], probeArgv[1:]...)
	cmd.Dir = dir
	cmd.Env = environment(home, nil)
	cmd.Stdin = bytes.NewReader(payload)
	configureProcess(cmd)
	cmd.WaitDelay = time.Second
	var out limitedBuffer
	out.limit = maxGoListBytes
	cmd.Stdout = &out
	cmd.Stderr = io.Discard
	runErr := cmd.Run()
	switch {
	case runCtx.Err() != nil:
		return probe, errors.New("mypy probe timed out")
	case out.exceeded:
		return probe, errors.New("mypy probe output exceeds limit")
	case runErr != nil:
		return probe, fmt.Errorf("mypy probe: %w", runErr)
	}
	var result struct {
		Sources       [][2]string          `json:"sources"`
		Bases         []string             `json:"bases"`
		Outside       []string             `json:"outside"`
		Packages      []string             `json:"packages"`
		Hidden        []string             `json:"hidden"`
		HiddenImports [][3]json.RawMessage `json:"hidden_imports"`
	}
	lines := strings.Split(strings.TrimSpace(out.b.String()), "\n")
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &result); err != nil {
		return probe, fmt.Errorf("mypy probe output: %w", err)
	}
	probe.Sources = map[string]string{}
	for _, s := range result.Sources {
		rel, ok := sourcePath(root, s[0])
		if !ok {
			continue
		}
		if clean, err := pathutil.RepoRelative(rel); err == nil {
			module := s[1]
			if !pythonDottedName.MatchString(module) {
				module = ""
			}
			probe.Sources[clean] = module
		}
	}
	for _, b := range result.Bases {
		if rel, ok := relativeDir(root, b); ok {
			probe.Bases = append(probe.Bases, filepath.ToSlash(rel))
		} else {
			// A base that cannot be placed in the repository is read as
			// code from outside the candidate.
			probe.Outside = append(probe.Outside, b)
		}
	}
	probe.Outside = append(probe.Outside, result.Outside...)
	probe.PackageDirs = result.Packages
	for _, m := range result.Hidden {
		if pythonDottedName.MatchString(m) && len(m) <= 100 {
			probe.Hidden = append(probe.Hidden, m)
		}
	}
	for _, entry := range result.HiddenImports {
		var filename, module string
		var line int
		if json.Unmarshal(entry[0], &filename) != nil || json.Unmarshal(entry[1], &line) != nil || json.Unmarshal(entry[2], &module) != nil {
			return probe, errors.New("invalid mypy import inventory")
		}
		rel, err := filepath.Rel(root, filename)
		if err != nil {
			return probe, errors.New("invalid mypy import path")
		}
		clean, err := pathutil.RepoRelative(filepath.ToSlash(rel))
		if err != nil || !pythonDottedName.MatchString(module) || len(module) > 100 || line < 1 {
			return probe, errors.New("invalid mypy import inventory")
		}
		probe.HiddenImports = append(probe.HiddenImports, MypyMissingImport{Path: clean, Line: line, Module: module})
	}
	return probe, nil
}

// MypyShadow returns the first of names that an installed-package directory
// also provides as a typed package (with a py.typed marker) or a stub
// package. mypy looks for a submodule the candidate lacks in every copy of
// its package, so a stale installed copy of a project package would supply
// modules a branch deleted.
func MypyShadow(packageDirs, names []string) string {
	for _, name := range names {
		if !pythonModuleName.MatchString(name) {
			continue
		}
		for _, dir := range packageDirs {
			if !filepath.IsAbs(dir) {
				continue
			}
			if info, err := os.Stat(filepath.Join(dir, name+"-stubs")); err == nil && info.IsDir() {
				return name
			}
			if info, err := os.Stat(filepath.Join(dir, name+".pyi")); err == nil && info.Mode().IsRegular() {
				return name
			}
			if info, err := os.Stat(filepath.Join(dir, name, "py.typed")); err == nil && info.Mode().IsRegular() {
				return name
			}
		}
	}
	return ""
}
