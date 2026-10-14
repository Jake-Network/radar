package evidence

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Jake-Network/radar/internal/model"
	"github.com/Jake-Network/radar/internal/runners"
)

func TestDeclaredSetupCacheAndRealVenvRelocation(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 unavailable")
	}
	ctx := context.Background()
	cache := runners.Cache{Dir: t.TempDir(), MaxBytes: 64 << 20}
	p := runners.Profile{Root: ".", Prefix: []string{".venv/bin/python"}, Setup: [][]string{{"python3", "-m", "venv", "--without-pip", ".venv"}, {"python3", "-c", `import os; p=os.path.abspath('.venv'); f=p+'/bin/check'; open(f,'w').write('#!'+p+'/bin/python\nimport sys; print(sys.prefix)\n'); os.chmod(f,0o755)`}}, Lockfiles: []string{"uv.lock"}, Artifacts: []string{".venv"}}
	newCandidate := func(lock string) string {
		root := t.TempDir()
		os.WriteFile(filepath.Join(root, "uv.lock"), []byte(lock), 0600)
		commitRunnerFixture(t, root)
		return root
	}
	one := newCandidate("one")
	first, err := PrepareRunner(ctx, one, "repo", p, cache, time.Minute)
	if err != nil || first.Status != model.StatusPassed || first.CacheHit || first.SetupCommands != 2 || first.CacheWarning != "" {
		t.Fatal(first, err)
	}
	two := newCandidate("one")
	budgetCalls := 0
	second, err := PrepareRunnerBudget(ctx, two, "repo", p, cache, time.Minute, func() bool { budgetCalls++; return false })
	if err != nil || !second.CacheHit || second.SetupCommands != 0 {
		t.Fatal(second, err)
	}
	if budgetCalls != 0 {
		t.Fatal("cache hit reserved setup budget")
	}
	output, err := exec.Command(filepath.Join(two, ".venv", "bin", "check")).Output()
	if err != nil || strings.TrimSpace(string(output)) != filepath.Join(two, ".venv") {
		t.Fatalf("venv not relocated: %q %v", output, err)
	}
	three := newCandidate("changed")
	third, err := PrepareRunner(ctx, three, "repo", p, cache, time.Minute)
	if err != nil || third.CacheHit || third.SetupCommands != 2 {
		t.Fatal(third, err)
	}
}

func TestRunnerSetupFailureNeverCreatesCache(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 unavailable")
	}
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "uv.lock"), []byte("lock"), 0600)
	commitRunnerFixture(t, root)
	p := runners.Profile{Root: ".", Prefix: []string{"python3"}, Setup: [][]string{{"python3", "-c", "raise SystemExit(1)"}}, Lockfiles: []string{"uv.lock"}, Artifacts: []string{".venv"}}
	cache := runners.Cache{Dir: t.TempDir(), MaxBytes: 1 << 20}
	r, err := PrepareRunner(context.Background(), root, "repo", p, cache, time.Minute)
	if err != nil || r.Status != model.StatusError || r.CacheHit {
		t.Fatal(r, err)
	}
	if _, err := os.Stat(filepath.Join(cache.Dir, r.CacheKey)); !os.IsNotExist(err) {
		t.Fatal("failed setup cache published")
	}
}

func commitRunnerFixture(t *testing.T, root string) {
	t.Helper()
	for _, argv := range [][]string{{"init", "-q"}, {"add", "."}, {"-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-qm", "fixture"}} {
		cmd := exec.Command("git", argv...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git fixture: %s %v", out, err)
		}
	}
}

func TestRunnerSetupSourceMutationCannotPoisonCache(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "uv.lock"), []byte("lock"), 0600)
	os.WriteFile(filepath.Join(root, "app.py"), []byte("original"), 0600)
	commitRunnerFixture(t, root)
	p := runners.Profile{Root: ".", Prefix: []string{"python3"}, Setup: [][]string{{"python3", "-c", `import os; os.mkdir('.venv'); open('.venv/marker','w').write('cached'); open('app.py','w').write('poisoned')`}}, Lockfiles: []string{"uv.lock"}, Artifacts: []string{".venv"}}
	cache := runners.Cache{Dir: t.TempDir(), MaxBytes: 1 << 20}
	r, err := PrepareRunner(context.Background(), root, "repo", p, cache, time.Minute)
	if err != nil || r.Status != model.StatusIncomplete {
		t.Fatal(r, err)
	}
	if _, err := os.Stat(filepath.Join(cache.Dir, r.CacheKey)); !os.IsNotExist(err) {
		t.Fatal("mutated setup published a cache")
	}
}

