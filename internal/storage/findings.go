package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/radar-engine/radar/internal/model"
	"time"
)

func (s *Store) SaveFindings(ctx context.Context, findings []model.Finding) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, f := range findings {
		b, e := json.Marshal(f)
		if e != nil {
			return e
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO findings VALUES(?,?,?) ON CONFLICT(id) DO UPDATE SET recorded_at=excluded.recorded_at,payload=excluded.payload`, f.ID, time.Now().UTC().Format(time.RFC3339Nano), b); err != nil {
			return err
		}
	}
	return tx.Commit()
}
func (s *Store) Finding(ctx context.Context, id string) (model.Finding, error) {
	var b []byte
	err := s.db.QueryRowContext(ctx, `SELECT payload FROM findings WHERE id=?`, id).Scan(&b)
	var f model.Finding
	if err != nil {
		return f, fmt.Errorf("finding %q unavailable: %w", id, err)
	}
	err = json.Unmarshal(b, &f)
	return f, err
}
