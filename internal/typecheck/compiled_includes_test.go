package typecheck

import (
	"context"
	"sort"
	"strings"
	"testing"
)

func cppHeaderTargets(t *testing.T, files tree, changed ...string) ([]string, []Gap) {
	t.Helper()
	p, err := Select(context.Background(), files, changed)
	if err != nil {
		t.Fatal(err)
	}
	var units []string
	for _, target := range p.Targets {
		if target.Tool == "cpp" {
			units = append(units, target.Command[len(target.Command)-2])
		}
	}
	sort.Strings(units)
	return units, p.Gaps
}

func cppDatabase(units ...string) string {
	var entries []string
	for _, u := range units {
		entries = append(entries, `{"directory":".","file":"`+u+`","arguments":["g++","-c","`+u+`"]}`)
	}
	return "[" + strings.Join(entries, ",") + "]"
}

func TestChangedHeaderSelectsOnlyUnitsThatMayIncludeIt(t *testing.T) {
	files := tree{
		"api.hpp":               "int answer();\n",
		"mid.hpp":               "#include \"sub/API.hpp\"\n",
		"direct.cpp":            "#include \"api.hpp\"\n",
		"chain.cpp":             "  #  include <mid.hpp>\n",
		"other.cpp":             "// api.hpp mentioned only in a comment\nint x;\n",
		"compile_commands.json": cppDatabase("direct.cpp", "chain.cpp", "other.cpp"),
	}
	units, gaps := cppHeaderTargets(t, files, "api.hpp")
	if strings.Join(units, ",") != "chain.cpp,direct.cpp" || len(gaps) != 0 {
		t.Fatalf("units=%v gaps=%+v", units, gaps)
	}
}

func TestChangedHeaderFallsBackToEveryUnitWhenIncludesAreUnbounded(t *testing.T) {
	files := tree{
		"api.hpp":               "int answer();\n",
		"a.cpp":                 "#include \"api.hpp\"\n",
		"b.cpp":                 "#define HEADER \"x.hpp\"\n#include HEADER\n",
		"c.cpp":                 "int c;\n",
		"compile_commands.json": cppDatabase("a.cpp", "b.cpp", "c.cpp"),
	}
	units, _ := cppHeaderTargets(t, files, "api.hpp")
	if strings.Join(units, ",") != "a.cpp,b.cpp,c.cpp" {
		t.Fatalf("computed include did not select every unit: %v", units)
	}
}

func TestForcedIncludeSelectsUnit(t *testing.T) {
	files := tree{
		"api.hpp": "int answer();\n",
		"a.cpp":   "int a;\n",
		"b.cpp":   "int b;\n",
		"compile_commands.json": `[{"directory":".","file":"a.cpp","arguments":["g++","-include","api.hpp","-c","a.cpp"]},` +
			`{"directory":".","file":"b.cpp","arguments":["g++","-c","b.cpp"]}]`,
	}
	p, err := Select(context.Background(), files, []string{"api.hpp"})
	if err != nil {
		t.Fatal(err)
	}
	var cpp int
	for _, target := range p.Targets {
		if target.Tool == "cpp" {
			cpp++
			if !strings.Contains(strings.Join(target.Command, " "), "a.cpp") {
				t.Fatalf("selected unit without forced include: %v", target.Command)
			}
		}
	}
	if cpp != 1 {
		t.Fatalf("targets=%+v", p.Targets)
	}
}

func TestDeletedHeaderStillSelectsItsIncluders(t *testing.T) {
	files := tree{
		"a.cpp":                 "#include \"gone.hpp\"\n",
		"b.cpp":                 "int b;\n",
		"compile_commands.json": cppDatabase("a.cpp", "b.cpp"),
	}
	units, _ := cppHeaderTargets(t, files, "gone.hpp")
	if strings.Join(units, ",") != "a.cpp" {
		t.Fatalf("units=%v", units)
	}
}

func TestHeaderNoUnitIncludesIsAGap(t *testing.T) {
	files := tree{
		"unused.hpp":            "int unused();\n",
		"a.cpp":                 "int a;\n",
		"compile_commands.json": cppDatabase("a.cpp"),
	}
	units, gaps := cppHeaderTargets(t, files, "unused.hpp")
	if len(units) != 0 || len(gaps) != 1 || gaps[0].Path != "unused.hpp" || !strings.Contains(gaps[0].Reason, "includes this header") {
		t.Fatalf("units=%v gaps=%+v", units, gaps)
	}
}

