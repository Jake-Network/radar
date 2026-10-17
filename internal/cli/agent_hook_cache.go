package cli

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"

	"github.com/Jake-Network/radar/internal/buildidentity"
	"github.com/Jake-Network/radar/internal/composition"
	gitrepo "github.com/Jake-Network/radar/internal/git"
	"github.com/Jake-Network/radar/internal/project"
)

// Keep a single last result per worktree in the existing run/coverage state
// directory. Writing inside an uninitialized checkout would itself be a dirty
// input and invalidate the result. This is a static preview, never test evidence.
type hookCacheRecord struct {
	Key      string            `json:"key"`
	Response agentHookResponse `json:"response"`
	Digest   string            `json:"digest"`
}

func hookCacheKey(ctx context.Context, root, state string) string {
	head, err := gitrepo.Resolve(ctx, root, "HEAD")
	if err != nil {
		return ""
	}
	executable, err := os.Executable()
	if err != nil {
		return ""
	}
	st, err := os.Stat(executable)
	if err != nil {
		return ""
	}
	digest := buildidentity.Identity()
	if digest == "" {
		return ""
	}
	data, _ := json.Marshal([]any{"hook-preview-v3-self-pair", Version, digest, executable, st.Size(), st.ModTime().UnixNano(), root, head, state})
	return configurationDigest(data)
}

func hookCachePath(root string) (string, error) {
	dir, err := composition.StateDir()
	if err != nil {
		return "", err
	}
	return project.SafePath(dir, filepath.Join("hook-previews", configurationDigest([]byte(root))+".json"))
}

func hookCacheDigest(key string, response agentHookResponse) string {
	data, _ := json.Marshal([]any{key, response})
	return configurationDigest(data)
}

func loadHookCache(root, key string) (agentHookResponse, bool) {
	if key == "" {
		return agentHookResponse{}, false
	}
	path, err := hookCachePath(root)
	if err != nil {
		return agentHookResponse{}, false
	}
	f, err := os.Open(path)
	if err != nil {
		return agentHookResponse{}, false
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() || st.Size() > 16384 {
		return agentHookResponse{}, false
	}
	data, err := io.ReadAll(io.LimitReader(f, 16385))
	var cached hookCacheRecord
	if err != nil || json.Unmarshal(data, &cached) != nil || cached.Key != key || cached.Digest != hookCacheDigest(key, cached.Response) {
		return agentHookResponse{}, false
	}
	return cached.Response, true
}

func saveHookCache(root, key string, response agentHookResponse) {
	if key == "" {
		return
	}
	path, err := hookCachePath(root)
	if err != nil {
		return
	}
	if os.MkdirAll(filepath.Dir(path), 0700) != nil {
		return
	}
	// Recheck after creating the directory and publish a complete record atomically.
	path, err = hookCachePath(root)
	if err != nil {
		return
	}
	data, err := json.Marshal(hookCacheRecord{key, response, hookCacheDigest(key, response)})
	if err != nil || len(data) > 16384 {
		return
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".preview-")
	if err != nil {
		return
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err != nil {
		f.Close()
		return
	}
	if f.Close() != nil {
		return
	}
	_ = os.Rename(f.Name(), path)
}
