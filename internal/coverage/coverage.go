// Package coverage stores bounded, candidate-bound runtime file observations.
// A command's coverage is a suite relationship, not proof that each case ran.
package coverage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/Jake-Network/radar/internal/indexer"
	"github.com/Jake-Network/radar/internal/pathutil"
	_ "modernc.org/sqlite"
)

const MaxBytes = 16 << 20
const MaxEdges = 50000

type Edge struct {
	Test     string `json:"test"`
	File     string `json:"file"`
	Commit   string `json:"commit"`
	Baseline string `json:"baseline"`
	Command  string `json:"command"`
	Stale    bool   `json:"stale"`
}
type Store struct{ db *sql.DB }

func Open(filename string) (*Store, error) {
	if filename == "" {
		return nil, fmt.Errorf("coverage database path required")
	}
	if _, err := pathutil.StatePath(filepath.Dir(filename), filepath.Base(filename)); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(filename), 0700); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", filename)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`PRAGMA busy_timeout=5000; CREATE TABLE IF NOT EXISTS coverage_observations (repository TEXT NOT NULL, commit_sha TEXT NOT NULL, command_id TEXT NOT NULL, baseline_sha TEXT NOT NULL, test TEXT NOT NULL, file TEXT NOT NULL, test_hash TEXT NOT NULL, file_hash TEXT NOT NULL, PRIMARY KEY(repository,commit_sha,command_id,test,file));`)
	if err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}
