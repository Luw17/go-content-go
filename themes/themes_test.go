package themes

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMissingThemeDirectoryUsesEmbeddedDefault(t *testing.T) {
	manager, err := New(filepath.Join(t.TempDir(), "missing"))
	if err != nil {
		t.Fatal(err)
	}
	available, err := manager.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(available) != 1 || available[0].ID != DefaultName || !available[0].Valid {
		t.Fatalf("available themes = %+v, want embedded default", available)
	}
	active, templates, err := manager.Resolve("not-installed")
	if err != nil {
		t.Fatal(err)
	}
	if active != DefaultName {
		t.Fatalf("resolved theme = %q, want %q", active, DefaultName)
	}
	var rendered strings.Builder
	if err := templates.ExecuteTemplate(&rendered, "index.html", map[string]any{"Theme": active}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rendered.String(), "/themes/default/style.css") {
		t.Fatalf("embedded homepage did not load theme stylesheet: %s", rendered.String())
	}
	asset, err := manager.OpenAsset(DefaultName, "style.css")
	if err != nil {
		t.Fatal(err)
	}
	defer asset.Close()
	css, err := io.ReadAll(asset)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(css), "system-ui") {
		t.Fatalf("embedded stylesheet missing expected CSS: %s", css)
	}
}

func TestEditorialThemeIsAvailable(t *testing.T) {
	manager, err := New(".")
	if err != nil {
		t.Fatal(err)
	}
	available, err := manager.List()
	if err != nil {
		t.Fatal(err)
	}
	for _, theme := range available {
		if theme.ID == "editorial" {
			if theme.Name != "Editorial" || theme.Version != "1.0.0" || !theme.Valid {
				t.Fatalf("editorial theme metadata = %+v", theme)
			}
			if err := manager.Validate("editorial"); err != nil {
				t.Fatalf("validate editorial theme: %v", err)
			}
			asset, err := manager.OpenAsset("editorial", "style.css")
			if err != nil {
				t.Fatalf("open editorial stylesheet: %v", err)
			}
			defer asset.Close()
			css, err := io.ReadAll(asset)
			if err != nil {
				t.Fatalf("read editorial stylesheet: %v", err)
			}
			if !strings.Contains(string(css), "--accent: #d65335") {
				t.Fatal("editorial stylesheet does not contain its theme palette")
			}
			return
		}
	}
	t.Fatal("editorial theme is not listed")
}

func TestThemeMetadataTemplatesAndAssetTraversal(t *testing.T) {
	root := t.TempDir()
	writeTheme(t, root, "custom")
	manager, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	available, err := manager.List()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, theme := range available {
		if theme.ID == "custom" {
			found = true
			if theme.Name != "Custom theme" || theme.Author != "Theme author" || theme.Version != "2.0" || !theme.Valid {
				t.Errorf("custom theme metadata = %+v", theme)
			}
		}
	}
	if !found {
		t.Fatal("custom theme missing from list")
	}
	active, templates, err := manager.Resolve("custom")
	if err != nil || active != "custom" {
		t.Fatalf("Resolve(custom) = %q, %v", active, err)
	}
	var rendered strings.Builder
	if err := templates.ExecuteTemplate(&rendered, "index.html", map[string]any{"Theme": active}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rendered.String(), "custom-index") {
		t.Fatalf("custom template not loaded: %s", rendered.String())
	}
	if _, err := manager.OpenAsset("custom", "../outside"); !errors.Is(err, ErrNotFound) {
		t.Errorf("path traversal error = %v, want ErrNotFound", err)
	}

	outside := filepath.Join(t.TempDir(), "outside.css")
	if err := os.WriteFile(outside, []byte("outside"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "custom", "escape.css")); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.OpenAsset("custom", "escape.css"); !errors.Is(err, ErrNotFound) {
		t.Errorf("symlink escape error = %v, want ErrNotFound", err)
	}
}

func TestIncompleteThemeIsInvalidAndFallsBack(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "broken"), 0700); err != nil {
		t.Fatal(err)
	}
	manager, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Validate("broken"); err == nil {
		t.Fatal("Validate(broken) succeeded for incomplete theme")
	}
	active, _, err := manager.Resolve("broken")
	if err != nil {
		t.Fatal(err)
	}
	if active != DefaultName {
		t.Errorf("resolved incomplete theme = %q, want embedded default", active)
	}
}

func writeTheme(t *testing.T, root, name string) {
	t.Helper()
	directory := filepath.Join(root, name)
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"theme.json":  `{"name":"Custom theme","author":"Theme author","version":"2.0"}`,
		"index.html":  `<h1>custom-index</h1>`,
		"single.html": `<h1>custom-single</h1>`,
		"page.html":   `<h1>custom-page</h1>`,
		"404.html":    `<h1>custom-404</h1>`,
		"style.css":   `body { color: red; }`,
	}
	for filename, content := range files {
		if err := os.WriteFile(filepath.Join(directory, filename), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
}
