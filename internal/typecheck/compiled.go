package typecheck

import (
	"encoding/json"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/Jake-Network/radar/internal/indexer"
	"github.com/Jake-Network/radar/internal/model"
	"github.com/Jake-Network/radar/internal/pathutil"
)

// selectCompiled selects whole build units so unchanged consumers participate.
// Build files are data here: no package manager or compiler runs in selection.
func selectCompiled(files map[string]indexer.Entry, entries []indexer.Entry, read func(string) ([]byte, bool), changed []string, plan *Plan) {
	selected := map[string]*Target{}
	add := func(tool, root, file string, argv []string) {
		key := tool + ":" + root + ":" + strings.Join(argv, "\x00")
		t := selected[key]
		if t == nil {
			t = &Target{ID: model.StableID("typecheck", key), Tool: tool, Root: root, CWD: root, Command: argv}
			selected[key] = t
		}
		t.Changed = append(t.Changed, file)
	}
	ancestor := func(file, name string, outer bool) string {
		found := ""
		for dir := path.Dir(file); ; dir = path.Dir(dir) {
			p := path.Join(dir, name)
			if _, ok := files[p]; ok {
				found = dir
				if !outer {
					break
				}
			}
			if dir == "." {
				break
			}
		}
		return found
	}
	var ccChanged []string
	for _, f := range changed {
		switch path.Ext(f) {
		case ".rs":
			root := ancestor(f, "Cargo.toml", true)
			if root == "" {
				plan.Gaps = append(plan.Gaps, Gap{f, "no committed Cargo manifest owns this Rust source"})
				continue
			}
			add("cargo", root, f, []string{"cargo", "check", "--workspace", "--all-targets", "--locked", "--offline", "--message-format=json"})
		case ".java", ".kt":
			root := ancestor(f, "pom.xml", true)
			if root != "" {
				add("java", root, f, []string{"mvn", "-o", "-B", "test-compile"})
				continue
			}
			settings := ancestor(f, "settings.gradle", true)
			if settings == "" {
				settings = ancestor(f, "settings.gradle.kts", true)
			}
			if settings != "" {
				tasks, ok := gradleCompileTasks(settings, files, read)
				if !ok {
					plan.Gaps = append(plan.Gaps, Gap{f, "Gradle module layout is computed or unsupported; compile target coverage is unknown"})
					continue
				}
				add("java", settings, f, append([]string{"gradle", "--offline", "--no-daemon", "--console=plain"}, tasks...))
				continue
			}
			root = ancestor(f, "build.gradle", false)
			if root == "" {
				root = ancestor(f, "build.gradle.kts", false)
			}
			if root == "" {
				plan.Gaps = append(plan.Gaps, Gap{f, "no supported committed JVM build owns this source"})
				continue
			}
			add("java", root, f, []string{"gradle", "--offline", "--no-daemon", "--console=plain", "testClasses"})
		default:
			if CPPSource(f) {
				ccChanged = append(ccChanged, f)
			}
		}
	}
	if len(ccChanged) > 0 {
		selectCC(files, entries, read, ccChanged, plan, add)
	}
	keys := make([]string, 0, len(selected))
	for k := range selected {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		t := selected[k]
		sort.Strings(t.Changed)
		plan.Targets = append(plan.Targets, *t)
	}
}

