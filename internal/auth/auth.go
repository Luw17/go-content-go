package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const (
	SessionDuration = 12 * time.Hour
	minPasswordSize = 12
	maxPasswordSize = 72
)

var (
	ErrSetupComplete      = errors.New("initial setup is already complete")
	ErrInvalidCredentials = errors.New("invalid username or password")
	ErrInvalidUsername    = errors.New("username must be 3-32 characters: lowercase letters, numbers, underscores, or hyphens")
	ErrInvalidPassword    = errors.New("password must be between 12 and 72 bytes")
	ErrInvalidRole        = errors.New("role must be admin, editor, or author")
)

type User struct {
	ID       int64
	Username string
	Role     string
}

// SetupAvailable reports whether the first administrator still needs to be created.
func SetupAvailable(ctx context.Context, db *sql.DB) (bool, error) {
	var exists bool
	if err := db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM users)").Scan(&exists); err != nil {
		return false, fmt.Errorf("check initial administrator: %w", err)
	}
	return !exists, nil
}

// CreateInitialAdmin creates the first administrator only while the user table is empty.
func CreateInitialAdmin(ctx context.Context, db *sql.DB, username, password string) (User, error) {
	username = strings.ToLower(strings.TrimSpace(username))
	if !validUsername(username) {
		return User{}, ErrInvalidUsername
	}
	if len(password) < minPasswordSize || len(password) > maxPasswordSize {
		return User{}, ErrInvalidPassword
	}

	passwordHash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return User{}, fmt.Errorf("hash administrator password: %w", err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	result, err := db.ExecContext(ctx, `
		INSERT INTO users (username, password_hash, role, created_at, updated_at)
		SELECT ?, ?, 'admin', ?, ?
		WHERE NOT EXISTS (SELECT 1 FROM users)
	`, username, string(passwordHash), now, now)
	if err != nil {
		return User{}, fmt.Errorf("create initial administrator: %w", err)
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return User{}, fmt.Errorf("check initial administrator creation: %w", err)
	}
	if inserted != 1 {
		return User{}, ErrSetupComplete
	}

	var user User
	if err := db.QueryRowContext(ctx, "SELECT id, username, role FROM users WHERE username = ?", username).
		Scan(&user.ID, &user.Username, &user.Role); err != nil {
		return User{}, fmt.Errorf("load initial administrator: %w", err)
	}
	return user, nil
}

// Authenticate verifies credentials without revealing whether the username exists.
func Authenticate(ctx context.Context, db *sql.DB, username, password string) (User, error) {
	username = strings.ToLower(strings.TrimSpace(username))
	var user User
	var passwordHash string
	err := db.QueryRowContext(ctx,
		"SELECT id, username, role, password_hash FROM users WHERE username = ?",
		username,
	).Scan(&user.ID, &user.Username, &user.Role, &passwordHash)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return User{}, fmt.Errorf("load user credentials: %w", err)
	}
	if err != nil {
		// Keep invalid usernames on the same password-hashing path as existing users.
		_ = bcrypt.CompareHashAndPassword([]byte("$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy"), []byte(password))
		return User{}, ErrInvalidCredentials
	}
	if !ValidRole(user.Role) || bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(password)) != nil {
		return User{}, ErrInvalidCredentials
	}
	return user, nil
}

// CreateSession stores only a hash of the opaque session token.
func CreateSession(ctx context.Context, db *sql.DB, userID int64) (string, time.Time, error) {
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return "", time.Time{}, fmt.Errorf("generate session token: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(tokenBytes)
	tokenHash := sha256.Sum256([]byte(token))
	expiresAt := time.Now().UTC().Add(SessionDuration)
	_, err := db.ExecContext(ctx, `
		DELETE FROM sessions WHERE expires_at <= ?
	`, time.Now().UTC().UnixNano())
	if err != nil {
		return "", time.Time{}, fmt.Errorf("remove expired sessions: %w", err)
	}
	_, err = db.ExecContext(ctx, `
		INSERT INTO sessions (token_hash, user_id, expires_at, created_at)
		VALUES (?, ?, ?, ?)
	`, tokenHash[:], userID, expiresAt.UnixNano(), time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return "", time.Time{}, fmt.Errorf("store session: %w", err)
	}
	return token, expiresAt, nil
}

// SessionUser validates a token and returns its user.
func SessionUser(ctx context.Context, db *sql.DB, token string) (User, bool, error) {
	if token == "" {
		return User{}, false, nil
	}
	tokenHash := sha256.Sum256([]byte(token))
	var user User
	err := db.QueryRowContext(ctx, `
		SELECT users.id, users.username, users.role
		FROM sessions
		JOIN users ON users.id = sessions.user_id
		WHERE sessions.token_hash = ? AND sessions.expires_at > ?
	`, tokenHash[:], time.Now().UTC().UnixNano()).
		Scan(&user.ID, &user.Username, &user.Role)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, false, nil
	}
	if err != nil {
		return User{}, false, fmt.Errorf("validate session: %w", err)
	}
	return user, ValidRole(user.Role), nil
}

func ValidRole(role string) bool {
	return role == "admin" || role == "editor" || role == "author"
}

func CreateUser(ctx context.Context, db *sql.DB, username, password, role string) (User, error) {
	username = strings.ToLower(strings.TrimSpace(username))
	if !validUsername(username) {
		return User{}, ErrInvalidUsername
	}
	if len(password) < minPasswordSize || len(password) > maxPasswordSize {
		return User{}, ErrInvalidPassword
	}
	if !ValidRole(role) {
		return User{}, ErrInvalidRole
	}
	passwordHash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return User{}, fmt.Errorf("hash user password: %w", err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	result, err := db.ExecContext(ctx, `
		INSERT INTO users (username, password_hash, role, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?)
	`, username, string(passwordHash), role, now, now)
	if err != nil {
		return User{}, fmt.Errorf("create user: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return User{}, fmt.Errorf("load created user ID: %w", err)
	}
	return User{ID: id, Username: username, Role: role}, nil
}

func Users(ctx context.Context, db *sql.DB) ([]User, error) {
	rows, err := db.QueryContext(ctx, "SELECT id, username, role FROM users ORDER BY username")
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	defer rows.Close()
	var users []User
	for rows.Next() {
		var user User
		if err := rows.Scan(&user.ID, &user.Username, &user.Role); err != nil {
			return nil, fmt.Errorf("scan user: %w", err)
		}
		users = append(users, user)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read users: %w", err)
	}
	return users, nil
}

// DeleteSession invalidates a token if it exists.
func DeleteSession(ctx context.Context, db *sql.DB, token string) error {
	if token == "" {
		return nil
	}
	tokenHash := sha256.Sum256([]byte(token))
	if _, err := db.ExecContext(ctx, "DELETE FROM sessions WHERE token_hash = ?", tokenHash[:]); err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}

func validUsername(username string) bool {
	if len(username) < 3 || len(username) > 32 {
		return false
	}
	for _, char := range username {
		if (char < 'a' || char > 'z') && (char < '0' || char > '9') && char != '_' && char != '-' {
			return false
		}
	}
	return true
}
