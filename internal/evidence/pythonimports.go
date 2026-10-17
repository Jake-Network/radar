package evidence

import (
	"bytes"
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

	"github.com/Jake-Network/radar/internal/indexer"
)

var pythonExecutableName = regexp.MustCompile(`^python(?:[23](?:\.[0-9]+)?)?(?:\.exe)?$`)
var pythonModuleName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
var pythonDottedName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)*$`)

// pythonImportCheck runs only after execution opt-in. It probes the same
// interpreter (including a declared uv prefix), working directory and environment
// as the test. No path is injected to conceal an externally editable package.
//
// Package names alone cannot establish provenance: a merge can delete or rename
// a package that an editable install of the original checkout still provides.
// The probe therefore also rejects any import location outside the candidate
// and the interpreter's own installation, whatever names it serves.
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
		// Unknown wrappers around a recognised Python harness or interpreter
		// (env, poetry run, unsupported uv flags) cannot establish which
		// interpreter runs, so no import location can be checked.
		if pythonLaunchers[strings.TrimSuffix(strings.ToLower(filepath.Base(argv[0])), ".exe")] {
			for _, arg := range argv[1:] {
				if pythonExecutableName.MatchString(strings.ToLower(filepath.Base(arg))) {
					return pythonImportDiagnosis("python", false)
				}
			}
		}
		if uv || uses(argv, "pytest") || uses(argv, "py.test") {
			return pythonImportDiagnosis("python", false)
		}
		return nil
	}
	names, err := pythonProbeNames(ctx, root, dir, unwrapped)
	if err != nil {
		return pythonImportDiagnosis("python", false)
	}
	// Preserve the actual launcher context. uv resolves the console interpreter
	// inside its own run environment, never through the parent's PATH Python.
	offset := len(argv) - len(unwrapped)
	probe := []string{}
	// The probe puts this file's directory first on sys.path, as the
	// interpreter does for a console or script; otherwise the CWD.
	scriptPath := ""
	if console {
		if uv {
			probe = append(probe, argv[:offset]...)
			probe = append(probe, "python", "-c", pythonConsoleProbe, unwrapped[0])
		} else {
			var e error
			scriptPath, e = pythonConsoleInterpreter(dir, unwrapped[0], &probe)
			if e != nil {
				return pythonImportDiagnosis("python", false)
			}
		}
	} else if python {
		probe = append(probe, argv[:offset+1]...)
		for _, flag := range unwrapped[1:] {
			if flag == "-m" || flag == "-c" {
				break
			}
			if !strings.HasPrefix(flag, "-") {
				scriptPath = flag
				if !filepath.IsAbs(scriptPath) {
					scriptPath = filepath.Join(dir, scriptPath)
				}
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
	if uv && console {
		probe = append(probe, pythonImportProbe)
	} else {
		probe = append(probe, "-c", pythonImportProbe, scriptPath)
	}
	names.Root, names.Console = root, console
	// Names travel on stdin so that a large workspace cannot exceed the
	// platform's argument size limit.
	payload, err := json.Marshal(names)
	if err != nil {
		return pythonImportDiagnosis("python", false)
	}
	var out output
	state, err := executeInput(ctx, probe, dir, env, bytes.NewReader(payload), &out)
	if err != nil || state.ExitCode() != 0 || out.exceeded {
		return pythonImportDiagnosis("python", false)
	}
	var result struct {
		Checked bool   `json:"checked"`
		Reason  string `json:"reason"`
		Outside string `json:"outside"`
	}
	// Launchers such as uv may write progress before the probe's single
	// result line; only that last line is parsed.
	lines := strings.Split(strings.TrimSpace(out.b.String()), "\n")
	if json.Unmarshal([]byte(lines[len(lines)-1]), &result) != nil || !result.Checked {
		return pythonImportDiagnosis("python", false)
	}
	identifier := pythonDottedName.MatchString(result.Outside) && safeName.MatchString(result.Outside)
	switch {
	case result.Reason == "":
		return nil
	case result.Reason == "package" && identifier:
		return pythonImportDiagnosis(result.Outside, true)
	case result.Reason == "path":
		return &Diagnosis{Kind: "python_import_unverified", Name: "sys.path", Message: "Python import path includes a location outside the private candidate and the interpreter installation"}
	case result.Reason == "hook":
		return &Diagnosis{Kind: "python_import_unverified", Name: "sys.meta_path", Message: "Python import hook can load modules outside the private candidate"}
	case result.Reason == "module" && identifier:
		return &Diagnosis{Kind: "python_import_unverified", Name: result.Outside, Message: fmt.Sprintf("Python loaded module %s from outside the private candidate before the test started", result.Outside)}
	case result.Reason == "distribution" && pythonDistributionName.MatchString(result.Outside):
		return &Diagnosis{Kind: "python_import_unverified", Name: result.Outside, Message: fmt.Sprintf("Python distribution %s was not installed from the private candidate source tree", result.Outside)}
	}
	return pythonImportDiagnosis("python", false)
}

// pythonLaunchers run another program named among their arguments.
var pythonLaunchers = map[string]bool{"env": true, "timeout": true, "nice": true, "time": true, "stdbuf": true, "xvfb-run": true, "coverage": true, "poetry": true, "pdm": true, "pipenv": true, "hatch": true, "rye": true, "conda": true, "mamba": true, "micromamba": true, "pixi": true, "uv": true, "uvx": true, "tox": true, "nox": true}

var pythonDistributionName = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9._-]{0,98}[A-Za-z0-9])?$`)

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

