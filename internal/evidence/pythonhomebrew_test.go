package evidence

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Exercise the exact probe against a synthetic physical Homebrew installation.
// Only interpreter installation facts are substituted; path, module and
// distribution classification run unchanged. No host installation is modified.
func TestPythonHomebrewInstalledFileAliases(t *testing.T) {
	interpreter, _ := pythonTestVenv(t)
	versionBytes, err := exec.Command(interpreter, "-c", `import sys; print('%d.%d' % sys.version_info[:2])`).Output()
	if err != nil {
		t.Fatal(err)
	}
	version := strings.TrimSpace(string(versionBytes))
	extensionBytes, err := exec.Command(interpreter, "-c", `import importlib.machinery; print(importlib.machinery.EXTENSION_SUFFIXES[0])`).Output()
	if err != nil {
		t.Fatal(err)
	}
	extension := strings.TrimSpace(string(extensionBytes))
	for _, name := range []string{"installed", "native_extension", "loaded", "base_mismatch", "shared_mismatch", "nested_alias", "first_party", "other_first_party", "copied_project", "unrelated_distribution", "parent_path", "sibling_module", "outside_cellar", "wrong_version", "directory", "renamed", "dangling", "pth", "wrong_interpreter", "candidate_target"} {
		t.Run(name, func(t *testing.T) {
			brew, root := t.TempDir(), t.TempDir()
			base := filepath.Join(brew, "Cellar", "python@"+version, "3.14.7", "Frameworks", "Python.framework", "Versions", version)
			baseExec := base
			executable := filepath.Join(base, "bin", "python3")
			shared := filepath.Join(brew, "lib", "python"+version, "site-packages")
			targetSite := filepath.Join(brew, "Cellar", "dependency", "2.1", "lib", "python"+version, "site-packages")
			target := filepath.Join(targetSite, "dependency.py")
			alias := filepath.Join(shared, "dependency.py")
			extra := ""
			input := pythonProbeInput{Root: root, Command: []string{}, Other: []string{}, Unnamed: []string{}, Projects: []string{"sample"}}
			want := ""
			switch name {
			case "native_extension":
				target = filepath.Join(targetSite, "dependency"+extension)
				alias = filepath.Join(shared, "dependency"+extension)
			case "base_mismatch":
				baseExec = filepath.Join(t.TempDir(), "different")
				want = "path"
			case "shared_mismatch":
				shared = filepath.Join(t.TempDir(), "site-packages")
				alias = filepath.Join(shared, "dependency.py")
				want = "path"
			case "nested_alias":
				target = filepath.Join(targetSite, "nested", "dependency.py")
				want = "path"
			case "loaded":
				extra = "import dependency\n"
			case "first_party":
				input.Command = []string{"dependency"}
				want = "package"
			case "other_first_party":
				input.Other = []string{"dependency"}
				want = "package"
			case "copied_project":
				input.Other = []string{"dependency"}
				want = "package"
				pythonTestPut(t, shared, "sample-1.0.dist-info/METADATA", "Metadata-Version: 2.1\nName: sample\nVersion: 1.0\n")
				pythonTestPut(t, shared, "sample-1.0.dist-info/RECORD", "dependency.py,,\n")
			case "unrelated_distribution":
				input.Other = []string{"dependency"}
				pythonTestPut(t, shared, "dependency-1.0.dist-info/METADATA", "Metadata-Version: 2.1\nName: dependency\nVersion: 1.0\n")
				pythonTestPut(t, shared, "dependency-1.0.dist-info/RECORD", "dependency.py,,\n")
			case "parent_path":
				extra = "sys.path.append(" + strconvQuote(targetSite) + ")\n"
				want = "path"
			case "sibling_module":
				pythonTestPut(t, targetSite, "unrelated.py", "")
				extra = "import importlib.util\nspec = importlib.util.spec_from_file_location('unrelated', " + strconvQuote(filepath.Join(targetSite, "unrelated.py")) + ")\nm = importlib.util.module_from_spec(spec)\nsys.modules['unrelated'] = m\n"
				want = "module"
			case "outside_cellar":
				target = filepath.Join(t.TempDir(), "Cellar", "dependency", "2.1", "lib", "python"+version, "site-packages", "dependency.py")
				want = "path"
			case "wrong_version":
				target = filepath.Join(brew, "Cellar", "dependency", "2.1", "lib", "python0.0", "site-packages", "dependency.py")
				want = "path"
			case "directory":
				target = filepath.Join(targetSite, "dependency")
				alias = filepath.Join(shared, "dependency")
				want = "path"
			case "renamed":
				alias = filepath.Join(shared, "different.py")
				want = "path"
			case "dangling":
				want = "path"
			case "pth":
				target = filepath.Join(targetSite, "dependency.pth")
				alias = filepath.Join(shared, "dependency.pth")
				want = "path"
			case "wrong_interpreter":
				executable = filepath.Join(t.TempDir(), "python3")
				want = "path"
			case "candidate_target":
				root = filepath.Join(brew, "Cellar", "dependency", "2.1")
				input.Root = root
				input.Command = []string{"dependency"}
				want = "" // Candidate code is already trusted; no installation promotion.
			}
			if err := os.MkdirAll(root, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(shared, 0700); err != nil {
				t.Fatal(err)
			}
			if name == "directory" {
				if err := os.MkdirAll(target, 0700); err != nil {
					t.Fatal(err)
				}
			} else if name != "dangling" {
				pythonTestPut(t, filepath.Dir(target), filepath.Base(target), "VALUE = 1\n")
			}
			if err := os.Symlink(target, alias); err != nil {
				if runtime.GOOS == "windows" && errors.Is(err, syscall.Errno(1314)) {
					t.Skip("Windows account cannot create file symlinks (ERROR_PRIVILEGE_NOT_HELD)")
				}
				t.Fatal(err)
			}
			call := `homebrew_site_aliases(sys.base_prefix, sys.base_exec_prefix, sys.executable, "%d.%d" % sys.version_info[:2], sites, root)`
			replacement := "homebrew_site_aliases(" + strconvQuote(base) + ", " + strconvQuote(baseExec) + ", " + strconvQuote(executable) + ", " + strconvQuote(version) + ", sites, root)"
			probe := strings.Replace(pythonImportProbe, "    site_aliases = "+call, "    sites.add(real("+strconvQuote(shared)+"))\n    sys.path.append("+strconvQuote(shared)+")\n    "+strings.ReplaceAll(strings.TrimSuffix(extra, "\n"), "\n", "\n    ")+"\n    site_aliases = "+replacement, 1)
			payload, err := json.Marshal(input)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, interpreter, "-c", probe)
			cmd.Dir = root
			cmd.Stdin = strings.NewReader(string(payload))
			cmd.Env = environment(t.TempDir(), nil)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("probe: %v: %s", err, out)
			}
			var result struct {
				Checked bool   `json:"checked"`
				Reason  string `json:"reason"`
				Outside string `json:"outside"`
			}
			if err := json.Unmarshal(out, &result); err != nil {
				t.Fatalf("decode: %v: %s", err, out)
			}
			if !result.Checked || result.Reason != want {
				t.Fatalf("got %+v, want reason %q", result, want)
			}
		})
	}
}
