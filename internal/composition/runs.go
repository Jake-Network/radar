package composition

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"

	"github.com/Jake-Network/radar/internal/workspace"
)

// RecordVersion is the run-record format version.
const RecordVersion = 1

// KeepRuns is how many run records each workspace keeps.
const KeepRuns = 50

// Selection is how a run chose its inputs, with every repo spelled out so a
// repeated run resolves it identically from any repository.
type Selection struct {
	// Mode is "auto" (worktree branches) or "named" (positional targets).
	Mode    string   `json:"mode"`
	Targets []string `json:"targets"`
	Bases   []string `json:"bases"`
	With    []string `json:"with"`
	Only    []string `json:"only"`
	// All lists every repo ID in scope before --only.
	All []string `json:"all"`
}

// RecordRepo is the pinned state of one repository in a run.
type RecordRepo struct {
	ID              string   `json:"id"`
	Path            string   `json:"path"`
	CommonDir       string   `json:"common_dir"`
	BaseRef         string   `json:"base_ref"`
	Base            string   `json:"base"`
	BaseSource      string   `json:"base_source"`
	Selection       string   `json:"selection_source"`
	Branches        []Branch `json:"branches"`
	CandidateCommit string   `json:"candidate_commit,omitempty"`
	CandidateTree   string   `json:"candidate_tree,omitempty"`
	Conflicts       []string `json:"conflicts,omitempty"`
	Error           string   `json:"error,omitempty"`
}

// Record is a run's snapshot: enough to repeat its selection (--again) or
// rebuild its exact commits (--replay).
type Record struct {
	Version   int                  `json:"version"`
	RunID     string               `json:"run_id"`
	CreatedAt string               `json:"created_at"`
	Workspace string               `json:"workspace"`
	Key       string               `json:"key"`
	Digest    string               `json:"digest"`
	Verdict   string               `json:"verdict"`
	Selection Selection            `json:"selection"`
	Repos     []RecordRepo         `json:"repos"`
	Team      *Team                `json:"team_file,omitempty"`
	TeamHome  *workspace.ScopeRepo `json:"team_home,omitempty"`
	Links     []Link               `json:"links,omitempty"`
}

// NewRecord captures the pinned state of results. The caller adds the
// link results it reported, so they are not computed twice.
func NewRecord(scope workspace.Scope, selection Selection, results []Result, digest, verdict string, teams ...*Team) Record {
	rec := Record{Version: RecordVersion, Workspace: scope.Workspace, Key: scope.Key, Digest: digest, Verdict: verdict, Selection: selection, Repos: []RecordRepo{}}
	for _, r := range results {
		rr := RecordRepo{ID: r.ID, Path: r.Path, CommonDir: r.CommonDir, BaseRef: r.BaseRef, Base: r.Base, BaseSource: r.BaseSource, Selection: r.Selection, Branches: []Branch{}, Error: r.Error}
		for _, b := range r.Branches {
			rr.Branches = append(rr.Branches, Branch{Ref: b.Ref, Commit: b.Commit, Source: b.Source, Changed: []string{}})
		}
		if r.Report != nil {
			rr.CandidateCommit, rr.CandidateTree, rr.Conflicts = r.Report.CandidateCommit, r.Report.CandidateTree, r.Report.Conflicts
		}
		rec.Repos = append(rec.Repos, rr)
	}
	if len(teams) > 0 && teams[0] != nil {
		copy := *teams[0]
		copy.File = nil
		rec.Team = &copy
		rec.TeamHome = teams[0].locator
	}
	return rec
}

// Store holds the run records of one workspace key.
type Store struct{ Dir string }

// StateDir is RADAR_STATE_DIR, or <user cache dir>/radar. Run records are
// never written inside a repository.
func StateDir() (string, error) {
	if dir := os.Getenv("RADAR_STATE_DIR"); dir != "" {
		return filepath.Abs(dir)
	}
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("no cache directory for run records (set RADAR_STATE_DIR): %w", err)
	}
	return filepath.Join(dir, "radar"), nil
}

var keyPattern = regexp.MustCompile(`^_?[A-Za-z][A-Za-z0-9_-]*$`)

// OpenStore returns the store for a workspace key.
func OpenStore(key string) (Store, error) {
	if !keyPattern.MatchString(key) {
		return Store{}, fmt.Errorf("invalid workspace key %q", key)
	}
	dir, err := StateDir()
	if err != nil {
		return Store{}, err
	}
	return Store{Dir: filepath.Join(dir, "runs", key)}, nil
}

var runPattern = regexp.MustCompile(`^[0-9]{8}T[0-9]{6}\.[0-9]{3}Z-[0-9a-f]{6}$`)