// pythonProbeInput is the probe's stdin. Command packages belong to the
// projects the command runs in: the repository root and the projects that
// contain the working directory or a path argument. Any resolution outside
// the candidate refuses them. Other packages belong to the remaining projects
// (siblings, test fixtures, examples). One hidden by the standard library, or
// by a distribution of another name, is not stale first-party code; one hidden
// by a distribution named like a repository project, by an unowned copy or,
// when its project has no readable name (Unnamed), by any copy is refused.
// A site-packages copy counts as candidate code only when every distribution
// that installs it was built from the candidate. Namespace packages also list
// their members as dotted names, so that other distributions may share the
// namespace. Projects are the normalized distribution names of the
// repository's projects. `test` and `tests` are put on sys.path by the test
// runner itself, so their startup resolution is not probed by name.
type pythonProbeInput struct {
	Root     string   `json:"root"`
	Console  bool     `json:"console"`
	Command  []string `json:"command"`
	Other    []string `json:"other"`
	Unnamed  []string `json:"unnamed"`
	Projects []string `json:"projects"`
}

func pythonProbeNames(ctx context.Context, root, dir string, argv []string) (pythonProbeInput, error) {
	in := pythonProbeInput{Command: []string{}, Other: []string{}, Unnamed: []string{}, Projects: []string{}}
	projects, err := pythonProjectsContext(ctx, root)
	if err != nil {
		return in, err
	}
	command := map[string]bool{}
	for _, project := range pythonCommandProjects(root, dir, argv) {
		command[project] = true
	}
	names := map[string]bool{}
	other := map[string]bool{}
	unnamed := map[string]bool{}
	distributions := map[string]bool{}
	for _, project := range projects {
		packages := map[string]bool{}
		if err := pythonProjectPackages(project, packages); err != nil {
			return in, err
		}
		distribution := ""
		manifest := filepath.Join(project, "pyproject.toml")
		if st, err := os.Lstat(manifest); err == nil && st.Mode().IsRegular() {
			content, err := os.ReadFile(manifest)
			if err != nil {
				return in, err
			}
			distribution = indexer.PyProjectName(content)
		}
		if distribution != "" {
			distributions[pythonNormalizedDistribution(distribution)] = true
		}
		for name := range packages {
			if command[project] {
				names[name] = true
			} else {
				other[name] = true
				if distribution == "" {
					unnamed[name] = true
				}
			}
		}
	}
	for name := range names {
		delete(other, name)
		delete(unnamed, name)
	}
	for _, set := range []struct {
		from map[string]bool
		to   *[]string
	}{{names, &in.Command}, {other, &in.Other}, {unnamed, &in.Unnamed}, {distributions, &in.Projects}} {
		for name := range set.from {
			top := strings.SplitN(name, ".", 2)[0]
			if top != "test" && top != "tests" || set.to == &in.Projects {
				*set.to = append(*set.to, name)
			}
		}
		sort.Strings(*set.to)
	}
	return in, nil
}

var pythonDistributionSeparators = regexp.MustCompile(`[-_.]+`)