func (s *Store) Close() error { return s.db.Close() }
func digest(b []byte) string  { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

// Record validates files against an immutable provider. Caller must only record
// a successful recognized test execution whose source remained unchanged.
func (s *Store) Record(ctx context.Context, p indexer.Provider, repository, commit, command string, tests, files []string) error {
	return s.RecordAtBaseline(ctx, p, repository, commit, commit, command, tests, files)
}
func (s *Store) RecordAtBaseline(ctx context.Context, p indexer.Provider, repository, baseline, commit, command string, tests, files []string) error {
	if repository == "" || baseline == "" || commit == "" || command == "" || len(tests) == 0 || len(files) == 0 || len(tests)*len(files) > MaxEdges {
		return fmt.Errorf("bounded coverage identity, tests and files required")
	}
	hashes, err := readHashes(ctx, p, append(append([]string{}, tests...), files...))
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var existing int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM coverage_observations WHERE repository=?`, repository).Scan(&existing); err != nil {
		return err
	}
	if existing+len(tests)*len(files) > MaxEdges {
		return fmt.Errorf("coverage storage budget exceeded")
	}
	for _, test := range tests {
		for _, file := range files {
			_, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO coverage_observations VALUES (?,?,?,?,?,?,?,?)`, repository, commit, command, baseline, test, file, hashes[test], hashes[file])
			if err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

// Load retains stale observations for diagnostics but selection must never use
// them. Both test and covered-file contents must match the current baseline.
func (s *Store) Load(ctx context.Context, p indexer.Provider, repository string) ([]Edge, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT commit_sha,command_id,baseline_sha,test,file,test_hash,file_hash FROM coverage_observations WHERE repository=? ORDER BY commit_sha,command_id,test,file LIMIT ?`, repository, MaxEdges+1)
	if err != nil {
		return nil, err
	}
	type record struct {
		edge               Edge
		testHash, fileHash string
	}
	var records []record
	var paths []string
	for rows.Next() {
		var r record
		if err := rows.Scan(&r.edge.Commit, &r.edge.Command, &r.edge.Baseline, &r.edge.Test, &r.edge.File, &r.testHash, &r.fileHash); err != nil {
			rows.Close()
			return nil, err
		}
		records = append(records, r)
		paths = append(paths, r.edge.Test, r.edge.File)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if len(records) > MaxEdges {
		return nil, fmt.Errorf("coverage observation budget exceeded")
	}
	hashes, _ := readHashes(ctx, p, paths)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	out := []Edge{}
	for _, r := range records {
		r.edge.Stale = hashes[r.edge.Test] != r.testHash || hashes[r.edge.File] != r.fileHash
		out = append(out, r.edge)
	}
	return out, nil
}
func readHashes(ctx context.Context, p indexer.Provider, paths []string) (map[string]string, error) {
	hashes := map[string]string{}
	wanted := map[string]bool{}
	for _, f := range paths {
		clean, err := pathutil.RepoRelative(f)
		if err != nil || clean != f {
			return hashes, fmt.Errorf("unsafe observed path")
		}
		wanted[f] = true
	}
	entries, err := p.Entries(ctx)
	if err != nil {
		return hashes, err
	}
	var total int64
	for _, e := range entries {
		if !wanted[e.Path] {
			continue
		}
		if e.Mode != indexer.ModeRegular || e.Size > 1<<20 {
			return hashes, fmt.Errorf("unsafe or oversized observed file")
		}
		b, err := p.Read(ctx, e, 1<<20)
		if err != nil {
			return hashes, err
		}
		total += int64(len(b))
		if total > MaxBytes {
			return hashes, fmt.Errorf("coverage source read budget exceeded")
		}
		hashes[e.Path] = digest(b)
	}
	for f := range wanted {
		if _, ok := hashes[f]; !ok {
			return hashes, fmt.Errorf("observed file missing from baseline")
		}
	}
	return hashes, nil
}

// Parse supports coverage.py JSON, Go coverprofile, JaCoCo XML, llvm-cov export
// and Istanbul/c8 JSON. Aggregate reports describe the executed suite only.
func Parse(format string, data []byte, root string) ([]string, error) {
	return ParseAt(format, data, root, ".")
}

// ParseAt interprets relative report paths in the command's CWD while keeping
// absolute in-repository paths anchored at the repository root.
func ParseAt(format string, data []byte, root, cwd string) ([]string, error) {
	if cwd == "" {
		cwd = "."
	}
	if _, err := pathutil.RepoRelative(cwd); err != nil {
		return nil, err
	}
	if len(data) > MaxBytes {
		return nil, fmt.Errorf("coverage report exceeds size limit")
	}
	var paths []string
	switch format {
	case "python", "c8":
		if format == "python" {
			var report struct {
				Files map[string]struct {
					Executed []int `json:"executed_lines"`
				} `json:"files"`
			}
			if err := json.Unmarshal(data, &report); err != nil {
				return nil, err
			}
			for p, f := range report.Files {
				if len(f.Executed) > 0 {
					paths = append(paths, p)
				}
			}
		} else {
			var report map[string]struct {
				Path   string         `json:"path"`
				Counts map[string]int `json:"s"`
			}
			if err := json.Unmarshal(data, &report); err != nil {
				return nil, err
			}
			for key, f := range report {
				for _, n := range f.Counts {
					if n > 0 {
						p := f.Path
						if p == "" {
							p = key
						}
						paths = append(paths, p)
						break
					}
				}
			}
		}
	case "go":
		lines := strings.Split(string(data), "\n")
		if len(lines) == 0 || !strings.HasPrefix(lines[0], "mode: ") {
			return nil, fmt.Errorf("invalid Go coverprofile")
		}
		for _, line := range lines[1:] {
			if strings.TrimSpace(line) == "" {
				continue
			}
			fields := strings.Fields(line)
			if len(fields) != 3 {
				return nil, fmt.Errorf("invalid Go coverage record")
			}
			count, err := strconv.Atoi(fields[2])
			if err != nil {
				return nil, err
			}
			cut := strings.LastIndex(fields[0], ":")
			if cut < 1 {
				return nil, fmt.Errorf("invalid Go coverage path")
			}
			if count > 0 {
				paths = append(paths, fields[0][:cut])
			}
		}
	case "jacoco":
		var report struct {
			Packages []struct {
				Name    string `xml:"name,attr"`
				Sources []struct {
					Name  string `xml:"name,attr"`
					Lines []struct {
						Covered int `xml:"ci,attr"`
					} `xml:"line"`
				} `xml:"sourcefile"`
			} `xml:"package"`
		}
		if err := xml.Unmarshal(data, &report); err != nil {
			return nil, err
		}
		for _, pkg := range report.Packages {
			for _, s := range pkg.Sources {
				for _, l := range s.Lines {
					if l.Covered > 0 {
						paths = append(paths, pkg.Name+"/"+s.Name)
						break
					}
				}
			}
		}
	case "llvm":
		var report struct {
			Data []struct {
				Files []struct {
					Filename string              `json:"filename"`
					Segments [][]json.RawMessage `json:"segments"`
				} `json:"files"`
			} `json:"data"`
		}
		if err := json.Unmarshal(data, &report); err != nil {
			return nil, err
		}
		for _, d := range report.Data {
			for _, f := range d.Files {
				for _, s := range f.Segments {
					if len(s) < 4 {
						continue
					}
					var count int
					var hasCount bool
					_ = json.Unmarshal(s[2], &count)
					_ = json.Unmarshal(s[3], &hasCount)
					if count > 0 && hasCount {
						paths = append(paths, f.Filename)
						break
					}
				}
			}
		}
	case "files":
		// Python audit hook writes one JSON pathname per line, including data files.
		dec := json.NewDecoder(bytes.NewReader(data))
		for {
			var p string
			err := dec.Decode(&p)
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, err
			}
			paths = append(paths, p)
		}
	default:
		return nil, fmt.Errorf("unsupported coverage format %q", format)
	}
	seen := map[string]bool{}
	out := []string{}
	for _, p := range paths {
		if filepath.IsAbs(p) {
			rel, err := filepath.Rel(root, p)
			if err != nil {
				continue
			}
			p = filepath.ToSlash(rel)
		} else if format != "go" && format != "jacoco" && cwd != "." {
			if _, err := pathutil.RepoRelative(p); err != nil {
				return nil, err
			}
			p = filepath.ToSlash(filepath.Join(cwd, p))
		}
		p, err := pathutil.RepoRelative(p)
		if err != nil {
			return nil, fmt.Errorf("coverage contains an outside-repository path")
		}
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
		if len(out) > MaxEdges {
			return nil, fmt.Errorf("coverage file budget exceeded")
		}
	}
	sort.Strings(out)
	return out, nil
}

// Resolve matches tool module paths (Go/JaCoCo) to unique repository suffixes.
// Ambiguous matches are omitted; they must not become observation evidence.
func Resolve(paths []string, entries []indexer.Entry) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, p := range paths {
		matches := []string{}
		for _, e := range entries {
			if e.Mode != indexer.ModeRegular {
				continue
			}
			if e.Path == p {
				matches = append(matches, e.Path)
			}
		}
		if len(matches) == 1 && !seen[matches[0]] {
			seen[matches[0]] = true
			out = append(out, matches[0])
		}
	}
	sort.Strings(out)
	return out
}

// ResolveOwned strips only a declared Go module path, and resolves JaCoCo's
// package paths against unambiguous conventional committed Java source roots.
// Generic/Python reports require exact repository paths.
func ResolveOwned(ctx context.Context, format string, paths []string, p indexer.Provider) ([]string, error) {
	entries, err := p.Entries(ctx)
	if err != nil {
		return nil, err
	}
	if format != "go" && format != "jacoco" {
		return Resolve(paths, entries), nil
	}
	if format == "jacoco" {
		out := []string{}
		for _, f := range paths {
			matches := []string{}
			for _, e := range entries {
				if e.Mode == indexer.ModeRegular && (e.Path == f || strings.HasSuffix(e.Path, "/src/main/java/"+f) || strings.HasSuffix(e.Path, "/src/test/java/"+f) || e.Path == "src/main/java/"+f || e.Path == "src/test/java/"+f) {
					matches = append(matches, e.Path)
				}
			}
			if len(matches) == 1 {
				out = append(out, matches[0])
			}
		}
		return Resolve(out, entries), nil
	}
	type module struct{ prefix, root string }
	var modules []module
	for _, e := range entries {
		if filepath.Base(e.Path) != "go.mod" || e.Mode != indexer.ModeRegular || e.Size > 1<<20 {
			continue
		}
		b, err := p.Read(ctx, e, 1<<20)
		if err != nil {
			return nil, err
		}
		for _, line := range strings.Split(string(b), "\n") {
			fields := strings.Fields(line)
			if len(fields) == 2 && fields[0] == "module" {
				modules = append(modules, module{strings.Trim(fields[1], "\""), filepath.ToSlash(filepath.Dir(e.Path))})
				break
			}
		}
	}
	out := []string{}
	for _, f := range paths {
		for _, m := range modules {
			if strings.HasPrefix(f, m.prefix+"/") {
				out = append(out, filepath.ToSlash(filepath.Join(m.root, strings.TrimPrefix(f, m.prefix+"/"))))
			}
		}
	}
	return Resolve(out, entries), nil
}
