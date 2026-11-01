package themes

import (
	"archive/zip"
	"bytes"
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
	if len(available) != 2 || available[0].ID != DefaultName || !available[0].Valid ||
		available[1].ID != "editorial" || available[1].Name != "Editorial" || !available[1].Valid {
		t.Fatalf("available themes = %+v, want embedded default and editorial themes", available)
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

	active, templates, err = manager.Resolve("editorial")
	if err != nil {
		t.Fatal(err)
	}
	if active != "editorial" {
		t.Fatalf("resolved theme = %q, want editorial", active)
	}
	rendered.Reset()
	if err := templates.ExecuteTemplate(&rendered, "index.html", map[string]any{"Theme": active}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rendered.String(), "/themes/editorial/style.css") {
		t.Fatalf("embedded editorial homepage did not load its stylesheet: %s", rendered.String())
	}
	asset, err = manager.OpenAsset("editorial", "style.css")
	if err != nil {
		t.Fatal(err)
	}
	defer asset.Close()
	css, err = io.ReadAll(asset)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(css), "--accent: #d65335") {
		t.Fatalf("embedded editorial stylesheet missing expected palette: %s", css)
	}
}

func TestEmbeddedThemesTakePrecedenceOverPersistentThemeCopies(t *testing.T) {
	root := t.TempDir()
	writeTheme(t, root, DefaultName)
	writeTheme(t, root, "editorial")
	writeTheme(t, root, "default-antigo")
	manager, err := New(root)
	if err != nil {
		t.Fatal(err)
	}

	available, err := manager.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(available) != 3 || available[0].Name != "Default" || available[1].Name != "Editorial" ||
		available[2].ID != "default-antigo" {
		t.Fatalf("theme list = %+v, want bundled themes and the separately named custom theme", available)
	}

	for _, name := range embeddedThemeIDs {
		active, templates, err := manager.Resolve(name)
		if err != nil {
			t.Fatalf("resolve %q: %v", name, err)
		}
		if active != name {
			t.Fatalf("resolved theme = %q, want %q", active, name)
		}
		var rendered strings.Builder
		if err := templates.ExecuteTemplate(&rendered, "index.html", map[string]any{"Theme": active}); err != nil {
			t.Fatalf("render embedded %q theme: %v", name, err)
		}
		if strings.Contains(rendered.String(), "custom-index") {
			t.Errorf("%q used the stale template from the themes directory", name)
		}

		asset, err := manager.OpenAsset(name, "style.css")
		if err != nil {
			t.Fatalf("open %q stylesheet: %v", name, err)
		}
		css, err := io.ReadAll(asset)
		closeErr := asset.Close()
		if err != nil {
			t.Fatalf("read %q stylesheet: %v", name, err)
		}
		if closeErr != nil {
			t.Fatalf("close %q stylesheet: %v", name, closeErr)
		}
		if strings.Contains(string(css), "color: red") {
			t.Errorf("%q used the stale stylesheet from the themes directory", name)
		}
		if name == DefaultName && !strings.Contains(string(css), `content: "|";`) {
			t.Error("default theme did not serve its bundled menu separator")
		}
	}

	active, templates, err := manager.Resolve("default-antigo")
	if err != nil || active != "default-antigo" {
		t.Fatalf("resolve older custom theme = %q, %v", active, err)
	}
	var rendered strings.Builder
	if err := templates.ExecuteTemplate(&rendered, "index.html", map[string]any{"Theme": active}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rendered.String(), "custom-index") {
		t.Fatal("separately named older theme template was not loaded from the persistent themes directory")
	}
	asset, err := manager.OpenAsset(active, "style.css")
	if err != nil {
		t.Fatal(err)
	}
	css, err := io.ReadAll(asset)
	if closeErr := asset.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(css), "color: red") {
		t.Fatal("separately named older theme was not loaded from the persistent themes directory")
	}
}

func TestBundledThemeCannotBeReplacedByUpload(t *testing.T) {
	for _, name := range embeddedThemeIDs {
		var archive bytes.Buffer
		writer := zip.NewWriter(&archive)
		for filename, content := range map[string]string{
			name + "/index.html":  "<h1>custom</h1>",
			name + "/single.html": "<h1>custom</h1>",
			name + "/page.html":   "<h1>custom</h1>",
			name + "/404.html":    "<h1>custom</h1>",
			name + "/style.css":   "body { color: red; }",
		} {
			file, err := writer.Create(filename)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := file.Write([]byte(content)); err != nil {
				t.Fatal(err)
			}
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}

		manager, err := New(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := manager.InstallZip(bytes.NewReader(archive.Bytes()), name+".zip"); !errors.Is(err, ErrBundledTheme) {
			t.Errorf("install bundled theme %q error = %v, want ErrBundledTheme", name, err)
		}
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