func pythonNormalizedDistribution(name string) string {
	return strings.ToLower(pythonDistributionSeparators.ReplaceAllString(name, "-"))
}

// pythonCommandProjects returns the repository root project and the nearest
// project containing the working directory or an existing path argument.
func pythonCommandProjects(root, dir string, argv []string) []string {
	isProject := func(p string) bool {
		st, err := os.Lstat(filepath.Join(p, "pyproject.toml"))
		return err == nil && st.Mode().IsRegular()
	}
	inside := func(p string) bool {
		rel, err := filepath.Rel(root, p)
		return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
	}
	anchors := []string{dir}
	for _, arg := range argv[1:] {
		arg = strings.SplitN(arg, "::", 2)[0]
		if arg == "" || strings.HasPrefix(arg, "-") {
			continue
		}
		if !filepath.IsAbs(arg) {
			arg = filepath.Join(dir, arg)
		}
		if _, err := os.Stat(arg); err == nil {
			anchors = append(anchors, arg)
		}
	}
	found := map[string]bool{}
	if isProject(root) {
		found[root] = true
	}
	for _, anchor := range anchors {
		for p := filepath.Clean(anchor); inside(p); p = filepath.Dir(p) {
			if isProject(p) {
				found[p] = true
				break
			}
			if p == root {
				break
			}
		}
	}
	out := []string{}
	for p := range found {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// pythonProjectsContext returns every directory with a pyproject.toml.
func pythonProjectsContext(ctx context.Context, root string) ([]string, error) {
	projects := []string{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if e := ctx.Err(); e != nil {
			return e
		}
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", ".venv", "venv", ".tox", ".nox", "node_modules", "__pycache__":
				return filepath.SkipDir
			}
			// A directory with its own pyproject.toml is a project even
			// when an environment was created in it.
			if p != root && pythonEnvironmentDirectory(p) {
				if _, err := os.Lstat(filepath.Join(p, "pyproject.toml")); err != nil {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if d.Name() == "pyproject.toml" {
			projects = append(projects, filepath.Dir(p))
		}
		return nil
	})
	return projects, err
}

func pythonProjectPackages(project string, names map[string]bool) error {
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
				if pythonEnvironmentDirectory(filepath.Join(layout, name)) {
					continue
				}
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
				if isPackage && initErr != nil {
					if err := pythonNamespaceMembers(filepath.Join(layout, name), name, names, 1); err != nil {
						return err
					}
				}
			} else if !entry.IsDir() && strings.HasSuffix(name, ".py") {
				name = strings.TrimSuffix(name, ".py")
				if pythonModuleName.MatchString(name) && name != "__init__" && name != "conftest" && name != "setup" && !strings.HasPrefix(name, "test_") && !strings.HasSuffix(name, "_test") {
					names[name] = true
				}
			}
		}
	}
	return nil
}

// pythonNamespaceMembers lists the modules and packages of a namespace package
// as dotted names, descending into nested namespaces.
func pythonNamespaceMembers(dir, prefix string, names map[string]bool, depth int) error {
	if depth > 32 {
		return fmt.Errorf("namespace package nested too deeply")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		if !entry.IsDir() {
			module := strings.TrimSuffix(name, ".py")
			if module != name && module != "__init__" && pythonModuleName.MatchString(module) {
				names[prefix+"."+module] = true
			}
			continue
		}
		path := filepath.Join(dir, name)
		if !pythonModuleName.MatchString(name) || pythonEnvironmentDirectory(path) {
			continue
		}
		if _, err := os.Stat(filepath.Join(path, "pyproject.toml")); err == nil {
			continue
		} else if !os.IsNotExist(err) {
			return err
		}
		_, initErr := os.Stat(filepath.Join(path, "__init__.py"))
		if initErr != nil && !os.IsNotExist(initErr) {
			return initErr
		}
		if initErr == nil {
			names[prefix+"."+name] = true
			continue
		}
		namespace, err := containsPython(path)
		if err != nil {
			return err
		}
		if namespace {
			names[prefix+"."+name] = true
			if err := pythonNamespaceMembers(path, prefix+"."+name, names, depth+1); err != nil {
				return err
			}
		}
	}
	return nil
}

