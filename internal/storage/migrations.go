package storage

import (
	"context"
	"fmt"
)

func (s *Store) migrate(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations(version INTEGER PRIMARY KEY);`); err != nil {
		return err
	}
	var v int
	if err = tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(version),0) FROM schema_migrations`).Scan(&v); err != nil {
		return err
	}
	if v > 1 {
		return fmt.Errorf("database schema %d is newer than this Radar", v)
	}
	if v == 0 {
		_, err = tx.ExecContext(ctx, `
 CREATE TABLE snapshots(repository TEXT NOT NULL, revision TEXT NOT NULL, indexed_at TEXT NOT NULL, payload BLOB NOT NULL, PRIMARY KEY(repository,revision));
 CREATE TABLE nodes(repository TEXT NOT NULL, revision TEXT NOT NULL, id TEXT NOT NULL, kind TEXT NOT NULL, name TEXT NOT NULL, payload BLOB NOT NULL, PRIMARY KEY(repository,revision,id), FOREIGN KEY(repository,revision) REFERENCES snapshots(repository,revision) ON DELETE CASCADE);
 CREATE TABLE edges(repository TEXT NOT NULL, revision TEXT NOT NULL, id TEXT NOT NULL, source TEXT NOT NULL, target TEXT NOT NULL, kind TEXT NOT NULL, payload BLOB NOT NULL, PRIMARY KEY(repository,revision,id), FOREIGN KEY(repository,revision,source) REFERENCES nodes(repository,revision,id), FOREIGN KEY(repository,revision,target) REFERENCES nodes(repository,revision,id), FOREIGN KEY(repository,revision) REFERENCES snapshots(repository,revision) ON DELETE CASCADE);
 CREATE INDEX nodes_kind ON nodes(repository,revision,kind);
 CREATE INDEX edges_target ON edges(repository,revision,target);
 CREATE TABLE findings(id TEXT PRIMARY KEY, recorded_at TEXT NOT NULL,payload BLOB NOT NULL);
 CREATE TABLE artifacts(id TEXT PRIMARY KEY,kind TEXT NOT NULL,recorded_at TEXT NOT NULL,payload BLOB NOT NULL);
 INSERT INTO schema_migrations(version) VALUES(1);`)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}
