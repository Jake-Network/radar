package evidence

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/Jake-Network/radar/internal/pathutil"
)

// harnessResult counts recognized, executed test cases. Run excludes skipped
// cases; Failed includes errors. BuildErrors holds compiler errors located in
// repository source when the test build itself failed.
type harnessResult struct {
	Run, Failed, Skipped int
	Harness              string
	BuildErrors          []sourceBuildError
}

var (
	ansi          = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)
	unittestRan   = regexp.MustCompile(`(?m)^Ran ([0-9]+) tests? in `)
	unittestFail  = regexp.MustCompile(`(?m)^FAILED \(([^)]*)\)`)
	unittestOK    = regexp.MustCompile(`(?m)^OK \(([^)]*)\)`)
	keyValue      = regexp.MustCompile(`([a-z]+)=([0-9]+)`)
	pytestSummary = regexp.MustCompile(`(?m)^=*\s*((?:[0-9]+ [a-z]+(?:, )?)+) in [0-9.]+s`)
	countWord     = regexp.MustCompile(`([0-9]+) ([a-z]+)`)
	jestSummary   = regexp.MustCompile(`(?m)^Tests:\s+(.+)$`)
	vitestSummary = regexp.MustCompile(`(?m)^\s*Tests\s+(.+?)\s*\([0-9]+\)\s*$`)
	nodeCount     = regexp.MustCompile(`(?m)^# (pass|fail|skipped|todo) ([0-9]+)$`)
	cargoSummary  = regexp.MustCompile(`(?m)^test result: (?:ok|FAILED)\. ([0-9]+) passed; ([0-9]+) failed; ([0-9]+) ignored`)
)

func isUnittest(argv []string) bool {
	name := filepath.Base(argv[0])
	return (name == "python" || name == "python3") && len(argv) > 2 && argv[1] == "-m" && argv[2] == "unittest"
}

func uses(argv []string, tool string) bool {
	for _, a := range argv {
		if base := filepath.Base(a); base == tool || strings.HasPrefix(base, tool+"@") || base == tool+".js" {
			return true
		}
	}
	return false
}

// harnessCounts recognizes explicit results from supported harnesses, never
// arbitrary success output. dir is the command's working directory, used to
// read absolute compiler paths; empty accepts only relative ones.
func harnessCounts(argv []string, data []byte, dir string) harnessResult {
	if len(argv) < 2 {
		return harnessResult{}
	}
	text := ansi.ReplaceAllString(string(data), "")
	name := filepath.Base(argv[0])
	switch {
	case name == "go" && argv[1] == "test":
		return goTest(argv, data, dir)
	case isUnittest(argv):
		return unittest(text)
	case uses(argv, "pytest") || uses(argv, "py.test"):
		return pytest(text)
	case uses(argv, "vitest"):
		return summaryWords(vitestSummary, text, "vitest")
	case uses(argv, "jest"):
		return summaryWords(jestSummary, text, "jest")
	case name == "node" && slices.Contains(argv[1:], "--test"):
		return nodeTest(text)
	case name == "cargo" && argv[1] == "test":
		return cargo(text, dir)
	}
	return harnessResult{}
}

func goTest(argv []string, data []byte, dir string) harnessResult {
	r := harnessResult{Harness: "go-test-json"}
	if !slices.Contains(argv[2:], "-json") {
		return harnessResult{}
	}
	for _, line := range bytes.Split(data, []byte{'\n'}) {
		var event struct {
			Action string
			Test   string
		}
		if json.Unmarshal(line, &event) != nil || event.Test == "" || strings.Contains(event.Test, "/") {
			continue
		}
		switch event.Action {
		case "pass":
			r.Run++
		case "fail":
			r.Run++
			r.Failed++
		case "skip":
			r.Skipped++
		}
	}
	r.BuildErrors = goBuildErrors(data, dir)
	return r
}

func unittest(text string) harnessResult {
	r := harnessResult{Harness: "unittest"}
	for _, m := range unittestRan.FindAllStringSubmatch(text, -1) {
		v, _ := strconv.Atoi(m[1])
		r.Run += v
	}
	for _, re := range []*regexp.Regexp{unittestFail, unittestOK} {
		for _, m := range re.FindAllStringSubmatch(text, -1) {
			for _, kv := range keyValue.FindAllStringSubmatch(m[1], -1) {
				v, _ := strconv.Atoi(kv[2])
				switch kv[1] {
				case "failures", "errors":
					r.Failed += v
				case "skipped":
					r.Skipped += v
				}
			}
		}
	}
	r.Run -= r.Skipped
	if r.Run < 0 {
		r.Run = 0
	}
	return r
}

func pytest(text string) harnessResult {
	r := harnessResult{Harness: "pytest"}
	matches := pytestSummary.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		return r
	}
	for _, m := range countWord.FindAllStringSubmatch(matches[len(matches)-1][1], -1) {
		v, _ := strconv.Atoi(m[1])
		switch m[2] {
		case "passed", "xfailed", "xpassed":
			r.Run += v
		case "failed", "error", "errors":
			r.Run += v
			r.Failed += v
		case "skipped":
			r.Skipped += v
		}
	}
	return r
}

// summaryWords parses jest ("1 failed, 4 passed, 5 total") and vitest
// ("4 passed | 1 failed") summary lines.
func summaryWords(re *regexp.Regexp, text, harness string) harnessResult {
	r := harnessResult{Harness: harness}
	matches := re.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		return r
	}
	for _, m := range countWord.FindAllStringSubmatch(matches[len(matches)-1][1], -1) {
		v, _ := strconv.Atoi(m[1])
		switch m[2] {
		case "passed":
			r.Run += v
		case "failed":
			r.Run += v
			r.Failed += v
		case "skipped", "todo":
			r.Skipped += v
		}
	}
	return r
}

