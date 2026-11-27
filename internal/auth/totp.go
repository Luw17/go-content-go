package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base32"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

const (
	totpPeriodSeconds       = 30
	totpDigits              = 6
	recoveryCodeCount       = 8
	recoveryCodeHalfLength  = 5
	recoveryCodeAlphabet    = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
)

var (
	ErrInvalidTOTP = errors.New("invalid two-factor authentication code")
	totpEncoding   = base32.StdEncoding.WithPadding(base32.NoPadding)
)

// GenerateTOTPSecret generates a 160-bit Base32-encoded secret compatible with RFC 6238 authenticators.
func GenerateTOTPSecret() (string, error) {
	raw := make([]byte, 20)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate TOTP secret: %w", err)
	}
	return totpEncoding.EncodeToString(raw), nil
}

// TOTPProvisioningURI returns a standard otpauth:// URI for authenticator apps.
func TOTPProvisioningURI(issuer, username, secret string) string {
	issuer = strings.TrimSpace(issuer)
	if issuer == "" {
		issuer = "Go Content Go!"
	}
	label := url.PathEscape(issuer + ":" + username)
	query := url.Values{
		"secret":    {strings.ToUpper(strings.TrimSpace(secret))},
		"issuer":    {issuer},
		"algorithm": {"SHA1"},
		"digits":    {"6"},
		"period":    {"30"},
	}
	return "otpauth://totp/" + label + "?" + query.Encode()
}

// TOTPCode generates the 6-digit RFC 6238 code for a given secret and timestamp.
func TOTPCode(secret string, at time.Time) (string, error) {
	normalized := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(secret), " ", ""))
	if normalized == "" {
		return "", ErrInvalidTOTP
	}
	key, err := totpEncoding.DecodeString(normalized)
	if err != nil || len(key) < 10 {
		return "", ErrInvalidTOTP
	}
	counter := uint64(at.UTC().Unix() / totpPeriodSeconds)
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], counter)
	mac := hmac.New(sha1.New, key)
	_, _ = mac.Write(msg[:])
	sum := mac.Sum(nil)
	offset := sum[len(sum)-1] & 0x0f
	truncated := (uint32(sum[offset]&0x7f) << 24) |
		(uint32(sum[offset+1]) << 16) |
		(uint32(sum[offset+2]) << 8) |
		uint32(sum[offset+3])
	code := truncated % 1000000
	return fmt.Sprintf("%06d", code), nil
}

// VerifyTOTP checks a 6-digit TOTP code allowing +/- 1 time step for clock skew.
func VerifyTOTP(secret, code string, now time.Time) bool {
	_, ok := VerifyTOTPWithStep(secret, code, now)
	return ok
}

// VerifyTOTPWithStep checks a 6-digit TOTP code allowing +/- 1 time step for clock skew
// and returns the matched 30-second time step counter.
func VerifyTOTPWithStep(secret, code string, now time.Time) (int64, bool) {
	code = strings.ReplaceAll(strings.TrimSpace(code), " ", "")
	if len(code) != totpDigits {
		return 0, false
	}
	baseStep := now.UTC().Unix() / totpPeriodSeconds
	for _, skew := range []int64{-1, 0, 1} {
		step := baseStep + skew
		if step < 0 {
			continue
		}
		expected, err := TOTPCode(secret, time.Unix(step*totpPeriodSeconds, 0).UTC())
		if err != nil {
			return 0, false
		}
		if subtle.ConstantTimeCompare([]byte(code), []byte(expected)) == 1 {
			return step, true
		}
	}
	return 0, false
}

