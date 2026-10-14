package lockfiles

import (
	"context"
	"github.com/Jake-Network/radar/internal/migrations"
	"github.com/Jake-Network/radar/internal/model"
	"strings"
	"testing"
)

func fixture(label string, files map[string]string) (migrations.Tree, map[string][]byte) {
	t := migrations.Tree{Label: label, Revision: label}
	blobs := map[string][]byte{}
	for p, b := range files {
		oid := label + ":" + p
		t.Files = append(t.Files, migrations.File{Path: p, OID: oid})
		blobs[oid] = []byte(b)
	}
	return t, blobs
}
func compareFixture(t *testing.T, before, after map[string]string, changed []string) Result {
	t.Helper()
	a, ab := fixture("base", before)
	b, bb := fixture("combined", after)
	for k, v := range bb {
		ab[k] = v
	}
	r, e := Evaluate(context.Background(), []migrations.Tree{a, b}, changed, func(oid string) ([]byte, error) { return ab[oid], nil })
	if e != nil {
		t.Fatal(e)
	}
	return r
}
func TestMissingDirectDependency(t *testing.T) {
	cases := []struct{ name, manifest, lock, before, after, locked string }{
		{"npm", "package.json", "package-lock.json", `{"dependencies":{"a":"1"}}`, `{"dependencies":{"a":"1","new":"1"}}`, `{"lockfileVersion":3,"packages":{"":{"dependencies":{"a":"1"}},"node_modules/new":{"version":"1"}}}`},
		{"pnpm", "package.json", "pnpm-lock.yaml", `{"dependencies":{"a":"1"}}`, `{"dependencies":{"a":"1","new":"1"}}`, "lockfileVersion: '9.0'\nimporters:\n  .:\n    dependencies:\n      a:\n        specifier: '1'\n        version: '1'\npackages:\n  new@1: {}\n"},
		{"cargo", "Cargo.toml", "Cargo.lock", "[package]\nname = \"app\"\n[dependencies]\na = \"1\"", "[package]\nname = \"app\"\n[dependencies]\na = \"1\"\nnew = \"1\"", "version = 3\n[[package]]\nname = \"a\"\nversion = \"1\""},
		{"go", "go.mod", "go.sum", "module x\nrequire a v1.0.0", "module x\nrequire (\na v1.0.0\nnew v1.0.0\n)", "a v1.0.0 h1:abc\n"},
		{"uv", "pyproject.toml", "uv.lock", "[project]\nname = \"app\"\ndependencies = [\"a\"]", "[project]\nname = \"app\"\ndependencies = [\"a\", \"new\"]", "version = 1\n[[package]]\nname = \"app\"\n[package.metadata]\nrequires-dist = [{ name = \"a\" }]\n[[package]]\nname = \"new\"\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			before := map[string]string{c.manifest: c.before, c.lock: c.locked}
			after := map[string]string{c.manifest: c.after, c.lock: c.locked}
			r := compareFixture(t, before, after, []string{c.manifest})
			if !r.Applicable || r.Status != model.StatusFailed || len(r.Findings) != 1 || !strings.Contains(r.Findings[0].Explanation, "new") {
				t.Fatalf("%+v", r)
			}
			r = compareFixture(t, after, after, []string{c.lock})
			if r.Status != model.StatusPassed || r.Findings[0].Severity == model.SeverityError {
				t.Fatalf("base issue failed: %+v", r)
			}
		})
	}
}
func TestMalformedAndUnsupportedRemainUnknown(t *testing.T) {
	for _, lock := range []string{"", `{"packages":null}`, `{"packages":{"":false}}`} {
		r := compareFixture(t, map[string]string{"package.json": `{"dependencies":{"a":"1"}}`, "package-lock.json": `{"packages":{"":{"dependencies":{"a":"1"}}}}`}, map[string]string{"package.json": `{"dependencies":{"a":"1"}}`, "package-lock.json": lock}, []string{"package-lock.json"})
		if r.Status != model.StatusUnknown {
			t.Fatalf("%q: %+v", lock, r)
		}
	}
}
func TestGoExactModuleVersion(t *testing.T) {
	r := compareFixture(t, map[string]string{"go.mod": "module app\nrequire m v1.0.0", "go.sum": "m v1.0.0 h1:x"}, map[string]string{"go.mod": "module app\nrequire m v2.0.0", "go.sum": "m v1.0.0 h1:x"}, []string{"go.mod"})
	if r.Status != model.StatusFailed {
		t.Fatalf("%+v", r)
	}
}
func TestCargoRenamedAndLocalDependencies(t *testing.T) {
	m := "[package]\nname=\"app\"\n[dependencies]\nalias = { package = \"actual\", version = \"1\" }\nlocal = { path = \"../local\" }"
	lock := "version=3\n[[package]]\nname=\"actual\"\nversion=\"1\""
	r := compareFixture(t, map[string]string{"Cargo.toml": m, "Cargo.lock": lock}, map[string]string{"Cargo.toml": m, "Cargo.lock": lock}, []string{"Cargo.toml"})
	if r.Status != model.StatusPassed || len(r.Findings) != 0 {
		t.Fatalf("%+v", r)
	}
}
func TestWorkspaceImporterScopedComparison(t *testing.T) {
	cases := []struct{ name, lock, contents string }{
		{"npm", "package-lock.json", `{"packages":{"":{"dependencies":{"new":"1"}},"packages/app":{"dependencies":{"a":"1"}}}}`},
		{"pnpm", "pnpm-lock.yaml", "lockfileVersion: '9.0'\nimporters:\n  .:\n    dependencies:\n      new: {specifier: '1', version: '1'}\n  packages/app:\n    dependencies:\n      a: {specifier: '1', version: '1'}\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			before := map[string]string{"packages/app/package.json": `{"dependencies":{"a":"1"}}`, c.lock: c.contents}
			after := map[string]string{"packages/app/package.json": `{"dependencies":{"a":"1","new":"1"}}`, c.lock: c.contents}
			r := compareFixture(t, before, after, []string{"packages/app/package.json"})
			if r.Status != model.StatusFailed || len(r.Findings) != 1 {
				t.Fatalf("%+v", r)
			}
		})
	}
}
func TestUVWorkspaceMetadataScopedComparison(t *testing.T) {
	lock := "version=1\n[[package]]\nname=\"root\"\n[package.metadata]\nrequires-dist=[{name=\"new\"}]\n[[package]]\nname=\"app\"\n[package.metadata]\nrequires-dist=[{name=\"a\"}]"
	before := map[string]string{"packages/app/pyproject.toml": "[project]\nname=\"app\"\ndependencies=[\"a\"]", "uv.lock": lock}
	after := map[string]string{"packages/app/pyproject.toml": "[project]\nname=\"app\"\ndependencies=[\"a\",\"new\"]", "uv.lock": lock}
	r := compareFixture(t, before, after, []string{"packages/app/pyproject.toml"})
	if r.Status != model.StatusFailed {
		t.Fatalf("%+v", r)
	}
}
func TestCargoTableDependencyChecked(t *testing.T) {
	for _, section := range []string{"dependencies.foo", "target.'cfg(unix)'.dependencies.foo"} {
		before := "[package]\nname=\"app\"\n"
		after := before + "[" + section + "]\nversion=\"1\"\n"
		lock := "version=3\n[[package]]\nname=\"app\"\nversion=\"1\""
		r := compareFixture(t, map[string]string{"Cargo.toml": before, "Cargo.lock": lock}, map[string]string{"Cargo.toml": after, "Cargo.lock": lock}, []string{"Cargo.toml"})
		if r.Status != model.StatusFailed {
			t.Fatalf("%s %+v", section, r)
		}
	}
}
func TestFailureWinsOverLaterUnknownPair(t *testing.T) {
	before := map[string]string{"a/package.json": `{"dependencies":{}}`, "a/package-lock.json": `{"packages":{"":{"dependencies":{}}}}`, "z/package.json": `{"dependencies":{}}`, "z/package-lock.json": `{"packages":{"":{"dependencies":{}}}}`}
	after := map[string]string{"a/package.json": `{"dependencies":{"missing":"1"}}`, "a/package-lock.json": before["a/package-lock.json"], "z/package.json": before["z/package.json"], "z/package-lock.json": "{"}
	r := compareFixture(t, before, after, []string{"a/package.json", "z/package-lock.json"})
	if r.Status != model.StatusFailed {
		t.Fatalf("%+v", r)
	}
}
func TestUVExtrasAndNonliteralDependencies(t *testing.T) {
	lock := "version=1\n[[package]]\nname=\"app\"\n[package.metadata]\nrequires-dist=[{name=\"foo\"}]"
	for _, c := range []struct {
		array  string
		status model.Status
	}{{`["foo[extra]>=1"]`, model.StatusPassed}, {`["foo", unknown]`, model.StatusUnknown}, {`dynamic_value`, model.StatusUnknown}} {
		manifest := "[project]\nname=\"app\"\ndependencies=" + c.array
		files := map[string]string{"pyproject.toml": manifest, "uv.lock": lock}
		r := compareFixture(t, files, files, []string{"pyproject.toml"})
		if r.Status != c.status {
			t.Fatalf("%s %+v", c.array, r)
		}
	}
}
func TestCargoWorkspaceInheritedDependencyNames(t *testing.T) {
	root := "[workspace]\nmembers=[\"members/app\"]\n[workspace.dependencies]\nalias = { package = \"actual\", version = \"1\" }\nlocal = { path = \"members/local\" }\n[workspace.dependencies.extra]\nversion = \"1\"\n"
	lock := "version=3\n[[package]]\nname=\"actual\"\nversion=\"1\""
	member := "[package]\nname=\"app\"\n[dependencies]\nalias={workspace=true}\nlocal={workspace=true}\n"
	before := map[string]string{"Cargo.toml": root, "members/app/Cargo.toml": member, "Cargo.lock": lock}
	after := map[string]string{"Cargo.toml": root, "members/app/Cargo.toml": member + "[dependencies.extra]\nworkspace=true\n", "Cargo.lock": lock}
	r := compareFixture(t, before, after, []string{"members/app/Cargo.toml"})
	if r.Status != model.StatusFailed || len(r.Findings) != 1 || !strings.Contains(r.Findings[0].Explanation, "extra") {
		t.Fatalf("%+v", r)
	}
	r = compareFixture(t, before, before, []string{"members/app/Cargo.toml"})
	if r.Status != model.StatusPassed || len(r.Findings) != 0 {
		t.Fatalf("inherited alias/local %+v", r)
	}
}
func TestCargoInheritedDeclarationMissingUnknown(t *testing.T) {
	files := map[string]string{"Cargo.toml": "[workspace]\nmembers=[\"app\"]", "app/Cargo.toml": "[package]\nname=\"app\"\n[dependencies]\nmissing={workspace=true}", "Cargo.lock": "version=3\n[[package]]\nname=\"app\"\nversion=\"1\""}
	r := compareFixture(t, files, files, []string{"app/Cargo.toml"})
	if r.Status != model.StatusUnknown {
		t.Fatalf("%+v", r)
	}
}
func TestDivergentMajorVersionUpgradesWarn(t *testing.T) {
	s := map[string][]byte{}
	trees := []migrations.Tree{}
	versions := []string{"1", "2", "3", "3"}
	for i, label := range []string{"base", "branch a", "branch b", "combined"} {
		files := map[string]string{"package.json": `{"dependencies":{"dep":"` + versions[i] + `"}}`, "package-lock.json": `{"packages":{"":{"dependencies":{"dep":"` + versions[i] + `"}}}}`}
		tree, blobs := fixture(label, files)
		trees = append(trees, tree)
		for k, b := range blobs {
			s[k] = b
		}
	}
	r, e := Evaluate(context.Background(), trees, []string{"package.json", "package-lock.json"}, func(oid string) ([]byte, error) { return s[oid], nil })
	if e != nil || r.Status != model.StatusPassed || len(r.Findings) != 1 || r.Findings[0].Code != "dependency_major_versions_diverge" || r.Findings[0].Severity != model.SeverityWarning {
		t.Fatalf("%+v %v", r, e)
	}
}
func TestCargoMetadataNameCannotSatisfyDependency(t *testing.T) {
	before := "[package]\nname=\"app\"\n"
	after := before + "[dependencies]\nmissing=\"1\"\n"
	lock := "version=3\n[[package]]\nname=\"app\"\nversion=\"1\"\n[metadata]\nname=\"missing\"\n"
	r := compareFixture(t, map[string]string{"Cargo.toml": before, "Cargo.lock": lock}, map[string]string{"Cargo.toml": after, "Cargo.lock": lock}, []string{"Cargo.toml"})
	if r.Status != model.StatusFailed {
		t.Fatalf("%+v", r)
	}
}
func TestUVMetadataNestedExtrasAndCommentNames(t *testing.T) {
	lock := "version=1\n[[package]]\nname=\"app\"\n[package.metadata]\nrequires-dist=[{name=\"a\",extras=[\"feature\"]},{name=\"new\"}]\n"
	manifest := "[project]\nname=\"app\"\ndependencies=[\"a[feature]\",\"new\"]"
	files := map[string]string{"pyproject.toml": manifest, "uv.lock": lock}
	r := compareFixture(t, files, files, []string{"uv.lock"})
	if r.Status != model.StatusPassed || len(r.Findings) != 0 {
		t.Fatalf("nested extras %+v", r)
	}
	commentLock := "version=1\n[[package]]\nname=\"app\"\n[package.metadata]\n# requires-dist=[{name=\"a\"},{name=\"new\"}]\n"
	before := map[string]string{"pyproject.toml": "[project]\nname=\"app\"\ndependencies=[]", "uv.lock": commentLock}
	after := map[string]string{"pyproject.toml": manifest, "uv.lock": commentLock}
	r = compareFixture(t, before, after, []string{"pyproject.toml"})
	if r.Status != model.StatusFailed {
		t.Fatalf("comment must not satisfy dependencies: %+v", r)
	}
}

