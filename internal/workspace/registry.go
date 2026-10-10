package workspace

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"
)

// RegistryVersion is the local registry format version.
const RegistryVersion = 1

// RegistryFile is the registry's name inside the configuration directory.
const RegistryFile = "workspaces.json"

// Registry is the machine-local list of workspaces. It holds absolute paths,
// so it is never committed. Only `radar workspace add/remove` write it.
type Registry struct {
	Version    int         `json:"version"`
	Workspaces []Workspace `json:"workspaces"`
}

// Workspace names a group of repositories checked together.
type Workspace struct {
	Name  string `json:"name"`
	Repos []Repo `json:"repos"`
}

// Repo locates one repository. CommonDir, the absolute Git common directory,
// identifies it: every worktree of a repository shares it.
type Repo struct {
	ID        string `json:"id"`
	Path      string `json:"path"`
	CommonDir string `json:"common_dir"`
}

// ConfigDir is RADAR_CONFIG_DIR, or <user config dir>/radar.
func ConfigDir() (string, error) {
	if dir := os.Getenv("RADAR_CONFIG_DIR"); dir != "" {
		return filepath.Abs(dir)
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("no configuration directory for the workspace registry (set RADAR_CONFIG_DIR): %w", err)
	}
	return filepath.Join(dir, "radar"), nil
}

// Load reads the registry in dir. A missing registry is empty.
func Load(dir string) (Registry, error) {
	path := filepath.Join(dir, RegistryFile)
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Registry{Version: RegistryVersion, Workspaces: []Workspace{}}, nil
	}
	if err != nil {
		return Registry{}, fmt.Errorf("workspace registry %s: %w", path, err)
	}
	var r Registry
	if err = json.Unmarshal(b, &r); err != nil {
		return Registry{}, fmt.Errorf("workspace registry %s is not valid JSON: %w", path, err)
	}
	if r.Version != RegistryVersion {
		return Registry{}, fmt.Errorf("workspace registry %s has unsupported version %d", path, r.Version)
	}
	if err = r.Validate(); err != nil {
		return Registry{}, fmt.Errorf("workspace registry %s: %w", path, err)
	}
	return r, nil
}

// Validate enforces the registry invariants: valid unique names and IDs,
// absolute paths, and each repository in at most one workspace.
func (r Registry) Validate() error {
	names := map[string]bool{}
	common := map[string]string{}
	for _, w := range r.Workspaces {
		if err := ValidID(w.Name); err != nil {
			return fmt.Errorf("workspace name: %w", err)
		}
		if names[w.Name] {
			return fmt.Errorf("duplicate workspace %q", w.Name)
		}
		names[w.Name] = true
		ids := map[string]bool{}
		for _, repo := range w.Repos {
			if err := ValidID(repo.ID); err != nil {
				return fmt.Errorf("workspace %q: %w", w.Name, err)
			}
			if ids[repo.ID] {
				return fmt.Errorf("workspace %q has repo ID %q twice", w.Name, repo.ID)
			}
			ids[repo.ID] = true
			if !filepath.IsAbs(repo.Path) || !filepath.IsAbs(repo.CommonDir) {
				return fmt.Errorf("workspace %q repo %q needs absolute paths", w.Name, repo.ID)
			}
			if other, ok := common[repo.CommonDir]; ok {
				return fmt.Errorf("repository %s is in workspaces %q and %q; a repository belongs to one workspace", repo.CommonDir, other, w.Name)
			}
			common[repo.CommonDir] = w.Name
		}
	}
	return nil
}

// Find returns the workspace and repo indexes holding commonDir.
func (r Registry) Find(commonDir string) (int, int, bool) {
	for wi, w := range r.Workspaces {
		for ri, repo := range w.Repos {
			if repo.CommonDir == commonDir {
				return wi, ri, true
			}
		}
	}
	return -1, -1, false
}

// Named returns the index of the workspace called name.
func (r Registry) Named(name string) int {
	for i, w := range r.Workspaces {
		if w.Name == name {
			return i
		}
	}
	return -1
}

