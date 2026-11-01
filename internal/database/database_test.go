package database

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
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
		"migrations/007_navigation_menus.sql",
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

func TestOpenRestrictsDatabaseFilePermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private.db")
	db, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("CREATE TABLE permissions_check (value TEXT)"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO permissions_check (value) VALUES ('private')"); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		for _, filePath := range []string{path, path + "-wal", path + "-shm"} {
			info, err := os.Stat(filePath)
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := info.Mode().Perm(); got != 0600 {
				t.Fatalf("SQLite file %q permissions = %04o, want 0600", filepath.Base(filePath), got)
			}
		}
	}
}

func TestOpenRejectsDatabaseSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires elevated privileges on some Windows hosts")
	}
	root := t.TempDir()
	target := filepath.Join(root, "target.db")
	if err := os.WriteFile(target, nil, 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link.db")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(context.Background(), link); err == nil {
		t.Fatal("Open() accepted a symlink database path")
	}
}

func TestOpenRestrictsFileURIDatabasePermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "uri.db")
	db, err := Open(context.Background(), "file:"+filepath.ToSlash(path)+"?mode=rwc")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != 0600 {
			t.Fatalf("URI database permissions = %04o, want 0600", got)
		}
	}
}

func TestOpenRejectsRemoteDatabaseURIHost(t *testing.T) {
	if _, err := Open(context.Background(), "file://attacker.example/db.sqlite"); err == nil {
		t.Fatal("Open() accepted a database URI with a remote host")
	}
}

func TestOpenDoesNotCreateFileForNamedMemoryDatabase(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "shared-memory")
	db, err := Open(context.Background(), "file:"+filepath.ToSlash(path)+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("named in-memory database created a disk file: %v", err)
	}
}
