package plugins

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Luw17/go-content-go/internal/database"
)

func TestPluginSettingsIsolationAndExtendedHooks(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, filepath.Join(t.TempDir(), "plugins-ext.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := database.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}

	root := t.TempDir()
	pluginDir := filepath.Join(root, "seo-extra")
	if err := os.MkdirAll(pluginDir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{
		"id":"seo-extra",
		"name":"SEO Extra",
		"version":"1.0.0",
		"description":"Adds FAQ JSON-LD and admin page",
		"author":"Test",
		"main_file":"main.lua",
		"hooks":["content:before_render","content:after_render","head:inject_jsonld","admin:menu_register","admin:page_register"]
	}`
	script := `return {
		hooks = {
			["content:before_render"] = function(md)
				local prefix = settings.get("prefix") or ""
				return prefix .. md
			end,
			["content:after_render"] = function(html)
				return html .. "<p>after-hook</p>"
			end,
			["head:inject_jsonld"] = function(current, ctx)
				return '{"@context":"https://schema.org","@type":"FAQPage","name":"' .. (ctx.title or "") .. '"}'
			end,
			["admin:menu_register"] = function()
				return { label = "SEO Extra Menu" }
			end,
			["admin:page_register"] = function()
				local p = settings.get("prefix") or "none"
				return { title = "Configurar SEO Extra", html = "<p>Prefixo atual: " .. p .. "</p>" }
			end
		}
	}`
	if err := os.WriteFile(filepath.Join(pluginDir, "plugin.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginDir, "main.lua"), []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}

	mgr, err := New(root, DefaultTimeout)
	if err != nil {
		t.Fatal(err)
	}
	mgr.SetDB(db)
	if err := mgr.SetActive(ctx, db, "seo-extra", true); err != nil {
		t.Fatalf("SetActive() error = %v", err)
	}

	// Store settings for two distinct plugins to verify strict isolation
	if err := SetSetting(ctx, db, "seo-extra", "prefix", "INTRO: "); err != nil {
		t.Fatal(err)
	}
	if err := SetSetting(ctx, db, "other-plugin", "prefix", "SECRET: "); err != nil {
		t.Fatal(err)
	}

	before := mgr.ApplyFilters(ctx, "content:before_render", "corpo")
	if before != "INTRO: corpo" {
		t.Fatalf("content:before_render = %q, want %q", before, "INTRO: corpo")
	}

	after := mgr.ApplyFilters(ctx, "content:after_render", "<p>corpo</p>")
	if !strings.Contains(after.(string), "after-hook") {
		t.Fatalf("content:after_render = %q", after)
	}

	jsonld := mgr.ApplyFilters(ctx, "head:inject_jsonld", "", map[string]any{"title": "Site Teste"})
	if !strings.Contains(jsonld.(string), `"FAQPage"`) || !strings.Contains(jsonld.(string), `"Site Teste"`) {
		t.Fatalf("head:inject_jsonld = %q", jsonld)
	}

	menus := mgr.AdminMenus(ctx)
	if len(menus) != 1 || menus[0].Label != "SEO Extra Menu" || menus[0].Path != "/admin/plugin/seo-extra" {
		t.Fatalf("AdminMenus() = %+v", menus)
	}

	view, ok, err := mgr.RenderAdminPage(ctx, "seo-extra")
	if err != nil || !ok || view.Title != "Configurar SEO Extra" || !strings.Contains(view.HTML, "INTRO:") {
		t.Fatalf("RenderAdminPage() = (%+v, %v, %v)", view, ok, err)
	}
}
