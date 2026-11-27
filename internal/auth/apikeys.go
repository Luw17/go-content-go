package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	ErrInvalidAPIKeyName = errors.New("API key name must be between 2 and 80 characters")
	ErrAPIKeyNotFound    = errors.New("API key not found")
)

type APIKey struct {
	ID         int64
	Name       string
	KeyPrefix  string
	LastUsedAt string
	CreatedAt  string
}

func CreateAPIKey(ctx context.Context, db *sql.DB, name string, createdBy int64) (APIKey, string, error) {
	name = strings.TrimSpace(name)
	if len([]rune(name)) < 2 || len([]rune(name)) > 80 {
		return APIKey{}, "", ErrInvalidAPIKeyName
	}
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return APIKey{}, "", fmt.Errorf("generate API key: %w", err)
	}
	plaintext := "gocg_" + hex.EncodeToString(raw)
	prefix := plaintext[:12]
	sum := sha256.Sum256([]byte(plaintext))
	keyHash := hex.EncodeToString(sum[:])
	now := time.Now().UTC().Format(time.RFC3339Nano)

	res, err := db.ExecContext(ctx, `
		INSERT INTO api_keys (name, key_prefix, key_hash, created_by, created_at)
		VALUES (?, ?, ?, ?, ?)
	`, name, prefix, keyHash, createdBy, now)
	if err != nil {
		return APIKey{}, "", fmt.Errorf("store API key: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return APIKey{}, "", fmt.Errorf("load API key ID: %w", err)
	}
	return APIKey{
		ID:        id,
		Name:      name,
		KeyPrefix: prefix,
		CreatedAt: now,
	}, plaintext, nil
}

func ListAPIKeys(ctx context.Context, db *sql.DB) ([]APIKey, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT id, name, key_prefix, last_used_at, created_at
		FROM api_keys
		ORDER BY id DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("list API keys: %w", err)
	}
	defer rows.Close()
	var keys []APIKey
	for rows.Next() {
		var k APIKey
		if err := rows.Scan(&k.ID, &k.Name, &k.KeyPrefix, &k.LastUsedAt, &k.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan API key: %w", err)
		}
		keys = append(keys, k)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read API keys: %w", err)
	}
	return keys, nil
}

func DeleteAPIKey(ctx context.Context, db *sql.DB, id int64) error {
	if id <= 0 {
		return ErrAPIKeyNotFound
	}
	res, err := db.ExecContext(ctx, `DELETE FROM api_keys WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete API key: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("check API key delete: %w", err)
	}
	if affected == 0 {
		return ErrAPIKeyNotFound
	}
	return nil
}

func ValidateAPIKey(ctx context.Context, db *sql.DB, rawKey string) (bool, error) {
	rawKey = strings.TrimSpace(rawKey)
	if !strings.HasPrefix(rawKey, "gocg_") || len(rawKey) < 20 {
		return false, nil
	}
	sum := sha256.Sum256([]byte(rawKey))
	keyHash := hex.EncodeToString(sum[:])
	var id int64
	err := db.QueryRowContext(ctx, `SELECT id FROM api_keys WHERE key_hash = ?`, keyHash).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("validate API key: %w", err)
	}
	_, _ = db.ExecContext(ctx, `UPDATE api_keys SET last_used_at = ? WHERE id = ?`, time.Now().UTC().Format(time.RFC3339Nano), id)
	return true, nil
}
