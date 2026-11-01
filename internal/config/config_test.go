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
