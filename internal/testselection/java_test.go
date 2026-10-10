package testselection

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Jake-Network/radar/internal/indexer"
	"github.com/Jake-Network/radar/internal/model"
)

func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for p, content := range files {
		full := filepath.Join(root, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

const junitTest = "import org.junit.jupiter.api.Test;\nclass X { @Test void ok() {} }\n"

func javaTest(pkg, imports string) string {
	return "package " + pkg + ";\n" + imports + junitTest
}

func mavenRepo(t *testing.T) string {
	return writeTree(t, map[string]string{
		"pom.xml": `<project><modules>
  <module>core</module>
  <module>./api/</module>
  <!-- <module>old</module> -->
</modules>
<profiles><profile><modules><module>bench</module></modules></profile></profiles></project>`,
		"mvnw":         "#!/bin/sh\n",
		"core/pom.xml": "<project/>",
		"core/src/main/java/com/shop/Inventory.java":     "package com.shop;\npublic class Inventory {}\n",
		"core/src/main/java/com/shop/Price.java":         "package com.shop;\npublic class Price {}\n",
		"core/src/test/java/com/shop/InventoryTest.java": javaTest("com.shop", ""),
		"core/src/test/java/com/shop/PriceTest.java":     javaTest("com.shop.pricing", "import com.shop.Price;\n"),
		"core/src/test/java/com/shop/Fixtures.java":      "package com.shop;\nclass Fixtures {}\n",
		"core/src/test/java/com/shop/Helper.java":        "package com.shop;\n// mentions @Test in prose only through a marker-free helper\nclass Helper {}\n",
		"api/pom.xml": "<project/>",
		"api/src/main/java/com/shop/api/Handler.java":     "package com.shop.api;\nimport com.shop.Inventory;\nclass Handler {}\n",
		"api/src/test/java/com/shop/api/HandlerTest.java": javaTest("com.shop.api", "import com.shop.api.Handler;\n"),
		"bench/pom.xml":                      "<project/>",
		"bench/src/test/java/BenchTest.java": junitTest,
	})
}

func selectJava(t *testing.T, root string, changed ...string) (Inventory, Proposal) {
	t.Helper()
	inv, err := Discover(context.Background(), root, "")
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := indexer.Index(context.Background(), root, "WORKTREE")
	if err != nil {
		t.Fatal(err)
	}
	p, err := Select(inv, snapshot, changed, Options{ToolAvailable: func(string) bool { return true }})
	if err != nil {
		t.Fatal(err)
	}
	return inv, p
}

func selectedFor(p Proposal, file string) *Command {
	for i, c := range p.Commands {
		if slices.Contains(c.TestFiles, file) {
			return &p.Commands[i]
		}
	}
	return nil
}

func TestMavenInventoryAndReactorCommands(t *testing.T) {
	root := mavenRepo(t)
	inv, p := selectJava(t, root, "core/src/main/java/com/shop/Inventory.java")
	byPath := map[string]Test{}
	for _, test := range inv.Tests {
		byPath[test.Path] = test
	}
	for _, helper := range []string{"core/src/test/java/com/shop/Fixtures.java", "core/src/test/java/com/shop/Helper.java"} {
		if _, ok := byPath[helper]; ok {
			t.Errorf("helper without a test marker inventoried: %s", helper)
		}
	}
	if test := byPath["core/src/test/java/com/shop/InventoryTest.java"]; test.Framework != "maven" || test.PackageRoot != "core" {
		t.Fatalf("maven test %+v", test)
	}
	if !slices.Contains(inv.Manifests, "mvnw") || !slices.Contains(inv.Manifests, "core/pom.xml") {
		t.Fatalf("manifests %v", inv.Manifests)
	}

	companion := selectedFor(p, "core/src/test/java/com/shop/InventoryTest.java")
	if companion == nil || companion.EvidenceReasons[0].Code != "java_package_companion" || companion.Priority != 70 {
		t.Fatalf("same-package test not selected: %+v", companion)
	}
	want := []string{"./mvnw", "-o", "-B", "-pl", "core", "-am", "-Dsurefire.failIfNoSpecifiedTests=false", "-DfailIfNoTests=false", "-Dtest=com.shop.InventoryTest", "test"}
	if !slices.Equal(companion.Command, want) || companion.CWD != "." || companion.JUnit != "core/target/surefire-reports" || !companion.ToolAvailable {
		t.Fatalf("companion command %+v", companion)
	}
	api := selectedFor(p, "api/src/test/java/com/shop/api/HandlerTest.java")
	if api == nil || api.EvidenceReasons[0].Code != "dependency_impact" || !slices.Contains(api.Command, "api") || api.JUnit != "api/target/surefire-reports" {
		t.Fatalf("cross-module dependency not selected: %+v", api)
	}
	// A different package in the same module is only a package fallback.
	if fallback := selectedFor(p, "core/src/test/java/com/shop/PriceTest.java"); fallback == nil || fallback.EvidenceReasons[0].Code != "package_fallback" {
		t.Fatalf("other-package test %+v", fallback)
	}

	// Profile-only modules are not in the default reactor; they run alone.
	_, p = selectJava(t, root, "bench/src/test/java/BenchTest.java")
	if bench := selectedFor(p, "bench/src/test/java/BenchTest.java"); bench == nil || bench.CWD != "bench" || slices.Contains(bench.Command, "-pl") || bench.Command[0] != "mvn" || bench.JUnit != "target/surefire-reports" {
		t.Fatalf("standalone module %+v", bench)
	}
}

func TestMavenClassesGroupPerModule(t *testing.T) {
	root := mavenRepo(t)
	_, p := selectJava(t, root, "core/src/main/java/com/shop/Inventory.java", "core/src/main/java/com/shop/Price.java")
	s, err := Plan(p, []string{"core/src/main/java/com/shop/Inventory.java", "core/src/main/java/com/shop/Price.java"}, ModeTargeted, 0)
	if err != nil {
		t.Fatal(err)
	}
	var core []Command
	for _, c := range s.Commands {
		if c.JUnit == "core/target/surefire-reports" {
			core = append(core, c)
		}
	}
	if len(core) != 1 || !slices.Contains(core[0].Command, "-Dtest=com.shop.InventoryTest,com.shop.pricing.PriceTest") || core[0].Command[len(core[0].Command)-1] != "test" || len(core[0].GroupedFrom) != 2 {
		t.Fatalf("core commands %+v", core)
	}
	full, err := Plan(p, nil, ModeFull, 0)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, c := range full.Commands {
		if c.Framework == "maven" && c.JUnit == "core/target/surefire-reports" {
			found = slices.Equal(c.Command, []string{"./mvnw", "-o", "-B", "-pl", "core", "-am", "test"})
		}
	}
	if !found {
		t.Fatalf("full suite %+v", full.Commands)
	}
}

func TestGradleLiteralProjects(t *testing.T) {
	root := writeTree(t, map[string]string{
		"settings.gradle":   "rootProject.name = 'shop' // include 'ignored'\ninclude 'core',\n    ':services:api'\n",
		"gradlew":           "#!/bin/sh\n",
		"build.gradle":      "",
		"core/build.gradle": "",
		"core/src/main/java/com/shop/Inventory.java":          "package com.shop;\npublic class Inventory {}\n",
		"core/src/test/java/com/shop/InventoryTest.java":      javaTest("com.shop", ""),
		"services/api/src/test/java/com/shop/ApiTest.java":    javaTest("com.shop.api", "import com.shop.Inventory;\n"),
		"src/test/java/com/shop/RootTest.java":                javaTest("com.shop.root", ""),
		"core/src/integrationTest/java/com/shop/StoreIT.java": javaTest("com.shop", ""),
		"tools/gen/build.gradle":                              "",
		"tools/gen/src/test/java/GenTest.java":                junitTest,
	})
	inv, p := selectJava(t, root, "core/src/main/java/com/shop/Inventory.java")
	core := selectedFor(p, "core/src/test/java/com/shop/InventoryTest.java")
	if core == nil || !slices.Equal(core.Command, []string{"./gradlew", "--offline", "--no-daemon", "--console=plain", ":core:test", "--tests", "com.shop.InventoryTest"}) || core.JUnit != "core/build/test-results/test" || core.CWD != "." {
		t.Fatalf("gradle core %+v", core)
	}
	// services/api has no build file of its own; settings still name it.
	api := selectedFor(p, "services/api/src/test/java/com/shop/ApiTest.java")
	if api == nil || !slices.Contains(api.Command, ":services:api:test") || api.JUnit != "services/api/build/test-results/test" {
		t.Fatalf("gradle nested project %+v", api)
	}
	for _, test := range inv.Tests {
		switch test.Path {
		case "src/test/java/com/shop/RootTest.java":
			if test.java == nil || !slices.Equal(test.java.module, []string{":test"}) || test.PackageRoot != "." {
				t.Fatalf("root project test %+v", test.java)
			}
		case "core/src/integrationTest/java/com/shop/StoreIT.java":
			if test.java != nil || test.PackageRoot != "core" || !strings.Contains(test.javaReason, "outside core/src/test/java") {
				t.Fatalf("custom source set accepted: %+v %q", test.java, test.javaReason)
			}
		case "tools/gen/src/test/java/GenTest.java":
			if test.java != nil || !strings.Contains(test.javaReason, "not a literally included project") {
				t.Fatalf("unincluded build accepted: %+v %q", test.java, test.javaReason)
			}
		}
	}
	_, p = selectJava(t, root, "tools/gen/src/test/java/GenTest.java")
	if len(p.Omitted) != 1 || p.Omitted[0].Reason != "unsupported_configuration" || p.Omitted[0].Tier != TierRequired {
		t.Fatalf("unincluded build test not omitted: %+v", p.Omitted)
	}
}

func TestGradleSettingsParsing(t *testing.T) {
	cases := []struct {
		settings  string
		projects  map[string]string
		uncertain bool
	}{
		{`include(":a", "b:c")`, map[string]string{"a": ":a", "b/c": ":b:c"}, false},
		{"include 'a'\ninclude \"x\"", map[string]string{"a": ":a", "x": ":x"}, false},
		{"/* include 'gone' */\ninclude 'kept'", map[string]string{"kept": ":kept"}, false},
		{"includeBuild '../plugins'\ninclude 'app'", map[string]string{"app": ":app"}, false},
		{`include(modules)`, map[string]string{}, true},
		{`include ":app-${flavor}"`, map[string]string{}, true},
		{"include 'app'\nproject(':app').projectDir = file('src/app')", map[string]string{"app": ":app"}, true},
		{"includeFlat 'sibling'", map[string]string{}, true},
	}
	for _, c := range cases {
		got := parseGradleSettings(c.settings)
		if got.uncertain != c.uncertain || len(got.projects) != len(c.projects) {
			t.Errorf("%q: %+v", c.settings, got)
			continue
		}
		for dir, project := range c.projects {
			if got.projects[dir] != project {
				t.Errorf("%q: %s -> %q", c.settings, dir, got.projects[dir])
			}
		}
	}
	root := writeTree(t, map[string]string{
		"settings.gradle.kts":              "listOf(\"a\", \"b\").forEach { include(it) }\n",
		"a/src/test/java/ATest.java":       junitTest,
		"both/pom.xml":                     "<project/>",
		"both/build.gradle":                "",
		"both/src/test/java/BothTest.java": junitTest,
	})
	inv, err := Discover(context.Background(), root, "")
	if err != nil {
		t.Fatal(err)
	}
	reasons := map[string]string{}
	for _, test := range inv.Tests {
		reasons[test.Path] = test.Framework + ": " + test.javaReason
	}
	if !strings.Contains(reasons["a/src/test/java/ATest.java"], "gradle: Gradle settings in . compute or relocate projects") {
		t.Errorf("computed settings: %q", reasons["a/src/test/java/ATest.java"])
	}
	if !strings.Contains(reasons["both/src/test/java/BothTest.java"], "java-unknown: both pom.xml and a Gradle build file") {
		t.Errorf("ambiguous build: %q", reasons["both/src/test/java/BothTest.java"])
	}
}

func TestJavaWithoutBuildFile(t *testing.T) {
	root := writeTree(t, map[string]string{"src/test/java/LoneTest.java": junitTest})
	_, p := selectJava(t, root, "src/test/java/LoneTest.java")
	if len(p.Commands) != 0 || len(p.Omitted) != 1 || !strings.Contains(p.Omitted[0].Explanation, "no pom.xml or Gradle build file") {
		t.Fatalf("buildless java test %+v %+v", p.Commands, p.Omitted)
	}
	// Without a JVM nothing is claimed runnable.
	inv, _ := Discover(context.Background(), mavenRepo(t), "")
	p, err := Select(inv, model.Snapshot{}, []string{"core/src/test/java/com/shop/InventoryTest.java"}, Options{ToolAvailable: func(tool string) bool { return tool != "java" }})
	if err != nil || len(p.Commands) != 1 || p.Commands[0].ToolAvailable {
		t.Fatalf("availability without java %+v %v", p.Commands, err)
	}
}

// A test selected through an import still relates its changed same-package
// sources, so they are not reported as uncovered.
func TestJavaImportAndCompanionReasons(t *testing.T) {
	root := writeTree(t, map[string]string{
		"pom.xml":                                  "<project/>",
		"src/main/java/com/shop/model/Item.java":   "package com.shop.model;\npublic class Item {}\n",
		"src/main/java/com/shop/Checkout.java":     "package com.shop;\npublic class Checkout {}\n",
		"src/test/java/com/shop/CheckoutTest.java": javaTest("com.shop", "import com.shop.model.Item;\n"),
	})
	changed := []string{"src/main/java/com/shop/model/Item.java", "src/main/java/com/shop/Checkout.java"}
	_, p := selectJava(t, root, changed...)
	c := selectedFor(p, "src/test/java/com/shop/CheckoutTest.java")
	if c == nil || len(c.EvidenceReasons) != 2 || c.EvidenceReasons[0].Code != "dependency_impact" || c.EvidenceReasons[1].Code != "java_package_companion" || !slices.Equal(c.EvidenceReasons[1].RelatedFiles, []string{"src/main/java/com/shop/Checkout.java"}) {
		t.Fatalf("reasons %+v", c)
	}
	if u := uncovered(p, changed); len(u) != 0 {
		t.Fatalf("uncovered %v", u)
	}
}

func TestJavaPackageIgnoresComments(t *testing.T) {
	source := "/*\n * Moved from\npackage com.old;\n */\n// package com.older;\npackage com.shop;\nclass A {}\n"
	if got := javaPackage(source); got != "com.shop" {
		t.Fatalf("package %q", got)
	}
	if got := javaClass("src/test/java/com/shop/ATest.java", source); got != "com.shop.ATest" {
		t.Fatalf("class %q", got)
	}
}
