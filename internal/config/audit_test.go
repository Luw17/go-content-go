package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestAuditPermissionsFlagsUnsafeModesAndAcceptsSafeModes(t *testing.T) {
	dir := t.TempDir()
	dbFile := filepath.Join(dir, "cms.db")
	if err := os.WriteFile(dbFile, []byte("sqlite"), 0o600); err != nil {
		t.Fatal(err)
	}
	safeDir := filepath.Join(dir, "uploads")
	if err := os.MkdirAll(safeDir, 0o750); err != nil {
		t.Fatal(err)
	}
	unsafeDir := filepath.Join(dir, "unsafe")
	if err := os.MkdirAll(unsafeDir, 0o777); err != nil {
		t.Fatal(err)
	}
	_ = os.Chmod(unsafeDir, 0o777)

	findings := AuditPermissions([]AuditTarget{
		{Label: "Banco SQLite", Path: dbFile, SensitiveDB: true},
		{Label: "Uploads", Path: safeDir},
		{Label: "Diretório inseguro", Path: unsafeDir},
		{Label: "Inexistente", Path: filepath.Join(dir, "missing")},
	})

	if len(findings) != 4 {
		t.Fatalf("findings len = %d, want 4", len(findings))
	}
	if findings[0].Status != "ok" {
		t.Errorf("dbFile status = %q, want ok (%s)", findings[0].Status, findings[0].Detail)
	}
	if findings[1].Status != "ok" {
		t.Errorf("safeDir status = %q, want ok (%s)", findings[1].Status, findings[1].Detail)
	}
	if runtime.GOOS != "windows" && findings[2].Status != "warning" {
		t.Errorf("unsafeDir status = %q, want warning (%s)", findings[2].Status, findings[2].Detail)
	}
	if findings[3].Status != "missing" {
		t.Errorf("missing path status = %q, want missing", findings[3].Status)
	}
}
