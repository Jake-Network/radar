package testselection

import (
	"path"
	"regexp"
	"strings"
)

// ctestBuildDir is where the private candidate is configured and built. It
// sits under build/, which candidate source inspection treats as generated.
const ctestBuildDir = "build/radar-ctest"

// ctestReport is the JUnit report the inner ctest writes, relative to the
// CMake root (CTest resolves --output-junit against the build directory).
const ctestReport = ctestBuildDir + "/radar-ctest.xml"

var (
	cppTestMarker   = regexp.MustCompile(`\b(?:TEST|TEST_F|TEST_P|TYPED_TEST|TEST_CASE|SCENARIO|BOOST_AUTO_TEST_CASE)\s*\(`)
	cMain           = regexp.MustCompile(`\bmain\s*\(`)
	cmakeTesting    = regexp.MustCompile(`(?i)\benable_testing\s*\(|\binclude\s*\(\s*CTest\s*\)`)
	cTestDirs       = map[string]bool{"test": true, "tests": true, "unittest": true, "unittests": true, "unit_tests": true}
	cTestSources    = map[string]bool{".c": true, ".cc": true, ".cpp": true, ".cxx": true, ".c++": true}
	cTestNameSuffix = []string{"_test", "_tests", "_unittest", "Test", "Tests"}
)

// cTestFile recognizes C/C++ test sources by directory or file name and a
// framework macro or a main function outside comments.
func cTestFile(file, content string) bool {
	if !cTestSources[strings.ToLower(path.Ext(file))] {
		return false
	}
	stem := strings.TrimSuffix(path.Base(file), path.Ext(file))
	conventional := strings.HasPrefix(stem, "test_")
	for _, suffix := range cTestNameSuffix {
		conventional = conventional || strings.HasSuffix(stem, suffix)
	}
	for _, dir := range strings.Split(path.Dir(file), "/") {
		conventional = conventional || cTestDirs[dir]
	}
	text := withoutComments(content)
	return conventional && (cppTestMarker.MatchString(text) || cMain.MatchString(text))
}

// cmakeRun returns the CMake project that builds a C/C++ test and the
// command that configures, builds and tests it in one invocation:
//
//	ctest --build-and-test . build/radar-ctest --build-generator "Unix Makefiles"
//	  --build-options -DFETCHCONTENT_FULLY_DISCONNECTED=ON
//	  --test-command ctest --output-on-failure --output-junit radar-ctest.xml
//
// The project is the outermost directory with a CMakeLists.txt; CTest finds
// tests only when that top-level file enables testing. FetchContent is kept
// offline; Radar does not download dependencies.
func cmakeRun(file string, contents map[string]string) (string, []string, string) {
	root := ""
	for dir := path.Dir(file); ; dir = path.Dir(dir) {
		if _, ok := contents[path.Join(dir, "CMakeLists.txt")]; ok {
			root = dir
		}
		if dir == "." {
			break
		}
	}
	if root == "" {
		return ".", nil, "no CMakeLists.txt encloses this C/C++ test; declare a reviewed plan test_run rule whose setup builds it"
	}
	if !cmakeTesting.MatchString(withoutHashComments(contents[path.Join(root, "CMakeLists.txt")])) {
		return root, nil, "the top-level " + path.Join(root, "CMakeLists.txt") + " does not call enable_testing() or include(CTest), so ctest finds no tests"
	}
	return root, []string{"ctest", "--build-and-test", ".", ctestBuildDir, "--build-generator", "Unix Makefiles", "--build-options", "-DFETCHCONTENT_FULLY_DISCONNECTED=ON", "--test-command", "ctest", "--output-on-failure", "--output-junit", "radar-ctest.xml"}, ""
}

var hashComment = regexp.MustCompile(`(?m)#.*$`)

func withoutHashComments(content string) string { return hashComment.ReplaceAllString(content, "") }

// ctestToolAvailable needs ctest and the Unix Makefiles build program; the
// compiler itself is found by CMake during configuration.
func ctestToolAvailable(available func(string) bool) bool {
	return available("ctest") && available("cmake") && available("make")
}
