package migrations

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strings"
)

// metadataProblem binds an integrity problem to the exact source blobs. A
// previous malformed directory cannot clear a different newly broken input.
func (h *history) metadataProblem(key, reason string, files ...File) {
	is := issue{Key: "integrity:" + key, Kind: kindIntegrity, Parent: reason, Paths: map[string]string{}, Lines: map[string]int{}}
	for _, f := range files {
		m := f.Path + "#" + f.OID
		is.Members = append(is.Members, m)
		is.Paths[m] = f.Path
		h.members[m] = true
	}
	sort.Strings(is.Members)
	h.issues = append(h.issues, is)
}

func (h *history) atlasIssues(l location, files []File, read Reader) {
	var sum File
	sql := []File{}
	for _, f := range files {
		if path.Base(f.Path) == "atlas.sum" {
			sum = f
		} else if strings.HasSuffix(f.Path, ".sql") && path.Dir(f.Path) != l.dir {
			h.unknown = append(h.unknown, f.Path+": nested Atlas migration directory is unsupported")
		} else if path.Dir(f.Path) == l.dir && strings.HasSuffix(f.Path, ".sql") {
			sql = append(sql, f)
		}
	}
	if sum.Path == "" {
		h.unknown = append(h.unknown, "Atlas integrity file atlas.sum is missing")
		return
	}
	data, err := read(sum.OID)
	if err != nil {
		h.unknown = append(h.unknown, "Atlas integrity file cannot be read")
		return
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) == 0 || !strings.HasPrefix(lines[0], "h1:") {
		h.unknown = append(h.unknown, "malformed Atlas integrity file")
		return
	}
	listed := map[string]string{}
	order := []string{}
	aggregate := sha256.New()
	for _, line := range lines[1:] {
		parts := strings.Fields(line)
		if len(parts) != 2 || !strings.HasPrefix(parts[1], "h1:") || path.Base(parts[0]) != parts[0] || listed[parts[0]] != "" {
			h.unknown = append(h.unknown, "malformed or duplicate Atlas checksum entry")
			return
		}
		name, hash := parts[0], strings.TrimPrefix(parts[1], "h1:")
		if b, e := base64.StdEncoding.DecodeString(hash); e != nil || len(b) != sha256.Size {
			h.unknown = append(h.unknown, "malformed Atlas checksum")
			return
		}
		listed[name] = hash
		order = append(order, name)
		aggregate.Write([]byte(name))
		aggregate.Write([]byte(hash))
	}
	if lines[0] != "h1:"+base64.StdEncoding.EncodeToString(aggregate.Sum(nil)) {
		h.metadataProblem("sum", "Atlas integrity file aggregate checksum does not match its entries", sum)
		return
	}
	sort.Slice(sql, func(i, j int) bool { return sql[i].Path < sql[j].Path })
	cumulative := sha256.New()
	seen := map[string]bool{}
	for i, f := range sql {
		name := path.Base(f.Path)
		content, e := read(f.OID)
		if e != nil {
			h.unknown = append(h.unknown, f.Path+": cannot read Atlas migration")
			continue
		}
		if strings.Contains(string(content), "atlas:sum") {
			h.unknown = append(h.unknown, f.Path+": Atlas checksum directives are not resolved")
			continue
		}
		cumulative.Write([]byte(name))
		cumulative.Write(content)
		seen[name] = true
		if i >= len(order) || order[i] != name || listed[name] != base64.StdEncoding.EncodeToString(cumulative.Sum(nil)) {
			h.metadataProblem(name, "Atlas migration contents or file order do not match atlas.sum", sum, f)
		}
	}
	for _, name := range order {
		if !seen[name] {
			h.metadataProblem(name, "Atlas integrity file names a migration that is missing", sum)
		}
	}
}

func (h *history) drizzleIssues(l location, files []File, read Reader) {
	var journal File
	byPath := map[string]File{}
	for _, f := range files {
		byPath[f.Path] = f
		if f.Path == path.Join(l.dir, "meta/_journal.json") {
			journal = f
		}
	}
	if journal.Path == "" {
		h.unknown = append(h.unknown, "Drizzle journal-free migration format is not yet resolved")
		return
	}
	data, e := read(journal.OID)
	if e != nil {
		h.unknown = append(h.unknown, "Drizzle journal cannot be read")
		return
	}
	var j struct {
		Version string `json:"version"`
		Entries []struct {
			Idx *int   `json:"idx"`
			Tag string `json:"tag"`
		} `json:"entries"`
	}
	if json.Unmarshal(data, &j) != nil || j.Entries == nil || (j.Version != "5" && j.Version != "6" && j.Version != "7") {
		h.unknown = append(h.unknown, "malformed Drizzle journal")
		return
	}
	indices := map[int]bool{}
	tags := map[string]bool{}
	for _, entry := range j.Entries {
		if entry.Idx == nil || *entry.Idx < 0 || entry.Tag == "" || path.Base(entry.Tag) != entry.Tag || entry.Tag == ".." || strings.ContainsAny(entry.Tag, "\\\x00") {
			h.unknown = append(h.unknown, "nonliteral Drizzle journal identity")
			continue
		}
		if indices[*entry.Idx] || tags[entry.Tag] {
			h.metadataProblem(fmt.Sprint(*entry.Idx)+":"+entry.Tag, "Drizzle journal repeats an index or migration tag", journal)
		}
		indices[*entry.Idx] = true
		tags[entry.Tag] = true
		if _, exists := byPath[path.Join(l.dir, entry.Tag+".sql")]; !exists {
			h.metadataProblem(entry.Tag, "Drizzle journal names a migration SQL file that is missing", journal)
		}
	}
	// Snapshot identities establish lineage only; no SQL semantic compatibility
	// is inferred from a clean snapshot graph.
	parents := map[string][]File{}
	ids := map[string]File{}
	for _, f := range files {
		if !strings.HasSuffix(f.Path, "_snapshot.json") {
			continue
		}
		b, e := read(f.OID)
		var snapshot struct {
			ID   string `json:"id"`
			Prev string `json:"prevId"`
		}
		if e != nil || json.Unmarshal(b, &snapshot) != nil || snapshot.ID == "" || snapshot.Prev == "" {
			h.unknown = append(h.unknown, f.Path+": unsupported Drizzle snapshot identity")
			continue
		}
		if old, ok := ids[snapshot.ID]; ok {
			h.metadataProblem(snapshot.ID, "Drizzle snapshots repeat an identity", old, f)
		}
		ids[snapshot.ID] = f
		parents[snapshot.Prev] = append(parents[snapshot.Prev], f)
	}
	for _, parent := range sortedKeys(keysOf(parents)) {
		if len(parents[parent]) > 1 {
			h.metadataProblem(parent, "Drizzle snapshot lineage forks at the same predecessor", parents[parent]...)
		}
	}
}
