package navigation

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Luw17/go-content-go/internal/auth"
	"github.com/Luw17/go-content-go/internal/content"
	"github.com/Luw17/go-content-go/internal/database"
)

func TestMenusAndPublicRendering(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := database.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	admin, err := auth.CreateInitialAdmin(ctx, db, "menu_admin", "a-very-secure-password")
	if err != nil {
		t.Fatal(err)
	}
	page, err := content.Save(ctx, db, content.Input{
		Type: content.TypePage, Title: `About <script>`, Content: "About", Status: content.StatusPublished,
	}, admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	menu, err := store.Create(ctx, "Main menu", LocationPrimary)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AddPage(ctx, menu.ID, page.ID, `About <script>`); err != nil {
		t.Fatal(err)
	}
	if err := store.AddLink(ctx, menu.ID, "External", "https://example.test/a?x=1&y=2"); err != nil {
		t.Fatal(err)
	}
	if err := store.AddLink(ctx, menu.ID, "Unsafe", "javascript:alert(1)"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("unsafe URL error = %v, want ErrInvalidInput", err)
	}
	items, err := store.Items(ctx, menu.ID)
	if err != nil || len(items) != 2 {
		t.Fatalf("menu items = %+v, error %v", items, err)
	}
	if err := store.Move(ctx, items[1].ID, "up"); err != nil {
		t.Fatal(err)
	}
	ordered, err := store.Items(ctx, menu.ID)
	if err != nil {
		t.Fatal(err)
	}
	if ordered[0].Label != "External" {
		t.Fatalf("first menu item after move = %q, want External", ordered[0].Label)
	}
	html, err := store.RenderHTML(ctx, LocationPrimary)
	if err != nil {
		t.Fatal(err)
	}
	rendered := string(html)
	if !strings.Contains(rendered, `href="/`+page.Slug+`"`) ||
		!strings.Contains(rendered, `About &lt;script&gt;`) ||
		!strings.Contains(rendered, `href="https://example.test/a?x=1&amp;y=2"`) ||
		strings.Contains(rendered, "<script>") {
		t.Fatalf("unsafe or unexpected rendered menu: %s", rendered)
	}
	if _, err := db.ExecContext(ctx, `UPDATE posts SET deleted_at = 'now' WHERE id = ?`, page.ID); err != nil {
		t.Fatal(err)
	}
	html, err = store.RenderHTML(ctx, LocationPrimary)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(html), "About") || !strings.Contains(string(html), "External") {
		t.Fatalf("deleted page was not ignored in public menu: %s", html)
	}
}

func TestMenuValidationAndDeletion(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := database.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(ctx, "Bad location", "header"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("invalid location error = %v", err)
	}
	menu, err := store.Create(ctx, "Footer", LocationFooter)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AddLink(ctx, menu.ID, "Bad", "//evil.test"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("protocol-relative link error = %v", err)
	}
	if err := store.DeleteMenu(ctx, menu.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteMenu(ctx, menu.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing menu deletion error = %v", err)
	}
}
