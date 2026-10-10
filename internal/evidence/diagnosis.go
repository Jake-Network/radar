package evidence

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// Diagnosis names why a command produced no test outcome or why the combined
// source failed to build, when its output matches a known shape. It is a fixed sentence built from a bounded name,
// never raw output, so persisted evidence keeps only the output digest.
type Diagnosis struct {
	// Kind is runner_missing (python -m RUNNER could not find RUNNER, or a
	// Maven/Gradle wrapper found no JVM),
	// command_missing (the shell could not find a program), module_missing
	// (an import failed: a dependency absent from this environment, or a
	// module a branch renamed or removed) or build_failed (the compiler
	// rejected repository source; Name is the undefined symbol or the file).
	Kind    string `json:"kind"`
	Name    string `json:"name"`
	Message string `json:"message"`
}

// Environment reports whether the diagnosis is about the machine running the
// command rather than about the combined source.
func (d *Diagnosis) Environment() bool {
	return d != nil && (d.Kind == "runner_missing" || d.Kind == "command_missing")
}

var (
	safeName       = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.@/-]{0,99}$`)
	importMissing  = regexp.MustCompile(`(?m)ModuleNotFoundError: No module named '([^'\s]+)'`)
	commandMissing = regexp.MustCompile(`(?m)^(?:[^:\n]*: )?(?:[0-9]+: )?([^\s:/]+): (?:command )?not found\s*$`)
	nodeMissing    = regexp.MustCompile(`Cannot find module '([^'\s]+)'`)
	// Maven and Gradle wrappers stop before building when no JVM is found.
	jvmMissing = regexp.MustCompile(`JAVA_HOME is not set and no 'java' command could be found|JAVA_HOME (?:environment variable )?is not defined correctly|Unable to locate a Java Runtime`)
)

// diagnose recognizes a missing runner, program or module in the output of a
// command that reported no test outcome. A missing program also needs the
// shell's exit 127, so test output that merely says "not found" is ignored.
func diagnose(argv []string, exit int, data []byte) *Diagnosis {
	if len(argv) == 0 {
		return nil
	}
	text := ansi.ReplaceAllString(string(data), "")
	tool := filepath.Base(argv[0])
	if strings.HasPrefix(tool, "python") && len(argv) > 2 && argv[1] == "-m" && safeName.MatchString(argv[2]) {
		runner := argv[2]
		if regexp.MustCompile(`No module named '?` + regexp.QuoteMeta(runner) + `'?\s*$`).MatchString(firstMatchingLine(text, "No module named")) {
			return &Diagnosis{Kind: "runner_missing", Name: runner, Message: fmt.Sprintf("%s is not installed for %s (it reported \"No module named %s\")", runner, tool, runner)}
		}
	}
	if buildTool(argv) != "" && jvmMissing.MatchString(text) {
		return &Diagnosis{Kind: "runner_missing", Name: "java", Message: fmt.Sprintf("java is not installed or not on PATH (%s could not find a JVM)", tool)}
	}
	if m := commandMissing.FindStringSubmatch(text); exit == 127 && m != nil && safeName.MatchString(m[1]) {
		return &Diagnosis{Kind: "command_missing", Name: m[1], Message: fmt.Sprintf("%s is not installed or not on PATH (it reported \"%s: not found\"); untracked directories such as node_modules are not copied into the private candidate", m[1], m[1])}
	}
	if m := importMissing.FindStringSubmatch(text); m != nil && safeName.MatchString(m[1]) {
		return &Diagnosis{Kind: "module_missing", Name: m[1], Message: fmt.Sprintf("Python could not import %s: a dependency missing from this environment, or a module a branch renamed or removed", m[1])}
	}
	if m := nodeMissing.FindStringSubmatch(text); m != nil && safeName.MatchString(m[1]) {
		return &Diagnosis{Kind: "module_missing", Name: m[1], Message: fmt.Sprintf("Node could not find module %s: a dependency missing from the private candidate (node_modules is not copied), or a module a branch renamed or removed", m[1])}
	}
	return nil
}

func firstMatchingLine(text, needle string) string {
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(line, needle) {
			return strings.TrimSpace(line)
		}
	}
	return ""
}
