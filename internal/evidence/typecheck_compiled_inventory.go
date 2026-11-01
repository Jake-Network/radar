package evidence

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/Jake-Network/radar/internal/pathutil"
)

// CompiledTypecheckFiles reads compiler-produced source inventories, never
// treating a successful build of an unrelated target as coverage. Missing or
// unsupported inventories leave changed files unchecked.
func CompiledTypecheckFiles(root, tool string) (map[string]bool, error) {
	return compiledTypecheckFiles(root, root, tool, nil)
}

func compiledTypecheckFiles(root, inventoryRoot, tool string, allowed map[string]bool) (map[string]bool, error) {
	out := map[string]bool{}
	visited := 0
	err := filepath.WalkDir(inventoryRoot, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		visited++
		if visited > 100000 {
			return errors.New("compiler inventory entry limit exceeded")
		}
		if d.Type()&os.ModeSymlink != 0 {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			if d.Name() == ".git" || d.Name() == "node_modules" || d.Name() == ".venv" {
				return filepath.SkipDir
			}
			return nil
		}
		rel, e := filepath.Rel(root, p)
		if e != nil {
			return e
		}
		rel = filepath.ToSlash(rel)
		match := tool == "cargo" && strings.HasSuffix(rel, ".d") && (inventoryRoot != root || strings.Contains("/"+rel, "/target/")) || tool == "java" && d.Name() == "inputFiles.lst" && strings.Contains("/"+rel, "/target/maven-status/")
		if !match || allowed != nil && !allowed[p] {
			return nil
		}
		info, e := d.Info()
		if e != nil {
			return e
		}
		if info.Size() > 4<<20 {
			return errors.New("compiler inventory exceeds size limit")
		}
		b, e := os.ReadFile(p)
		if e != nil {
			return e
		}
		var names []string
		if tool == "java" {
			names = strings.Split(string(b), "\n")
		} else {
			// Cargo's dep-info is Make syntax. Escaped spaces and continuations need
			// a full lexer; decline those instead of silently dropping inputs.
			if strings.Contains(string(b), "\\") {
				return errors.New("escaped Cargo dep-info is unsupported")
			}
			for _, line := range strings.Split(string(b), "\n") {
				if _, rest, ok := strings.Cut(line, ": "); ok {
					names = append(names, strings.Fields(rest)...)
				}
			}
		}
		for _, name := range names {
			name = strings.TrimSpace(name)
			if name == "" {
				continue
			}
			if filepath.IsAbs(name) {
				name, e = filepath.Rel(root, name)
				if e != nil {
					continue
				}
			}
			clean, e := pathutil.RepoRelative(filepath.ToSlash(name))
			if e != nil {
				continue
			}
			if _, e = pathutil.ResolveInside(root, clean); e == nil {
				out[clean] = true
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, errors.New("compiler did not provide a supported source inventory")
	}
	return out, nil
}
