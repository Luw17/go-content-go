package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfigurationPrecedence(t *testing.T) {
	envFile := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(envFile, []byte("GO_CONTENT_GO_ADDR=:9000\nGO_CONTENT_GO_DB='dotenv.db'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GO_CONTENT_GO_ADDR", ":7000")

	cfg, err := Load([]string{"-env-file", envFile, "-db", "flag.db"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Address != ":7000" {
		t.Errorf("Address = %q, want environment value :7000", cfg.Address)
	}
	if cfg.DatabasePath != "flag.db" {
		t.Errorf("DatabasePath = %q, want flag value flag.db", cfg.DatabasePath)
	}
}

func TestLoadDotenvAndDefaults(t *testing.T) {
	unsetEnv(t, "GO_CONTENT_GO_MAX_UPLOAD_SIZE")
	unsetEnv(t, "GO_CONTENT_GO_API_RATE_LIMIT")
	unsetEnv(t, "GO_CONTENT_GO_UPLOADS")
	unsetEnv(t, "GO_CONTENT_GO_THEMES")
	unsetEnv(t, "GO_CONTENT_GO_PLUGINS")
	envFile := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(envFile, []byte("export GO_CONTENT_GO_ADDR='127.0.0.1:8081'\n"), 0600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load([]string{"-env-file=" + envFile})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Address != "127.0.0.1:8081" {
		t.Errorf("Address = %q, want dotenv value", cfg.Address)
	}
	if cfg.DatabasePath != defaultDatabase {
		t.Errorf("DatabasePath = %q, want default %q", cfg.DatabasePath, defaultDatabase)
	}
	if cfg.UploadPath != defaultUploadPath || cfg.MaxUploadSize != defaultUploadLimit {
		t.Errorf("upload settings = (%q, %d), want defaults (%q, %d)", cfg.UploadPath, cfg.MaxUploadSize, defaultUploadPath, defaultUploadLimit)
	}
	if cfg.ThemesPath != defaultThemesPath {
		t.Errorf("themes path = %q, want default %q", cfg.ThemesPath, defaultThemesPath)
	}
	if cfg.PluginsPath != defaultPluginsPath {
		t.Errorf("plugins path = %q, want default %q", cfg.PluginsPath, defaultPluginsPath)
	}
	if cfg.APIRateLimit != defaultAPIRate {
		t.Errorf("API rate limit = %d, want default %d", cfg.APIRateLimit, defaultAPIRate)
	}
}

func TestLoadAPIRateLimit(t *testing.T) {
	unsetEnv(t, "GO_CONTENT_GO_API_RATE_LIMIT")
	envFile := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(envFile, []byte("GO_CONTENT_GO_API_RATE_LIMIT=120\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load([]string{"-env-file", envFile})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.APIRateLimit != 120 {
		t.Fatalf("API rate limit = %d, want 120", cfg.APIRateLimit)
	}
	if _, err := Load([]string{"-env-file", envFile, "-api-rate-limit", "0"}); err == nil {
		t.Fatal("Load() accepted a zero API rate limit")
	}
}

func TestLoadUploadConfiguration(t *testing.T) {
	unsetEnv(t, "GO_CONTENT_GO_UPLOADS")
	unsetEnv(t, "GO_CONTENT_GO_MAX_UPLOAD_SIZE")
	unsetEnv(t, "GO_CONTENT_GO_THEMES")
	unsetEnv(t, "GO_CONTENT_GO_PLUGINS")
	envFile := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(envFile, []byte("GO_CONTENT_GO_UPLOADS=./media-files\nGO_CONTENT_GO_MAX_UPLOAD_SIZE=2048\nGO_CONTENT_GO_THEMES=./site-themes\nGO_CONTENT_GO_PLUGINS=./site-plugins\n"), 0600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load([]string{"-env-file", envFile})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.UploadPath != "./media-files" || cfg.MaxUploadSize != 2048 || cfg.ThemesPath != "./site-themes" || cfg.PluginsPath != "./site-plugins" {
		t.Errorf("upload/theme/plugin configuration = (%q, %d, %q, %q), want (./media-files, 2048, ./site-themes, ./site-plugins)", cfg.UploadPath, cfg.MaxUploadSize, cfg.ThemesPath, cfg.PluginsPath)
	}

	if _, err := Load([]string{"-env-file", envFile, "-max-upload-size", "0"}); err == nil {
		t.Error("Load() accepted a zero upload limit")
	}
	if _, err := Load([]string{"-env-file", envFile, "-max-upload-size", "104857601"}); err == nil {
		t.Error("Load() accepted an upload limit over 100 MiB")
	}
}

func TestLoadRejectsInvalidDotenv(t *testing.T) {
	envFile := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(envFile, []byte("not-an-assignment\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load([]string{"-env-file", envFile}); err == nil {
		t.Fatal("Load() accepted an invalid dotenv assignment")
	}
}

func TestLoadTrustedProxiesAndBackupRetention(t *testing.T) {
	unsetEnv(t, "GO_CONTENT_GO_TRUSTED_PROXIES")
	unsetEnv(t, "GO_CONTENT_GO_BACKUP_RETENTION")
	envFile := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(envFile, []byte("GO_CONTENT_GO_TRUSTED_PROXIES=127.0.0.1,10.0.0.0/8,::1\nGO_CONTENT_GO_BACKUP_RETENTION=7\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load([]string{"-env-file", envFile})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(cfg.TrustedProxies) != 3 || cfg.TrustedProxies[0] != "127.0.0.1" || cfg.TrustedProxies[1] != "10.0.0.0/8" || cfg.TrustedProxies[2] != "::1" {
		t.Fatalf("TrustedProxies = %v, want [127.0.0.1 10.0.0.0/8 ::1]", cfg.TrustedProxies)
	}
	if cfg.BackupRetention != 7 {
		t.Fatalf("BackupRetention = %d, want 7", cfg.BackupRetention)
	}
	if _, err := Load([]string{"-env-file", envFile, "-trusted-proxies", "not-an-ip"}); err == nil {
		t.Fatal("Load() accepted an invalid trusted proxy entry")
	}
	if _, err := Load([]string{"-env-file", envFile, "-backup-retention", "0"}); err == nil {
		t.Fatal("Load() accepted a zero backup retention")
	}
}

func TestLoadGCGAliasesAndPortFallback(t *testing.T) {
	unsetEnv(t, "GCG_ADDR")
	unsetEnv(t, "GO_CONTENT_GO_ADDR")
	unsetEnv(t, "GCG_PORT")
	unsetEnv(t, "GO_CONTENT_GO_PORT")
	unsetEnv(t, "GCG_DB")
	unsetEnv(t, "GO_CONTENT_GO_DB")
	unsetEnv(t, "GCG_TRUSTED_PROXIES")
	unsetEnv(t, "GO_CONTENT_GO_TRUSTED_PROXIES")

	t.Setenv("GCG_PORT", "9090")
	t.Setenv("GO_CONTENT_GO_DB", "legacy.db")
	t.Setenv("GCG_DB", "short.db")
	t.Setenv("GCG_TRUSTED_PROXIES", "127.0.0.1,10.0.0.0/8")

	cfg, err := Load([]string{"-env-file", filepath.Join(t.TempDir(), "missing.env")})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Address != ":9090" {
		t.Errorf("Address = %q, want :9090 from GCG_PORT", cfg.Address)
	}
	if cfg.DatabasePath != "short.db" {
		t.Errorf("DatabasePath = %q, want short.db (GCG_DB takes precedence over GO_CONTENT_GO_DB)", cfg.DatabasePath)
	}
	if len(cfg.TrustedProxies) != 2 {
		t.Errorf("TrustedProxies = %v, want 2 entries", cfg.TrustedProxies)
	}
}

func unsetEnv(t *testing.T, key string) {
	t.Helper()
	value, exists := os.LookupEnv(key)
	if err := os.Unsetenv(key); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if exists {
			_ = os.Setenv(key, value)
		} else {
			_ = os.Unsetenv(key)
		}
	})
}
