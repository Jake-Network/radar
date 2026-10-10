package integration

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Jake-Network/radar/internal/gate"
	"github.com/Jake-Network/radar/internal/model"
)

// fakeMaven stands in for Maven so the gate's Java path runs without a JDK:
// it "compiles" by checking whether a caller of reserve() survives the
// rename, prints javac's absolute-path errors the way Maven does, and
// otherwise writes the Surefire report of the -pl module.
const fakeMaven = `#!/bin/sh
module=.
while [ $# -gt 0 ]; do
  case "$1" in -pl) module=$2; shift ;; esac
  shift
done
# -pl core -am builds only core; app compiles only when it is selected.
if [ "$module" = app ] && ! grep -q 'int reserve' core/src/main/java/com/shop/Inventory.java; then
  for f in app/src/main/java/com/shop/app/*.java; do
    if grep -q 'reserve(' "$f"; then
      echo "[ERROR] COMPILATION ERROR : "
      echo "[ERROR] $PWD/$f:[5,12] cannot find symbol"
      echo "  symbol:   method reserve(int)"
      echo "[ERROR] Failed to execute goal org.apache.maven.plugins:maven-compiler-plugin:3.13.0:compile on project app: Compilation failure"
      exit 1
    fi
  done
fi
mkdir -p "$module/target/surefire-reports"
echo '<testsuite><testcase name="ok"/></testsuite>' > "$module/target/surefire-reports/TEST-fake.xml"
`

