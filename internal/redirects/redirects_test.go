package redirects

import (
	"context"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/Luw17/go-content-go/internal/database"
)

func TestRedirectCRUDAndValidation(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, filepath.Join(t.TempDir(), "redirects.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := database.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}

	// Reject invalid cases
	invalidCases := []struct {
		src, dst string
		code     int
	}{
		{"/", "/home", 301},
		{"/admin/posts", "/novo", 301},
		{"/api/v1/posts", "/novo", 301},
		{"/mesmo", "/mesmo", 301},
		{"//evil.example", "/ok", 301},
		{"/ok", "//evil.example", 301},
		{"/ok", "/dest", 307},
	}
	for _, tc := range invalidCases {
		if _, err := Save(ctx, db, tc.src, tc.dst, tc.code); err == nil {
			t.Errorf("Save(%q, %q, %d) succeeded, want error", tc.src, tc.dst, tc.code)
		}
	}

	// Create 301 redirect
	saved, err := Save(ctx, db, "antigo/", "/novo/", http.StatusMovedPermanently)
	if err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if saved.SourcePath != "/antigo" || saved.TargetPath != "/novo" || saved.StatusCode != 301 {
		t.Fatalf("saved redirect = %+v", saved)
	}

	// Upsert updates target and status
	updated, err := Save(ctx, db, "/antigo", "https://destino.example/final", http.StatusFound)
	if err != nil {
		t.Fatalf("Save(upsert) error = %v", err)
	}
	if updated.TargetPath != "https://destino.example/final" || updated.StatusCode != 302 {
		t.Fatalf("updated redirect = %+v", updated)
	}

	found, ok, err := Find(ctx, db, "/antigo/")
	if err != nil || !ok || found.ID != updated.ID {
		t.Fatalf("Find() = (%+v, %v, %v)", found, ok, err)
	}

	items, err := List(ctx, db)
	if err != nil || len(items) != 1 {
		t.Fatalf("List() = (%d, %v), want 1", len(items), err)
	}

	if err := Delete(ctx, db, saved.ID); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if _, ok, err := Find(ctx, db, "/antigo"); err != nil || ok {
		t.Fatalf("Find after delete = (%v, %v), want (false, nil)", ok, err)
	}
}