// IDs returns the workspace's repo IDs in sorted order.
func (w Workspace) IDs() []string {
	out := []string{}
	for _, repo := range w.Repos {
		out = append(out, repo.ID)
	}
	sort.Strings(out)
	return out
}

// Repo returns the repo with id.
func (w Workspace) Repo(id string) (Repo, bool) {
	for _, repo := range w.Repos {
		if repo.ID == id {
			return repo, true
		}
	}
	return Repo{}, false
}

// lockTimeout bounds waiting for another writer; staleLock reclaims a lock
// left by a writer that crashed.
var (
	lockTimeout = 10 * time.Second
	staleLock   = 2 * time.Minute
)

// Update applies fn to the registry under an exclusive lock and replaces the
// file atomically, so concurrent writers serialize and readers never see a
// partial file. fn's changes are discarded when it returns an error.
func Update(dir string, fn func(*Registry) error) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("workspace registry directory: %w", err)
	}
	unlock, err := lock(filepath.Join(dir, RegistryFile+".lock"))
	if err != nil {
		return err
	}
	defer unlock()
	r, err := Load(dir)
	if err != nil {
		return err
	}
	if err = fn(&r); err != nil {
		return err
	}
	r.Version = RegistryVersion
	for i := range r.Workspaces {
		sort.Slice(r.Workspaces[i].Repos, func(a, b int) bool { return r.Workspaces[i].Repos[a].ID < r.Workspaces[i].Repos[b].ID })
	}
	sort.Slice(r.Workspaces, func(a, b int) bool { return r.Workspaces[a].Name < r.Workspaces[b].Name })
	if err = r.Validate(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return WriteAtomic(filepath.Join(dir, RegistryFile), append(data, '\n'))
}

// WriteAtomic replaces path with data through a synced temporary file and a
// rename in the same directory.
func WriteAtomic(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(tmp, path)
	}
	if err != nil {
		os.Remove(tmp)
	}
	return err
}

func lock(path string) (func(), error) {
	token, err := lockToken()
	if err != nil {
		return nil, fmt.Errorf("workspace registry lock: %w", err)
	}
	deadline := time.Now().Add(lockTimeout)
	for {
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err == nil {
			_, err = f.WriteString(token)
			if closeErr := f.Close(); err == nil {
				err = closeErr
			}
			if err != nil {
				os.Remove(path)
				return nil, fmt.Errorf("workspace registry lock: %w", err)
			}
			return func() { unlockOwned(path, token) }, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, fmt.Errorf("workspace registry lock: %w", err)
		}
		if breakStale(path) {
			continue
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("workspace registry is locked by another radar process (%s); retry, or remove the lock file if no radar workspace command is running", path)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// lockToken identifies one lock holder: its process and a random suffix.
func lockToken() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return strconv.Itoa(os.Getpid()) + " " + hex.EncodeToString(b) + "\n", nil
}

// unlockOwned removes the lock only while it still holds token, so a writer
// whose stale lock was taken over never removes its successor's lock.
func unlockOwned(path, token string) {
	if b, err := os.ReadFile(path); err == nil && string(b) == token {
		os.Remove(path)
	}
}

// breakStale removes a lock older than staleLock. Waiters remove it only
// while holding a second lock and after checking its age again there, so a
// waiter that saw the stale lock never removes the fresh lock another waiter
// took after breaking it. It reports whether the caller should retry now.
func breakStale(path string) bool {
	if info, err := os.Stat(path); err != nil || time.Since(info.ModTime()) <= staleLock {
		return false
	}
	breaker := path + ".break"
	f, err := os.OpenFile(breaker, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		// Breaking takes milliseconds; a break lock this old was left by a
		// waiter that crashed while breaking.
		if info, statErr := os.Stat(breaker); statErr == nil && time.Since(info.ModTime()) > staleLock {
			os.Remove(breaker)
		}
		return false
	}
	f.Close()
	defer os.Remove(breaker)
	if info, err := os.Stat(path); err == nil && time.Since(info.ModTime()) > staleLock {
		os.Remove(path)
	}
	return true
}
