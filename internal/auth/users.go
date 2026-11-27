package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

var (
	ErrUserNotFound = errors.New("user not found")
	ErrLastAdmin    = errors.New("cannot remove or demote the last active administrator")
)

func FindUserByUsername(ctx context.Context, db *sql.DB, username string) (User, error) {
	username = strings.ToLower(strings.TrimSpace(username))
	if username == "" {
		return User{}, ErrUserNotFound
	}
	var user User
	var totpEnabled, active int
	err := db.QueryRowContext(ctx, `
		SELECT id, username, role, totp_enabled, active
		FROM users
		WHERE username = ?
	`, username).Scan(&user.ID, &user.Username, &user.Role, &totpEnabled, &active)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrUserNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("load user by username: %w", err)
	}
	user.TOTPEnabled = totpEnabled == 1
	user.Active = active == 1
	return user, nil
}

func UpdateUserPassword(ctx context.Context, db *sql.DB, userID int64, newPassword string) error {
	if userID <= 0 {
		return ErrUserNotFound
	}
	if len(newPassword) < minPasswordSize || len(newPassword) > maxPasswordSize {
		return ErrInvalidPassword
	}
	passwordHash, err := bcrypt.GenerateFromPassword([]byte(newPassword), passwordCost)
	if err != nil {
		return fmt.Errorf("hash new password: %w", err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin password update: %w", err)
	}
	defer tx.Rollback()

	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := tx.ExecContext(ctx, `
		UPDATE users SET password_hash = ?, updated_at = ? WHERE id = ?
	`, string(passwordHash), now, userID)
	if err != nil {
		return fmt.Errorf("update user password: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("check password update: %w", err)
	}
	if affected == 0 {
		return ErrUserNotFound
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ?`, userID); err != nil {
		return fmt.Errorf("revoke sessions after password change: %w", err)
	}
	return tx.Commit()
}

func UpdateUserRole(ctx context.Context, db *sql.DB, userID int64, role string) error {
	if userID <= 0 {
		return ErrUserNotFound
	}
	if !ValidRole(role) {
		return ErrInvalidRole
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin role update: %w", err)
	}
	defer tx.Rollback()

	var currentRole string
	var currentActive int
	err = tx.QueryRowContext(ctx, `SELECT role, active FROM users WHERE id = ?`, userID).Scan(&currentRole, &currentActive)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrUserNotFound
	}
	if err != nil {
		return fmt.Errorf("load user for role update: %w", err)
	}
	if currentRole == "admin" && currentActive == 1 && role != "admin" {
		if otherAdmins, err := countOtherActiveAdmins(ctx, tx, userID); err != nil {
			return err
		} else if otherAdmins == 0 {
			return ErrLastAdmin
		}
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := tx.ExecContext(ctx, `UPDATE users SET role = ?, updated_at = ? WHERE id = ?`, role, now, userID); err != nil {
		return fmt.Errorf("update user role: %w", err)
	}
	return tx.Commit()
}

func SetUserActive(ctx context.Context, db *sql.DB, userID int64, active bool) error {
	if userID <= 0 {
		return ErrUserNotFound
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin user active update: %w", err)
	}
	defer tx.Rollback()

	var currentRole string
	var currentActive int
	err = tx.QueryRowContext(ctx, `SELECT role, active FROM users WHERE id = ?`, userID).Scan(&currentRole, &currentActive)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrUserNotFound
	}
	if err != nil {
		return fmt.Errorf("load user for active toggle: %w", err)
	}
	if !active && currentRole == "admin" && currentActive == 1 {
		if otherAdmins, err := countOtherActiveAdmins(ctx, tx, userID); err != nil {
			return err
		} else if otherAdmins == 0 {
			return ErrLastAdmin
		}
	}
	activeInt := 0
	if active {
		activeInt = 1
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := tx.ExecContext(ctx, `UPDATE users SET active = ?, updated_at = ? WHERE id = ?`, activeInt, now, userID); err != nil {
		return fmt.Errorf("update user active status: %w", err)
	}
	if !active {
		if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ?`, userID); err != nil {
			return fmt.Errorf("revoke sessions for deactivated user: %w", err)
		}
	}
	return tx.Commit()
}

func RevokeUserSessions(ctx context.Context, db *sql.DB, userID int64) error {
	if userID <= 0 {
		return ErrUserNotFound
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ?`, userID); err != nil {
		return fmt.Errorf("revoke user sessions: %w", err)
	}
	return nil
}

func DeleteUser(ctx context.Context, db *sql.DB, userID int64) error {
	if userID <= 0 {
		return ErrUserNotFound
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin delete user: %w", err)
	}
	defer tx.Rollback()

	var currentRole string
	var currentActive int
	err = tx.QueryRowContext(ctx, `SELECT role, active FROM users WHERE id = ?`, userID).Scan(&currentRole, &currentActive)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrUserNotFound
	}
	if err != nil {
		return fmt.Errorf("load user for deletion: %w", err)
	}
	if currentRole == "admin" && currentActive == 1 {
		if otherAdmins, err := countOtherActiveAdmins(ctx, tx, userID); err != nil {
			return err
		} else if otherAdmins == 0 {
			return ErrLastAdmin
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ?`, userID); err != nil {
		return fmt.Errorf("delete user sessions: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, userID); err != nil {
		return fmt.Errorf("delete user: %w", err)
	}
	return tx.Commit()
}

func countOtherActiveAdmins(ctx context.Context, tx *sql.Tx, excludeUserID int64) (int, error) {
	var count int
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM users
		WHERE role = 'admin' AND active = 1 AND id != ?
	`, excludeUserID).Scan(&count); err != nil {
		return 0, fmt.Errorf("count active admins: %w", err)
	}
	return count, nil
}
