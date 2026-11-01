package evidence

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	pathpkg "path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Jake-Network/radar/internal/model"
	"github.com/Jake-Network/radar/internal/pathutil"
)

const (
	// maxTypecheckDiagnostics bounds what a run can be compared on. More
	// located errors than this make the run incomplete, never a pass or a
	// partial comparison.
	maxTypecheckDiagnostics = 2000
	// maxStoredTypecheckDiagnostics bounds what is persisted in reports.
	maxStoredTypecheckDiagnostics = 50
)

// TypecheckDiagnostic is one type or compile error located in repository
// source. The message itself is never kept: Fingerprint hashes the tool, the
// path and the message with line and column numbers and candidate paths
// removed, so a base run can recognize the same error after lines moved.
// Symbol is an identifier-shaped name from a recognized "undefined" message.
type TypecheckDiagnostic struct {
	Path        string `json:"path"`
	Line        int    `json:"line"`
	Symbol      string `json:"symbol,omitempty"`
	Fingerprint string `json:"fingerprint"`
	// message hashes the tool and normalized message without the path, so a
	// file a branch only moved can still match its base error. It is not
	// persisted: only an in-process base comparison uses it.
	message string
}

// MessageKey is the path-independent message hash of an in-process
// diagnostic, or "" for one decoded from JSON.
func (d TypecheckDiagnostic) MessageKey() string { return d.message }

// Location is "path:line", or the path alone when the line is unknown (0).
func (d TypecheckDiagnostic) Location() string {
	if d.Line <= 0 {
		return d.Path
	}
	return fmt.Sprintf("%s:%d", d.Path, d.Line)
}

// TypecheckObservation is one type-check command observed in a private
// candidate. A pass is a tool result about types, not observed behavior.
// Diagnostics holds at most the first 50 located errors; All returns every
// located error the run is compared on.
type TypecheckObservation struct {
	Tool            string                `json:"tool"`
	Status          model.Status          `json:"status"`
	ExitCode        int                   `json:"exit_code"`
	OutputDigest    string                `json:"output_digest"`
	DiagnosticCount int                   `json:"diagnostic_count"`
	Diagnostics     []TypecheckDiagnostic `json:"diagnostics,omitempty"`
	Diagnosis       *Diagnosis            `json:"diagnosis,omitempty"`
	// Partial reports that the tool stopped at an error before checking
	// everything (go vet stops at a package's first type error and skips
	// its importers; mypy stops at a syntax error), so errors it would have
	// reported may be missing.
	Partial        bool `json:"partial,omitempty"`
	all            []TypecheckDiagnostic
	cargoArtifacts []string
}

// All returns every located diagnostic observed in this process, or the
// persisted prefix for an observation decoded from JSON.
func (o TypecheckObservation) CargoArtifacts() []string {
	return append([]string(nil), o.cargoArtifacts...)
}

func (o TypecheckObservation) All() []TypecheckDiagnostic {
	if o.all != nil {
		return o.all
	}
	return o.Diagnostics
}

// GoModule is a committed Go module: import path and repository directory.
type GoModule struct {
	Path string
	Dir  string
}

// TypecheckScope names what belongs to the repository: the committed Go
// modules and the checked Python project's top-level module names. An
// import of one of them that cannot be found is a source failure (a branch
// removed or moved it); any other missing import is the environment's.
type TypecheckScope struct {
	GoModules      []GoModule
	PythonPackages []string
	// PythonModules are dotted names of two or more parts the repository's
	// files can be imported as from some base directory (mypy_path).
	PythonModules []string
	// TypeScriptConfig is the repository-relative config passed to tsc. It
	// keeps import classification bound to that project rather than another
	// tsconfig nearer a diagnostic's source file.
	TypeScriptConfig string
}

// typecheckLabels names each tool in diagnoses.
var typecheckLabels = map[string]string{"go": "go vet", "mypy": "mypy", "ruff": "ruff check"}

