package evidence

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Jake-Network/radar/internal/model"
)

func TestGoLegacyBuildFailure(t *testing.T) {
	argv := []string{"go", "test", "-json", "."}
	suffix := `{"Action":"output","Package":"example.com/m","Output":"FAIL\texample.com/m [build failed]\n"}` + "\n"
	output := "# example.com/m [example.com/m.test]\n./args.go:149:6: undefined: commandNameMatches\n" + suffix
	r := harnessCounts(argv, []byte(output), "")
	if len(r.BuildErrors) != 1 || r.BuildErrors[0].Symbol != "commandNameMatches" || outcome(false, 1, r, false, argv, []byte(output)) != model.StatusFailed {
		t.Fatalf("legacy compiler failure not recognized: %+v", r)
	}
	for name, text := range map[string]string{
		"missing checksum": "missing go.sum entry for module providing package example.com/m\n" + output,
		"toolchain":        "requires go >= 1.24\n" + output,
		"absolute cache":   "/home/u/go/pkg/mod/x/args.go:149:6: undefined: commandNameMatches\n" + suffix,
		"parent escape":    "../args.go:149:6: undefined: commandNameMatches\n" + suffix,
		"no failed build":  "./args.go:149:6: undefined: commandNameMatches\n",
	} {
		t.Run(name, func(t *testing.T) {
			if got := harnessCounts(argv, []byte(text), ""); len(got.BuildErrors) != 0 {
				t.Fatalf("environment attributed to source: %+v", got)
			}
		})
	}
}

func TestGoLegacyBuildFailureRealCommand(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip(err)
	}
	dir := t.TempDir()
	for name, content := range map[string]string{"go.mod": "module example.com/legacy\n\ngo 1.23\n", "source.go": "package legacy\nfunc Broken() { missing() }\n", "source_test.go": "package legacy\nimport \"testing\"\nfunc TestBroken(t *testing.T) { Broken() }\n"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	argv := []string{"go", "test", "-json", "."}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GODEBUG=gotestjsonbuildtext=1", "GOWORK=off", "GOPROXY=off", "GOTOOLCHAIN=local")
	data, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("broken source compiled")
	}
	if strings.Contains(string(data), `"Action":"build-output"`) {
		t.Fatal("expected legacy compiler text")
	}
	got := harnessCounts(argv, data, dir)
	if len(got.BuildErrors) != 1 || got.BuildErrors[0].Symbol != "missing" {
		t.Fatalf("legacy build: %+v\n%s", got, data)
	}
}