func TestEditableVenvMetadataRelocatesToNewCandidate(t *testing.T) {
	cache := runners.Cache{Dir: t.TempDir(), MaxBytes: 64 << 20}
	p := runners.Profile{Root: ".", Prefix: []string{".venv/bin/python"}, Setup: [][]string{{"python3", "-m", "venv", "--without-pip", "--copies", ".venv"}, {".venv/bin/python", "-c", `import os, sysconfig; f=sysconfig.get_paths()['purelib']+'/local-editable.pth'; open(f,'w').write(os.path.abspath('libs')+'\n')`}}, Lockfiles: []string{"uv.lock"}, Artifacts: []string{".venv"}}
	makeRoot := func() string {
		root := t.TempDir()
		os.WriteFile(filepath.Join(root, "uv.lock"), []byte("same"), 0600)
		os.Mkdir(filepath.Join(root, "libs"), 0700)
		os.WriteFile(filepath.Join(root, "libs", "editable.py"), []byte("VALUE=7\n"), 0600)
		commitRunnerFixture(t, root)
		return root
	}
	first := makeRoot()
	r, err := PrepareRunner(context.Background(), first, "repo", p, cache, time.Minute)
	if err != nil || r.Status != model.StatusPassed || r.CacheWarning != "" {
		t.Fatal(r, err)
	}
	second := makeRoot()
	r, err = PrepareRunner(context.Background(), second, "repo", p, cache, time.Minute)
	if err != nil || !r.CacheHit {
		t.Fatal(r, err)
	}
	// Removing the first checkout makes stale editable paths observable.
	os.RemoveAll(first)
	out, err := exec.Command(filepath.Join(second, ".venv", "bin", "python"), "-c", "import editable; print(editable.VALUE)").CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) != "7" {
		t.Fatalf("editable path was stale: %q %v", out, err)
	}
}

func TestCompiledTargetCacheBindsSourceTree(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "Cargo.lock"), []byte("same lock"), 0600)
	os.WriteFile(filepath.Join(root, "app.py"), []byte("source one"), 0600)
	commitRunnerFixture(t, root)
	p := runners.Profile{Root: ".", Prefix: []string{"python3"}, Setup: [][]string{{"python3", "-c", `import os; os.mkdir('target'); open('target/build','w').write(open('app.py').read())`}}, Lockfiles: []string{"Cargo.lock"}, Artifacts: []string{"target"}}
	cache := runners.Cache{Dir: t.TempDir(), MaxBytes: 1 << 20}
	first, err := PrepareRunner(context.Background(), root, "repo", p, cache, time.Minute)
	if err != nil || first.CacheWarning != "" {
		t.Fatal(first, err)
	}
	os.RemoveAll(filepath.Join(root, "target"))
	os.WriteFile(filepath.Join(root, "app.py"), []byte("source two"), 0600)
	commitRunnerFixture(t, root)
	second, err := PrepareRunner(context.Background(), root, "repo", p, cache, time.Minute)
	if err != nil || second.CacheHit || first.CacheKey == second.CacheKey {
		t.Fatal("compiled target reused across changed source", first, second, err)
	}
}

func TestInstalledProjectCacheBindsSourceTree(t *testing.T) {
	needPython(t)
	for _, artifact := range []string{".venv", "node_modules"} {
		t.Run(artifact, func(t *testing.T) {
			root := t.TempDir()
			os.WriteFile(filepath.Join(root, "uv.lock"), []byte("same lock"), 0600)
			os.WriteFile(filepath.Join(root, "app.py"), []byte("VALUE=1\n"), 0600)
			os.Mkdir(filepath.Join(root, "tests"), 0700)
			os.WriteFile(filepath.Join(root, "tests", "test_installed.py"), []byte("import unittest,installed_app\nclass Installed(unittest.TestCase):\n def test_value(self): self.assertEqual(installed_app.VALUE,1)\n"), 0600)
			commitRunnerFixture(t, root)
			// Copying a local module into a dependency artifact models a
			// non-editable package installation without downloading dependencies.
			setup := `import os,shutil; os.mkdir('` + artifact + `'); shutil.copy('app.py','` + artifact + `/installed_app.py')`
			p := runners.Profile{Root: ".", Prefix: []string{"python3"}, Setup: [][]string{{"python3", "-c", setup}}, Lockfiles: []string{"uv.lock"}, Artifacts: []string{artifact}}
			cache := runners.Cache{Dir: t.TempDir(), MaxBytes: 1 << 20}
			first, err := PrepareRunner(context.Background(), root, "repo", p, cache, time.Minute)
			if err != nil || first.CacheWarning != "" || first.Status != model.StatusPassed {
				t.Fatal(first, err)
			}
			os.RemoveAll(filepath.Join(root, artifact))
			os.WriteFile(filepath.Join(root, "app.py"), []byte("VALUE=2\n"), 0600)
			commitRunnerFixture(t, root)
			second, err := PrepareRunner(context.Background(), root, "repo", p, cache, time.Minute)
			if err != nil || second.CacheHit || second.CacheKey == first.CacheKey || second.SetupCommands != 1 {
				t.Fatal("installed project reused across source trees", first, second, err)
			}
			// Run from the artifact so its installed copy is imported. The
			// previous candidate passes this assertion; the new source must fail.
			observation, err := ObserveCandidateAt(context.Background(), root, artifact, []string{"python3", "-m", "unittest", "discover", "-s", "../tests"}, time.Minute)
			if err != nil || observation.Status != model.StatusFailed || observation.TestsRun != 1 || observation.TestsFailed != 1 {
				t.Fatal("verification did not observe the new installed source", observation, err)
			}
		})
	}
}

