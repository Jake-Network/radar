package indexer

import (
	"encoding/json"
	"path"
)

// NearestTypeScriptConfig finds the same nearest script configuration used
// for import inference. exists reads an immutable file inventory; this helper
// never reads the host filesystem or executes a build.
func NearestTypeScriptConfig(importer string, exists func(string) bool) (string, bool) {
	for dir := path.Dir(importer); ; dir = path.Dir(dir) {
		for _, name := range []string{"tsconfig.json", "jsconfig.json"} {
			config := path.Join(dir, name)
			if exists(config) {
				return config, true
			}
		}
		if dir == "." || dir == "/" || dir == "" {
			return "", false
		}
	}
}

// UnmarshalJSONC reads JSON with the comment and trailing-comma syntax
// TypeScript configurations permit, using the indexer's existing decoder.
func UnmarshalJSONC(data []byte, value any) error {
	return json.Unmarshal(stripJSONC(data), value)
}
