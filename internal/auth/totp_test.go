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
