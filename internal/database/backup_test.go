package database

import (
	"context"
	"database/sql"
	"os"
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

func TestPruneBackupsRetention(t *testing.T) {
	dir := t.TempDir()
	files := []string{
		"go-content-go-20260101T000001.000000000Z.db",
		"go-content-go-20260102T000001.000000000Z.db",
		"go-content-go-20260103T000001.000000000Z.db",
		"go-content-go-20260104T000001.000000000Z.db",
		"keep-unrelated.txt",
	}
	for _, name := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("data"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	removed, err := PruneBackups(dir, 2)
	if err != nil {
		t.Fatalf("PruneBackups() error = %v", err)
	}
	if removed != 2 {
		t.Fatalf("removed = %d, want 2", removed)
	}
	for _, shouldExist := range []string{
		"go-content-go-20260103T000001.000000000Z.db",
		"go-content-go-20260104T000001.000000000Z.db",
		"keep-unrelated.txt",
	} {
		if _, err := os.Stat(filepath.Join(dir, shouldExist)); err != nil {
			t.Errorf("expected %q to remain: %v", shouldExist, err)
		}
	}
	for _, shouldBeGone := range []string{
		"go-content-go-20260101T000001.000000000Z.db",
		"go-content-go-20260102T000001.000000000Z.db",
	} {
		if _, err := os.Stat(filepath.Join(dir, shouldBeGone)); !os.IsNotExist(err) {
			t.Errorf("expected %q to be removed, stat err = %v", shouldBeGone, err)
		}
	}
}
