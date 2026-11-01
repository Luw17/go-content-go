package plugins

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Luw17/go-content-go/internal/database"
)

func TestActivationPersistsAndRunsOnlyActiveFilters(t *testing.T) {
	ctx := context.Background()
	db := newPluginTestDB(t)
	root := t.TempDir()
	writePlugin(t, root, "filter", `return {
  hooks = {
    ["content:render"] = function(value) return value .. " filtered" end
  }
}`)
	manager, err := New(root, 100*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if got := manager.ApplyFilters(ctx, "content:render", "source"); got != "source" {
		t.Fatalf("inactive plugin modified input: %#v", got)
	}
	if err := manager.SetActive(ctx, db, "filter", true); err != nil {
		t.Fatal(err)
	}
	if got := manager.ApplyFilters(ctx, "content:render", "source"); got != "source filtered" {
		t.Fatalf("active plugin result = %#v", got)
	}
	var active []string
	var raw string
	if err := db.QueryRowContext(ctx, "SELECT value FROM options WHERE key = 'active_plugins'").Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(raw), &active); err != nil || len(active) != 1 || active[0] != "filter" {
		t.Fatalf("persisted active plugins = %q, parsed %v, error %v", raw, active, err)
	}
	if err := manager.SetActive(ctx, db, "filter", false); err != nil {
		t.Fatal(err)
	}
	if got := manager.ApplyFilters(ctx, "content:render", "source"); got != "source" {
		t.Fatalf("deactivated plugin modified input: %#v", got)
	}
}

func TestPluginTimeoutErrorsAndSandbox(t *testing.T) {
	ctx := context.Background()
	db := newPluginTestDB(t)
	root := t.TempDir()
	writePlugin(t, root, "loop", `return {
  hooks = {
    ["content:render"] = function(value)
      if os ~= nil or io ~= nil or debug ~= nil or package ~= nil then error("unsafe library") end
      while true do end
      return value
    end
  }
}`)
	manager, err := New(root, 25*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.SetActive(ctx, db, "loop", true); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if got := manager.ApplyFilters(ctx, "content:render", "original"); got != "original" {
		t.Fatalf("failed plugin filter should preserve input, got %#v", got)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("timed out plugin took %s to return", elapsed)
	}
}

func TestInvalidManifestAndScriptCannotBeActivated(t *testing.T) {
	ctx := context.Background()
	db := newPluginTestDB(t)
	root := t.TempDir()
	invalidDirectory := filepath.Join(root, "bad")
	if err := os.MkdirAll(invalidDirectory, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(invalidDirectory, "plugin.json"), []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	writePlugin(t, root, "syntax", `return { hooks = { ["content:render"] = function( end } }`)
	manager, err := New(root, 100*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	items, err := manager.List(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		if item.ID == "bad" && item.Valid {
			t.Error("malformed manifest marked valid")
		}
		if item.ID == "syntax" && !item.Valid {
			t.Error("inactive script should not be loaded during plugin listing")
		}
	}
	if err := manager.SetActive(ctx, db, "bad", true); err == nil {
		t.Fatal("malformed manifest activated")
	}
	if err := manager.SetActive(ctx, db, "syntax", true); err == nil {
		t.Fatal("invalid Lua syntax activated")
	}
	if err := manager.SetActive(ctx, db, "missing", true); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing plugin error = %v, want ErrNotFound", err)
	}
	if err := os.Symlink(filepath.Join(t.TempDir(), "outside-plugin"), filepath.Join(root, "linked")); err != nil {
		t.Fatal(err)
	}
	if err := manager.SetActive(ctx, db, "linked", true); !errors.Is(err, ErrNotFound) {
		t.Errorf("symlinked plugin directory error = %v, want ErrNotFound", err)
	}
}

func TestActivePluginReloadAndHeadTagSanitizationInput(t *testing.T) {
	ctx := context.Background()
	db := newPluginTestDB(t)
	root := t.TempDir()
	writePlugin(t, root, "meta", `return {
  hooks = {
    ["head:inject_tags"] = function(value)
      return value .. '<meta name="description" content="safe"><script>alert(1)</script>'
    end
  }
}`)
	manager, err := New(root, 100*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.SetActive(ctx, db, "meta", true); err != nil {
		t.Fatal(err)
	}
	reloaded, err := New(root, 100*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if err := reloaded.Reload(ctx, db); err != nil {
		t.Fatal(err)
	}
	result := reloaded.ApplyFilters(ctx, "head:inject_tags", "")
	if !strings.Contains(result.(string), `<meta name="description" content="safe">`) ||
		!strings.Contains(result.(string), "<script>") {
		t.Fatalf("head hook did not return test payload: %#v", result)
	}
}

func TestBrokenActivePluginIsSkippedWithoutBlockingReloadOrDeactivation(t *testing.T) {
	ctx := context.Background()
	db := newPluginTestDB(t)
	root := t.TempDir()
	writePlugin(t, root, "reloadable", `return {
  hooks = {
    ["content:render"] = function(value) return value .. " active" end
  }
}`)
	manager, err := New(root, 100*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.SetActive(ctx, db, "reloadable", true); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "reloadable", "main.lua"), []byte("not valid Lua ("), 0600); err != nil {
		t.Fatal(err)
	}
	reloaded, err := New(root, 100*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if err := reloaded.Reload(ctx, db); err != nil {
		t.Fatalf("reload with broken active plugin: %v", err)
	}
	if got := reloaded.ApplyFilters(ctx, "content:render", "source"); got != "source" {
		t.Errorf("broken active plugin ran after reload: %#v", got)
	}
	if err := reloaded.SetActive(ctx, db, "reloadable", false); err != nil {
		t.Fatalf("deactivate broken plugin: %v", err)
	}
}

func writePlugin(t *testing.T, root, id, source string) {
	t.Helper()
	directory := filepath.Join(root, id)
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	manifest := `{"id":"` + id + `","name":"` + id + `","version":"1.0","description":"test plugin","author":"test","main_file":"main.lua","hooks":["content:render","head:inject_tags"]}`
	if strings.Contains(source, "head:inject_tags") && !strings.Contains(source, "content:render") {
		manifest = `{"id":"` + id + `","name":"` + id + `","version":"1.0","description":"test plugin","author":"test","main_file":"main.lua","hooks":["head:inject_tags"]}`
	}
	if strings.Contains(source, "content:render") && !strings.Contains(source, "head:inject_tags") {
		manifest = `{"id":"` + id + `","name":"` + id + `","version":"1.0","description":"test plugin","author":"test","main_file":"main.lua","hooks":["content:render"]}`
	}
	if err := os.WriteFile(filepath.Join(directory, "plugin.json"), []byte(manifest), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "main.lua"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
}

func newPluginTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := database.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(context.Background(), db); err != nil {
		db.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close plugin test database: %v", err)
		}
	})
	return db
}