func TestExplicitProfileSetupCanInstallWithoutInheritingSecrets(t *testing.T) {
	needPython(t)
	for _, name := range []string{"RADAR_SETUP_SECRET", "NPM_TOKEN", "PYTHONPATH", "PYTHONSTARTUP", "NODE_OPTIONS", "GIT_SSH_COMMAND", "PIP_NO_INDEX", "CARGO_NET_OFFLINE", "npm_config_offline", "GOPROXY", "GOSUMDB"} {
		t.Setenv(name, "sensitive-host-value")
	}
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "seed.txt"), []byte("source"), 0600)
	commitRunnerFixture(t, root)
	// No network call: the actual child checks the environment package managers
	// receive, including inherited variables that must remain scrubbed.
	check := `import os; forbidden=('RADAR_SETUP_SECRET','NPM_TOKEN','PYTHONPATH','PYTHONSTARTUP','NODE_OPTIONS','GIT_SSH_COMMAND','PIP_NO_INDEX','CARGO_NET_OFFLINE','npm_config_offline','GOPROXY','GOSUMDB'); assert not any(name in os.environ for name in forbidden); assert os.environ['GOTOOLCHAIN']=='local'; assert os.environ['GIT_TERMINAL_PROMPT']=='0'`
	p := runners.Profile{Root: ".", Prefix: []string{"python3"}, Setup: [][]string{{"python3", "-c", check}}}
	r, err := PrepareRunner(context.Background(), root, "repo", p, runners.Cache{Dir: t.TempDir(), MaxBytes: 1 << 20}, time.Minute)
	if err != nil || r.Status != model.StatusPassed || r.SetupCommands != 1 {
		t.Fatal(r, err)
	}
	regular := map[string]string{}
	for _, value := range environment(t.TempDir(), nil) {
		name, content, _ := strings.Cut(value, "=")
		regular[name] = content
	}
	for name, want := range map[string]string{"PIP_NO_INDEX": "1", "CARGO_NET_OFFLINE": "true", "npm_config_offline": "true", "GOPROXY": "off", "GOSUMDB": "off"} {
		if regular[name] != want {
			t.Fatalf("normal tests changed offline policy for %s", name)
		}
	}
	for _, name := range []string{"RADAR_SETUP_SECRET", "NPM_TOKEN", "PYTHONPATH", "PYTHONSTARTUP", "NODE_OPTIONS", "GIT_SSH_COMMAND"} {
		if _, ok := regular[name]; ok {
			t.Fatalf("normal runner inherited %s", name)
		}
	}
}

func TestObservedPythonRuntimeDataAndTool(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 unavailable")
	}
	root := t.TempDir()
	os.Mkdir(filepath.Join(root, "config"), 0700)
	os.WriteFile(filepath.Join(root, "config", "limits.json"), []byte(`{"limit":2}`), 0600)
	os.WriteFile(filepath.Join(root, "config", "child.json"), []byte(`{"child":true}`), 0600)
	os.WriteFile(filepath.Join(root, "test_runtime.py"), []byte("import unittest, json, subprocess, sys\nclass Runtime(unittest.TestCase):\n def test_config(self):\n  with open('config/limits.json') as f: self.assertEqual(json.load(f)['limit'],2)\n  subprocess.run([sys.executable, '-c', \"with open('config/child.json') as f: assert f.read()\"], check=True)\n"), 0600)
	r, files, err := ObserveCandidateCoverage(context.Background(), root, ".", []string{"python3", "-m", "unittest", "discover"}, "", time.Minute)
	if err != nil || r.Status != model.StatusPassed || r.TestsRun != 1 {
		t.Fatal(r, files, err)
	}
	found := false
	for _, f := range files {
		if f == "config/limits.json" {
			found = true
		}
	}
	if !found {
		t.Fatal("runtime data omitted", files)
	}
	child := false
	for _, f := range files {
		if f == "config/child.json" {
			child = true
		}
	}
	if !child {
		t.Fatal("child Python file reads were not observed", files)
	}
	tool, err := ObserveToolCandidate(context.Background(), root, ".", []string{"python3", "-c", "print('done')"}, time.Minute)
	if err != nil || tool.Status != model.StatusPassed || tool.TestsRun != 0 {
		t.Fatal(tool, err)
	}
	if got := harnessCounts([]string{"uv", "run", "--frozen", "python3", "-m", "unittest"}, []byte("Ran 2 tests in 0.1s\nOK\n"), root); got.Run != 2 {
		t.Fatal("wrapped unittest lost counts", got)
	}
}
