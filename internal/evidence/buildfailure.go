package evidence

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/Jake-Network/radar/internal/pathutil"
)

// sourceBuildError is one compiler error located in a repository-relative
// source file. Symbol is an identifier-shaped name taken from a recognized
// "not defined" message, never other compiler text.
type sourceBuildError struct {
	Path   string
	Line   int
	Symbol string
}

const maxBuildErrors = 20

var (
	goCompileLine = regexp.MustCompile(`^(\S+\.go):([0-9]+):(?:[0-9]+:)? (.+)$`)
	goUndefined   = regexp.MustCompile(`^undefined: ([A-Za-z_][A-Za-z0-9_.]{0,99})$`)
	// Missing modules, checksums or toolchains are about the environment, so
	// a build failure that mentions one is never attributed to the source.
	goDependency  = regexp.MustCompile(`no required module provides package|missing go\.sum entry|cannot find module|module lookup disabled|cannot find package|could not import|is not in std|updates to go\.mod needed|requires go >=`)
	cargoCompile  = regexp.MustCompile("(?m)^error: could not compile `")
	cargoError    = regexp.MustCompile("(?m)^error(?:\\[E[0-9]{4}\\])?: (.*)$")
	cargoLocation = regexp.MustCompile(`^\s*--> (\S+\.rs):([0-9]+):[0-9]+`)
	cargoMissing  = regexp.MustCompile("^(?:cannot find (?:value|function|type|struct|trait|macro|module) |unresolved import |failed to resolve: .*?)`([A-Za-z_][A-Za-z0-9_:]{0,99})`")
	sourceSymbol  = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.:]{0,99}$`)
)

// sourcePath accepts compiler paths that name repository files: relative to
// the execution directory without parent traversal, or absolute inside it
// (javac and CMake-driven compilers print absolute snapshot paths). Other
// absolute paths point into toolchains, module caches or registries, which
// are the environment's. dir is empty when no execution directory is known.
func sourcePath(dir, p string) (string, bool) {
	if p != "" && dir != "" && filepath.IsAbs(p) {
		return insidePath(dir, p)
	}
	if p == "" || filepath.IsAbs(p) || strings.HasPrefix(p, "/") || strings.HasPrefix(p, `\`) || (len(p) > 1 && p[1] == ':') {
		return "", false
	}
	clean, e := pathutil.RepoRelative(filepath.ToSlash(p))
	if e != nil {
		return "", false
	}
	return clean, true
}

// insidePath makes an absolute compiler path relative to dir, also through
// dir's resolved form (macOS reports /private/var for /var temporaries).
func insidePath(dir, p string) (string, bool) {
	bases := []string{dir}
	if resolved, e := filepath.EvalSymlinks(dir); e == nil && resolved != dir {
		bases = append(bases, resolved)
	}
	for _, base := range bases {
		rel, e := filepath.Rel(base, filepath.Clean(p))
		if e != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		if clean, e := pathutil.RepoRelative(filepath.ToSlash(rel)); e == nil {
			return clean, true
		}
	}
	return "", false
}

// goBuildErrors reads compiler errors from `go test -json` build-output
// events or the text diagnostics used before Go 1.24. It returns nothing
// unless a package build failed, every reported error is free of dependency
// or toolchain problems, and at least one names a repository source file.
func goBuildErrors(data []byte, dir string) []sourceBuildError {
	failed := false
	var errs []sourceBuildError
	for _, line := range bytes.Split(data, []byte{'\n'}) {
		var event struct {
			Action string
			Output string
		}
		text := string(line)
		if json.Unmarshal(line, &event) == nil {
			switch event.Action {
			case "build-fail":
				failed = true
				continue
			case "output":
				// Go 1.23 prints compiler diagnostics as text before JSON
				// test events, and identifies the failed build in this output.
				if strings.HasPrefix(event.Output, "FAIL\t") && strings.Contains(event.Output, "[build failed]") {
					failed = true
				}
				continue
			case "build-output":
				text = strings.TrimRight(event.Output, "\n")
			default:
				continue
			}
		}
		if goDependency.MatchString(text) {
			return nil
		}
		m := goCompileLine.FindStringSubmatch(text)
		if m == nil || len(errs) >= maxBuildErrors {
			continue
		}
		path, ok := sourcePath(dir, m[1])
		if !ok {
			continue
		}
		n, _ := strconv.Atoi(m[2])
		e := sourceBuildError{Path: path, Line: n}
		if u := goUndefined.FindStringSubmatch(m[3]); u != nil {
			e.Symbol = u[1]
		}
		errs = append(errs, e)
	}
	if !failed {
		return nil
	}
	return errs
}

// cargoBuildErrors reads rustc errors when cargo reports that a crate could
// not compile. Each error's first "-->" line locates it; errors located
// outside the repository (registry crates) make the failure environmental.
func cargoBuildErrors(text, dir string) []sourceBuildError {
	if !cargoCompile.MatchString(text) {
		return nil
	}
	var errs []sourceBuildError
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		m := cargoError.FindStringSubmatch(line)
		if m == nil || strings.HasPrefix(m[1], "could not compile") || strings.HasPrefix(m[1], "aborting") {
			continue
		}
		for _, next := range lines[i+1 : min(i+4, len(lines))] {
			loc := cargoLocation.FindStringSubmatch(next)
			if loc == nil {
				continue
			}
			path, ok := sourcePath(dir, loc[1])
			if !ok {
				return nil
			}
			n, _ := strconv.Atoi(loc[2])
			e := sourceBuildError{Path: path, Line: n}
			if s := cargoMissing.FindStringSubmatch(m[1]); s != nil {
				e.Symbol = s[1]
			}
			if len(errs) < maxBuildErrors {
				errs = append(errs, e)
			}
			break
		}
	}
	return errs
}

// buildDiagnosis explains a recognized source build failure with a fixed
// sentence from the first error's location and, when recognized, the name it
// could not find.
func buildDiagnosis(tool string, errs []sourceBuildError) *Diagnosis {
	if len(errs) == 0 {
		return nil
	}
	first := errs[0]
	for _, e := range errs {
		if e.Symbol != "" {
			first = e
			break
		}
	}
	where := fmt.Sprintf("%s:%d", first.Path, first.Line)
	if first.Symbol != "" && sourceSymbol.MatchString(first.Symbol) {
		return &Diagnosis{Kind: "build_failed", Name: first.Symbol, Message: fmt.Sprintf("the combined source does not compile (%s): %s uses %s, which the combined source does not define; one branch may have renamed or removed it while another still uses it", tool, where, first.Symbol)}
	}
	name := first.Path
	if len(name) > 100 {
		name = filepath.Base(name)
	}
	return &Diagnosis{Kind: "build_failed", Name: name, Message: fmt.Sprintf("the combined source does not compile (%s); the first compiler error is at %s", tool, where)}
}
