package typecheck

import (
	"path"
	"regexp"
	"strings"

	"github.com/Jake-Network/radar/internal/indexer"
)

// maxCPPIncludeFiles bounds the files read to relate translation units to a
// changed header. A larger tree falls back to checking every unit.
const maxCPPIncludeFiles = 20000

var (
	cppIncludeLiteral = regexp.MustCompile(`(?:#|%:)[ \t\v\f\r]*(?:include|include_next|import)[ \t\v\f\r]*([<"])([^>"]+)[>"]`)
	cppIncludeAny     = regexp.MustCompile(`(?:#|%:)[ \t\v\f\r]*(?:include|include_next|import)\b`)
	// A comment between the directive's # and its name, or a trigraph # or
	// backslash, hides the directive from the patterns above.
	cppHiddenDirective = regexp.MustCompile(`(?:#|%:)[ \t\v\f\r]*/[*/]|\?\?[=/]`)
	cppLineSplice      = regexp.MustCompile(`\\[ \t\v\f]*\n`)
)

// cppIncludeGraph records, by basename, what each committed C/C++ file
// includes. It is selection data only and never establishes coverage.
type cppIncludeGraph struct {
	includes map[string][]string
	// includers maps an included basename to the basenames of its includers.
	includers map[string][]string
	reach     map[string]map[string]bool
	// unbounded: a computed include, an unreadable file or the size limit
	// leaves possible includers unknown, so every unit is selected.
	unbounded bool
}

// changed names files the change touched, including deleted ones, so that a
// quoted include of a deleted header is not mistaken for a generated one.
func newCPPIncludeGraph(entries []indexer.Entry, read func(string) ([]byte, bool), changed []string) *cppIncludeGraph {
	g := &cppIncludeGraph{includes: map[string][]string{}, includers: map[string][]string{}, reach: map[string]map[string]bool{}}
	included := map[string]bool{}
	committed := map[string]bool{}
	for _, e := range entries {
		committed[includeName(e.Path)] = true
	}
	for _, f := range changed {
		committed[includeName(f)] = true
	}
	parse := func(p string) bool {
		if len(g.includes) >= maxCPPIncludeFiles {
			return false
		}
		b, ok := read(p)
		if !ok {
			return false
		}
		var names []string
		// Normalize line endings and splice continuation lines first, as
		// the preprocessor does (gcc accepts whitespace before the newline).
		text := strings.NewReplacer("\r\n", "\n", "\r", "\n").Replace(string(b))
		text = cppLineSplice.ReplaceAllString(text, "")
		for _, line := range strings.Split(text, "\n") {
			// Directives are found anywhere on a line (after a comment or
			// a byte order mark); one whose operand is not a literal, or a
			// quoted include of a file not committed (a generated header
			// can include anything), leaves includers unknown.
			literal := cppIncludeLiteral.FindAllStringSubmatch(line, -1)
			if len(cppIncludeAny.FindAllString(line, -1)) != len(literal) || cppHiddenDirective.MatchString(line) {
				return false
			}
			for _, m := range literal {
				name := includeName(m[2])
				if m[1] == `"` && !committed[name] {
					return false
				}
				names = append(names, name)
			}
		}
		g.includes[p] = names
		for _, name := range names {
			included[name] = true
			g.includers[name] = append(g.includers[name], includeName(p))
		}
		return true
	}
	for _, e := range entries {
		if (CPPHeader(e.Path) || CPPSource(e.Path)) && !parse(e.Path) {
			g.unbounded = true
			return g
		}
	}
	// Included files of any other extension (.inc, .def, extensionless
	// headers) can carry includes too; parse every one that is included.
	for added := true; added; {
		added = false
		for _, e := range entries {
			if _, done := g.includes[e.Path]; done || !included[includeName(e.Path)] {
				continue
			}
			if !parse(e.Path) {
				g.unbounded = true
				return g
			}
			added = true
		}
	}
	return g
}

// reaching returns the basenames through which a file may include header.
func (g *cppIncludeGraph) reaching(header string) map[string]bool {
	if r := g.reach[header]; r != nil {
		return r
	}
	start := includeName(header)
	r := map[string]bool{start: true}
	queue := []string{start}
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		for _, includer := range g.includers[name] {
			if !r[includer] {
				r[includer] = true
				queue = append(queue, includer)
			}
		}
	}
	g.reach[header] = r
	return r
}

// includeName compares includes by basename, ignoring case so that a
// case-insensitive file system cannot hide an includer.
func includeName(p string) string {
	return strings.ToLower(path.Base(strings.ReplaceAll(p, "\\", "/")))
}

// mayInclude reports whether the translation unit source, compiled with
// argv, may include header. Forced includes on the command line count.
func (g *cppIncludeGraph) mayInclude(source string, argv []string, header string) bool {
	if g == nil || g.unbounded {
		return true
	}
	r := g.reaching(header)
	names := append([]string(nil), g.includes[source]...)
	for i, a := range argv {
		switch {
		case a == "-include" && i+1 < len(argv):
			names = append(names, includeName(argv[i+1]))
		case strings.HasPrefix(a, "-include") && len(a) > len("-include"):
			names = append(names, includeName(strings.TrimPrefix(a, "-include")))
		}
	}
	if _, known := g.includes[source]; !known {
		return true
	}
	for _, name := range names {
		if r[name] {
			return true
		}
	}
	return false
}

// CPPIncludeDirs returns the repository directories a supported gcc/clang
// command searches for headers: relative -I, -iquote and -isystem operands,
// resolved against its working directory.
func CPPIncludeDirs(argv []string, cwd string) []string {
	var out []string
	for i := 1; i < len(argv); i++ {
		for _, flag := range []string{"-I", "-iquote", "-isystem"} {
			if !strings.HasPrefix(argv[i], flag) {
				continue
			}
			value := strings.TrimPrefix(argv[i], flag)
			if value == "" && i+1 < len(argv) {
				i++
				value = argv[i]
			}
			if value == "" || strings.HasPrefix(value, "/") || strings.Contains(value, "..") {
				break
			}
			out = append(out, path.Join(cwd, value))
			break
		}
	}
	return out
}
