package contracts

import (
	"context"
	"encoding/json"
	"fmt"
	gitrepo "github.com/radar-engine/radar/internal/git"
)

func LoadManifest(ctx context.Context, root, ref string) (Manifest, error) {
	var m Manifest
	b, err := gitrepo.ReadFile(ctx, root, ref, ".radar/contracts.json")
	if err != nil {
		return m, fmt.Errorf("explicit contract bindings unavailable: %w", err)
	}
	if err = json.Unmarshal(b, &m); err != nil {
		return m, fmt.Errorf("invalid contract manifest: %w", err)
	}
	if m.Version != 1 {
		return m, fmt.Errorf("unsupported contract manifest version %d", m.Version)
	}
	ids := map[string]bool{}
	for _, binding := range m.Bindings {
		if binding.ID == "" || binding.Schema == "" || ids[binding.ID] {
			return m, fmt.Errorf("binding requires unique id and schema")
		}
		if binding.Direction != "" && binding.Direction != "request" && binding.Direction != "response" {
			return m, fmt.Errorf("binding direction must be request or response")
		}
		ids[binding.ID] = true
	}
	return m, nil
}
func ReadSchema(ctx context.Context, root, ref, path string) (map[string]any, error) {
	b, err := gitrepo.ReadFile(ctx, root, ref, path)
	if err != nil {
		return nil, err
	}
	return DecodeDocument(b)
}
