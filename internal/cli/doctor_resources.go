package cli

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Resource advisories describe literal configuration, never runtime isolation.
// Values are only compared in memory and never included in the report.
type doctorResource struct {
	Kind      string   `json:"kind"`
	Status    string   `json:"status"`
	Key       string   `json:"key"`
	Worktrees []string `json:"worktrees"`
}

var resourceKey = regexp.MustCompile(`^(?:[A-Z_][A-Z0-9_]*_)?PORT$|^(?:[A-Z_][A-Z0-9_]*_)?DATABASE_URL$|^DB_URL$|^COMPOSE_PROJECT_NAME$`)
var composePort = regexp.MustCompile(`^["']?(?:[0-9.]+:)?([0-9]{1,5}):[0-9]{1,5}(?:/[a-z]+)?["']?$`)

func resourceFile(root, rel string) ([]byte, bool) {
	// Refuse symlinks in every component, including the leaf. Bounded reads only.
	current := root
	for _, part := range strings.Split(filepath.Clean(rel), string(filepath.Separator)) {
		if part == ".." || filepath.IsAbs(rel) {
			return nil, false
		}
		current = filepath.Join(current, part)
		st, err := os.Lstat(current)
		if err != nil || st.Mode()&os.ModeSymlink != 0 {
			return nil, false
		}
	}
	st, err := os.Stat(current)
	if err != nil || !st.Mode().IsRegular() || st.Size() > 1<<20 {
		return nil, false
	}
	b, err := os.ReadFile(current)
	return b, err == nil && len(b) <= 1<<20
}

func inspectResources(roots []string) []doctorResource {
	type identity struct{ key, digest string }
	shared := map[identity]map[string]bool{}
	unknown := map[string]map[string]bool{}
	add := func(root, key, value string) {
		value = strings.TrimSpace(value)
		if len(value) >= 2 && ((value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'')) {
			value = value[1 : len(value)-1]
		}
		if value == "" {
			return
		}
		if strings.ContainsAny(value, "$`\\") {
			if unknown[key] == nil {
				unknown[key] = map[string]bool{}
			}
			unknown[key][root] = true
			return
		}
		id := identity{key, fmt.Sprintf("%x", sha256.Sum256([]byte(value)))}
		if shared[id] == nil {
			shared[id] = map[string]bool{}
		}
		shared[id][root] = true
	}
	for _, root := range roots {
		// Local configuration is deliberately limited to conventional root files.
		for _, name := range []string{".env", ".env.local", ".env.development", ".env.test"} {
			if b, ok := resourceFile(root, name); ok {
				for _, line := range strings.Split(string(b), "\n") {
					line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "export "))
					key, value, ok := strings.Cut(line, "=")
					key = strings.TrimSpace(key)
					if ok && resourceKey.MatchString(key) {
						add(root, key, value)
					}
				}
			}
		}
		for _, name := range []string{"compose.yaml", "compose.yml", "docker-compose.yml", "docker-compose.yaml"} {
			if b, ok := resourceFile(root, name); ok {
				ports := false
				for _, line := range strings.Split(string(b), "\n") {
					trimmed := strings.TrimSpace(line)
					if strings.HasPrefix(line, "name:") {
						add(root, "COMPOSE_PROJECT_NAME", strings.TrimSpace(strings.TrimPrefix(line, "name:")))
					}
					if trimmed == "ports:" {
						ports = true
						continue
					}
					if ports && strings.HasPrefix(trimmed, "- ") {
						value := strings.TrimSpace(strings.TrimPrefix(trimmed, "- "))
						if match := composePort.FindStringSubmatch(value); match != nil {
							add(root, "PORT", match[1])
						} else if strings.Contains(value, "$") {
							add(root, "PORT", value)
						}
					} else if trimmed != "" && !strings.HasPrefix(trimmed, "#") {
						ports = false
					}
				}
			}
		}
	}
	out := []doctorResource{}
	sortedRoots := func(m map[string]bool) []string {
		paths := []string{}
		for p := range m {
			paths = append(paths, p)
		}
		sort.Strings(paths)
		return paths
	}
	for id, paths := range shared {
		if len(paths) > 1 {
			out = append(out, doctorResource{"shared_resource", "warning", id.key, sortedRoots(paths)})
		}
	}
	for key, paths := range unknown {
		out = append(out, doctorResource{"resource_expression", "unknown", key, sortedRoots(paths)})
	}
	// Collapse duplicate configuration declarations without exposing their digests.
	sort.Slice(out, func(i, j int) bool {
		return out[i].Key+out[i].Status+strings.Join(out[i].Worktrees, "\x00") < out[j].Key+out[j].Status+strings.Join(out[j].Worktrees, "\x00")
	})
	result := []doctorResource{}
	for _, item := range out {
		if len(result) == 0 || fmt.Sprint(item) != fmt.Sprint(result[len(result)-1]) {
			result = append(result, item)
		}
	}
	return result
}

func editableAdvisories(root, environment string) []doctorResource {
	if environment == "" {
		return nil
	}
	// Reading .pth is static: executable import lines cannot be interpreted safely.
	patterns := []string{"lib/python*/site-packages/*.pth", "Lib/site-packages/*.pth"}
	out := []doctorResource{}
	for _, pattern := range patterns {
		files, _ := filepath.Glob(filepath.Join(environment, filepath.FromSlash(pattern)))
		for _, name := range files {
			rel, err := filepath.Rel(environment, name)
			if err != nil {
				continue
			}
			b, ok := resourceFile(environment, rel)
			if !ok {
				continue
			}
			for _, line := range strings.Split(string(b), "\n") {
				line = strings.TrimSpace(line)
				if line == "" || strings.HasPrefix(line, "#") {
					continue
				}
				status, kind := "warning", "external_editable"
				if strings.HasPrefix(line, "import ") || strings.HasPrefix(line, "import\t") {
					status, kind = "unknown", "editable_loader"
				} else {
					target := line
					if !filepath.IsAbs(target) {
						target = filepath.Join(filepath.Dir(name), target)
					}
					relative, err := filepath.Rel(root, target)
					if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
						continue
					}
				}
				out = append(out, doctorResource{kind, status, "python editable environment", []string{root}})
				break
			}
		}
	}
	return out
}
