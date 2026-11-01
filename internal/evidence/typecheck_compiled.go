package evidence

import (
	"encoding/json"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

func init() {
	typecheckParsers["cargo"] = parseCargoTypecheck
	typecheckParsers["java"] = parseJVMTypecheck
	typecheckParsers["cpp"] = parseCPPTypecheck
	typecheckLabels["cargo"] = "cargo check"
	typecheckLabels["java"] = "JVM compilation"
	typecheckLabels["cpp"] = "C/C++ syntax checking"
}

func compiledEnvironment(tool string) *Diagnosis {
	return &Diagnosis{Kind: "module_missing", Name: tool, Message: "Compiler dependencies or toolchain are unavailable; source checking is incomplete"}
}

func parseCargoTypecheck(text string, n *typecheckNormalizer) typecheckParse {
	var p typecheckParse
	finished := false
	for _, line := range strings.Split(text, "\n") {
		var msg struct {
			Reason    string   `json:"reason"`
			Filenames []string `json:"filenames"`
			Message   struct {
				Message string `json:"message"`
				Level   string `json:"level"`
				Code    *struct {
					Code string `json:"code"`
				} `json:"code"`
				Spans []struct {
					File    string `json:"file_name"`
					Line    int    `json:"line_start"`
					Primary bool   `json:"is_primary"`
				} `json:"spans"`
			} `json:"message"`
		}
		if json.Unmarshal([]byte(line), &msg) != nil {
			if strings.Contains(line, "failed to download") || strings.Contains(line, "no matching package") || strings.Contains(line, "failed to run custom build command") || strings.Contains(line, "lock file") {
				p.environment = compiledEnvironment("cargo")
			}
			continue
		}
		if msg.Reason == "compiler-artifact" && len(p.cargoArtifacts) < 10000 {
			p.cargoArtifacts = append(p.cargoArtifacts, msg.Filenames...)
		}
		if msg.Reason == "build-finished" {
			finished = true
		}
		if msg.Reason != "compiler-message" || msg.Message.Level != "error" {
			continue
		}
		located := false
		for _, s := range msg.Message.Spans {
			if !s.Primary {
				continue
			}
			if f, ok := n.path(s.File); ok {
				code := ""
				if msg.Message.Code != nil {
					code = msg.Message.Code.Code
				}
				p.diagnostics = append(p.diagnostics, n.diagnostic(f, s.Line, code+": "+msg.Message.Message, ""))
				located = true
				break
			}
		}
		if !located {
			p.crashed = true
		}
	}
	p.partial = len(p.diagnostics) > 0
	p.noInputs = !finished && len(p.diagnostics) == 0
	return p
}

var jvmDiagnostic = regexp.MustCompile(`^(?:\[ERROR\] )?(.+\.(?:java|kt)):(?:\[)?([0-9]+)(?:,[0-9]+\]|:[0-9]+)?[: ]+(.+)$`)
var kotlinDiagnostic = regexp.MustCompile(`^e: (file:///.+\.kt):([0-9]+):([0-9]+) (.+)$`)

func parseJVMTypecheck(text string, n *typecheckNormalizer) typecheckParse {
	var p typecheckParse
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if strings.Contains(line, "Could not resolve") || strings.Contains(line, "Could not transfer artifact") || strings.Contains(line, "No compiler is provided") || strings.Contains(line, "JAVA_HOME") || strings.Contains(line, "Cannot access") && strings.Contains(line, "offline") {
			p.environment = compiledEnvironment("java")
		}
		m := jvmDiagnostic.FindStringSubmatch(line)
		if k := kotlinDiagnostic.FindStringSubmatch(line); k != nil {
			if u, err := url.Parse(k[1]); err == nil {
				m = []string{k[0], u.Path, k[2], k[4]}
			}
		}
		if m == nil {
			continue
		}
		if !strings.HasPrefix(line, "[ERROR]") && !strings.HasPrefix(line, "e: ") && !strings.Contains(m[3], "error:") {
			continue
		}
		if f, ok := n.path(m[1]); ok {
			ln, _ := strconv.Atoi(m[2])
			p.diagnostics = append(p.diagnostics, n.diagnostic(f, ln, m[3], ""))
		} else {
			p.environment = compiledEnvironment("java")
		}
	}
	p.partial = len(p.diagnostics) > 0
	return p
}

var cppDiagnostic = regexp.MustCompile(`^(.+\.(?i:c|cc|cpp|cxx|c\+\+|h|hh|hpp|hxx|h\+\+|ipp|tpp|inl)):([0-9]+):(?:[0-9]+:)? (fatal error|error): (.+)$`)

func parseCPPTypecheck(text string, n *typecheckNormalizer) typecheckParse {
	var p typecheckParse
	for _, line := range strings.Split(text, "\n") {
		m := cppDiagnostic.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		if strings.Contains(m[4], "No such file or directory") || strings.Contains(m[4], "file not found") {
			p.environment = compiledEnvironment("cpp")
			continue
		}
		if f, ok := n.path(m[1]); ok {
			ln, _ := strconv.Atoi(m[2])
			p.diagnostics = append(p.diagnostics, n.diagnostic(f, ln, m[4], ""))
		} else {
			p.environment = compiledEnvironment("cpp")
		}
	}
	p.partial = len(p.diagnostics) > 0
	return p
}
