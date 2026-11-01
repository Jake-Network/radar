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
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Jake-Network/radar/internal/indexer"
	"github.com/Jake-Network/radar/internal/pathutil"
)

func init() {
	typecheckParsers["tsc"] = parseTypeScript
	typecheckLabels["tsc"] = "tsc"
}

var (
	tsDiagnostic         = regexp.MustCompile(`^(.+)\(([0-9]+),([0-9]+)\): error TS([0-9]{3,5}): (.+)$`)
	tsGlobalDiagnostic   = regexp.MustCompile(`^error TS([0-9]{3,5}): (.+)$`)
	tsMissingModule      = regexp.MustCompile(`^Cannot find module ['"]([^'"\r\n]{1,300})['"]`)
	tsMissingDeclaration = regexp.MustCompile(`^Could not find a declaration file for module ['"]([^'"\r\n]{1,300})['"]`)
	tsUndefined          = regexp.MustCompile(`^Cannot find name ['"]([A-Za-z_$][A-Za-z0-9_$]{0,99})['"]`)
	tsMissingProperty    = regexp.MustCompile(`^Property ['"]([A-Za-z_$][A-Za-z0-9_$]{0,99})['"] does not exist`)
)

func tsEnvironment(name string) *Diagnosis {
	if !sourceSymbol.MatchString(name) || len(name) > 100 {
		name = "typescript"
	}
	return &Diagnosis{Kind: "module_missing", Name: name, Message: fmt.Sprintf("TypeScript could not use %s from the declared environment; the combined source was not type-checked", name)}
}

func parseTypeScript(text string, n *typecheckNormalizer) typecheckParse {
	var result typecheckParse
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		line = strings.TrimRight(line, "\r")
		if global := tsGlobalDiagnostic.FindStringSubmatch(line); global != nil {
			switch global[1] {
			case "18003", "18002":
				result.noInputs = true
			case "2688":
				result.environment, result.diagnostics = tsEnvironment("typescript"), nil
				return result
			default:
				result.crashed = true
			}
			continue
		}
		match := tsDiagnostic.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		file, inside := typeScriptSourcePath(n.root, n.dir, match[1])
		if !inside || indexer.ExcludedPath(file) {
			result.environment, result.diagnostics = tsEnvironment("typescript"), nil
			return result
		}
		code, message := match[4], match[5]
		for _, continuation := range lines[i+1:] {
			if continuation == "" || continuation[0] != ' ' && continuation[0] != '\t' {
				break
			}
			message += "\n" + strings.TrimRight(continuation, "\r")
		}
		if number, _ := strconv.Atoi(code); number >= 1000 && number < 2000 {
			result.partial = true
		}
		if code == "2688" {
			result.environment, result.diagnostics = tsEnvironment("typescript"), nil
			return result
		}
		if code == "2307" || code == "2792" || code == "7016" {
			missing := tsMissingModule.FindStringSubmatch(message)
			if code == "7016" {
				missing = tsMissingDeclaration.FindStringSubmatch(message)
			}
			if missing == nil || !typeScriptLocalImport(n.root, file, missing[1], n.tsconfig) {
				name := "typescript"
				if missing != nil {
					name = missing[1]
				}
				result.environment, result.diagnostics = tsEnvironment(name), nil
				return result
			}
		}
		number, _ := strconv.Atoi(match[2])
		symbol := ""
		if missing := tsUndefined.FindStringSubmatch(message); missing != nil {
			symbol = missing[1]
		}
		if missing := tsMissingProperty.FindStringSubmatch(message); missing != nil {
			symbol = missing[1]
		}
		result.diagnostics = append(result.diagnostics, n.diagnostic(file, number, "TS"+code+": "+message, symbol))
	}
	return result
}

