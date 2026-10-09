package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/radar-engine/radar/internal/graph"
	"github.com/radar-engine/radar/internal/model"
	"time"
)

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
	for _, q := range []string{`DELETE FROM edges WHERE repository=? AND revision=?`, `DELETE FROM nodes WHERE repository=? AND revision=?`, `DELETE FROM snapshots WHERE repository=? AND revision=?`} {
		if _, err = tx.ExecContext(ctx, q, snap.Repository, snap.Revision); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO snapshots VALUES(?,?,?,?)`, snap.Repository, snap.Revision, time.Now().UTC().Format(time.RFC3339Nano), raw); err != nil {
		return err
	}
	for _, n := range snap.Nodes {
		b, e := json.Marshal(n)
		if e != nil {
			return e
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO nodes VALUES(?,?,?,?,?,?)`, snap.Repository, snap.Revision, n.ID, n.Kind, n.Name, b); err != nil {
			return err
		}
	}
	for _, e := range snap.Edges {
		b, x := json.Marshal(e)
		if x != nil {
			return x
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO edges VALUES(?,?,?,?,?,?,?)`, snap.Repository, snap.Revision, e.ID, e.From, e.To, e.Kind, b); err != nil {
			return err
		}
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