func selectCC(files map[string]indexer.Entry, entries []indexer.Entry, read func(string) ([]byte, bool), changed []string, plan *Plan, add func(string, string, string, []string)) {
	covered := map[string]bool{}
	for _, e := range entries {
		if path.Base(e.Path) != "compile_commands.json" {
			continue
		}
		b, ok := read(e.Path)
		if !ok {
			continue
		}
		var commands []struct {
			Directory string   `json:"directory"`
			File      string   `json:"file"`
			Arguments []string `json:"arguments"`
			Command   string   `json:"command"`
		}
		if json.Unmarshal(b, &commands) != nil || len(commands) > 2000 {
			continue
		}
		for _, c := range commands {
			dir := path.Clean(c.Directory)
			if dir == "" {
				dir = "."
			}
			if _, err := pathutil.RepoRelative(dir); err != nil {
				continue
			}
			source := path.Join(dir, c.File)
			if _, err := pathutil.RepoRelative(source); err != nil {
				continue
			}
			if _, ok := files[source]; !ok {
				continue
			}
			args := c.Arguments
			if len(args) == 0 {
				args, _ = compilationArgv(c.Command)
			}
			argv, ok := syntaxOnly(args)
			if !ok {
				continue
			}
			present := false
			for _, arg := range argv[1:] {
				if arg == c.File {
					present = true
				}
			}
			if !present {
				continue
			}
			for _, f := range changed {
				header := CPPHeader(f)
				if f != source && !header {
					continue
				}
				// Checking every TU is conservative for a changed header; exact include
				// coverage is confirmed at execution rather than inferred from filenames.
				add("cpp", dir, f, argv)
				covered[f] = true
			}
		}
	}
	for _, f := range changed {
		if !covered[f] {
			goOwned := false
			for _, target := range plan.Targets {
				if target.Tool == ToolGo && contains(target.Changed, f) {
					goOwned = true
				}
			}
			if goOwned {
				continue
			}
			plan.Gaps = append(plan.Gaps, Gap{f, "no safely relocatable gcc/clang arguments entry covers this source; shell commands and external build paths are unsupported"})
		}
	}
	// A compile database can describe a separate native project inside a Go
	// module. Keep checking the Go module, but do not demand that go list also
	// names a file whose coverage is supplied by its native compiler target.
	for i := range plan.Targets {
		t := &plan.Targets[i]
		if t.Tool != ToolGo {
			continue
		}
		var changed []string
		for _, f := range t.Changed {
			if !covered[f] {
				changed = append(changed, f)
			}
		}
		t.Changed = changed
	}
}

// compilationArgv implements quoting only, never shell expansion. Shell
// operators and substitutions outside quotes are rejected as unsupported.
func compilationArgv(command string) ([]string, bool) {
	var out []string
	var word strings.Builder
	var quote rune
	escaped, started := false, false
	flush := func() {
		if started {
			out = append(out, word.String())
			word.Reset()
			started = false
		}
	}
	for _, c := range command {
		if c == '\x00' || c == '\n' || c == '\r' {
			return nil, false
		}
		if escaped {
			word.WriteRune(c)
			started = true
			escaped = false
			continue
		}
		if c == '\\' && quote != '\'' {
			escaped = true
			started = true
			continue
		}
		if quote != 0 {
			if c == quote {
				quote = 0
			} else {
				if c == '$' || c == '`' {
					return nil, false
				}
				word.WriteRune(c)
			}
			continue
		}
		switch c {
		case '\'', '"':
			quote = c
			started = true
		case ' ', '\t':
			flush()
		case ';', '|', '&', '<', '>', '$', '`':
			return nil, false
		default:
			word.WriteRune(c)
			started = true
		}
	}
	if escaped || quote != 0 {
		return nil, false
	}
	flush()
	return out, len(out) > 0
}

