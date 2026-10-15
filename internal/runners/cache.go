package runners

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Jake-Network/radar/internal/pathutil"
)

// Cache stores dependency artifacts only, never source, output or environment.
// Entries are published atomically and operations share a bounded lock.
type Cache struct {
	Dir      string
	MaxBytes int64
	// SourceRoot is the current private candidate root, supplied by execution.
	// It relocates editable environment metadata without persisting old paths.
	SourceRoot string
}

func (c Cache) locked(ctx context.Context) (func(), error) {
	if c.MaxBytes <= 0 || c.MaxBytes > 10<<30 {
		return nil, fmt.Errorf("invalid cache budget")
	}
	if err := os.MkdirAll(c.Dir, 0700); err != nil {
		return nil, err
	}
	info, err := os.Lstat(c.Dir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("unsafe cache root")
	}
	lock := filepath.Join(c.Dir, ".lock")
	waitCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	for {
		err := os.Mkdir(lock, 0700)
		if err == nil {
			return func() { _ = os.Remove(lock) }, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return nil, err
		}
		select {
		case <-waitCtx.Done():
			return nil, fmt.Errorf("dependency cache busy: %w", waitCtx.Err())
		case <-time.After(20 * time.Millisecond):
		}
	}
}
func validKey(key string) bool {
	if len(key) != 64 {
		return false
	}
	for _, r := range key {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}

func (c Cache) Restore(ctx context.Context, key, root string, p Profile) (bool, error) {
	if !validKey(key) {
		return false, fmt.Errorf("invalid cache key")
	}
	if err := Validate(p); err != nil {
		return false, err
	}
	release, err := c.locked(ctx)
	if err != nil {
		return false, err
	}
	defer release()
	entry := filepath.Join(c.Dir, key)
	info, err := os.Lstat(entry)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return false, fmt.Errorf("unsafe cache entry")
	}
	// Stage each artifact and rename only after validation. Existing artifacts
	// must never be overwritten (including committed dependency directories).
	stage, err := os.MkdirTemp(root, ".radar-cache-restore-")
	if err != nil {
		return false, err
	}
	defer os.RemoveAll(stage)
	var total int64
	for _, a := range p.Artifacts {
		dst, err := pathutil.StatePath(root, a)
		if err != nil {
			return false, err
		}
		if _, err := os.Lstat(dst); err == nil {
			return false, fmt.Errorf("dependency artifact already exists: %s", a)
		} else if !errors.Is(err, fs.ErrNotExist) {
			return false, err
		}
		if err := copyArtifact(ctx, filepath.Join(entry, a), filepath.Join(stage, a), filepath.Join(root, a), c.SourceRoot, &total, c.MaxBytes); err != nil {
			return false, err
		}
	}
	var moved []string
	for _, a := range p.Artifacts {
		dst, _ := pathutil.StatePath(root, a)
		if err = os.MkdirAll(filepath.Dir(dst), 0700); err == nil {
			err = os.Rename(filepath.Join(stage, a), dst)
		}
		if err != nil {
			for _, f := range moved {
				_ = os.RemoveAll(f)
			}
			return false, err
		}
		moved = append(moved, dst)
	}
	now := time.Now()
	_ = os.Chtimes(entry, now, now)
	return true, nil
}

