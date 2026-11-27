package auth

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Luw17/go-content-go/internal/database"
)

func TestTOTPGenerationVerificationAndPersistence(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, filepath.Join(t.TempDir(), "totp.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := database.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}

	admin, err := CreateInitialAdmin(ctx, db, "admin", "strong-password-123")
	if err != nil {
		t.Fatal(err)
	}

	secret, err := GenerateTOTPSecret()
	if err != nil {
		t.Fatalf("GenerateTOTPSecret() error = %v", err)
	}
	if len(secret) < 16 {
		t.Fatalf("secret too short: %q", secret)
	}
	uri := TOTPProvisioningURI("Go Content Go!", admin.Username, secret)
	if !strings.HasPrefix(uri, "otpauth://totp/") || !strings.Contains(uri, secret) {
		t.Fatalf("unexpected provisioning URI: %q", uri)
	}

	now := time.Now().UTC()
	code, err := TOTPCode(secret, now)
	if err != nil {
		t.Fatalf("TOTPCode() error = %v", err)
	}
	if !VerifyTOTP(secret, code, now) {
		t.Fatalf("VerifyTOTP rejected valid code %q", code)
	}
	if !VerifyTOTP(secret, code, now.Add(29*time.Second)) {
		t.Fatalf("VerifyTOTP should allow +/-1 step clock skew")
	}
	if VerifyTOTP(secret, "000000", now) && code != "000000" {
		t.Fatalf("VerifyTOTP accepted invalid code")
	}

	// Reject enabling with bad code
	if err := EnableTOTP(ctx, db, admin.ID, secret, "999999"); err == nil && code != "999999" {
		t.Fatalf("expected EnableTOTP to fail with wrong code")
	}
	// Enable with valid code
	if err := EnableTOTP(ctx, db, admin.ID, secret, code); err != nil {
		t.Fatalf("EnableTOTP() error = %v", err)
	}

	authed, err := Authenticate(ctx, db, "admin", "strong-password-123")
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	if !authed.TOTPEnabled {
		t.Fatalf("expected TOTPEnabled=true after enabling 2FA")
	}
	ok, err := VerifyUserTOTP(ctx, db, admin.ID, code)
	if err != nil || !ok {
		t.Fatalf("VerifyUserTOTP() = (%v, %v), want (true, nil)", ok, err)
	}

	if err := DisableTOTP(ctx, db, admin.ID); err != nil {
		t.Fatalf("DisableTOTP() error = %v", err)
	}
	authed, err = Authenticate(ctx, db, "admin", "strong-password-123")
	if err != nil {
		t.Fatal(err)
	}
	if authed.TOTPEnabled {
		t.Fatalf("expected TOTPEnabled=false after disabling 2FA")
	}
}

func TestTOTPReplayProtectionAndRecoveryCodes(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, filepath.Join(t.TempDir(), "totp-replay.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := database.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	admin, err := CreateInitialAdmin(ctx, db, "admin", "strong-password-123")
	if err != nil {
		t.Fatal(err)
	}
	secret, err := GenerateTOTPSecret()
	if err != nil {
		t.Fatal(err)
	}
	baseTime := time.Now().UTC().Truncate(30 * time.Second).Add(15 * time.Second)
	codeNow, err := TOTPCode(secret, baseTime)
	if err != nil {
		t.Fatal(err)
	}
	if err := EnableTOTP(ctx, db, admin.ID, secret, codeNow); err != nil {
		t.Fatalf("EnableTOTP() error = %v", err)
	}

	// First verification succeeds
	ok, err := verifyUserTOTPAt(ctx, db, admin.ID, codeNow, baseTime)
	if err != nil || !ok {
		t.Fatalf("first verifyUserTOTPAt() = (%v, %v), want (true, nil)", ok, err)
	}
	// Replay of the exact same code within the same window must be rejected
	ok, err = verifyUserTOTPAt(ctx, db, admin.ID, codeNow, baseTime.Add(5*time.Second))
	if err != nil || ok {
		t.Fatalf("replay verifyUserTOTPAt() = (%v, %v), want (false, nil)", ok, err)
	}
	// Next 30s step code succeeds
	nextTime := baseTime.Add(30 * time.Second)
	codeNext, err := TOTPCode(secret, nextTime)
	if err != nil {
		t.Fatal(err)
	}
	ok, err = verifyUserTOTPAt(ctx, db, admin.ID, codeNext, nextTime)
	if err != nil || !ok {
		t.Fatalf("next step verifyUserTOTPAt() = (%v, %v), want (true, nil)", ok, err)
	}

	// Recovery codes: generate, consume once, reject replay, and clear on disable
	codes, err := RegenerateRecoveryCodes(ctx, db, admin.ID)
	if err != nil {
		t.Fatalf("RegenerateRecoveryCodes() error = %v", err)
	}
	if len(codes) != recoveryCodeCount {
		t.Fatalf("recovery codes len = %d, want %d", len(codes), recoveryCodeCount)
	}
	remaining, err := RemainingRecoveryCodes(ctx, db, admin.ID)
	if err != nil || remaining != recoveryCodeCount {
		t.Fatalf("RemainingRecoveryCodes() = (%d, %v), want (%d, nil)", remaining, err, recoveryCodeCount)
	}

	// Accept lowercase without hyphen
	firstCode := strings.ToLower(strings.ReplaceAll(codes[0], "-", ""))
	ok, err = VerifyUserTOTP(ctx, db, admin.ID, firstCode)
	if err != nil || !ok {
		t.Fatalf("VerifyUserTOTP(recovery code) = (%v, %v), want (true, nil)", ok, err)
	}
	// Reject reuse of same recovery code
	ok, err = VerifyUserTOTP(ctx, db, admin.ID, codes[0])
	if err != nil || ok {
		t.Fatalf("reused recovery code = (%v, %v), want (false, nil)", ok, err)
	}
	remaining, err = RemainingRecoveryCodes(ctx, db, admin.ID)
	if err != nil || remaining != recoveryCodeCount-1 {
		t.Fatalf("RemainingRecoveryCodes() after use = (%d, %v), want (%d, nil)", remaining, err, recoveryCodeCount-1)
	}

	// Disabling 2FA clears all recovery codes
	if err := DisableTOTP(ctx, db, admin.ID); err != nil {
		t.Fatal(err)
	}
	remaining, err = RemainingRecoveryCodes(ctx, db, admin.ID)
	if err != nil || remaining != 0 {
		t.Fatalf("RemainingRecoveryCodes() after disable = (%d, %v), want (0, nil)", remaining, err)
	}
}
