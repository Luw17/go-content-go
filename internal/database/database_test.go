package database

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
)

func TestMigrateCreatesCoreTablesAndIsIdempotent(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "content.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if err := Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(ctx, db); err != nil {
		t.Fatalf("second migration run: %v", err)
	}

	var count int
	if err := db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM sqlite_master
		WHERE type = 'table' AND name IN ('users', 'posts', 'options')
	`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 3 {
		t.Errorf("core table count = %d, want 3", count)
	}

	names, err := MigrationNames(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{
		"migrations/001_initial_schema.sql",
		"migrations/002_auth_sessions.sql",
		"migrations/003_content_management.sql",
		"migrations/004_media.sql",
		"migrations/005_active_theme.sql",
		"migrations/006_active_plugins.sql",
	}; !reflect.DeepEqual(names, want) {
		t.Errorf("MigrationNames() = %v, want %v", names, want)
	}
	var activeTheme string
	if err := db.QueryRowContext(ctx, "SELECT value FROM options WHERE key = 'active_theme'").Scan(&activeTheme); err != nil {
		t.Fatal(err)
	}
	if activeTheme != "default" {
		t.Errorf("active theme default = %q, want default", activeTheme)
	}
	var activePlugins string
	if err := db.QueryRowContext(ctx, "SELECT value FROM options WHERE key = 'active_plugins'").Scan(&activePlugins); err != nil {
		t.Fatal(err)
	}
	if activePlugins != "[]" {
		t.Errorf("active plugins default = %q, want []", activePlugins)
	}
}