// TypeScript prints paths relative to the configured project, including
// ../sibling source. Such paths are accepted only after resolving them back
// inside the full candidate; dependency and toolchain paths remain external.
func typeScriptSourcePath(root, dir, file string) (string, bool) {
	file = strings.ReplaceAll(file, `\`, "/")
	full := filepath.FromSlash(file)
	if len(file) > 1 && file[1] == ':' && !filepath.IsAbs(full) {
		return "", false
	}
	if !filepath.IsAbs(full) {
		full = filepath.Join(dir, full)
	}
	roots := []string{root}
	if physical, err := filepath.EvalSymlinks(root); err == nil && physical != root {
		roots = append(roots, physical)
	}
	for _, base := range roots {
		rel, err := filepath.Rel(base, full)
		if err != nil {
			continue
		}
		clean, err := pathutil.RepoRelative(filepath.ToSlash(rel))
		if err != nil {
			continue
		}
		if _, err := pathutil.ResolveInside(root, clean); err == nil {
			return clean, true
		}
	}
	return "", false
}

type tsImportSettings struct {
	Extends         json.RawMessage `json:"extends"`
	CompilerOptions struct {
		BaseURL *string             `json:"baseUrl"`
		Paths   map[string][]string `json:"paths"`
	} `json:"compilerOptions"`
}

// typeScriptLocalImport recognizes explicit repository-relative imports and
// tsconfig paths mappings. A bare dependency is not made first-party simply
// because an identically named directory exists somewhere in the repository.
func typeScriptLocalImport(root, importer, module, project string) bool {
	inside := func(candidate string) bool {
		if filepath.IsAbs(candidate) || strings.Contains(candidate, `\`) {
			return false
		}
		clean, err := pathutil.RepoRelative(path.Clean(candidate))
		if err != nil || indexer.ExcludedPath(clean) {
			return false
		}
		for ancestor := clean; ancestor != "."; ancestor = path.Dir(ancestor) {
			if _, statErr := os.Lstat(filepath.Join(root, filepath.FromSlash(ancestor))); statErr == nil {
				_, resolveErr := pathutil.ResolveInside(root, ancestor)
				return resolveErr == nil
			} else if !errors.Is(statErr, os.ErrNotExist) {
				return false
			}
		}
		return true
	}
	if strings.HasPrefix(module, "./") || strings.HasPrefix(module, "../") {
		return inside(path.Join(path.Dir(importer), module))
	}
	read := func(file string) ([]byte, bool) {
		full, err := pathutil.ResolveInside(root, file)
		if err != nil {
			return nil, false
		}
		info, err := os.Stat(full)
		if err != nil || !info.Mode().IsRegular() || info.Size() > 1<<20 {
			return nil, false
		}
		data, err := os.ReadFile(full)
		return data, err == nil
	}
	config, exists := project, project != ""
	if !exists {
		config, exists = indexer.NearestTypeScriptConfig(importer, func(file string) bool { _, ok := read(file); return ok })
	}
	if !exists {
		return false
	}
	var paths map[string][]string
	pathsDir, base := "", ""
	hasBase := false
	seen := map[string]bool{}
	var walk func(string)
	walk = func(config string) {
		if seen[config] || len(seen) >= 16 {
			return
		}
		seen[config] = true
		data, ok := read(config)
		var settings tsImportSettings
		if !ok || indexer.UnmarshalJSONC(data, &settings) != nil {
			return
		}
		if settings.CompilerOptions.Paths != nil && paths == nil {
			paths, pathsDir = settings.CompilerOptions.Paths, path.Dir(config)
		}
		if settings.CompilerOptions.BaseURL != nil && !hasBase {
			value := *settings.CompilerOptions.BaseURL
			if !path.IsAbs(value) && !strings.Contains(value, `\`) {
				base = path.Join(path.Dir(config), value)
				hasBase = true
			}
		}
		var parent string
		var parents []string
		if json.Unmarshal(settings.Extends, &parent) == nil && parent != "" {
			parents = []string{parent}
		} else {
			_ = json.Unmarshal(settings.Extends, &parents)
		}
		for i := len(parents) - 1; i >= 0; i-- {
			parent = parents[i]
			if !strings.HasPrefix(parent, "./") && !strings.HasPrefix(parent, "../") {
				continue
			}
			parent = path.Join(path.Dir(config), parent)
			if !inside(parent) {
				continue
			}
			if _, ok := read(parent); !ok {
				if _, ok := read(parent + ".json"); ok {
					parent += ".json"
				} else {
					parent = path.Join(parent, "tsconfig.json")
				}
			}
			walk(parent)
		}
	}
	walk(config)
	matches := map[string]string{}
	bestLength := -1
	if _, exact := paths[module]; exact && !strings.Contains(module, "*") {
		matches[module] = ""
	}
	for pattern := range paths {
		if _, exact := paths[module]; exact && !strings.Contains(module, "*") {
			break
		}
		prefix, suffix, wildcard := strings.Cut(pattern, "*")
		if !wildcard {
			continue
		}
		if strings.Contains(suffix, "*") || !strings.HasPrefix(module, prefix) || !strings.HasSuffix(module, suffix) || len(module) < len(prefix)+len(suffix) {
			continue
		}
		if len(prefix) > bestLength {
			matches = map[string]string{}
			bestLength = len(prefix)
		}
		if len(prefix) == bestLength {
			matches[pattern] = module[len(prefix) : len(module)-len(suffix)]
		}
	}
	if len(matches) > 0 {
		dir := pathsDir
		if hasBase {
			dir = base
		}
		for pattern, capture := range matches {
			if len(paths[pattern]) == 0 {
				return false
			}
			for _, target := range paths[pattern] {
				if path.IsAbs(target) || strings.Contains(target, `\`) || !inside(path.Join(dir, strings.Replace(target, "*", capture, 1))) {
					return false
				}
			}
		}
		return true
	}
	if hasBase && inside(path.Join(base, module)) {
		for _, suffix := range []string{".ts", ".tsx", ".mts", ".cts", ".d.ts", "/index.ts", "/index.d.ts"} {
			if _, ok := read(path.Join(base, module) + suffix); ok {
				return true
			}
		}
	}
	return false
}

func typeScriptNodeScript(argv []string) (int, bool) {
	for i := 0; i+1 < len(argv); i++ {
		node := strings.ToLower(filepath.Base(argv[i]))
		if node != "node" && node != "node.exe" {
			continue
		}
		if strings.HasSuffix(filepath.ToSlash(argv[i+1]), "/typescript/bin/tsc") {
			return i + 1, true
		}
	}
	return 0, false
}

// typeScriptProject identifies only an explicit project invocation. Positional
// file invocations ignore tsconfig settings and need a different input probe.
func typeScriptProject(root, cwd string, argv []string) string {
	script, ok := typeScriptNodeScript(argv)
	if !ok {
		return ""
	}
	project := ""
	for i := script + 1; i < len(argv); i++ {
		if (argv[i] == "-p" || argv[i] == "--project") && i+1 < len(argv) {
			project = argv[i+1]
			i++
		} else if value, found := strings.CutPrefix(argv[i], "--project="); found {
			project = value
		}
	}
	if project == "" {
		return ""
	}
	dir := root
	if cwd != "" && cwd != "." {
		dir = filepath.Join(root, filepath.FromSlash(cwd))
	}
	full := filepath.FromSlash(project)
	if !filepath.IsAbs(full) {
		full = filepath.Join(dir, full)
	}
	rel, err := filepath.Rel(root, full)
	if err != nil {
		return ""
	}
	full, err = pathutil.ResolveInside(root, filepath.ToSlash(rel))
	if err != nil {
		return ""
	}
	if info, err := os.Stat(full); err == nil && info.IsDir() {
		full = filepath.Join(full, "tsconfig.json")
	}
	if file, ok := typeScriptSourcePath(root, root, full); ok {
		return file
	}
	return ""
}

// ResolveTypeScriptCommand finds TypeScript only in candidate-local
// node_modules, from the project upwards. It never falls back to global tsc
// or the original checkout's untracked dependencies.
func ResolveTypeScriptCommand(root, cwd string, argv []string) ([]string, error) {
	script, ok := typeScriptNodeScript(argv)
	if !ok {
		return nil, errors.New("TypeScript command needs an explicit node and candidate-local TypeScript script")
	}
	dir := cwd
	if dir == "" {
		dir = "."
	}
	if dir != "." {
		if _, err := pathutil.RepoRelative(dir); err != nil {
			return nil, errors.New("TypeScript working directory is outside the candidate")
		}
	}
	workingDir, err := pathutil.ResolveInside(root, dir)
	if err != nil {
		return nil, errors.New("TypeScript working directory is unavailable in the candidate")
	}
	for current := dir; ; current = path.Dir(current) {
		dependencyDir := path.Join(current, "node_modules")
		if _, statErr := os.Lstat(filepath.Join(root, filepath.FromSlash(dependencyDir))); statErr == nil {
			if _, err := pathutil.ResolveInside(root, dependencyDir); err != nil {
				return nil, errors.New("TypeScript dependency path is outside the candidate")
			}
		}
		candidate := path.Join(current, "node_modules/typescript/bin/tsc")
		full, err := pathutil.ResolveInside(root, candidate)
		if err == nil {
			info, statErr := os.Stat(full)
			if statErr == nil && info.Mode().IsRegular() {
				rel, relErr := filepath.Rel(workingDir, full)
				if relErr != nil {
					return nil, errors.New("TypeScript launcher path is unavailable")
				}
				out := append([]string(nil), argv...)
				out[script] = filepath.ToSlash(rel)
				return out, nil
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, errors.New("TypeScript dependency path is outside the candidate")
		}
		if current == "." {
			break
		}
	}
	return nil, errors.New("TypeScript is absent from candidate-local node_modules; use declared runner setup")
}

const typeScriptProbeScript = `
const fs = require("fs"), path = require("path");
try {
  const input = JSON.parse(fs.readFileSync(0, "utf8"));
  const ts = require(input.library);
  const parsed = ts.parseCommandLine(input.args);
  if (parsed.errors.length) throw new Error("TypeScript command options could not be read");
  if (!parsed.options.project || parsed.fileNames.length || Object.prototype.hasOwnProperty.call(parsed.options, "baseUrl"))
    throw new Error("TypeScript input inventory requires an explicit project configuration");
  if (parsed.options.showConfig || parsed.options.listFilesOnly || parsed.options.help ||
      parsed.options.version || parsed.options.init || parsed.options.watch)
    throw new Error("TypeScript command does not perform a bounded type check");
  const config = path.resolve(parsed.options.project || "tsconfig.json");
  let invalid = false;
  const host = Object.assign({}, ts.sys, {onUnRecoverableConfigFileDiagnostic: () => {invalid = true;}});
  const settings = ts.getParsedCommandLineOfConfigFile(config, parsed.options, host);
  if (invalid || !settings || settings.errors.length) throw new Error("TypeScript configuration or its input files could not be read");
  if (settings.options.composite || (settings.projectReferences && settings.projectReferences.length))
    throw new Error("TypeScript project references and composite projects are not supported");
  if (settings.options.noCheck) throw new Error("TypeScript checking is disabled by configuration");
  const program = ts.createProgram({rootNames: settings.fileNames, options: Object.assign({}, settings.options, {noEmit: true}), projectReferences: settings.projectReferences});
  const root = fs.realpathSync(input.root);
  const sources = [];
  for (const source of program.getSourceFiles()) {
    const real = fs.realpathSync(source.fileName);
    const relative = path.relative(root, real);
    if (path.isAbsolute(relative) || relative === ".." || relative.startsWith(".."+path.sep))
      throw new Error("TypeScript reads source outside the candidate");
    if ((settings.options.skipLibCheck && source.isDeclarationFile) ||
        (source.checkJsDirective && source.checkJsDirective.enabled === false)) continue;
    sources.push(real);
  }
  if (!settings.fileNames.length) throw new Error("No TypeScript source files were selected");
  process.stdout.write(JSON.stringify({sources}));
} catch (error) { process.stdout.write(JSON.stringify({error: ""+error.message})); process.exitCode = 1; }
`

// TypeScriptSources uses the same local compiler package and command options
// to inventory the project. A probe failure is not coverage: callers must
// leave affected changed files unchecked. It performs no emit or cache write.
func TypeScriptSources(ctx context.Context, root, cwd string, argv []string, timeout time.Duration) (map[string]bool, error) {
	if timeout <= 0 || timeout > 30*time.Minute {
		return nil, errors.New("invalid TypeScript probe timeout")
	}
	script, ok := typeScriptNodeScript(argv)
	if !ok {
		return nil, errors.New("TypeScript input inventory requires an explicit candidate-local node compiler command")
	}
	dir := root
	if cwd != "" && cwd != "." {
		var err error
		dir, err = pathutil.ResolveInside(root, cwd)
		if err != nil {
			return nil, errors.New("TypeScript working directory is outside the candidate")
		}
	}
	compiler := filepath.FromSlash(argv[script])
	if !filepath.IsAbs(compiler) {
		compiler = filepath.Join(dir, compiler)
	}
	rel, err := filepath.Rel(root, compiler)
	if err != nil {
		return nil, errors.New("TypeScript compiler is outside the candidate")
	}
	compiler, err = pathutil.ResolveInside(root, filepath.ToSlash(rel))
	if err != nil {
		return nil, errors.New("TypeScript compiler is unavailable in the candidate")
	}
	library := filepath.Join(filepath.Dir(filepath.Dir(compiler)), "lib", "typescript.js")
	rel, err = filepath.Rel(root, library)
	if err != nil {
		return nil, errors.New("TypeScript compiler library is outside the candidate")
	}
	library, err = pathutil.ResolveInside(root, filepath.ToSlash(rel))
	if err != nil {
		return nil, errors.New("TypeScript compiler library is unavailable in the candidate")
	}
	payload, err := json.Marshal(struct {
		Root    string   `json:"root"`
		Library string   `json:"library"`
		Args    []string `json:"args"`
	}{root, library, argv[script+1:]})
	if err != nil {
		return nil, err
	}
	home, err := os.MkdirTemp("", "radar-typescript-probe-home-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(home)
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	probe := append(append([]string(nil), argv[:script]...), "-e", typeScriptProbeScript)
	if err := checkExecutable(probe); err != nil {
		return nil, err
	}
	command := exec.CommandContext(runCtx, probe[0], probe[1:]...)
	command.Dir, command.Env, command.Stdin = dir, environment(home, nil), bytes.NewReader(payload)
	configureProcess(command)
	command.WaitDelay = time.Second
	var output limitedBuffer
	output.limit = maxGoListBytes
	command.Stdout, command.Stderr = &output, io.Discard
	runErr := command.Run()
	if runCtx.Err() != nil {
		return nil, errors.New("TypeScript input inventory timed out")
	}
	if output.exceeded {
		return nil, errors.New("TypeScript input inventory exceeds the output limit")
	}
	var result struct {
		Sources []string `json:"sources"`
		Error   string   `json:"error"`
	}
	if err := json.Unmarshal(output.b.Bytes(), &result); err != nil {
		return nil, errors.New("TypeScript input inventory was not recognized")
	}
	if result.Error != "" {
		// Return only fixed reasons, never arbitrary compiler/plugin output.
		if result.Error == "TypeScript project references and composite projects are not supported" {
			return nil, errors.New(result.Error)
		}
		return nil, errors.New("TypeScript input inventory is unavailable")
	}
	if runErr != nil {
		return nil, errors.New("TypeScript input inventory command failed")
	}
	sources := map[string]bool{}
	for _, file := range result.Sources {
		if rel, ok := typeScriptSourcePath(root, root, file); ok && !indexer.ExcludedPath(rel) {
			sources[rel] = true
		}
	}
	if len(sources) == 0 {
		return nil, errors.New("TypeScript input inventory contains no checked repository source")
	}
	return sources, nil
}