func TestGoRequireCommentsAndBlankLines(t *testing.T) {
	before := map[string]string{"go.mod": "module app\nrequire example.com/dep v1.0.0\n", "go.sum": "example.com/dep v1.0.0 h1:x\n"}
	after := map[string]string{"go.mod": "module app\nrequire (\n// direct dependencies\n\nexample.com/dep v1.0.0 // keep this dependency\n)\n", "go.sum": before["go.sum"]}
	r := compareFixture(t, before, after, []string{"go.mod"})
	if !r.Applicable || r.Status != model.StatusPassed || len(r.Findings) != 0 {
		t.Fatalf("valid require comments must remain supported: %+v", r)
	}
}

func TestGoReplacementChecksumObligations(t *testing.T) {
	before := map[string]string{"go.mod": "module app\nrequire example.com/old v1.0.0\n", "go.sum": "example.com/old v1.0.0 h1:x\n"}
	for _, c := range []struct {
		name, replacement, sums string
		status                  model.Status
	}{
		{"local", "replace example.com/added => ./local\n", "", model.StatusPassed},
		{"local-version-specific", "replace (\n// local dependency\nexample.com/added v1.0.0 => ../local\n)\n", "", model.StatusPassed},
		{"remote", "replace example.com/added => example.com/fork v2.0.0\n", "example.com/fork v2.0.0 h1:x\n", model.StatusPassed},
		{"remote-missing", "replace example.com/added => example.com/fork v2.0.0\n", "example.com/added v1.0.0 h1:x\n", model.StatusFailed},
		{"other-version", "replace example.com/added v2.0.0 => ./local\n", "", model.StatusFailed},
		{"version-specific-precedence", "replace (\nexample.com/added => ./local\nexample.com/added v1.0.0 => example.com/fork v2.0.0\n)\n", "", model.StatusFailed},
	} {
		t.Run(c.name, func(t *testing.T) {
			after := map[string]string{"go.mod": before["go.mod"] + "require example.com/added v1.0.0\n" + c.replacement, "go.sum": before["go.sum"] + c.sums}
			r := compareFixture(t, before, after, []string{"go.mod"})
			if !r.Applicable || r.Status != c.status {
				t.Fatalf("replacement obligations: %+v", r)
			}
		})
	}
}