func nodeTest(text string) harnessResult {
	r := harnessResult{Harness: "node-test"}
	for _, m := range nodeCount.FindAllStringSubmatch(text, -1) {
		v, _ := strconv.Atoi(m[2])
		switch m[1] {
		case "pass":
			r.Run += v
		case "fail":
			r.Run += v
			r.Failed += v
		default:
			r.Skipped += v
		}
	}
	return r
}

func cargo(text, dir string) harnessResult {
	r := harnessResult{Harness: "cargo-test"}
	for _, m := range cargoSummary.FindAllStringSubmatch(text, -1) {
		passed, _ := strconv.Atoi(m[1])
		failed, _ := strconv.Atoi(m[2])
		ignored, _ := strconv.Atoi(m[3])
		r.Run += passed + failed
		r.Failed += failed
		r.Skipped += ignored
	}
	r.BuildErrors = cargoBuildErrors(text, dir)
	return r
}

const maxJUnitBytes = 16 << 20

// maxJUnitFiles bounds the per-class reports read from a report directory.
const maxJUnitFiles = 4096

// readJUnit parses a JUnit XML report written by the command inside the
// snapshot. Every <testcase> counts; <failure>/<error> fail it and <skipped>
// skips it. Any framework with a JUnit reporter is therefore supported. A
// directory (Maven Surefire, Gradle) contributes its regular TEST-*.xml files,
// without recursion or symlinks, within the same total byte bound.
func readJUnit(dest, report string) (harnessResult, error) {
	clean, err := pathutil.RepoRelative(report)
	if err != nil {
		return harnessResult{}, err
	}
	path, err := pathutil.ResolveInside(dest, clean)
	if err != nil {
		return harnessResult{}, err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return harnessResult{}, err
	}
	if !info.IsDir() {
		f, err := os.Open(path)
		if err != nil {
			return harnessResult{}, err
		}
		defer f.Close()
		return parseJUnit(io.LimitReader(f, maxJUnitBytes))
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return harnessResult{}, err
	}
	total := harnessResult{Harness: "junit"}
	var remaining int64 = maxJUnitBytes
	files, cases := 0, 0
	for _, entry := range entries {
		name := entry.Name()
		if !entry.Type().IsRegular() || !strings.HasPrefix(name, "TEST-") || !strings.HasSuffix(name, ".xml") {
			continue
		}
		if files++; files > maxJUnitFiles {
			return harnessResult{}, errors.New("JUnit report directory exceeds file limit")
		}
		r, n, err := readJUnitFile(filepath.Join(path, name), remaining)
		if err != nil {
			return harnessResult{}, err
		}
		remaining -= n
		total.Run += r.Run
		total.Failed += r.Failed
		total.Skipped += r.Skipped
		cases += r.Run + r.Skipped
	}
	if cases == 0 {
		return harnessResult{}, errors.New("JUnit report directory contains no test cases")
	}
	return total, nil
}

// readJUnitFile counts one report within the remaining byte budget and
// returns the bytes it consumed.
func readJUnitFile(path string, remaining int64) (harnessResult, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return harnessResult{}, 0, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return harnessResult{}, 0, err
	}
	if info.Size() > remaining {
		return harnessResult{}, 0, errors.New("JUnit reports exceed byte limit")
	}
	r, _, err := countJUnit(io.LimitReader(f, remaining))
	return r, info.Size(), err
}

func parseJUnit(reader io.Reader) (harnessResult, error) {
	r, cases, err := countJUnit(reader)
	if err != nil {
		return harnessResult{}, err
	}
	if cases == 0 {
		return harnessResult{}, errors.New("JUnit report contains no test cases")
	}
	return r, nil
}

func countJUnit(reader io.Reader) (harnessResult, int, error) {
	r := harnessResult{Harness: "junit"}
	d := xml.NewDecoder(reader)
	inCase, failed, skipped, cases := false, false, false, 0
	for {
		token, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return harnessResult{}, 0, err
		}
		switch t := token.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "testcase":
				inCase, failed, skipped = true, false, false
				cases++
			case "failure", "error":
				failed = failed || inCase
			case "skipped":
				skipped = skipped || inCase
			}
		case xml.EndElement:
			if t.Name.Local == "testcase" && inCase {
				inCase = false
				switch {
				case skipped:
					r.Skipped++
				case failed:
					r.Run++
					r.Failed++
				default:
					r.Run++
				}
			}
		}
	}
	return r, cases, nil
}

var missingModule = regexp.MustCompile(`(?m)^(?:E\s+)?ModuleNotFoundError: No module named`)

// environmentFailure recognizes runs that failed because an import could not
// be resolved before any test outcome was observed. Such a run is an
// environment error (missing dependency or virtual environment), never an
// observed test failure. A pytest run that also passed or failed tests keeps
// its observed failure status.
func environmentFailure(argv []string, data []byte) bool {
	switch {
	case isUnittest(argv):
		if !dependencyError.Match(data) {
			return false
		}
		// Import-loader pseudo-tests are setup errors. Preserve any actual
		// failed test even when another module could not be imported.
		for _, m := range failedCase.FindAllSubmatch(data, -1) {
			if !strings.Contains(string(m[2]), "unittest.loader._FailedTest") {
				return false
			}
		}
		return true
	case uses(argv, "pytest") || uses(argv, "py.test"):
		text := ansi.ReplaceAllString(string(data), "")
		if !missingModule.MatchString(text) {
			return false
		}
		matches := pytestSummary.FindAllStringSubmatch(text, -1)
		if len(matches) == 0 {
			return true
		}
		last := matches[len(matches)-1][1]
		return !strings.Contains(last, "passed") && !strings.Contains(last, "failed")
	}
	return false
}
