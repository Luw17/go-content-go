package database

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

func TestHotBackupProducesConsistentDatabase(t *testing.T) {
	ctx := context.Background()
	directory := t.TempDir()
	db, err := Open(ctx, filepath.Join(directory, "source.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO options (key, value, updated_at) VALUES ('backup-test', 'present', 'now')
	`); err != nil {
		t.Fatal(err)
	}
	path, err := HotBackup(ctx, db, filepath.Join(directory, "backups"))
	if err != nil {
		t.Fatal(err)
	}
	backup, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer backup.Close()
	var value string
	if err := backup.QueryRowContext(ctx, "SELECT value FROM options WHERE key = 'backup-test'").Scan(&value); err != nil {
		t.Fatal(err)
	}
	if value != "present" {
		t.Fatalf("backup option = %q, want present", value)
	}
}