// ObserveTypecheck runs an explicitly authorized type-check command in cwd of
// a private candidate and reads its output with tool's format.
func ObserveTypecheck(ctx context.Context, root, cwd, tool string, scope TypecheckScope, argv []string, timeout time.Duration) (TypecheckObservation, error) {
	return ObserveTypecheckWithOptions(ctx, root, cwd, tool, scope, argv, timeout, TypecheckOptions{})
}

// ObserveTypecheckWithOptions accepts only explicitly allowlisted Radar-owned
// compiler settings; no arbitrary environment is forwarded.
func ObserveTypecheckWithOptions(ctx context.Context, root, cwd, tool string, scope TypecheckScope, argv []string, timeout time.Duration, options TypecheckOptions) (TypecheckObservation, error) {
	r := TypecheckObservation{Tool: tool, Status: model.StatusError, ExitCode: -1}
	if err := checkExecutable(argv); err != nil {
		return r, err
	}
	if timeout <= 0 || timeout > 30*time.Minute {
		return r, errors.New("invalid type-check timeout")
	}
	if _, ok := typecheckParsers[tool]; !ok {
		return r, fmt.Errorf("unsupported type-check tool %q", tool)
	}
	if cwd == "" {
		cwd = "."
	}
	dir, err := pathutil.ResolveInside(root, cwd)
	if err != nil {
		return r, err
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return r, errors.New("type-check working directory unavailable")
	}
	home, err := os.MkdirTemp("", "radar-typecheck-home-")
	if err != nil {
		return r, err
	}
	defer os.RemoveAll(home)
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	env := environment(home, nil)
	extra, optionErr := options.Environment(tool)
	if optionErr != nil {
		return r, optionErr
	}
	env = append(env, extra...)
	if tool == "mypy" {
		// mypy resolves imports through the interpreter's sys.path, like a
		// test: an editable install of the original checkout would let it
		// read modules the combined tree no longer has.
		if d := pythonImportCheck(runCtx, root, dir, argv, env); d != nil {
			r.Diagnosis = d
			return r, nil
		}
	}
	var out output
	state, err := execute(runCtx, argv, dir, env, &out)
	r.OutputDigest = hex.EncodeToString(out.sum())
	if err != nil {
		return r, err
	}
	r.ExitCode = state.ExitCode()
	data := out.b.Bytes()
	if tool == "tsc" {
		scope.TypeScriptConfig = typeScriptProject(root, cwd, argv)
	}
	parsed := parseTypecheck(tool, data, root, dir, cwd, scope)
	switch {
	case runCtx.Err() != nil:
		r.Status = model.StatusTimeout
	case out.exceeded:
		// A truncated listing cannot be compared with base.
		r.Status = model.StatusIncomplete
	case parsed.environment != nil:
		r.Status, r.Diagnosis = model.StatusError, parsed.environment
	case parsed.crashed:
		r.Status = model.StatusError
	case len(parsed.diagnostics) > maxTypecheckDiagnostics:
		r.Status = model.StatusIncomplete
	case len(parsed.diagnostics) == 0 && parsed.noInputs:
		r.Status = model.StatusUnknown
	case r.ExitCode == 0 && len(parsed.diagnostics) == 0:
		r.Status = model.StatusPassed
	case r.ExitCode == 0:
		// Errors with a successful exit are not a shape Radar recognizes.
		r.Status = model.StatusError
	case len(parsed.diagnostics) > 0:
		r.Status = model.StatusFailed
		r.Diagnosis = typecheckDiagnosis(typecheckLabels[tool], parsed.diagnostics)
	default:
		r.Status, r.Diagnosis = model.StatusError, diagnose(argv, r.ExitCode, data)
	}
	r.cargoArtifacts = append([]string(nil), parsed.cargoArtifacts...)
	r.DiagnosticCount = len(parsed.diagnostics)
	// mypy exits 2 when an error blocked further checking.
	r.Partial = len(parsed.diagnostics) > 0 && (tool == "go" || parsed.partial || tool == "mypy" && r.ExitCode == 2)
	if len(parsed.diagnostics) <= maxTypecheckDiagnostics {
		r.all = parsed.diagnostics
		r.Diagnostics = append([]TypecheckDiagnostic(nil), parsed.diagnostics[:min(len(parsed.diagnostics), maxStoredTypecheckDiagnostics)]...)
	}
	return r, nil
}

