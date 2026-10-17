package indexer

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/Jake-Network/radar/internal/buildidentity"
)

// The format version is separate from the automatic linker build identity. A new
// extractor binary invalidates disk syntax without a manual developer bump.
const parseCacheVersion = "radar-syntax-3"

var parserIdentity struct {
	sync.Once
	value string
}

func parserFingerprint() string {
	parserIdentity.Do(func() { parserIdentity.value = fingerprintForBuild(buildidentity.Identity()) })
	return parserIdentity.value
}
func fingerprintForBuild(build string) string {
	sum := sha256.Sum256([]byte(parseCacheVersion + "\x00" + build))
	return hex.EncodeToString(sum[:])
}

type CacheStats struct {
	Hits      uint64 `json:"hits"`
	Misses    uint64 `json:"misses"`
	Entries   int    `json:"entries"`
	Bytes     int64  `json:"bytes"`
	Evictions uint64 `json:"evictions"`
}

func (c *ParseCache) Stats() CacheStats {
	if c == nil {
		return CacheStats{}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return CacheStats{Hits: c.hits, Misses: c.misses, Entries: len(c.entries), Bytes: c.bytes, Evictions: c.evictions}
}

type diskCache struct {
	Version string                `json:"version"`
	Digest  string                `json:"digest"`
	Entries map[string]cacheEntry `json:"entries"`
}

// LoadParseCache loads syntax only. Callers must rebuild file inventory, imports,
// manifests and resolution on every Build. Missing/corrupt caches are disposable;
// an error must never be converted into evidence about source correctness.
func LoadParseCache(name string) (*ParseCache, error) {
	c := NewParseCache()
	info, err := os.Lstat(name)
	if os.IsNotExist(err) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	if !info.Mode().IsRegular() || info.Size() > MaxTotalSourceBytes {
		return c, fmt.Errorf("invalid parse cache file")
	}
	f, err := os.Open(name)
	if err != nil {
		return c, err
	}
	defer f.Close()
	var disk diskCache
	decoder := json.NewDecoder(io.LimitReader(f, MaxTotalSourceBytes+1))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&disk); err != nil {
		return c, err
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return c, fmt.Errorf("trailing parse cache data")
	}
	if disk.Version != parserFingerprint() {
		return c, fmt.Errorf("parse cache parser version changed")
	}
	raw, err := json.Marshal(disk.Entries)
	if err != nil {
		return c, err
	}
	sum := sha256.Sum256(raw)
	if disk.Digest != hex.EncodeToString(sum[:]) {
		return c, fmt.Errorf("parse cache integrity mismatch")
	}
	keys := make([]string, 0, len(disk.Entries))
	for k := range disk.Entries {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := disk.Entries[keys[i]], disk.Entries[keys[j]]
		if a.Used != b.Used {
			return a.Used < b.Used
		}
		return keys[i] < keys[j]
	})
	if len(keys) > MaxSourceFiles {
		return c, fmt.Errorf("parse cache entry limit exceeded")
	}
	for _, k := range keys {
		if !strings.HasPrefix(k, disk.Version+"\x00") {
			return NewParseCache(), fmt.Errorf("parse cache key version mismatch")
		}
		entry := disk.Entries[k]
		if entry.SourceBytes < 0 || entry.SourceBytes > MaxSourceBytes {
			return NewParseCache(), fmt.Errorf("invalid cached source byte count")
		}
		c.put(k, entry.Result, entry.SourceBytes)
	}
	return c, nil
}

// Save writes a bounded snapshot atomically. Cache contents are local acceleration
// data, not authenticated evidence. Concurrent writers safely replace whole files.
func (c *ParseCache) Save(name string) error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	entries := make(map[string]cacheEntry, len(c.entries))
	for k, v := range c.entries {
		v.Result = cloneParseResult(v.Result, "", "")
		entries[k] = v
	}
	c.mu.Unlock()
	raw, err := json.Marshal(entries)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(raw)
	data, err := json.Marshal(diskCache{parserFingerprint(), hex.EncodeToString(sum[:]), entries})
	if err != nil {
		return err
	}
	if len(data) > MaxTotalSourceBytes {
		return fmt.Errorf("serialized parse cache exceeds size limit")
	}
	dir := filepath.Dir(name)
	if err = os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	// Refuse a symlinked parent; callers normally use the canonical project state directory.
	canonical, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return err
	}
	absolute, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	if canonical != absolute {
		return fmt.Errorf("parse cache parent is not canonical")
	}
	if st, e := os.Lstat(name); e == nil && !st.Mode().IsRegular() {
		return fmt.Errorf("parse cache destination is not regular")
	}
	f, err := os.CreateTemp(dir, ".parse-cache-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), name)
}
