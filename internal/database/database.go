package database

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

// Open opens the SQLite database and prepares its parent directory when needed.
func Open(ctx context.Context, path string) (*sql.DB, error) {
	if path == "" {
		return nil, fmt.Errorf("database path must not be empty")
	}
	filePath, isFile, err := localDatabasePath(path)
	if err != nil {
		return nil, err
	}
	if isFile {
		if err := os.MkdirAll(filepath.Dir(filePath), 0750); err != nil {
			return nil, fmt.Errorf("create database directory: %w", err)
		}
		if err := ensurePrivateDatabaseFile(filePath); err != nil {
			return nil, err
		}
	}

	dsn := path
	if path == ":memory:" {
		dsn = "file::memory:?cache=shared"
	} else {
		separator := "?"
		if strings.Contains(dsn, "?") {
			separator = "&"
		}
		dsn += separator + "_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open SQLite database: %w", err)
	}
	if path == ":memory:" {
		db.SetMaxOpenConns(1)
		db.SetMaxIdleConns(1)
	} else {
		db.SetMaxOpenConns(8)
		db.SetMaxIdleConns(8)
	}

	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("connect to SQLite database: %w", err)
	}
	if _, err := db.ExecContext(ctx, "PRAGMA foreign_keys = ON"); err != nil {
		db.Close()
		return nil, fmt.Errorf("enable SQLite foreign keys: %w", err)
	}
	if _, err := db.ExecContext(ctx, "PRAGMA busy_timeout = 5000"); err != nil {
		db.Close()
		return nil, fmt.Errorf("configure SQLite busy timeout: %w", err)
	}
	if isFile {
		if err := secureSQLiteFiles(filePath); err != nil {
			db.Close()
			return nil, err
		}
	}
	return db, nil
}

func localDatabasePath(dsn string) (string, bool, error) {
	if dsn == ":memory:" {
		return "", false, nil
	}
	if !strings.HasPrefix(dsn, "file:") {
		return dsn, true, nil
	}
	raw := strings.TrimPrefix(dsn, "file:")
	raw, query, _ := strings.Cut(raw, "?")
	parameters, err := url.ParseQuery(query)
	if err != nil {
		return "", false, fmt.Errorf("parse SQLite database URI: %w", err)
	}
	if parameters.Get("mode") == "memory" {
		return "", false, nil
	}
	if raw == "" || raw == ":memory:" {
		return "", false, nil
	}
	if strings.HasPrefix(raw, "//") && !strings.HasPrefix(raw, "///") {
		return "", false, fmt.Errorf("SQLite database URI must not specify a remote host")
	}
	if strings.HasPrefix(raw, "///") {
		raw = raw[2:]
	}
	decoded, err := url.PathUnescape(raw)
	if err != nil {
		return "", false, fmt.Errorf("decode SQLite database path: %w", err)
	}
	return decoded, true, nil
}

func ensurePrivateDatabaseFile(path string) error {
	info, err := os.Lstat(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("inspect database file: %w", err)
	}
	if err == nil && (info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular()) {
		return fmt.Errorf("database path must be a regular file, not a symlink")
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return fmt.Errorf("create database file securely: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close prepared database file: %w", err)
	}
	if err := os.Chmod(path, 0600); err != nil {
		return fmt.Errorf("restrict database file permissions: %w", err)
	}
	return nil
}

func secureSQLiteFiles(path string) error {
	for _, filePath := range []string{path, path + "-wal", path + "-shm", path + "-journal"} {
		if err := os.Chmod(filePath, 0600); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("restrict SQLite file permissions: %w", err)
		}
	}
	return nil
}
