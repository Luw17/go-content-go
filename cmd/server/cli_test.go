package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Luw17/go-content-go/internal/auth"
	"github.com/Luw17/go-content-go/internal/database"
)

func TestCLIVersionAndHelp(t *testing.T) {
	var out bytes.Buffer
	if err := runCLI([]string{"version"}, &out); err != nil {
		t.Fatalf("gcg version error = %v", err)
	}
	if !strings.HasPrefix(out.String(), "gcg ") {
		t.Fatalf("gcg version output = %q, want prefix 'gcg '", out.String())
	}

	out.Reset()
	if err := runCLI([]string{"help"}, &out); err != nil {
		t.Fatalf("gcg help error = %v", err)
	}
	for _, want := range []string{"gcg serve", "gcg backup", "gcg reset-password", "gcg disable-2fa"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("gcg help missing %q", want)
		}
	}

	if err := runCLI([]string{"nonexistent-cmd"}, &out); err == nil {
		t.Fatalf("expected error for unknown subcommand")
	}
}

func TestCLIBackupResetPasswordAndDisable2FA(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	dbPath := filepath.Join(root, "gcg.db")
	backupDir := filepath.Join(root, "backups")

	db, err := database.Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	admin, err := auth.CreateInitialAdmin(ctx, db, "ops_admin", "initial-strong-password")
	if err != nil {
		t.Fatal(err)
	}
	secret, err := auth.GenerateTOTPSecret()
	if err != nil {
		t.Fatal(err)
	}
	code, err := auth.TOTPCode(secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := auth.EnableTOTP(ctx, db, admin.ID, secret, code); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.RegenerateRecoveryCodes(ctx, db, admin.ID); err != nil {
		t.Fatal(err)
	}
	sessionTok, _, err := auth.CreateSession(ctx, db, admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	// 1. gcg backup
	var out bytes.Buffer
	if err := runCLI([]string{"backup", "-db", dbPath, "-backups", backupDir, "-backup-retention", "2"}, &out); err != nil {
		t.Fatalf("gcg backup error = %v", err)
	}
	if !strings.Contains(out.String(), "Backup created:") {
		t.Fatalf("unexpected backup output: %q", out.String())
	}
	entries, err := os.ReadDir(backupDir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("backups = %v (err=%v), want 1 file", entries, err)
	}

	// 2. gcg reset-password
	out.Reset()
	if err := runCLI([]string{"reset-password", "--user", "ops_admin", "--password", "brand-new-strong-password", "--db", dbPath}, &out); err != nil {
		t.Fatalf("gcg reset-password error = %v", err)
	}
	if !strings.Contains(out.String(), "Password reset") {
		t.Fatalf("unexpected reset-password output: %q", out.String())
	}

	// 3. gcg disable-2fa
	out.Reset()
	if err := runCLI([]string{"disable-2fa", "--user", "ops_admin", "--db", dbPath}, &out); err != nil {
		t.Fatalf("gcg disable-2fa error = %v", err)
	}
	if !strings.Contains(out.String(), "2FA disabled") {
		t.Fatalf("unexpected disable-2fa output: %q", out.String())
	}

	// Verify effects in DB
	db, err = database.Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if _, valid, err := auth.SessionUser(ctx, db, sessionTok); err != nil || valid {
		t.Fatalf("expected session to be revoked after reset-password, got valid=%v err=%v", valid, err)
	}
	authed, err := auth.Authenticate(ctx, db, "ops_admin", "brand-new-strong-password")
	if err != nil {
		t.Fatalf("Authenticate with new password failed: %v", err)
	}
	if authed.TOTPEnabled {
		t.Fatalf("expected TOTPEnabled=false after gcg disable-2fa")
	}
	remaining, err := auth.RemainingRecoveryCodes(ctx, db, authed.ID)
	if err != nil || remaining != 0 {
		t.Fatalf("expected 0 recovery codes after gcg disable-2fa, got %d (err=%v)", remaining, err)
	}
}
