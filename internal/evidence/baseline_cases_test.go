package evidence

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Jake-Network/radar/internal/model"
)

func TestPytestBaselineFailureIdentities(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "test_case.py"), []byte("# tests\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var many strings.Builder
	for i := 0; i < 25; i++ {
		fmt.Fprintf(&many, "FAILED test_case.py::test_%d - AssertionError\n", i)
	}
	for _, tc := range []struct {
		name, output string
		count        int
	}{
		{"failure and error", "FAILED test_case.py::test_old - AssertionError: raw secret\nERROR test_case.py::TestCase::test_new - missing fixture\n", 2},
		{"collection error is not a case identity", "ERROR test_case.py - ValueError: existing collection failure\n", 0},
		{"new collection cause is still unidentified", "ERROR test_case.py - ImportError: new missing name\n", 0},
		{"escape", "FAILED ../test_case.py::test_bad - AssertionError\n", 0},
		{"untracked location", "FAILED missing.py::test_bad - AssertionError\n", 0},
		{"parameter values", "FAILED test_case.py::test_bad[private-value] - AssertionError\n", 0},
		{"duplicates cannot fill identity gaps", strings.Repeat("FAILED test_case.py::test_old - AssertionError\n", 25), 1},
		{"bound", many.String(), 20},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := CandidateObservation{Status: model.StatusFailed, TestsFailed: 2, Harness: "pytest"}
			decorateCandidateObservation(&o, []string{"python3", "-m", "pytest"}, []byte(tc.output), root, root, ".")
			if len(o.FailedCases) != tc.count {
				t.Fatalf("%+v", o)
			}
			for _, name := range o.FailedCases {
				if strings.Contains(name, "secret") || strings.Contains(name, "AssertionError") {
					t.Fatal(name)
				}
			}
		})
	}
}

func TestDuplicateUnittestFailureIdentities(t *testing.T) {
	o := CandidateObservation{Status: model.StatusFailed, TestsFailed: 2, Harness: "unittest"}
	decorateCandidateObservation(&o, []string{"python3", "-m", "unittest"},
		[]byte(strings.Repeat("FAIL: test_old (test_case.Case)\n", 25)), t.TempDir(), t.TempDir(), ".")
	if len(o.FailedCases) != 1 {
		t.Fatalf("repeated names disguised missing failure identities: %+v", o)
	}
}

func TestUnittestTerminalNormalizationPreservesFailureIdentity(t *testing.T) {
	argv := []string{"python3", "-m", "unittest"}
	for _, ending := range []string{"\n", "\r\n"} {
		for _, colored := range []bool{false, true} {
			data := "FAIL: test_old (test_case.Case)\nRan 1 test in 0.01s\nFAILED (failures=1)\n"
			if colored {
				data = "\x1b[31m" + strings.ReplaceAll(data, "\n", "\x1b[0m\n\x1b[31m") + "\x1b[0m"
			}
			data = strings.ReplaceAll(data, "\n", ending)
			count := harnessCounts(argv, []byte(data), "")
			observed := CandidateObservation{Status: model.StatusFailed, TestsRun: count.Run, TestsFailed: count.Failed}
			decorateCandidateObservation(&observed, argv, []byte(data), t.TempDir(), t.TempDir(), ".")
			if count.Run != 1 || count.Failed != 1 || len(observed.FailedCases) != 1 || observed.FailedCases[0] != "test_case.Case.test_old" {
				t.Fatalf("ending=%q colored=%v count=%+v observation=%+v", ending, colored, count, observed)
			}
			// A loader error mixed with a real failure is not purely an environment error.
			mixed := data + strings.ReplaceAll("ERROR: test_missing (unittest.loader._FailedTest.test_missing)\nModuleNotFoundError: No module named 'missing'\n", "\n", ending)
			if environmentFailure(argv, []byte(mixed)) {
				t.Fatal("real failing test hidden by loader error")
			}
		}
	}
}