type typecheckParse struct {
	cargoArtifacts []string
	diagnostics    []TypecheckDiagnostic
	environment    *Diagnosis
	noInputs       bool
	// partial: the tool skipped what follows an error it reported.
	partial bool
	// crashed: the tool failed internally; located lines are not findings.
	crashed bool
}

type typecheckParser func(text string, n *typecheckNormalizer) typecheckParse

var typecheckParsers = map[string]typecheckParser{"go": parseGoTypecheck, "mypy": parseMypy, "ruff": parseRuff}

func parseTypecheck(tool string, data []byte, root, dir, cwd string, scope TypecheckScope) typecheckParse {
	parser := typecheckParsers[tool]
	if parser == nil {
		return typecheckParse{}
	}
	n := newTypecheckNormalizer(tool, root, dir, cwd)
	n.local, n.python, n.modules = scope.GoModules, scope.PythonPackages, scope.PythonModules
	n.tsconfig = scope.TypeScriptConfig
	return parser(ansi.ReplaceAllString(string(data), ""), n)
}

// typecheckNormalizer turns tool paths into repository paths and messages
// into run-independent fingerprints.
type typecheckNormalizer struct {
	tool, root, dir, cwd string
	prefixes             []string
	local                []GoModule
	python               []string
	modules              []string
	tsconfig             string
}

// localPythonModule reports a dotted module name whose top-level package is
// one of the checked project's own.
func (n *typecheckNormalizer) localPythonModule(module string) bool {
	top, _, _ := strings.Cut(module, ".")
	for _, name := range n.python {
		if name == top {
			return true
		}
	}
	for _, name := range n.modules {
		if name == module {
			return true
		}
	}
	return false
}

// localPackage reports an import path inside a repository module.
func (n *typecheckNormalizer) localPackage(p string) bool {
	_, ok := n.packageDir(p)
	return ok
}

// packageDir maps an import path inside a repository module to its
// repository directory, using the longest matching module path.
func (n *typecheckNormalizer) packageDir(p string) (string, bool) {
	best := -1
	dir := ""
	for _, m := range n.local {
		if m.Path == "" || p != m.Path && !strings.HasPrefix(p, m.Path+"/") || len(m.Path) <= best {
			continue
		}
		best = len(m.Path)
		dir = pathpkg.Join(m.Dir, strings.TrimPrefix(strings.TrimPrefix(p, m.Path), "/"))
	}
	if best < 0 {
		return "", false
	}
	clean, err := pathutil.RepoRelative(dir)
	if err != nil {
		if dir == "." {
			return ".", true
		}
		return "", false
	}
	return clean, true
}

func newTypecheckNormalizer(tool, root, dir, cwd string) *typecheckNormalizer {
	n := &typecheckNormalizer{tool: tool, root: root, dir: dir, cwd: cwd}
	for _, p := range []string{dir, root} {
		n.prefixes = append(n.prefixes, p+string(filepath.Separator), p)
		if resolved, err := filepath.EvalSymlinks(p); err == nil && resolved != p {
			n.prefixes = append(n.prefixes, resolved+string(filepath.Separator), resolved)
		}
	}
	return n
}

// path makes a tool-reported path repository-relative. Paths outside the
// execution directory (toolchain, module cache) are not repository source.
func (n *typecheckNormalizer) path(p string) (string, bool) {
	rel, ok := sourcePath(n.dir, p)
	if !ok {
		return "", false
	}
	if n.cwd != "" && n.cwd != "." {
		rel = filepath.ToSlash(filepath.Join(n.cwd, rel))
	}
	clean, err := pathutil.RepoRelative(rel)
	if err != nil {
		return "", false
	}
	if _, err := pathutil.ResolveInside(n.root, clean); err != nil {
		return "", false
	}
	return clean, true
}