func TestGoLocalReplacementAllowsRemovedSums(t *testing.T) {
	before := map[string]string{"go.mod": "module app\nrequire example.com/dep v1.0.0\n", "go.sum": "example.com/dep v1.0.0 h1:x\n"}
	after := map[string]string{"go.mod": before["go.mod"] + "replace example.com/dep => ./dep\n"}
	r := compareFixture(t, before, after, []string{"go.mod", "go.sum"})
	if r.Status != model.StatusPassed || len(r.Findings) != 0 {
		t.Fatalf("local dependencies have no checksum obligation: %+v", r)
	}
}

func TestGoLocalOnlyModuleNeedsNoSumFile(t *testing.T) {
	manifest := []byte("module app\nrequire example.com/local v0.0.0\nreplace example.com/local => ./local\n")
	deps, locked, err := dependencies(pair{manifest: "go.mod", lock: "go.sum", tool: "go"}, manifest, nil)
	if err != nil || len(deps) != 0 || len(locked) != 0 {
		t.Fatalf("local-only module must not require a go.sum file: deps=%v locked=%v err=%v", deps, locked, err)
	}
	r := compareFixture(t, map[string]string{"go.mod": "module app\n"}, map[string]string{"go.mod": string(manifest), "local/go.mod": "module example.com/local\n"}, []string{"go.mod", "local/go.mod"})
	if r.Applicable || len(r.Findings) != 0 {
		t.Fatalf("absent lockfile pair must not invent a checksum check: %+v", r)
	}
}
