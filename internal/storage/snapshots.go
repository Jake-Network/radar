package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/radar-engine/radar/internal/graph"
	"github.com/radar-engine/radar/internal/model"
)

// KeepWorkingTreeSnapshots bounds informational working-tree snapshots per
// repository. Commit snapshots are retained because plans pin them.
const KeepWorkingTreeSnapshots = 10

func (s *Store) SaveSnapshot(ctx context.Context, snap model.Snapshot) error {
	if _, err := graph.New(snap); err != nil {
		return err
	}
	if snap.Repository == "" || snap.Revision == "" {
		return fmt.Errorf("snapshot requires repository and revision")
	}
	raw, err := json.Marshal(snap)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Same-revision reindex is replaced within one transaction; other versions remain.
	if _, err = tx.ExecContext(ctx, `INSERT INTO snapshots VALUES(?,?,?,?) ON CONFLICT(repository,revision) DO UPDATE SET indexed_at=excluded.indexed_at,payload=excluded.payload`, snap.Repository, snap.Revision, time.Now().UTC().Format(time.RFC3339Nano), raw); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM snapshots WHERE repository=? AND revision LIKE 'WORKTREE%' AND revision NOT IN (SELECT revision FROM snapshots WHERE repository=? AND revision LIKE 'WORKTREE%' ORDER BY indexed_at DESC LIMIT ?)`, snap.Repository, snap.Repository, KeepWorkingTreeSnapshots); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) Snapshot(ctx context.Context, repository, revision string) (model.Snapshot, error) {
	var b []byte
	var err error
	if revision == "" {
		err = s.db.QueryRowContext(ctx, `SELECT payload FROM snapshots WHERE repository=? ORDER BY indexed_at DESC LIMIT 1`, repository).Scan(&b)
	} else {
		err = s.db.QueryRowContext(ctx, `SELECT payload FROM snapshots WHERE repository=? AND revision=?`, repository, revision).Scan(&b)
	}
	var snap model.Snapshot
	if err != nil {
		return snap, fmt.Errorf("no indexed snapshot: run radar index: %w", err)
	}
	err = json.Unmarshal(b, &snap)
	return snap, err
}
