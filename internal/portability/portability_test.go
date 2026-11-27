package portability

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Luw17/go-content-go/internal/auth"
	"github.com/Luw17/go-content-go/internal/content"
	"github.com/Luw17/go-content-go/internal/database"
)

func TestExportAndImportJSONMarkdownAndWXR(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, filepath.Join(t.TempDir(), "portability.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := database.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	admin, err := auth.CreateInitialAdmin(ctx, db, "port_admin", "a-very-secure-password")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := content.Save(ctx, db, content.Input{
		Type:        content.TypePost,
		Title:       "Primeiro Artigo",
		Slug:        "primeiro-artigo",
		Content:     "Texto em **Markdown**.",
		Status:      content.StatusPublished,
		NewCategory: "Guias",
		Tags:        "go, cms",
	}, admin.ID); err != nil {
		t.Fatal(err)
	}

	// 1. Export JSON & re-import
	jsonBytes, err := ExportJSON(ctx, db)
	if err != nil {
		t.Fatalf("ExportJSON() error = %v", err)
	}
	if !strings.Contains(string(jsonBytes), "Primeiro Artigo") {
		t.Fatalf("ExportJSON missing post title")
	}
	jsonSummary, err := ImportJSON(ctx, db, bytes.NewReader(jsonBytes), admin.ID)
	if err != nil || jsonSummary.Imported != 1 {
		t.Fatalf("ImportJSON() = (%+v, %v), want Imported=1", jsonSummary, err)
	}

	// 2. Export Markdown & re-import
	mdBytes, err := ExportMarkdown(ctx, db)
	if err != nil {
		t.Fatalf("ExportMarkdown() error = %v", err)
	}
	mdSummary, err := ImportMarkdown(ctx, db, bytes.NewReader(mdBytes), admin.ID)
	if err != nil || mdSummary.Imported != 2 {
		t.Fatalf("ImportMarkdown() = (%+v, %v), want Imported=2", mdSummary, err)
	}

	// 3. Import WordPress WXR XML
	wxr := `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0" xmlns:content="http://purl.org/rss/1.0/modules/content/" xmlns:wp="http://wordpress.org/export/1.2/">
  <channel>
    <item>
      <title>Artigo do WordPress</title>
      <wp:post_name>artigo-wp</wp:post_name>
      <wp:post_type>post</wp:post_type>
      <wp:status>publish</wp:status>
      <content:encoded>Conteudo migrado do WordPress.</content:encoded>
      <category domain="post_tag">migracao</category>
    </item>
  </channel>
</rss>`
	wxrSummary, err := ImportWordPressWXR(ctx, db, strings.NewReader(wxr), admin.ID)
	if err != nil || wxrSummary.Imported != 1 {
		t.Fatalf("ImportWordPressWXR() = (%+v, %v), want Imported=1", wxrSummary, err)
	}
}
