package evidence

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// counts recognizes explicit test results from supported harnesses, not arbitrary success output.
func counts(argv []string, data []byte) int {
	if len(argv) < 2 {
		return 0
	}
	name := filepath.Base(argv[0])
	n := 0
	if name == "go" && argv[1] == "test" {
		hasJSON := false
		for _, a := range argv[2:] {
			if a == "-json" {
				hasJSON = true
			}
		}
		if !hasJSON {
			return 0
		}
		for _, line := range bytes.Split(data, []byte{'\n'}) {
			var event struct {
				Action string
				Test   string
			}
			if json.Unmarshal(line, &event) == nil && (event.Action == "pass" || event.Action == "fail") && event.Test != "" && !strings.Contains(event.Test, "/") {
				n++
			}
		}
		return n
	}
	pattern := ""
	if (name == "python" || name == "python3") && len(argv) > 2 && argv[1] == "-m" && argv[2] == "unittest" {
		pattern = `(?m)^Ran ([0-9]+) tests? in `
	}
	if name == "node" {
		for _, a := range argv[1:] {
			if a == "--test" {
				pattern = `(?m)^# (?:pass|fail) ([0-9]+)$`
			}
		}
	}
	if name == "cargo" && argv[1] == "test" {
		for _, m := range regexp.MustCompile(`(?m)^test result: (?:ok|FAILED)\. ([0-9]+) passed; ([0-9]+) failed;`).FindAllSubmatch(data, -1) {
			a, _ := strconv.Atoi(string(m[1]))
			b, _ := strconv.Atoi(string(m[2]))
			n += a + b
		}
		return n
	}
	if pattern != "" {
		for _, m := range regexp.MustCompile(pattern).FindAllSubmatch(data, -1) {
			v, _ := strconv.Atoi(string(m[1]))
			n += v
		}
	}
	if (name == "python" || name == "python3") && n > 0 {
		for _, m := range regexp.MustCompile(`(?m)^OK \(skipped=([0-9]+)\)`).FindAllSubmatch(data, -1) {
			v, _ := strconv.Atoi(string(m[1]))
			n -= v
		}
		if n < 0 {
			n = 0
		}
	}
	return n
}
