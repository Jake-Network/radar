package storage

import (
	"context"
	"fmt"
)

const schemaVersion = 2

var migrations = map[int]string{
	1: `
 CREATE TABLE snapshots(repository TEXT NOT NULL, revision TEXT NOT NULL, indexed_at TEXT NOT NULL, payload BLOB NOT NULL, PRIMARY KEY(repository,revision));
 CREATE TABLE nodes(repository TEXT NOT NULL, revision TEXT NOT NULL, id TEXT NOT NULL, kind TEXT NOT NULL, name TEXT NOT NULL, payload BLOB NOT NULL, PRIMARY KEY(repository,revision,id), FOREIGN KEY(repository,revision) REFERENCES snapshots(repository,revision) ON DELETE CASCADE);
 CREATE TABLE edges(repository TEXT NOT NULL, revision TEXT NOT NULL, id TEXT NOT NULL, source TEXT NOT NULL, target TEXT NOT NULL, kind TEXT NOT NULL, payload BLOB NOT NULL, PRIMARY KEY(repository,revision,id), FOREIGN KEY(repository,revision,source) REFERENCES nodes(repository,revision,id), FOREIGN KEY(repository,revision,target) REFERENCES nodes(repository,revision,id), FOREIGN KEY(repository,revision) REFERENCES snapshots(repository,revision) ON DELETE CASCADE);
 CREATE INDEX nodes_kind ON nodes(repository,revision,kind);
 CREATE INDEX edges_target ON edges(repository,revision,target);
 CREATE TABLE findings(id TEXT PRIMARY KEY, recorded_at TEXT NOT NULL,payload BLOB NOT NULL);
 CREATE TABLE artifacts(id TEXT PRIMARY KEY,kind TEXT NOT NULL,recorded_at TEXT NOT NULL,payload BLOB NOT NULL);`,
	// Graph queries read the snapshot payload; per-row copies were never queried.
	2: `
 DROP TABLE edges;
 DROP TABLE nodes;
 CREATE INDEX snapshots_recent ON snapshots(repository, indexed_at);`,
}

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
	if v > schemaVersion {
		return fmt.Errorf("database schema %d is newer than this Radar", v)
	}
	for next := v + 1; next <= schemaVersion; next++ {
		if _, err = tx.ExecContext(ctx, migrations[next]); err != nil {
			return fmt.Errorf("migration %d: %w", next, err)
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO schema_migrations(version) VALUES(?)`, next); err != nil {
			return err
		}
	}
	return tx.Commit()
}