func TestChangedSourceAndHeaderTogether(t *testing.T) {
	files := tree{
		"api.hpp":               "int answer();\n",
		"a.cpp":                 "int a;\n",
		"b.cpp":                 "#include \"api.hpp\"\n",
		"compile_commands.json": cppDatabase("a.cpp", "b.cpp"),
	}
	units, gaps := cppHeaderTargets(t, files, "a.cpp", "api.hpp")
	if strings.Join(units, ",") != "a.cpp,b.cpp" || len(gaps) != 0 {
		t.Fatalf("units=%v gaps=%+v", units, gaps)
	}
}

func TestHeaderSelectionFollowsEveryLiteralIncludeForm(t *testing.T) {
	for name, files := range map[string]tree{
		"include through .inc":         {"tables.inc": "#include \"api.hpp\"\n", "via.cpp": "#include \"tables.inc\"\n"},
		"include through .def":         {"x.def": "#include \"api.hpp\"\n", "via.cpp": "#include \"x.def\"\n"},
		"extensionless header":         {"Api": "#include \"api.hpp\"\n", "via.cpp": "#include <Api>\n"},
		"byte order mark":              {"via.cpp": "\xef\xbb\xbf#include \"api.hpp\"\n"},
		"comment before directive":     {"via.cpp": "/* c */ #include \"api.hpp\"\n"},
		"line splice inside directive": {"via.cpp": "#\\\ninclude \"api.hpp\"\n"},
		"CRLF line splice":             {"via.cpp": "#\\\r\ninclude \"api.hpp\"\r\n"},
		"backslash path":               {"via.cpp": "#include \"sub\\api.hpp\"\n"},
		"comment inside directive":     {"via.cpp": "#/* x */include \"api.hpp\"\n"},
		"comment spanning lines":       {"via.cpp": "#/*\n*/include \"api.hpp\"\n"},
		"trigraph":                     {"via.cpp": "??=include \"api.hpp\"\n"},
		"vertical tab":                 {"via.cpp": "#\vinclude \"api.hpp\"\n"},
		"backslash space newline":      {"via.cpp": "#\\ \ninclude \"api.hpp\"\n"},
		"backslash tab inside name":    {"via.cpp": "#inc\\\t\nlude \"api.hpp\"\n"},
		"CR-only line splice":          {"via.cpp": "#\\\rinclude \"api.hpp\"\r"},
		"trigraph backslash":           {"via.cpp": "#??/\ninclude \"api.hpp\"\n"},
		"digraph":                      {"via.cpp": "%:include \"api.hpp\"\n"},
		"uncommitted generated header": {"via.cpp": "#include \"generated.h\"\n"},
	} {
		t.Run(name, func(t *testing.T) {
			files["api.hpp"] = "int answer();\n"
			files["direct.cpp"] = "#include \"api.hpp\"\n"
			files["compile_commands.json"] = cppDatabase("direct.cpp", "via.cpp")
			units, _ := cppHeaderTargets(t, files, "api.hpp")
			if strings.Join(units, ",") != "direct.cpp,via.cpp" {
				t.Fatalf("units=%v", units)
			}
		})
	}
}

func TestMacroContinuationsDoNotDisablePruning(t *testing.T) {
	files := tree{
		"api.hpp":               "int answer();\n",
		"a.cpp":                 "#define LONG(x) \\\n  (x)\n#include \"api.hpp\"\n",
		"b.cpp":                 "#include <vector>\nint b;\n",
		"compile_commands.json": cppDatabase("a.cpp", "b.cpp"),
	}
	units, _ := cppHeaderTargets(t, files, "api.hpp")
	if strings.Join(units, ",") != "a.cpp" {
		t.Fatalf("units=%v", units)
	}
}

func TestCPPIncludeDirs(t *testing.T) {
	got := CPPIncludeDirs([]string{"g++", "-Iinclude", "-I", "third_party/z", "-iquote", ".", "-isystem/usr/include", "-I../up", "a.cpp"}, "native")
	if strings.Join(got, ",") != "native/include,native/third_party/z,native" {
		t.Fatalf("%v", got)
	}
}