func newRunID(now time.Time) string {
	b := make([]byte, 3)
	rand.Read(b)
	return now.UTC().Format("20060102T150405.000Z") + "-" + hex.EncodeToString(b)
}

// Save writes the record and report under a new run ID and keeps the newest
// KeepRuns runs. Each run is written to a private
// directory and renamed into place, so concurrent runs never overwrite each
// other and readers never see a partial run. report is called once the run
// ID is assigned, so the stored report can carry it.
func (s Store) Save(rec *Record, report func() any) error {
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return err
	}
	now := time.Now()
	rec.Version = RecordVersion
	rec.CreatedAt = now.UTC().Format(time.RFC3339)
	var final string
	for {
		rec.RunID = newRunID(now)
		final = filepath.Join(s.Dir, rec.RunID)
		_, err := os.Stat(final)
		if errors.Is(err, os.ErrNotExist) {
			break
		}
		if err != nil {
			return fmt.Errorf("run records: %w", err)
		}
	}
	tmp, err := os.MkdirTemp(s.Dir, ".tmp-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	for name, value := range map[string]any{"snapshot.json": rec, "report.json": report()} {
		data, err := json.MarshalIndent(value, "", "  ")
		if err != nil {
			return err
		}
		if err = os.WriteFile(filepath.Join(tmp, name), append(data, '\n'), 0o600); err != nil {
			return err
		}
	}
	if err = os.Rename(tmp, final); err != nil {
		return err
	}
	s.prune()
	return nil
}

// ids lists run IDs, newest first.
func (s Store) ids() []string {
	entries, err := os.ReadDir(s.Dir)
	if err != nil {
		return nil
	}
	out := []string{}
	for _, e := range entries {
		if e.IsDir() && runPattern.MatchString(e.Name()) {
			out = append(out, e.Name())
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(out)))
	return out
}

func (s Store) prune() {
	ids := s.ids()
	for i := KeepRuns; i < len(ids); i++ {
		os.RemoveAll(filepath.Join(s.Dir, ids[i]))
	}
}

// ErrNoRun reports a store without the requested run.
var ErrNoRun = errors.New("no recorded run")

// Load reads a run's record by run ID, or the newest kept run with "last".
// "last" is resolved from the run IDs rather than a pointer written by Save:
// with concurrent saves the run finishing last can start earliest, and prune
// may already have removed it.
func (s Store) Load(id string) (Record, error) {
	if id == "last" {
		ids := s.ids()
		if len(ids) == 0 {
			return Record{}, ErrNoRun
		}
		id = ids[0]
	}
	if !runPattern.MatchString(id) {
		return Record{}, fmt.Errorf("%w %q: run IDs look like 20261010T120000.000Z-1a2b3c", ErrNoRun, id)
	}
	b, err := os.ReadFile(filepath.Join(s.Dir, id, "snapshot.json"))
	if err != nil {
		return Record{}, fmt.Errorf("%w %s", ErrNoRun, id)
	}
	var rec Record
	if err = json.Unmarshal(b, &rec); err != nil || rec.Version != RecordVersion {
		return Record{}, fmt.Errorf("run %s has an unreadable record", id)
	}
	return rec, nil
}

// Recent returns up to n records, newest first.
func (s Store) Recent(n int) []Record {
	out := []Record{}
	for _, id := range s.ids() {
		if len(out) == n {
			break
		}
		if rec, err := s.Load(id); err == nil {
			out = append(out, rec)
		}
	}
	return out
}

// RunChange lists how a repeated selection differs from the previous run.
type RunChange struct {
	PreviousRun string   `json:"previous_run"`
	Mode        string   `json:"mode"`
	Added       []string `json:"added"`
	Removed     []string `json:"removed"`
	Moved       []string `json:"moved"`
}

// CompareRun reports which repository branches were added, removed or moved
// to another commit since the previous run.
func CompareRun(previous Record, results []Result) *RunChange {
	c := &RunChange{PreviousRun: previous.RunID, Mode: previous.Selection.Mode, Added: []string{}, Removed: []string{}, Moved: []string{}}
	before := map[string]string{}
	for _, r := range previous.Repos {
		for _, b := range r.Branches {
			before[r.ID+":"+b.Ref] = b.Commit
		}
	}
	now := map[string]bool{}
	for _, r := range results {
		for _, b := range r.Branches {
			key := r.ID + ":" + b.Ref
			now[key] = true
			commit, ok := before[key]
			switch {
			case !ok:
				c.Added = append(c.Added, key)
			case commit != b.Commit:
				c.Moved = append(c.Moved, key)
			}
		}
	}
	for _, r := range previous.Repos {
		for _, b := range r.Branches {
			if key := r.ID + ":" + b.Ref; !now[key] {
				c.Removed = append(c.Removed, key)
			}
		}
	}
	return c
}