// EnableTOTP verifies the user's code against the pending secret and activates 2FA.
func EnableTOTP(ctx context.Context, db *sql.DB, userID int64, secret, code string) error {
	secret = strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(secret), " ", ""))
	if userID <= 0 || !VerifyTOTP(secret, code, time.Now()) {
		return ErrInvalidTOTP
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := db.ExecContext(ctx, `
		UPDATE users SET totp_secret = ?, totp_enabled = 1, totp_last_step = -1, updated_at = ?
		WHERE id = ?
	`, secret, now, userID)
	if err != nil {
		return fmt.Errorf("enable TOTP: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("check TOTP activation: %w", err)
	}
	if affected == 0 {
		return ErrInvalidCredentials
	}
	return nil
}

// DisableTOTP clears the user's TOTP secret, recovery codes, and disables 2FA.
func DisableTOTP(ctx context.Context, db *sql.DB, userID int64) error {
	if userID <= 0 {
		return ErrInvalidCredentials
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin disable TOTP: %w", err)
	}
	defer tx.Rollback()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := tx.ExecContext(ctx, `
		UPDATE users SET totp_secret = '', totp_enabled = 0, totp_last_step = -1, updated_at = ?
		WHERE id = ?
	`, now, userID); err != nil {
		return fmt.Errorf("disable TOTP: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM user_recovery_codes WHERE user_id = ?`, userID); err != nil {
		return fmt.Errorf("clear recovery codes: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit disable TOTP: %w", err)
	}
	return nil
}

// RegenerateRecoveryCodes replaces all recovery codes for a user and returns the new plaintext codes once.
func RegenerateRecoveryCodes(ctx context.Context, db *sql.DB, userID int64) ([]string, error) {
	if userID <= 0 {
		return nil, ErrInvalidCredentials
	}
	codes := make([]string, recoveryCodeCount)
	hashes := make([]string, recoveryCodeCount)
	for i := range codes {
		code, err := generateRecoveryCode()
		if err != nil {
			return nil, err
		}
		codes[i] = code
		hashes[i] = hashRecoveryCode(code)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin recovery code regeneration: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM user_recovery_codes WHERE user_id = ?`, userID); err != nil {
		return nil, fmt.Errorf("delete old recovery codes: %w", err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, h := range hashes {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO user_recovery_codes (user_id, code_hash, created_at)
			VALUES (?, ?, ?)
		`, userID, h, now); err != nil {
			return nil, fmt.Errorf("insert recovery code: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit recovery codes: %w", err)
	}
	return codes, nil
}

// RemainingRecoveryCodes returns the count of unused recovery codes for a user.
func RemainingRecoveryCodes(ctx context.Context, db *sql.DB, userID int64) (int, error) {
	if userID <= 0 {
		return 0, nil
	}
	var count int
	err := db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM user_recovery_codes
		WHERE user_id = ? AND used_at IS NULL
	`, userID).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count recovery codes: %w", err)
	}
	return count, nil
}

// VerifyUserTOTP checks a 6-digit TOTP code (blocking replay within the TOTP window)
// or a single-use recovery code against the user's 2FA configuration.
func VerifyUserTOTP(ctx context.Context, db *sql.DB, userID int64, code string) (bool, error) {
	return verifyUserTOTPAt(ctx, db, userID, code, time.Now())
}

func verifyUserTOTPAt(ctx context.Context, db *sql.DB, userID int64, code string, now time.Time) (bool, error) {
	var secret string
	var enabled int
	var lastStep int64
	err := db.QueryRowContext(ctx, `
		SELECT totp_secret, totp_enabled, totp_last_step FROM users WHERE id = ?
	`, userID).Scan(&secret, &enabled, &lastStep)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("load user TOTP config: %w", err)
	}
	if enabled != 1 || secret == "" {
		return true, nil
	}
	if step, ok := VerifyTOTPWithStep(secret, code, now); ok {
		if step <= lastStep {
			return false, nil
		}
		res, err := db.ExecContext(ctx, `
			UPDATE users SET totp_last_step = ?
			WHERE id = ? AND totp_last_step < ?
		`, step, userID, step)
		if err != nil {
			return false, fmt.Errorf("record TOTP step: %w", err)
		}
		affected, err := res.RowsAffected()
		if err != nil {
			return false, fmt.Errorf("check TOTP step update: %w", err)
		}
		return affected == 1, nil
	}
	return consumeRecoveryCode(ctx, db, userID, code, now)
}

func consumeRecoveryCode(ctx context.Context, db *sql.DB, userID int64, rawCode string, now time.Time) (bool, error) {
	normalized := normalizeRecoveryCode(rawCode)
	if len(normalized) != recoveryCodeHalfLength*2 {
		return false, nil
	}
	candidateHash := hashRecoveryCode(normalized)
	rows, err := db.QueryContext(ctx, `
		SELECT id, code_hash FROM user_recovery_codes
		WHERE user_id = ? AND used_at IS NULL
	`, userID)
	if err != nil {
		return false, fmt.Errorf("load recovery codes: %w", err)
	}
	defer rows.Close()

	var matchedID int64
	for rows.Next() {
		var id int64
		var storedHash string
		if err := rows.Scan(&id, &storedHash); err != nil {
			return false, fmt.Errorf("scan recovery code: %w", err)
		}
		if subtle.ConstantTimeCompare([]byte(candidateHash), []byte(storedHash)) == 1 && matchedID == 0 {
			matchedID = id
		}
	}
	if err := rows.Err(); err != nil {
		return false, fmt.Errorf("read recovery codes: %w", err)
	}
	if matchedID == 0 {
		return false, nil
	}
	res, err := db.ExecContext(ctx, `
		UPDATE user_recovery_codes SET used_at = ?
		WHERE id = ? AND used_at IS NULL
	`, now.UTC().Format(time.RFC3339Nano), matchedID)
	if err != nil {
		return false, fmt.Errorf("mark recovery code used: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("check recovery code update: %w", err)
	}
	return affected == 1, nil
}

func generateRecoveryCode() (string, error) {
	total := recoveryCodeHalfLength * 2
	raw := make([]byte, total)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate recovery code: %w", err)
	}
	var b strings.Builder
	b.Grow(total + 1)
	for i, v := range raw {
		if i == recoveryCodeHalfLength {
			b.WriteByte('-')
		}
		b.WriteByte(recoveryCodeAlphabet[int(v)%len(recoveryCodeAlphabet)])
	}
	return b.String(), nil
}

func normalizeRecoveryCode(code string) string {
	code = strings.ToUpper(strings.TrimSpace(code))
	code = strings.ReplaceAll(code, "-", "")
	code = strings.ReplaceAll(code, " ", "")
	return code
}

func hashRecoveryCode(code string) string {
	sum := sha256.Sum256([]byte(normalizeRecoveryCode(code)))
	return hex.EncodeToString(sum[:])
}
