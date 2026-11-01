package evidence

import (
	"context"
	"errors"
	gitrepo "github.com/Jake-Network/radar/internal/git"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/Jake-Network/radar/internal/pathutil"
)

// ClearMavenTypecheckInventory removes only untracked compiler inventories in
// the private copy. A cached or committed list is not evidence of this command.
func ClearMavenTypecheckInventory(ctx context.Context, root string) error {
	entries, err := gitrepo.Entries(ctx, root, "HEAD")
	if err != nil {
		return err
	}
	tracked := map[string]bool{}
	for _, e := range entries {
		tracked[e.Path] = true
	}
	count := 0
	return filepath.WalkDir(root, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		count++
		if count > 100000 {
			return errors.New("Maven inventory scan limit exceeded")
		}
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if d.IsDir() {
			if d.Name() == ".git" || d.Name() == "node_modules" || d.Name() == ".venv" {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if d.Name() != "inputFiles.lst" || !strings.Contains("/"+rel, "/target/maven-status/") {
			return nil
		}
		if tracked[rel] {
			return errors.New("committed Maven compiler inventory cannot establish execution coverage")
		}
		safe, err := pathutil.StatePath(root, filepath.FromSlash(rel))
		if err != nil {
			return err
		}
		return os.Remove(safe)
	})
}

const gradleInventoryScript = `allprojects {
    tasks.configureEach { task ->
        if (task instanceof org.gradle.api.tasks.compile.JavaCompile || task.class.name.contains('KotlinCompile')) {
            task.doFirst {
                def inputs = null
                if (task instanceof org.gradle.api.tasks.compile.JavaCompile) {
                    inputs = task.source.files
                } else if (task.hasProperty('sources')) {
                    def sources = task.property('sources')
                    if (sources instanceof org.gradle.api.file.FileCollection) { inputs = sources.files }
                }
                if (inputs != null) {
                    def inventory = new File(gradle.startParameter.currentDir, System.getProperty('radar.typecheck.inputs'))
                    inventory.append(inputs.collect { it.canonicalPath }.join('\n') + '\n', 'UTF-8')
                }
            }
        }
    }
}
`

// PrepareJVMTypecheck installs a fixed input observer in the private candidate,
// never in the user's checkout. Returned artifact paths are repository-relative;
// source integrity must still inspect every tracked file, including build/.
func PrepareJVMTypecheck(root, cwd string, argv []string) (actual []string, inventory string, artifacts []string, err error) {
	if len(argv) == 0 {
		return nil, "", nil, errors.New("empty JVM command")
	}
	base := filepath.Base(argv[0])
	if base != "gradle" && base != "gradlew" {
		return append([]string(nil), argv...), "", nil, nil
	}
	if _, err = pathutil.ResolveInside(root, cwd); err != nil {
		return nil, "", nil, err
	}
	build, err := pathutil.StatePath(root, filepath.Join(cwd, "build"))
	if err != nil {
		return nil, "", nil, err
	}
	if err = os.MkdirAll(build, 0700); err != nil {
		return nil, "", nil, err
	}
	private, err := os.MkdirTemp(build, "radar-typecheck-")
	if err != nil {
		return nil, "", nil, err
	}
	script := filepath.Join(private, "inputs.gradle")
	inventory = filepath.Join(private, "inputs.lst")
	if err = os.WriteFile(script, []byte(gradleInventoryScript), 0600); err != nil {
		return nil, "", nil, err
	}
	if err = os.WriteFile(inventory, nil, 0600); err != nil {
		return nil, "", nil, err
	}
	for _, f := range []string{script, inventory} {
		rel, e := filepath.Rel(root, f)
		if e != nil {
			return nil, "", nil, e
		}
		artifacts = append(artifacts, filepath.ToSlash(rel))
	}
	dir, err := pathutil.ResolveInside(root, cwd)
	if err != nil {
		return nil, "", nil, err
	}
	scriptArg, err := filepath.Rel(dir, script)
	if err != nil {
		return nil, "", nil, err
	}
	inventoryArg, err := filepath.Rel(dir, inventory)
	if err != nil {
		return nil, "", nil, err
	}
	actual = append(append([]string(nil), argv...), "--init-script", scriptArg, "-Dradar.typecheck.inputs="+inventoryArg)
	return actual, inventory, artifacts, nil
}

// JVMTypecheckFiles reads only this execution's fresh input inventory.
func JVMTypecheckFiles(root, inventory string) (map[string]bool, error) {
	rel, err := filepath.Rel(root, inventory)
	if err != nil {
		return nil, err
	}
	full, err := pathutil.ResolveInside(root, filepath.ToSlash(rel))
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(full)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 4<<20 {
		return nil, errors.New("invalid JVM source inventory")
	}
	b, err := os.ReadFile(full)
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, name := range strings.Split(string(b), "\n") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if !filepath.IsAbs(name) {
			continue
		}
		rel, err := filepath.Rel(root, name)
		if err != nil {
			continue
		}
		clean, err := pathutil.RepoRelative(filepath.ToSlash(rel))
		if err != nil {
			continue
		}
		if _, err := pathutil.ResolveInside(root, clean); err == nil {
			out[clean] = true
		}
	}
	if len(out) == 0 {
		return nil, errors.New("JVM compiler reported no supported source inventory")
	}
	return out, nil
}