func (c Cache) Store(ctx context.Context, key, root string, p Profile) error {
	if !validKey(key) {
		return fmt.Errorf("invalid cache key")
	}
	if err := Validate(p); err != nil {
		return err
	}
	release, err := c.locked(ctx)
	if err != nil {
		return err
	}
	defer release()
	stage, err := os.MkdirTemp(c.Dir, ".entry-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	var total int64
	for _, a := range p.Artifacts {
		src, err := pathutil.StatePath(root, a)
		if err != nil {
			return err
		}
		if err := copyArtifact(ctx, src, filepath.Join(stage, a), "__RADAR_VENV_ROOT__", c.SourceRoot, &total, c.MaxBytes); err != nil {
			return err
		}
	}
	dest := filepath.Join(c.Dir, key)
	if _, err := os.Lstat(dest); err == nil {
		return nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err := c.prune(total); err != nil {
		return err
	}
	return os.Rename(stage, dest)
}

func (c Cache) prune(incoming int64) error {
	type entry struct {
		name string
		size int64
		time time.Time
	}
	var entries []entry
	var total int64
	dirs, err := os.ReadDir(c.Dir)
	if err != nil {
		return err
	}
	for _, d := range dirs {
		if !validKey(d.Name()) {
			continue
		}
		if !d.IsDir() {
			return fmt.Errorf("unsafe cache entry")
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		var size int64
		err = filepath.WalkDir(filepath.Join(c.Dir, d.Name()), func(_ string, d fs.DirEntry, e error) error {
			if e != nil {
				return e
			}
			if d.Type().IsRegular() {
				i, e := d.Info()
				if e != nil {
					return e
				}
				size += i.Size()
			}
			return nil
		})
		if err != nil {
			return err
		}
		total += size
		entries = append(entries, entry{d.Name(), size, info.ModTime()})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].time.Before(entries[j].time) })
	for _, e := range entries {
		if total+incoming <= c.MaxBytes {
			break
		}
		if err := os.RemoveAll(filepath.Join(c.Dir, e.name)); err != nil {
			return err
		}
		total -= e.size
	}
	return nil
}

func copyArtifact(ctx context.Context, src, dst, relocate, sourceRoot string, total *int64, limit int64) error {
	count := 0
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		count++
		if count > 100000 {
			return fmt.Errorf("dependency artifact file budget exceeded")
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		base := strings.ToLower(d.Name())
		if base == ".env" || strings.HasPrefix(base, ".env.") || base == ".npmrc" || base == ".pypirc" || base == "credentials" || base == "id_rsa" || base == "id_ed25519" {
			return fmt.Errorf("sensitive dependency artifact refused")
		}
		if info.Mode()&os.ModeSymlink != 0 {
			link, err := os.Readlink(p)
			if err != nil {
				return err
			}
			resolved := filepath.Clean(filepath.Join(filepath.Dir(p), link))
			if filepath.IsAbs(link) {
				resolved = link
			}
			inside, err := filepath.Rel(src, resolved)
			internal := err == nil && inside != ".." && !strings.HasPrefix(inside, ".."+string(filepath.Separator))
			// Virtual environments commonly point their interpreter at a system
			// binary. Preserve that link only for the current-platform executable.
			interpreter := filepath.Base(src) == ".venv" && filepath.Dir(rel) == "bin" && strings.HasPrefix(filepath.Base(p), "python") && currentPythonInterpreter(resolved)
			if !internal && !interpreter {
				return fmt.Errorf("dependency symlink escapes artifact")
			}
			if filepath.IsAbs(link) && internal {
				return fmt.Errorf("absolute internal dependency symlink is not relocatable")
			}
			if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
				return err
			}
			return os.Symlink(link, target)
		}
		if d.IsDir() {
			return os.MkdirAll(target, 0700)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("non-regular dependency artifact refused")
		}
		*total += info.Size()
		if *total > limit {
			return fmt.Errorf("dependency cache byte budget exceeded")
		}
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			return err
		}
		// Venv console scripts embed their absolute environment directory. Relocate
		// only a recognized interpreter shebang, never arbitrary library contents.
		if filepath.Base(src) == ".venv" && strings.HasPrefix(filepath.ToSlash(rel), "bin/") && info.Size() < 1<<20 {
			data, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			prefix := "#!" + src + string(filepath.Separator) + "bin" + string(filepath.Separator)
			if strings.HasPrefix(string(data), "#!__RADAR_VENV_ROOT__/bin/") {
				prefix = "#!__RADAR_VENV_ROOT__/bin/"
			}
			if strings.HasPrefix(string(data), prefix) {
				data = []byte("#!" + relocate + "/bin/" + strings.TrimPrefix(string(data), prefix))
				*total += int64(len(data)) - info.Size()
				if *total > limit {
					return fmt.Errorf("relocated dependency exceeds cache byte budget")
				}
				return os.WriteFile(target, data, info.Mode().Perm()&0777)
			}
		}
		// direct_url.json records, as a file URL, the source a local install came from.
		if filepath.Base(src) == ".venv" && base == "direct_url.json" && strings.HasSuffix(filepath.Dir(p), ".dist-info") {
			if info.Size() > 1<<20 || sourceRoot == "" {
				return fmt.Errorf("editable environment metadata cannot be relocated")
			}
			data, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			data, err = relocateDirectURL(data, sourceRoot, relocate == "__RADAR_VENV_ROOT__")
			if err != nil {
				return err
			}
			*total += int64(len(data)) - info.Size()
			if *total > limit {
				return fmt.Errorf("editable metadata exceeds cache byte budget")
			}
			return os.WriteFile(target, data, info.Mode().Perm()&0777)
		}
		if filepath.Base(src) == ".venv" && (strings.HasSuffix(base, ".pth") || strings.HasPrefix(base, "__editable__") && strings.HasSuffix(base, ".py")) {
			if info.Size() > 1<<20 || sourceRoot == "" {
				return fmt.Errorf("editable environment metadata cannot be relocated")
			}
			data, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			text := string(data)
			if relocate == "__RADAR_VENV_ROOT__" {
				text = strings.ReplaceAll(text, sourceRoot, "__RADAR_PROJECT_ROOT__")
			} else {
				text = strings.ReplaceAll(text, "__RADAR_PROJECT_ROOT__", sourceRoot)
			}
			if strings.HasSuffix(base, ".pth") {
				for _, line := range strings.Split(text, "\n") {
					line = strings.TrimSpace(line)
					if strings.HasPrefix(line, "/") && !(relocate != "__RADAR_VENV_ROOT__" && strings.HasPrefix(line, sourceRoot+"/")) {
						return fmt.Errorf("external editable dependency path cannot be cached")
					}
				}
			}
			*total += int64(len(text)) - info.Size()
			if *total > limit {
				return fmt.Errorf("editable metadata exceeds cache byte budget")
			}
			return os.WriteFile(target, []byte(text), info.Mode().Perm()&0777)
		}
		in, err := os.Open(p)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm()&0777)
		if err != nil {
			return err
		}
		_, err = io.Copy(out, io.LimitReader(in, info.Size()+1))
		closeErr := out.Close()
		if err != nil {
			return err
		}
		return closeErr
	})
}

