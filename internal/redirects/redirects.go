package redirects

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var (
	ErrInvalidRedirect = errors.New("invalid redirect")
	ErrNotFound        = errors.New("redirect not found")
)

type Redirect struct {
	ID         int64
	SourcePath string
	TargetPath string
	StatusCode int
	CreatedAt  string
	UpdatedAt  string
}

func NormalizeSourcePath(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.ContainsAny(raw, " \t\r\n?#") {
		return "", fmt.Errorf("%w: invalid source path", ErrInvalidRedirect)
	}
	if !strings.HasPrefix(raw, "/") {
		raw = "/" + raw
	}
	if strings.HasPrefix(raw, "//") {
		return "", fmt.Errorf("%w: protocol-relative source path is not allowed", ErrInvalidRedirect)
	}
	if len(raw) > 1 {
		raw = strings.TrimRight(raw, "/")
	}
	if raw == "/" || strings.HasPrefix(raw, "/admin") || strings.HasPrefix(raw, "/api/") {
		return "", fmt.Errorf("%w: reserved path cannot be redirected", ErrInvalidRedirect)
	}
	if len(raw) > 512 {
		return "", fmt.Errorf("%w: source path too long", ErrInvalidRedirect)
	}
	return raw, nil
}

func NormalizeTargetPath(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.ContainsAny(raw, "\r\n") || len(raw) > 2048 {
		return "", fmt.Errorf("%w: invalid target path", ErrInvalidRedirect)
	}
	if strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://") {
		parsed, err := url.Parse(raw)
		if err != nil || parsed.Host == "" {
			return "", fmt.Errorf("%w: invalid target URL", ErrInvalidRedirect)
		}
		return raw, nil
	}
	if strings.HasPrefix(raw, "//") {
		return "", fmt.Errorf("%w: protocol-relative target is not allowed", ErrInvalidRedirect)
	}
	if !strings.HasPrefix(raw, "/") {
		raw = "/" + raw
	}
	if len(raw) > 1 && !strings.Contains(raw, "?") {
		raw = strings.TrimRight(raw, "/")
	}
	return raw, nil
}

func Save(ctx context.Context, db *sql.DB, sourcePath, targetPath string, statusCode int) (Redirect, error) {
	src, err := NormalizeSourcePath(sourcePath)
	if err != nil {
		return Redirect{}, err
	}
	dst, err := NormalizeTargetPath(targetPath)
	if err != nil {
		return Redirect{}, err
	}
	if src == dst {
		return Redirect{}, fmt.Errorf("%w: source and target paths must differ", ErrInvalidRedirect)
	}
	if statusCode != http.StatusMovedPermanently && statusCode != http.StatusFound {
		return Redirect{}, fmt.Errorf("%w: status code must be 301 or 302", ErrInvalidRedirect)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := db.ExecContext(ctx, `
		INSERT INTO redirects (source_path, target_path, status_code, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(source_path) DO UPDATE SET
			target_path = excluded.target_path,
			status_code = excluded.status_code,
			updated_at = excluded.updated_at
	`, src, dst, statusCode, now, now); err != nil {
		return Redirect{}, fmt.Errorf("save redirect: %w", err)
	}
	item, found, err := Find(ctx, db, src)
	if err != nil {
		return Redirect{}, err
	}
	if !found {
		return Redirect{}, ErrNotFound
	}
	return item, nil
}

func Find(ctx context.Context, db *sql.DB, requestPath string) (Redirect, bool, error) {
	cleaned := strings.TrimSpace(requestPath)
	if cleaned == "" || cleaned == "/" {
		return Redirect{}, false, nil
	}
	if len(cleaned) > 1 {
		cleaned = strings.TrimRight(cleaned, "/")
	}
	var item Redirect
	err := db.QueryRowContext(ctx, `
		SELECT id, source_path, target_path, status_code, created_at, updated_at
		FROM redirects
		WHERE source_path = ?
	`, cleaned).Scan(&item.ID, &item.SourcePath, &item.TargetPath, &item.StatusCode, &item.CreatedAt, &item.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Redirect{}, false, nil
	}
	if err != nil {
		return Redirect{}, false, fmt.Errorf("find redirect: %w", err)
	}
	return item, true, nil
}

func List(ctx context.Context, db *sql.DB) ([]Redirect, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT id, source_path, target_path, status_code, created_at, updated_at
		FROM redirects
		ORDER BY updated_at DESC, id DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("list redirects: %w", err)
	}
	defer rows.Close()
	var items []Redirect
	for rows.Next() {
		var item Redirect
		if err := rows.Scan(&item.ID, &item.SourcePath, &item.TargetPath, &item.StatusCode, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan redirect: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read redirects: %w", err)
	}
	return items, nil
}

func Delete(ctx context.Context, db *sql.DB, id int64) error {
	if id <= 0 {
		return ErrNotFound
	}
	res, err := db.ExecContext(ctx, `DELETE FROM redirects WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete redirect: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("check redirect delete: %w", err)
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}
