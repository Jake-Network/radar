package typecheck

import (
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/Jake-Network/radar/internal/indexer"
)

var gradleIncludeLiteral = regexp.MustCompile(`['"]([A-Za-z0-9_:-]+)['"]`)

// gradleCompileTasks supports literal conventional project layouts only.
// Settings scripts remain data; computed includes cannot narrow coverage.
func gradleCompileTasks(root string, files map[string]indexer.Entry, read func(string) ([]byte, bool)) ([]string, bool) {
	var b []byte
	var ok bool
	for _, name := range []string{"settings.gradle", "settings.gradle.kts"} {
		if b, ok = read(path.Join(root, name)); ok {
			break
		}
	}
	if !ok {
		return nil, false
	}
	text := string(b)
	for _, dynamic := range []string{"projectDir", "includeFlat", "includeBuild", "apply(", "apply from", "for (", "for("} {
		if strings.Contains(text, dynamic) {
			return nil, false
		}
	}
	modules := map[string]bool{}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(strings.SplitN(line, "//", 2)[0])
		if !strings.HasPrefix(line, "include") {
			continue
		}
		rest := strings.TrimSpace(strings.TrimPrefix(line, "include"))
		rest = strings.TrimSpace(strings.Trim(rest, "()"))
		matches := gradleIncludeLiteral.FindAllStringSubmatch(rest, -1)
		if len(matches) == 0 {
			return nil, false
		}
		residue := gradleIncludeLiteral.ReplaceAllString(rest, "")
		if strings.Trim(residue, ", \t") != "" {
			return nil, false
		}
		for _, m := range matches {
			module := strings.TrimPrefix(m[1], ":")
			if module == "" || strings.Contains(module, "::") {
				return nil, false
			}
			dir := path.Join(root, strings.ReplaceAll(module, ":", "/"))
			if _, g := files[path.Join(dir, "build.gradle")]; !g {
				if _, k := files[path.Join(dir, "build.gradle.kts")]; !k {
					return nil, false
				}
			}
			modules[":"+module+":testClasses"] = true
		}
	}
	for _, name := range []string{"build.gradle", "build.gradle.kts"} {
		if _, ok := files[path.Join(root, name)]; ok {
			if src, ok := read(path.Join(root, name)); ok && (strings.Contains(string(src), "java") || strings.Contains(string(src), "kotlin")) {
				modules[":testClasses"] = true
			}
		}
	}
	tasks := make([]string, 0, len(modules))
	for task := range modules {
		tasks = append(tasks, task)
	}
	sort.Strings(tasks)
	return tasks, len(tasks) > 0
}