// relocateDirectURL replaces a file URL inside sourceRoot with a placeholder
// when storing, and the placeholder with the current root's file URL when
// restoring. URLs are percent-encoded, so the path is compared, never the text.
func relocateDirectURL(data []byte, sourceRoot string, store bool) ([]byte, error) {
	const placeholder = "__RADAR_PROJECT_ROOT__"
	var record map[string]any
	if err := json.Unmarshal(data, &record); err != nil {
		return nil, fmt.Errorf("editable environment metadata cannot be relocated")
	}
	raw, _ := record["url"].(string)
	switch {
	case store:
		u, err := url.Parse(raw)
		if err != nil || u.Scheme != "file" || (u.Host != "" && u.Host != "localhost") {
			return data, nil
		}
		p := u.Path
		if len(p) > 2 && p[0] == '/' && p[2] == ':' {
			p = p[1:] // file:///C:/x on Windows
		}
		rel, err := filepath.Rel(sourceRoot, filepath.FromSlash(p))
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
			return data, nil
		}
		record["url"] = placeholder + "/" + filepath.ToSlash(rel)
	case strings.HasPrefix(raw, placeholder+"/"):
		p := filepath.ToSlash(filepath.Join(sourceRoot, filepath.FromSlash(strings.TrimPrefix(raw, placeholder+"/"))))
		if !strings.HasPrefix(p, "/") {
			p = "/" + p
		}
		record["url"] = (&url.URL{Scheme: "file", Path: p}).String()
	default:
		return data, nil
	}
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(record); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// The executable selected by the current PATH may live in a hosted toolcache,
// pyenv installation or system directory. Match file identity rather than
// trusting a directory prefix or a Python-shaped filename.
func currentPythonInterpreter(filename string) bool {
	info, err := os.Stat(filename)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&0111 == 0 {
		return false
	}
	for _, name := range []string{"python", "python3"} {
		selected, err := exec.LookPath(name)
		if err != nil {
			continue
		}
		selectedInfo, err := os.Stat(selected)
		if err == nil && os.SameFile(info, selectedInfo) {
			return true
		}
	}
	return false
}