var (
	// positionSuffix is ":LINE[:COL]" after a file extension inside a message
	// (for example "other declaration at x.go:12:3").
	positionSuffix = regexp.MustCompile(`(\.[A-Za-z0-9+]{1,8})(?::[0-9]+){1,2}\b`)
	// lineReference is "line N" inside a message (for example "already
	// defined on line 3").
	lineReference = regexp.MustCompile(`\bline [0-9]+\b`)
	spaceRun      = regexp.MustCompile(`\s+`)
)

func (n *typecheckNormalizer) diagnostic(path string, line int, message, symbol string) TypecheckDiagnostic {
	for _, p := range n.prefixes {
		message = strings.ReplaceAll(message, p, "")
	}
	message = positionSuffix.ReplaceAllString(message, "$1")
	message = lineReference.ReplaceAllString(message, "line")
	message = strings.TrimSpace(spaceRun.ReplaceAllString(message, " "))
	sum := sha256.Sum256([]byte(n.tool + "\x00" + path + "\x00" + message))
	key := sha256.Sum256([]byte(n.tool + "\x00" + message))
	d := TypecheckDiagnostic{Path: path, Line: line, Fingerprint: hex.EncodeToString(sum[:12]), message: hex.EncodeToString(key[:12])}
	if symbol != "" && sourceSymbol.MatchString(symbol) {
		d.Symbol = symbol
	}
	return d
}

// go vet prints the first type error of each package as "vet: FILE:L:C: msg"
// and analyzer findings as "FILE:L:C: msg"; package headers start with "#".
var (
	goTypecheckLine = regexp.MustCompile(`^(?:vet: )?(\S+\.go):([0-9]+):(?:[0-9]+:)? (.+)$`)
	goNoPackages    = regexp.MustCompile(`matched no packages`)
	goPackageLine   = regexp.MustCompile(`^package ([A-Za-z0-9_.~/-]{1,200})$`)
	goImportsFrom   = regexp.MustCompile(`^\s+imports ([A-Za-z0-9_.~/-]{1,200}) from ([A-Za-z0-9_.-]{1,200}\.go)`)
	goCycle         = regexp.MustCompile(`import cycle not allowed`)
	goMixedPackages = regexp.MustCompile(`^found packages ([A-Za-z0-9_]{1,100}) \(([A-Za-z0-9_.-]{1,200}\.go)\) and ([A-Za-z0-9_]{1,100}) \(([A-Za-z0-9_.-]{1,200}\.go)\) in (.+)$`)
	goMissingModule = regexp.MustCompile(`(?:no required module provides package|package) ([A-Za-z0-9_.~/-]{1,200})(?:;| is not in std)`)
	// cgo reports C compiler and header problems at the .go file through its
	// line mapping; a missing header, compiler or C declaration cannot be
	// told apart from the environment.
	goCgoEnvironment = regexp.MustCompile(`fatal error: .*(?:No such file or directory|file not found)|executable file not found in \$PATH|could not determine kind of name for C\.|cgo: .*(?:not found|exit status)`)
)