func syntaxOnly(args []string) ([]string, bool) {
	if len(args) == 0 {
		return nil, false
	}
	driver := path.Base(args[0])
	switch driver {
	case "cc", "c++", "gcc", "g++", "clang", "clang++":
	default:
		return nil, false
	}
	out := []string{driver}
	pathOperand := false
	for i := 1; i < len(args); i++ {
		a := args[i]
		if pathOperand {
			if a == "" || strings.Contains(a, "..") || strings.HasPrefix(a, "/") || strings.HasPrefix(a, "=") || strings.HasPrefix(a, "$") || strings.HasPrefix(a, "-") {
				return nil, false
			}
			pathOperand = false
			out = append(out, a)
			continue
		}

		// These modes bypass semantic checking even when -fsyntax-only is
		// appended (notably -E), or turn required errors into warnings.
		switch a {
		case "-E", "-S", "--help", "--version", "-###", "-fdirectives-only", "-fpermissive":
			return nil, false
		}
		if strings.HasPrefix(a, "-print-") || strings.HasPrefix(a, "--help=") || strings.HasPrefix(a, "-dumpspecs") || strings.HasPrefix(a, "-dumpversion") || strings.HasPrefix(a, "-dumpmachine") {
			return nil, false
		}
		if strings.ContainsAny(a, "\x00\r\n") || strings.HasPrefix(a, "@") {
			return nil, false
		}
		switch a {
		case "-o", "-MF", "-MT", "-MQ", "-MJ":
			i++
			if i >= len(args) {
				return nil, false
			}
			continue
		case "-c", "-MD", "-MMD", "-MP", "-MG", "-M", "-MM":
			continue
		}
		if strings.HasPrefix(a, "-o") || strings.HasPrefix(a, "-MF") || strings.HasPrefix(a, "-MT") || strings.HasPrefix(a, "-MQ") || strings.HasPrefix(a, "-MJ") {
			continue
		}
		if strings.HasPrefix(a, "/") || strings.Contains(a, "../") {
			return nil, false
		}
		// Absolute project include paths could read an original checkout,
		// allowing deleted candidate headers to reappear through a shadow.
		for _, prefix := range []string{"-I", "-iquote", "-include", "-isystem"} {
			if strings.HasPrefix(a, prefix) {
				value := strings.TrimPrefix(a, prefix)
				if value == "" {
					pathOperand = true
				}
				if strings.HasPrefix(value, "/") || strings.HasPrefix(value, "=") || strings.HasPrefix(value, "$") || strings.HasPrefix(value, "-") {
					return nil, false
				}
			}
		}
		if strings.HasPrefix(a, "-") && !safeCPPFlag(a) {
			return nil, false
		}
		out = append(out, a)
	}
	if pathOperand {
		return nil, false
	}
	return append(out, "-fsyntax-only"), true
}

// CanonicalCPPCommand validates and normalizes a declared compiler invocation.
// Wrappers and shell execution are intentionally not inferred from argv.
func CanonicalCPPCommand(args []string) ([]string, bool) { return syntaxOnly(args) }

var cppDebugFlag = regexp.MustCompile(`^-g(?:[0-3]|gdb[0-3]?|dwarf-[2345]|line-tables-only)?$`)
var cppOptimizeFlag = regexp.MustCompile(`^-O(?:[0-3sgz]|fast)?$`)

func safeCPPFlag(a string) bool {
	switch a {
	case "-I", "-iquote", "-isystem", "-include", "-D", "-U", "-x", "-pthread", "-pedantic", "-pedantic-errors", "-nostdinc", "-nostdinc++", "-m32", "-m64", "-fsyntax-only", "-fPIC", "-fpic", "-fPIE", "-fpie", "-fno-exceptions", "-fexceptions", "-fno-rtti", "-frtti", "-fno-omit-frame-pointer", "-ffunction-sections", "-fdata-sections":
		return true
	}
	for _, prefix := range []string{"-std=", "-D", "-U", "-I", "-iquote", "-isystem", "-include", "-march=", "-mtune=", "-fvisibility="} {
		if strings.HasPrefix(a, prefix) {
			return true
		}
	}
	return strings.HasPrefix(a, "-W") && !strings.HasPrefix(a, "-Wp,") && !strings.HasPrefix(a, "-Wa,") && !strings.HasPrefix(a, "-Wl,") || cppDebugFlag.MatchString(a) || cppOptimizeFlag.MatchString(a)
}

// CPPHeader is shared by built-in and declared-command input selection.
func CPPHeader(file string) bool {
	switch strings.ToLower(path.Ext(file)) {
	case ".h", ".hh", ".hpp", ".hxx", ".h++", ".ipp", ".tpp", ".inl":
		return true
	}
	return false
}
func CPPSource(file string) bool {
	switch strings.ToLower(path.Ext(file)) {
	case ".c", ".cc", ".cpp", ".cxx", ".c++":
		return true
	}
	return CPPHeader(file)
}
