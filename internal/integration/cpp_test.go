package integration

import (
	"context"
	"os/exec"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Jake-Network/radar/internal/gate"
	"github.com/Jake-Network/radar/internal/model"
)

const cppInventory = "#pragma once\nnamespace shop {\nclass Inventory {\n public:\n  int reserve(int n);\n};\n}\n"

// cppFixture is a CMake project whose library globs its sources: one branch
// renames Inventory::reserve to hold, another adds a restock module (with
// its own header and test) that calls reserve.
func cppFixture(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("Unix Makefiles generator")
	}
	for _, tool := range []string{"cmake", "ctest", "make", "c++"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s required: %v", tool, err)
		}
	}
	root := t.TempDir()
	gitTest(t, root, "init", "-q")
	put(t, root, ".gitignore", "build/\n")
	put(t, root, "CMakeLists.txt", "cmake_minimum_required(VERSION 3.16)\nproject(shop CXX)\nenable_testing()\nfile(GLOB SHOP_SOURCES CONFIGURE_DEPENDS src/*.cpp)\nadd_library(shop STATIC ${SHOP_SOURCES})\ntarget_include_directories(shop PUBLIC include)\nfile(GLOB SHOP_TESTS CONFIGURE_DEPENDS tests/*_test.cpp)\nforeach(test ${SHOP_TESTS})\n  get_filename_component(name ${test} NAME_WE)\n  add_executable(${name} ${test})\n  target_link_libraries(${name} shop)\n  add_test(NAME ${name} COMMAND ${name})\nendforeach()\n")
	put(t, root, "include/shop/inventory.h", cppInventory)
	put(t, root, "src/inventory.cpp", "#include \"shop/inventory.h\"\nint shop::Inventory::reserve(int n) { return n; }\n")
	put(t, root, "tests/inventory_test.cpp", "#include <shop/inventory.h>\nint main() { return shop::Inventory().reserve(1) == 1 ? 0 : 1; }\n")
	gitTest(t, root, "add", ".")
	gitTest(t, root, "commit", "-qm", "baseline")
	gitTest(t, root, "branch", "baseline")
	gitTest(t, root, "checkout", "-qb", "rename")
	put(t, root, "include/shop/inventory.h", strings.Replace(cppInventory, "reserve", "hold", 1))
	put(t, root, "src/inventory.cpp", "#include \"shop/inventory.h\"\nint shop::Inventory::hold(int n) { return n; }\n")
	put(t, root, "tests/inventory_test.cpp", "#include <shop/inventory.h>\nint main() { return shop::Inventory().hold(1) == 1 ? 0 : 1; }\n")
	gitTest(t, root, "commit", "-qam", "rename reserve to hold")
	gitTest(t, root, "checkout", "-qb", "restock", "baseline")
	put(t, root, "include/shop/restock.h", "#pragma once\n#include \"shop/inventory.h\"\nnamespace shop {\nint drain(Inventory& i);\n}\n")
	put(t, root, "src/restock.cpp", "#include \"shop/restock.h\"\nint shop::drain(Inventory& i) {\n  return i.reserve(9);\n}\n")
	put(t, root, "tests/restock_test.cpp", "#include <shop/restock.h>\nint main() {\n  shop::Inventory i;\n  return shop::drain(i) == 9 ? 0 : 1;\n}\n")
	gitTest(t, root, "add", ".")
	gitTest(t, root, "commit", "-qm", "add restock")
	gitTest(t, root, "checkout", "-q", "baseline")
	return root
}

func TestCppCombinationDoesNotCompile(t *testing.T) {
	root := cppFixture(t)
	o := Options{Base: "baseline", Verify: true, AllowExecution: true, Suite: "recommended", Timeout: 2 * time.Minute}
	for _, branch := range []string{"rename", "restock"} {
		o.Branches = []string{branch}
		r, err := Preview(context.Background(), root, o)
		if err != nil {
			t.Fatal(err)
		}
		if r.Gate.Verdict != gate.Pass || len(r.Executions) != 1 {
			t.Fatalf("%s alone: %+v %+v uncovered %v", branch, r.Gate, r.Executions, r.Selection.Uncovered)
		}
		ev := r.Executions[0]
		if ev.Status != model.StatusPassed || ev.Observation.Harness != "junit" || ev.Observation.TestsRun == 0 || ev.SourceAfterExecution != "unchanged" || ev.CWD != "." {
			t.Fatalf("%s alone execution %+v", branch, ev)
		}
		if !slices.Equal(ev.Command[:2], []string{"ctest", "--build-and-test"}) {
			t.Fatalf("%s alone command %v", branch, ev.Command)
		}
	}
	o.Branches = []string{"rename", "restock"}
	r, err := Preview(context.Background(), root, o)
	if err != nil {
		t.Fatal(err)
	}
	if r.Gate.Verdict != gate.Fail || len(r.Executions) != 1 || r.Executions[0].Status != model.StatusFailed {
		t.Fatalf("combined gate %+v %+v", r.Gate, r.Executions)
	}
	obs := r.Executions[0].Observation
	if d := obs.Diagnosis; d == nil || d.Kind != "build_failed" || d.Name != "reserve" || !strings.Contains(d.Message, "src/restock.cpp:3") {
		t.Fatalf("diagnosis %+v", d)
	}
	if len(obs.Locations) != 1 || obs.Locations[0].Path != "src/restock.cpp" {
		t.Fatalf("locations %+v", obs.Locations)
	}
}
