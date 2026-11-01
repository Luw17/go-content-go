package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

func HotBackup(ctx context.Context, db *sql.DB, directory string) (string, error) {
	if db == nil || directory == "" {
		return "", fmt.Errorf("database and backup directory are required")
	}
	if err := os.MkdirAll(directory, 0o750); err != nil {
		return "", fmt.Errorf("create backup directory: %w", err)
	}
	name := "go-content-go-" + time.Now().UTC().Format("20060102T150405.000000000Z") + ".db"
	path := filepath.Join(directory, name)
	temporary := path + ".tmp"
	connection, err := db.Conn(ctx)
	if err != nil {
		return "", fmt.Errorf("acquire database connection for backup: %w", err)
	}
	defer connection.Close()

	err = connection.Raw(func(driverConnection any) error {
		backuper, ok := driverConnection.(interface {
			NewBackup(string) (*sqlite.Backup, error)
		})
		if !ok {
			return fmt.Errorf("SQLite driver does not support online backup")
		}
		backup, err := backuper.NewBackup(temporary)
		if err != nil {
			return fmt.Errorf("start online backup: %w", err)
		}
		finished := false
		defer func() {
			if !finished {
				_ = backup.Finish()
			}
		}()
		for {
			if err := ctx.Err(); err != nil {
				return err
			}
			more, err := backup.Step(128)
			if err != nil {
				if isRetryableBackupError(err) {
					timer := time.NewTimer(25 * time.Millisecond)
					select {
					case <-ctx.Done():
						timer.Stop()
						return ctx.Err()
					case <-timer.C:
						continue
					}
				}
				return fmt.Errorf("copy database pages: %w", err)
			}

			if !more {
				finished = true
				return backup.Finish()
			}
		}
	})
	if err != nil {
		_ = os.Remove(temporary)
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return "", err
		}
		return "", fmt.Errorf("create online backup: %w", err)
	}
	if err := os.Chmod(temporary, 0o600); err != nil {
		_ = os.Remove(temporary)
		return "", fmt.Errorf("secure backup file: %w", err)
	}
	if err := os.Rename(temporary, path); err != nil {
		_ = os.Remove(temporary)
		return "", fmt.Errorf("finalize backup file: %w", err)
	}
	return path, nil
}

func isRetryableBackupError(err error) bool {
	var sqliteError *sqlite.Error
	if !errors.As(err, &sqliteError) {
		return false
	}
	code := sqliteError.Code() & 0xff
	return code == sqlite3.SQLITE_BUSY || code == sqlite3.SQLITE_LOCKED
}

func Optimize(ctx context.Context, db *sql.DB) error {
	if db == nil {
		return fmt.Errorf("database must not be nil")
	}
	if _, err := db.ExecContext(ctx, "PRAGMA optimize"); err != nil {
		return fmt.Errorf("optimize database: %w", err)
	}
	if _, err := db.ExecContext(ctx, "VACUUM"); err != nil {
		return fmt.Errorf("vacuum database: %w", err)
	}
	return nil
}

func RunScheduledBackups(ctx context.Context, db *sql.DB, directory string, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			backupContext, cancel := context.WithTimeout(ctx, 10*time.Minute)
			path, err := HotBackup(backupContext, db, directory)
			cancel()
			if err != nil {
				if ctx.Err() == nil {
					log.Printf("scheduled database backup failed: %v", err)
				}
				continue
			}
			log.Printf("scheduled database backup created: %s", path)
		}
	}
}
