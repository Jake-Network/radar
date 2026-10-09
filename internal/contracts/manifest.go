package contracts

import (
	"context"
	"encoding/json"
	"fmt"

	gitrepo "github.com/Jake-Network/radar/internal/git"
	"github.com/Jake-Network/radar/internal/jsonptr"
	"github.com/Jake-Network/radar/internal/pathutil"
)

// ManifestPath is the committed location of explicit contract bindings.
const ManifestPath = ".radar/contracts.json"

func LoadManifest(ctx context.Context, root, ref string) (Manifest, error) {
	var m Manifest
	b, err := gitrepo.ReadFile(ctx, root, ref, ManifestPath)
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
		if _, err := pathutil.RepoRelative(binding.Schema); err != nil {
			return m, fmt.Errorf("binding %s schema: %w", binding.ID, err)
		}
		if err := jsonptr.Validate(binding.Pointer); err != nil {
			return m, fmt.Errorf("binding %s pointer: %w", binding.ID, err)
		}
		if binding.Direction != "" && binding.Direction != "request" && binding.Direction != "response" {
			return m, fmt.Errorf("binding direction must be request or response")
		}
		ids[binding.ID] = true
	}
	return m, nil
}

// ReadSchema reads a JSON or YAML contract document at ref.
func ReadSchema(ctx context.Context, root, ref, path string) (map[string]any, error) {
	b, err := gitrepo.ReadFile(ctx, root, ref, path)
	if err != nil {
		return nil, err
	}
	return DecodeDocumentAt(path, b)
}