func javaFixture(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell fake runner")
	}
	bin := t.TempDir()
	for name, script := range map[string]string{"mvn": fakeMaven, "java": "#!/bin/sh\nexit 0\n"} {
		if e := os.WriteFile(filepath.Join(bin, name), []byte(script), 0o755); e != nil {
			t.Fatal(e)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	root := t.TempDir()
	gitTest(t, root, "init", "-q")
	put(t, root, ".gitignore", "target/\n")
	put(t, root, "pom.xml", "<project><modules><module>core</module><module>app</module></modules></project>\n")
	put(t, root, "core/pom.xml", "<project/>\n")
	put(t, root, "app/pom.xml", "<project/>\n")
	put(t, root, "core/src/main/java/com/shop/Inventory.java", "package com.shop;\npublic class Inventory {\n  public int reserve(int n) { return n; }\n}\n")
	put(t, root, "core/src/test/java/com/shop/InventoryTest.java", "package com.shop;\nimport org.junit.jupiter.api.Test;\nclass InventoryTest { @Test void ok() { new Inventory().reserve(1); } }\n")
	put(t, root, "app/src/main/java/com/shop/app/Checkout.java", "package com.shop.app;\nimport com.shop.Inventory;\npublic class Checkout {\n  static int buy(Inventory i) { return i.reserve(1); }\n}\n")
	put(t, root, "app/src/test/java/com/shop/app/CheckoutTest.java", "package com.shop.app;\nimport org.junit.jupiter.api.Test;\nclass CheckoutTest { @Test void ok() {} }\n")
	gitTest(t, root, "add", ".")
	gitTest(t, root, "commit", "-qm", "baseline")
	gitTest(t, root, "branch", "baseline")
	gitTest(t, root, "checkout", "-qb", "rename")
	put(t, root, "core/src/main/java/com/shop/Inventory.java", "package com.shop;\npublic class Inventory {\n  public int hold(int n) { return n; }\n}\n")
	put(t, root, "app/src/main/java/com/shop/app/Checkout.java", "package com.shop.app;\nimport com.shop.Inventory;\npublic class Checkout {\n  static int buy(Inventory i) { return i.hold(1); }\n}\n")
	put(t, root, "core/src/test/java/com/shop/InventoryTest.java", "package com.shop;\nimport org.junit.jupiter.api.Test;\nclass InventoryTest { @Test void ok() { new Inventory().hold(1); } }\n")
	gitTest(t, root, "commit", "-qam", "rename reserve to hold")
	gitTest(t, root, "checkout", "-qb", "restock", "baseline")
	put(t, root, "app/src/main/java/com/shop/app/Restock.java", "package com.shop.app;\nimport com.shop.Inventory;\npublic class Restock {\n  static int drain(Inventory i) {\n    return i.reserve(9);\n  }\n}\n")
	gitTest(t, root, "add", ".")
	gitTest(t, root, "commit", "-qm", "add restock")
	gitTest(t, root, "checkout", "-q", "baseline")
	return root
}

func TestJavaCombinationDoesNotCompile(t *testing.T) {
	root := javaFixture(t)
	o := Options{Base: "baseline", Verify: true, AllowExecution: true, Suite: "recommended", Timeout: time.Minute, Policy: &gate.Policy{Version: 1, Require: []string{"textual_merge", "integration_execution"}}}
	for _, branch := range []string{"rename", "restock"} {
		o.Branches = []string{branch}
		r, err := Preview(context.Background(), root, o)
		if err != nil {
			t.Fatal(err)
		}
		if r.Gate.Verdict != gate.Pass || len(r.Executions) == 0 {
			t.Fatalf("%s alone: %+v %+v", branch, r.Gate, r.Executions)
		}
		for _, ev := range r.Executions {
			if ev.Status != model.StatusPassed || ev.Observation.Harness != "junit" || ev.Observation.TestsRun != 1 || ev.SourceAfterExecution != "unchanged" {
				t.Fatalf("%s alone execution %+v", branch, ev)
			}
			if ev.CWD != "." || !slices.Contains(ev.Command, "-pl") || !slices.Contains(ev.Command, "-am") {
				t.Fatalf("%s alone did not run from the reactor root: %+v", branch, ev.Command)
			}
		}
	}
	o.Branches = []string{"rename", "restock"}
	r, err := Preview(context.Background(), root, o)
	if err != nil {
		t.Fatal(err)
	}
	if r.Gate.Verdict != gate.Fail {
		t.Fatalf("combined gate %+v", r.Gate)
	}
	var failed []ExecutionEvidence
	for _, ev := range r.Executions {
		if ev.Status == model.StatusFailed {
			failed = append(failed, ev)
		}
	}
	if len(failed) != 1 || !slices.Contains(failed[0].Command, "app") {
		t.Fatalf("failed executions %+v", r.Executions)
	}
	d := failed[0].Observation.Diagnosis
	if d == nil || d.Kind != "build_failed" || d.Name != "reserve" || !strings.Contains(d.Message, "app/src/main/java/com/shop/app/Restock.java:5") {
		t.Fatalf("diagnosis %+v", d)
	}
	if locs := failed[0].Observation.Locations; len(locs) != 1 || locs[0].Path != "app/src/main/java/com/shop/app/Restock.java" {
		t.Fatalf("locations %+v", locs)
	}
}

// A stale report committed into the tree cannot stand in for the run.
func TestJavaCommittedReportIsRejected(t *testing.T) {
	root := javaFixture(t)
	gitTest(t, root, "checkout", "-q", "restock")
	put(t, root, "app/target/surefire-reports/TEST-old.xml", "<testsuite><testcase name=\"old\"/></testsuite>\n")
	gitTest(t, root, "add", "-f", "app/target/surefire-reports/TEST-old.xml")
	gitTest(t, root, "commit", "-qm", "commit a report")
	gitTest(t, root, "checkout", "-q", "baseline")
	o := Options{Base: "baseline", Branches: []string{"restock"}, Verify: true, AllowExecution: true, Suite: "recommended", Timeout: time.Minute}
	r, err := Preview(context.Background(), root, o)
	if err != nil {
		t.Fatal(err)
	}
	if r.Gate.Verdict == gate.Pass {
		t.Fatalf("stale report passed the gate: %+v", r.Executions)
	}
	stale := false
	for _, ev := range r.Executions {
		stale = stale || strings.Contains(ev.ExecutionError, "stale JUnit report")
	}
	if !stale {
		t.Fatalf("stale report not reported: %+v", r.Executions)
	}
}
