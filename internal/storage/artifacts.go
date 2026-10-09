package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// Artifact reads a persisted evidence or report artifact; callers validate its kind.
func (s *Store) Artifact(ctx context.Context, id string) (string, json.RawMessage, error) {
	var kind string
	var raw []byte
	e := s.db.QueryRowContext(ctx, `SELECT kind,payload FROM artifacts WHERE id=?`, id).Scan(&kind, &raw)
	if e != nil {
		return "", nil, fmt.Errorf("artifact %q unavailable: %w", id, e)
	}
	return kind, json.RawMessage(raw), nil
}

// SaveEvidence is insert-only: an existing proof cannot be overwritten by a new value.
func (s *Store) SaveEvidence(ctx context.Context, id string, value any) error {
	b, e := json.Marshal(value)
	if e != nil {
		return e
	}
	_, e = s.db.ExecContext(ctx, `INSERT INTO artifacts VALUES(?,?,datetime('now'),?)`, id, "test_evidence", b)
	return e
}

func (s *Store) SaveArtifact(ctx context.Context, id, kind string, value any) error {
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO artifacts VALUES(?,?,?,?) ON CONFLICT(id) DO UPDATE SET kind=excluded.kind,recorded_at=excluded.recorded_at,payload=excluded.payload`, id, kind, time.Now().UTC().Format(time.RFC3339Nano), b)
	return err
}