func parseGoTypecheck(text string, n *typecheckNormalizer) typecheckParse {
	var p typecheckParse
	// An import cycle is printed as a chain from the package go started at:
	// "package P0", then "imports P1 from f0.go" lines, the last one ending
	// in "import cycle not allowed".
	type importEdge struct{ from, to, file string }
	current, chain := "", []importEdge(nil)
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimRight(line, "\r")
		if m := goPackageLine.FindStringSubmatch(line); m != nil {
			current, chain = m[1], nil
		} else if m := goImportsFrom.FindStringSubmatch(line); m != nil && current != "" {
			chain = append(chain, importEdge{from: current, to: m[1], file: m[2]})
			current = m[1]
		}
		if goCycle.MatchString(line) && len(chain) > 0 {
			// Two branches can each add one edge of a cycle, and the package
			// go started at may not be in it: every edge from the repeated
			// package on is located at its importing file.
			cycle := chain[len(chain)-1:]
			for i, e := range chain {
				if e.from == chain[len(chain)-1].to {
					cycle = chain[i:]
					break
				}
			}
			for _, e := range cycle {
				dir, ok := n.packageDir(e.from)
				if !ok {
					continue
				}
				where := pathpkg.Join(dir, e.file)
				if _, err := pathutil.ResolveInside(n.root, where); err == nil {
					p.diagnostics = append(p.diagnostics, n.diagnostic(where, 0, "import cycle not allowed: "+e.from+" imports "+e.to, ""))
				}
			}
			current, chain = "", nil
			continue
		}
		if m := goMixedPackages.FindStringSubmatch(line); m != nil {
			// One directory holding two package names: branches disagree on
			// the package of a directory.
			if rel, ok := relativeDir(n.dir, m[5]); ok {
				where := pathpkg.Join(n.cwd, rel, m[4])
				if _, err := pathutil.ResolveInside(n.root, where); err == nil {
					p.diagnostics = append(p.diagnostics, n.diagnostic(where, 1, "found packages "+m[1]+" and "+m[3]+" in one directory", ""))
				}
			}
			continue
		}
		if goCgoEnvironment.MatchString(line) {
			p.diagnostics = nil
			name := "cgo"
			if m := headerMissing.FindStringSubmatch(line); m != nil && safeName.MatchString(m[1]) {
				name = m[1]
			}
			p.environment = &Diagnosis{Kind: "module_missing", Name: name, Message: fmt.Sprintf("cgo could not use %s (a C header, library or compiler missing from this environment); the combined source was not type-checked", name)}
			return p
		}
		if m := goMissingModule.FindStringSubmatch(line); m != nil && n.localPackage(m[1]) {
			// A package of this repository is missing: a branch removed or
			// moved what another still imports. That is the source's.
			if loc := goTypecheckLine.FindStringSubmatch(line); loc != nil {
				if path, ok := n.path(loc[1]); ok {
					number, _ := strconv.Atoi(loc[2])
					p.diagnostics = append(p.diagnostics, n.diagnostic(path, number, "missing repository package "+m[1], ""))
					continue
				}
			}
		}
		if goDependency.MatchString(line) {
			// Errors listed before an unresolved dependency may follow from
			// it, so none of them is reported as the source's.
			p.diagnostics = nil
			if m := goMissingModule.FindStringSubmatch(line); m != nil && safeName.MatchString(m[1]) {
				p.environment = &Diagnosis{Kind: "module_missing", Name: m[1], Message: fmt.Sprintf("go could not resolve package %s, which no module available in this environment provides (Radar disables module downloads); the combined source was not type-checked", m[1])}
			} else {
				p.environment = &Diagnosis{Kind: "module_missing", Name: "go", Message: "go could not resolve a module, checksum or toolchain from this environment (Radar disables module downloads); the combined source was not type-checked"}
			}
			return p
		}
		if goNoPackages.MatchString(line) {
			p.noInputs = true
			continue
		}
		m := goTypecheckLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		path, ok := n.path(m[1])
		if !ok {
			continue
		}
		number, _ := strconv.Atoi(m[2])
		symbol := ""
		if u := goUndefined.FindStringSubmatch(m[3]); u != nil {
			symbol = u[1]
		}
		p.diagnostics = append(p.diagnostics, n.diagnostic(path, number, m[3], symbol))
	}
	return p
}