// pythonEnvironmentDirectory reports a virtual or conda environment or an
// installation tree. It holds installed copies, not repository projects.
func pythonEnvironmentDirectory(dir string) bool {
	switch filepath.Base(dir) {
	case "site-packages", "dist-packages":
		return true
	}
	for _, marker := range []string{"pyvenv.cfg", "conda-meta"} {
		if _, err := os.Lstat(filepath.Join(dir, marker)); err == nil {
			return true
		}
	}
	return false
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
			if p != root && (!flatNamespaceDirectory(d.Name()) || pythonEnvironmentDirectory(p)) {
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

// pythonImportProbe reads pythonProbeInput on stdin and prints one JSON line.
// It runs no first-party code. Every module it needs is imported, and every
// import hook is checked, before the working directory (or the console script
// or script directory) goes on sys.path, so candidate files cannot answer for
// it and unknown finders are never invoked. Allowed locations are the
// candidate, the interpreter's standard library and site-packages trees and
// its prefix directories. Findings, by priority: hook (an import hook other
// than the standard ones, the virtualenv and distutils shims, or a setuptools
// or editables mapping that stays in allowed locations), package (see
// pythonProbeInput), then the first of path (a sys.path entry or a
// site-packages symlink outside the allowed locations), module (a module
// already loaded from outside them) and distribution (a distribution installed
// from a local path outside the candidate that still exists, or a distribution
// named like a repository project that was not built from the candidate).
const pythonImportProbe = `import sys
startup = set(sys.modules)
script = sys.argv[1] if len(sys.argv) > 1 else ""
cwd_entry = sys.path[:1] == [""]
if cwd_entry:
    del sys.path[0]
import csv, email.parser, importlib.machinery, importlib.util, json, os, pathlib, re, site, sysconfig, types, zipfile, zipimport
from urllib.parse import urlparse, unquote
if os.name == "nt":
    from urllib.request import url2pathname
try:
    from importlib import metadata
except ImportError:
    metadata = None


def done(result):
    print(json.dumps(result))
    sys.exit(0)


def report(reason, name=""):
    done({"checked": True, "reason": reason, "outside": name})


def real(path):
    return os.path.normcase(os.path.realpath(os.path.abspath(path)))


def within(path, base):
    try:
        return os.path.commonpath([base, path]) == base
    except ValueError:
        return False


def normalize(name):
    return re.sub(r"[-_.]+", "-", name).lower()


def homebrew_site_aliases(base_prefix, base_exec_prefix, executable, version, configured_sites, candidate):
    # Homebrew links installed dependency files into its shared site-packages.
    # Prove the layout from the physical interpreter, then trust each file
    # alias as site content; never grant its parent directory source access.
    base = pathlib.Path(real(base_prefix))
    if real(base_exec_prefix) != str(base):
        return set()
    suffix = tuple(os.path.normcase(part) for part in ("Frameworks", "Python.framework", "Versions", version))
    if tuple(base.parts[-4:]) != suffix:
        return set()
    keg = base.parents[3]
    if keg.parent.name != "python@" + version or keg.parent.parent.name != os.path.normcase("Cellar"):
        return set()
    cellar = keg.parent.parent
    if not within(real(executable), str(base / "bin")):
        return set()
    shared = cellar.parent / "lib" / ("python" + version) / "site-packages"
    if real(shared) != str(shared) or str(shared) not in configured_sites or not shared.is_dir():
        return set()
    aliases = set()
    for link in shared.iterdir():
        if not link.is_symlink():
            continue
        target = pathlib.Path(real(link))
        if not target.is_file() or within(str(target), candidate):
            continue
        try:
            parts = target.relative_to(cellar).parts
        except ValueError:
            continue
        if len(parts) != 6 or parts[2:5] != ("lib", "python" + version, "site-packages") or parts[5] != link.name:
            continue
        if not (link.name.endswith(".py") or any(link.name.endswith(suffix) for suffix in importlib.machinery.EXTENSION_SUFFIXES)):
            continue
        aliases.add(str(target))
    return aliases


try:
    data = json.loads(sys.stdin.buffer.read().decode("utf-8"))
    root = real(data["root"])
    projects = set(data["projects"])
    unnamed = set(data["unnamed"])
    paths = sysconfig.get_paths()
    stdlib = {real(paths[key]) for key in ("stdlib", "platstdlib") if paths.get(key)}
    sites = {real(paths[key]) for key in ("purelib", "platlib") if paths.get(key)}
    if hasattr(site, "getsitepackages"):
        sites.update(real(path) for path in site.getsitepackages())
    if hasattr(site, "getusersitepackages"):
        sites.add(real(site.getusersitepackages()))
    site_aliases = homebrew_site_aliases(sys.base_prefix, sys.base_exec_prefix, sys.executable, "%d.%d" % sys.version_info[:2], sites, root)
    exact = set()
    for prefix in (sys.prefix, sys.exec_prefix, sys.base_prefix, sys.base_exec_prefix):
        exact.add(real(prefix))
    if os.name == "nt":
        # A venv still loads standard-library .pyd modules from its base
        # interpreter's DLLs directory, outside sysconfig's Lib directory.
        # Venv DLLs aliases must not turn arbitrary external code into stdlib.
        for prefix in (sys.base_prefix, sys.base_exec_prefix):
            dlls = real(os.path.join(prefix, "DLLs"))
            if os.path.isdir(dlls) and within(dlls, real(prefix)):
                stdlib.add(dlls)
    exact.add(real(os.path.dirname(sys.executable)))
    if paths.get("scripts"):
        exact.add(real(paths["scripts"]))
    # An environment inside the candidate directory holds installed copies
    # and checkouts (pip puts -e VCS requirements under its src), not
    # candidate source.
    local = {real(prefix) for prefix in (sys.prefix, sys.exec_prefix, sys.base_prefix, sys.base_exec_prefix)}
    local = {prefix for prefix in local if within(prefix, root) and prefix != root}
    first = real(os.path.dirname(os.path.realpath(script))) if script else real(os.getcwd())
    if data["console"] and script:
        exact.add(first)
except SystemExit:
    raise
except Exception:
    done({"checked": False})


def allowed(path):
    return path in exact or any(within(path, tree) for tree in sites | stdlib)


def location(path):
    # site-packages comes first: a venv inside the candidate can still hold
    # copies installed from elsewhere.
    if not isinstance(path, str) or not path:
        return ""
    resolved = real(path)
    if resolved in site_aliases or any(within(resolved, tree) for tree in sites):
        return "site"
    if any(within(resolved, prefix) for prefix in local):
        return "install" if allowed(resolved) else ""
    if within(resolved, root):
        return "candidate"
    if allowed(resolved):
        return "install"
    # Debian links startup configuration from the standard library to /etc.
    link = os.path.normcase(os.path.abspath(path))
    if os.path.basename(link) in ("sitecustomize.py", "usercustomize.py") and os.path.islink(link) and allowed(real(os.path.dirname(link))):
        return "install"
    return ""


def direct(dist):
    # The parsed direct_url.json: None when absent, {} when unreadable.
    try:
        text = dist.read_text("direct_url.json")
        if not text:
            return None
        data = json.loads(text)
    except Exception:
        return {}
    return data if isinstance(data, dict) else {}


def source(dist):
    # The local path a distribution was installed from, None when it names
    # no local path, or "" when that path cannot be established.
    data = direct(dist)
    if data is None:
        return None
    url = data.get("url")
    if not isinstance(url, str):
        return ""
    if not url.lower().startswith("file:"):
        return None
    parsed = urlparse(url)
    if parsed.netloc not in ("", "localhost"):
        return ""
    return url2pathname(parsed.path) if os.name == "nt" else unquote(parsed.path)


def from_candidate(dist):
    # Built from the candidate's source tree itself (not from an archive or
    # VCS snapshot that may hold an old release), or (a legacy develop
    # install) with its metadata in the candidate source tree.
    data = direct(dist)
    if data is None:
        try:
            return location(str(dist.locate_file(""))) == "candidate"
        except Exception:
            return False
    path = source(dist)
    return "dir_info" in data and bool(path) and location(path) == "candidate"


def name_of(dist):
    try:
        return str(dist.metadata.get("Name") or "")
    except Exception:
        return ""


def project(dist):
    return normalize(name_of(dist)) in projects


owners = None
listed = {}


def files_of(dist):
    # Every file the distribution records, or None when they cannot be read.
    try:
        return list(dist.files or [])
    except Exception:
        return None


def owned(name):
    # The distributions that install this module or package, by dotted name.
    global owners
    if owners is None:
        owners = {}
        for dist in metadata.distributions():
            try:
                names = set((dist.read_text("top_level.txt") or "").split())
            except Exception:
                names = set()
            for file in files_of(dist) or []:
                parts = list(file.parts)
                if not parts or parts[0] == ".." or parts[0].endswith((".dist-info", ".egg-info", ".data")):
                    continue
                if parts[-1].endswith(".py"):
                    parts[-1] = parts[-1][:-3]
                elif parts[-1].endswith((".so", ".pyd")):
                    parts[-1] = parts[-1].split(".")[0]
                elif parts[-1].endswith(".pyc") and parts[-2:-1] != ["__pycache__"]:
                    parts[-1] = parts[-1][:-4]
                else:
                    parts.pop()
                for depth in range(1, len(parts) + 1):
                    names.add(".".join(parts[:depth]))
            for owned_name in names:
                owners.setdefault(owned_name, []).append(dist)
    return owners.get(name, [])


def built_from_candidate(name, origin):
    # Every distribution that installs this name was built from the candidate,
    # and one of them recorded the file that actually loads.
    dists = owned(name)
    if not dists or not all(from_candidate(dist) for dist in dists):
        return False
    if not origin:
        return True
    for dist in dists:
        if id(dist) not in listed:
            listed[id(dist)] = {real(str(dist.locate_file(file))) for file in files_of(dist) or []}
        if real(origin) in listed[id(dist)]:
            return True
    return False


def importable(path):
    # Sources, extensions or sourceless bytecode outside __pycache__.
    for _, dirs, files in os.walk(path):
        dirs[:] = [name for name in dirs if name != "__pycache__"]
        if any(file.endswith((".py", ".pyc", ".so", ".pyd")) for file in files):
            return True
    return False


def unowned_member(name, path):
    # A file in a namespace's site-packages portion that no distribution
    # installed.
    for entry in os.scandir(path):
        child = entry.name
        if entry.is_dir():
            if child == "__pycache__" or not importable(entry.path):
                continue
        elif child.endswith(".py"):
            child = child[:-3]
        elif child.endswith(".pyc"):
            child = child[:-4]
        elif child.endswith((".so", ".pyd")):
            child = child.split(".")[0]
        else:
            continue
        if not owned(name + "." + child):
            return True
    return False


def foreign(name):
    # Every distribution that installs this name belongs to another project,
    # and the project declaring the name has a distribution name to tell.
    dists = owned(name)
    return bool(dists) and name not in unnamed and all(from_candidate(dist) or not project(dist) for dist in dists)


try:
    if metadata is None:
        done({"checked": False})
    editable = set()
    standard = {importlib.machinery.BuiltinImporter, importlib.machinery.FrozenImporter, importlib.machinery.PathFinder}
    if hasattr(importlib.machinery, "WindowsRegistryFinder"):
        standard.add(importlib.machinery.WindowsRegistryFinder)
    for finder in sys.meta_path:
        kind = finder if isinstance(finder, type) else type(finder)
        if kind in standard:
            continue
        module = sys.modules.get(getattr(kind, "__module__", None))
        if module is None or not location(getattr(module, "__file__", None)):
            report("hook")
        if module.__name__ in ("_virtualenv", "_distutils_hack"):
            continue
        # hatch dev-mode-exact (editables) redirects names to source paths.
        redirections = getattr(kind, "_redirections", None)
        if module.__name__ == "editables.redirector" and isinstance(redirections, dict):
            if any(not location(target) for target in redirections.values()):
                report("hook")
            continue
        # setuptools editable installs map names to source paths.
        mapping = getattr(module, "MAPPING", None)
        namespaces = getattr(module, "NAMESPACES", {})
        if not (module.__name__.startswith("__editable___") and isinstance(mapping, dict) and isinstance(namespaces, dict)):
            report("hook")
        targets = list(mapping.values())
        for value in namespaces.values():
            targets.extend(value if isinstance(value, (list, tuple)) else [None])
        if any(not location(target) for target in targets):
            report("hook")
        editable.add(module.__name__)
    for hook in sys.path_hooks:
        module = getattr(hook, "__module__", "")
        if hook is zipimport.zipimporter or module in ("importlib._bootstrap_external", "_frozen_importlib_external") or module in editable:
            continue
        report("hook")

    finding = None
    for entry in list(sys.path) + ([first] if cwd_entry else []):
        if not isinstance(entry, str):
            done({"checked": False})
        if os.path.exists(entry or os.getcwd()) and not location(entry or os.getcwd()):
            finding = finding or ("path", "")
    for tree in sorted(sites):
        if os.path.isdir(tree):
            for item in os.scandir(tree):
                if item.is_symlink() and not location(item.path):
                    finding = finding or ("path", "")
    for name, module in list(sys.modules.items()):
        if name == "__main__" or module is None:
            continue
        if getattr(getattr(module, "__spec__", None), "origin", None) in ("built-in", "frozen"):
            continue
        found = []
        origin = getattr(module, "__file__", None)
        if isinstance(origin, str):
            found.append(origin)
        found.extend(path for path in (getattr(module, "__path__", None) or []) if isinstance(path, str))
        if any(not location(path) for path in found):
            finding = finding or ("module", name.split(".")[0])
    for dist in metadata.distributions():
        path = source(dist)
        name = name_of(dist)
        if path == "" or path and location(path) != "candidate" and os.path.exists(path):
            finding = finding or ("distribution", name)
        elif project(dist) and not from_candidate(dist):
            finding = finding or ("distribution", name)

    if cwd_entry:
        sys.path.insert(0, first)

    def locate(name, path):
        # The probe's own imports must not hide a candidate package. A
        # member's parent is a namespace, which runs no code when imported.
        if name in startup:
            return importlib.util.find_spec(name)
        # A nested namespace spec reads its parent's __path__ from
        # sys.modules; a code-free stand-in provides it.
        parent = name.rpartition(".")[0]
        if parent and parent not in sys.modules:
            stub = types.ModuleType(parent)
            stub.__path__ = path
            sys.modules[parent] = stub
        for finder in sys.meta_path:
            # The shims never locate first-party code, and asking them can
            # import setuptools from the candidate.
            if getattr(finder if isinstance(finder, type) else type(finder), "__module__", "") in ("_distutils_hack", "_virtualenv"):
                continue
            find = getattr(finder, "find_spec", None)
            spec = find(name, path) if find else None
            if spec is not None:
                return spec
        return None

    namespaces = {}
    for strict, names in ((True, data["command"]), (False, data["other"])):
        # Parents first: a namespace member is found through its parent.
        for name in sorted(names, key=lambda name: (name.count("."), name)):
            parent = name.rpartition(".")[0]
            if parent and parent not in namespaces:
                continue
            spec = locate(name, namespaces[parent] if parent else None)
            if spec is None:
                continue
            if spec.origin in ("built-in", "frozen"):
                if strict:
                    report("package", name)
                continue
            found = list(spec.submodule_search_locations or [])
            namespace = spec.origin in (None, "namespace") and bool(found)
            if spec.origin and not namespace:
                found.append(spec.origin)
            if not found:
                done({"checked": False})
            origin = spec.origin if spec.origin and not namespace else None
            for place in {location(path) for path in found}:
                if place == "candidate" or place == "site" and not namespace and built_from_candidate(name, origin):
                    continue
                # Other distributions may share a namespace; its first-party
                # members are checked by name, and nothing else may be there.
                if place == "site" and namespace:
                    if any(location(path) == "site" and os.path.isdir(path) and unowned_member(name, path) for path in found):
                        report("package", name)
                    continue
                # Another project's name hidden by the standard library, or by
                # a distribution of another name, is not stale first-party code.
                if not strict and (place == "install" or place == "site" and foreign(name)):
                    continue
                report("package", name)
            if namespace:
                namespaces[name] = list(spec.submodule_search_locations)
    if finding:
        report(*finding)
except SystemExit:
    raise
except Exception:
    done({"checked": False})
done({"checked": True, "reason": "", "outside": ""})
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

const pythonConsoleProbe = `import sys
if sys.path[:1] == [""]:
    del sys.path[0]
import os, re, shutil
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
if not re.fullmatch(r"python(?:[23](?:\.[0-9]+)?)?(?:\.exe)?", os.path.basename(parts[0]).lower()):
    sys.exit(3)
os.execv(parts[0], [parts[0], "-c", sys.argv[2], command])
`
