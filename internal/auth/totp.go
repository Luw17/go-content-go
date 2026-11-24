package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"database/sql"
	"encoding/base32"
	"encoding/binary"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

const (
	totpPeriodSeconds = 30
	totpDigits        = 6
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
	code = strings.ReplaceAll(strings.TrimSpace(code), " ", "")
	if len(code) != totpDigits {
		return false
	}
	for _, skew := range []int{-1, 0, 1} {
		expected, err := TOTPCode(secret, now.Add(time.Duration(skew*totpPeriodSeconds)*time.Second))
		if err != nil {
			return false
		}
		if subtle.ConstantTimeCompare([]byte(code), []byte(expected)) == 1 {
			return true
		}
	}
	return false
}

// EnableTOTP verifies the user's code against the pending secret and activates 2FA.
func EnableTOTP(ctx context.Context, db *sql.DB, userID int64, secret, code string) error {
	secret = strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(secret), " ", ""))
	if userID <= 0 || !VerifyTOTP(secret, code, time.Now()) {
		return ErrInvalidTOTP
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := db.ExecContext(ctx, `
		UPDATE users SET totp_secret = ?, totp_enabled = 1, updated_at = ?
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

// DisableTOTP clears the user's TOTP secret and disables 2FA.
func DisableTOTP(ctx context.Context, db *sql.DB, userID int64) error {
	if userID <= 0 {
		return ErrInvalidCredentials
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := db.ExecContext(ctx, `
		UPDATE users SET totp_secret = '', totp_enabled = 0, updated_at = ?
		WHERE id = ?
	`, now, userID); err != nil {
		return fmt.Errorf("disable TOTP: %w", err)
	}
	return nil
}

// VerifyUserTOTP checks the code against the stored TOTP secret for the user.
func VerifyUserTOTP(ctx context.Context, db *sql.DB, userID int64, code string) (bool, error) {
	var secret string
	var enabled int
	err := db.QueryRowContext(ctx, `
		SELECT totp_secret, totp_enabled FROM users WHERE id = ?
	`, userID).Scan(&secret, &enabled)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("load user TOTP config: %w", err)
	}
	if enabled != 1 || secret == "" {
		return true, nil
	}
	return VerifyTOTP(secret, code, time.Now()), nil
}