// typecheckDiagnosis is a fixed sentence naming the first located error and,
// when recognized, the name the combined source does not define.
func typecheckDiagnosis(command string, diags []TypecheckDiagnostic) *Diagnosis {
	if len(diags) == 0 {
		return nil
	}
	first := diags[0]
	for _, d := range diags {
		if d.Symbol != "" {
			first = d
			break
		}
	}
	where := first.Location()
	if first.Symbol != "" {
		return &Diagnosis{Kind: "typecheck_failed", Name: first.Symbol, Message: fmt.Sprintf("type checking (%s) reports %d error(s) in repository source; %s uses %s, which the checked source does not define", command, len(diags), where, first.Symbol)}
	}
	name := first.Path
	if len(name) > 100 {
		name = filepath.Base(name)
	}
	if len(name) > 100 {
		name = name[len(name)-100:]
	}
	return &Diagnosis{Kind: "typecheck_failed", Name: name, Message: fmt.Sprintf("type checking (%s) reports %d error(s) in repository source; the first is at %s", command, len(diags), where)}
}

// maxGoListBytes bounds the go list package stream.
const maxGoListBytes = 16 << 20

// GoCompiledFiles asks the go command that type-checks, in the same private
// environment, which repository files "./..." in cwd compiles: Go, cgo and
// test files of every matched package. Files excluded by build constraints,
// cgo files without cgo, and packages the pattern skips are absent. The
// command runs only after explicit execution opt-in, like the type check.
func GoCompiledFiles(ctx context.Context, root, cwd string, timeout time.Duration) (map[string]bool, error) {
	if timeout <= 0 || timeout > 30*time.Minute {
		return nil, errors.New("invalid go list timeout")
	}
	dir := root
	if cwd != "" && cwd != "." {
		var err error
		if dir, err = pathutil.ResolveInside(root, cwd); err != nil {
			return nil, err
		}
	}
	home, err := os.MkdirTemp("", "radar-golist-home-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(home)
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(runCtx, "go", "list", "-e", "-json=Dir,GoFiles,CgoFiles,TestGoFiles,XTestGoFiles", "./...")
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
		return nil, errors.New("go list timed out")
	}
	if out.exceeded {
		return nil, errors.New("go list output exceeds limit")
	}
	files := map[string]bool{}
	decoder := json.NewDecoder(bytes.NewReader(out.b.Bytes()))
	for {
		var pkg struct {
			Dir                                          string
			GoFiles, CgoFiles, TestGoFiles, XTestGoFiles []string
		}
		if err := decoder.Decode(&pkg); err == io.EOF {
			break
		} else if err != nil {
			return nil, fmt.Errorf("go list output: %w", err)
		}
		rel, ok := relativeDir(root, pkg.Dir)
		if !ok {
			continue
		}
		for _, list := range [][]string{pkg.GoFiles, pkg.CgoFiles, pkg.TestGoFiles, pkg.XTestGoFiles} {
			for _, f := range list {
				files[pathpkg.Join(rel, f)] = true
			}
		}
	}
	if runErr != nil && len(files) == 0 {
		return nil, fmt.Errorf("go list: %w", runErr)
	}
	return files, nil
}

// relativeDir makes an absolute directory inside root repository-relative,
// with "." for root itself.
func relativeDir(root, dir string) (string, bool) {
	if dir == "" {
		return "", false
	}
	for _, base := range []string{root, resolvedPath(root)} {
		if filepath.Clean(dir) == base || resolvedPath(dir) == base {
			return ".", true
		}
	}
	return insidePath(root, dir)
}

func resolvedPath(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return filepath.Clean(p)
}

// limitedBuffer keeps at most limit bytes and records truncation.
type limitedBuffer struct {
	b        bytes.Buffer
	limit    int
	exceeded bool
}

func (l *limitedBuffer) Write(p []byte) (int, error) {
	if l.b.Len()+len(p) > l.limit {
		l.exceeded = true
		if left := l.limit - l.b.Len(); left > 0 {
			l.b.Write(p[:left])
		}
		return len(p), nil
	}
	return l.b.Write(p)
}
