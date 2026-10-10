package testselection

import (
	"slices"
	"strings"
	"testing"
)

func TestCMakeSelectionThroughHeaderPairing(t *testing.T) {
	root := writeTree(t, map[string]string{
		"CMakeLists.txt":           "cmake_minimum_required(VERSION 3.21)\nproject(shop CXX)\nenable_testing()\nadd_subdirectory(tests)\n",
		"tests/CMakeLists.txt":     "add_executable(inventory_test inventory_test.cpp ../src/inventory.cpp)\nadd_test(NAME inventory COMMAND inventory_test)\n",
		"include/shop/inventory.h": "#pragma once\nnamespace shop { class Inventory { public: int reserve(int); }; }\n",
		"src/inventory.cpp":        "#include \"shop/inventory.h\"\nint shop::Inventory::reserve(int n) { return n; }\n",
		"src/report.cpp":           "int report() { return 0; }\n",
		"tests/inventory_test.cpp": "#include <shop/inventory.h>\nint main() { return shop::Inventory().reserve(1) == 1 ? 0 : 1; }\n",
		"tests/helpers.cpp":        "// int main() would make this a test\nint helper() { return 0; }\n",
		"tests/util_test.cpp":      "#include <gtest/gtest.h>\nTEST(Util, Works) { EXPECT_TRUE(true); }\n",
	})
	inv, p := selectJava(t, root, "src/inventory.cpp")
	frameworks := map[string]string{}
	for _, test := range inv.Tests {
		frameworks[test.Path] = test.Framework
	}
	if frameworks["tests/inventory_test.cpp"] != "ctest" || frameworks["tests/util_test.cpp"] != "ctest" {
		t.Fatalf("inventory %v", frameworks)
	}
	if _, ok := frameworks["tests/helpers.cpp"]; ok {
		t.Fatal("helper without main or a test macro inventoried")
	}
	c := selectedFor(p, "tests/inventory_test.cpp")
	if c == nil || c.EvidenceReasons[0].Code != "dependency_impact" || c.CWD != "." || c.JUnit != "build/radar-ctest/radar-ctest.xml" || !c.ToolAvailable {
		t.Fatalf("header pairing did not select the test: %+v", c)
	}
	if !slices.Equal(c.Command[:4], []string{"ctest", "--build-and-test", ".", "build/radar-ctest"}) || !slices.Contains(c.Command, "-DFETCHCONTENT_FULLY_DISCONNECTED=ON") {
		t.Fatalf("ctest command %v", c.Command)
	}
	// Both tests share the project's one ctest invocation.
	if !slices.Contains(c.TestFiles, "tests/util_test.cpp") {
		if fallback := selectedFor(p, "tests/util_test.cpp"); fallback == nil || fallback.ID != c.ID {
			t.Fatalf("project tests not merged into one command: %+v", p.Commands)
		}
	}
	s, err := Plan(p, []string{"src/inventory.cpp"}, ModeTargeted, 0)
	if err != nil || len(s.Commands) != 1 || len(s.Blocking) != 0 || len(s.Uncovered) != 0 {
		t.Fatalf("targeted selection %+v %v", s, err)
	}
	full, err := Plan(p, nil, ModeFull, 0)
	if err != nil || len(full.Commands) != 1 || full.Commands[0].JUnit != ctestReport || len(full.Commands[0].TestFiles) != 2 {
		t.Fatalf("full selection %+v %v", full.Commands, err)
	}
	if _, p = selectJava(t, root, "src/report.cpp"); selectedFor(p, "tests/inventory_test.cpp").EvidenceReasons[0].Code != "package_fallback" {
		t.Fatalf("unrelated source not a fallback: %+v", p.Commands)
	}
}

func TestCTestUnsupportedProjects(t *testing.T) {
	cases := map[string]map[string]string{
		"does not call enable_testing()": {
			"CMakeLists.txt":    "project(x C) # enable_testing() is commented out\n",
			"tests/test_list.c": "int main(void) { return 0; }\n",
		},
		"no CMakeLists.txt encloses": {
			"Makefile":          "test:\n\tcc tests/test_list.c\n",
			"tests/test_list.c": "int main(void) { return 0; }\n",
		},
	}
	for want, files := range cases {
		root := writeTree(t, files)
		inv, p := selectJava(t, root, "tests/test_list.c")
		if len(inv.Tests) != 1 || inv.Tests[0].Framework != "c-unknown" || inv.Tests[0].ctest != nil {
			t.Fatalf("%s: inventory %+v", want, inv.Tests)
		}
		if len(p.Commands) != 0 || len(p.Omitted) != 1 || p.Omitted[0].Tier != TierRequired || !strings.Contains(p.Omitted[0].Explanation, want) {
			t.Fatalf("%s: %+v %+v", want, p.Commands, p.Omitted)
		}
	}
}
