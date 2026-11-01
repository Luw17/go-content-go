package siteconfig

import (
	"context"
	"errors"
	"testing"

	"github.com/Luw17/go-content-go/internal/database"
)

func TestSettingsDefaultsAndPersistence(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := database.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	settings, err := Load(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if settings != Defaults() {
		t.Fatalf("default settings = %+v, want %+v", settings, Defaults())
	}
	want := Settings{
		Title: "Site <title>", Tagline: "A short description", Logo: "/uploads/logo.png",
		ShowOnFront: FrontPage, PageOnFront: 42, HomeIntroTitle: "Welcome",
		HomePostsTitle: "Latest writing",
	}
	if err := Save(ctx, db, want); err != nil {
		t.Fatal(err)
	}
	got, err := Load(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("loaded settings = %+v, want %+v", got, want)
	}
}

func TestSettingsValidationAndFallbacks(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := database.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	invalid := []Settings{
		{Title: "", ShowOnFront: FrontPosts},
		{Title: "Valid", ShowOnFront: "external"},
		{Title: "Valid", ShowOnFront: FrontPage},
		{Title: "Valid", ShowOnFront: FrontPosts, PageOnFront: 1},
	}
	for _, settings := range invalid {
		if err := Save(ctx, db, settings); !errors.Is(err, ErrInvalidSettings) {
			t.Errorf("Save(%+v) error = %v, want ErrInvalidSettings", settings, err)
		}
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO options (key, value, updated_at) VALUES
			('site_title', '', 'now'),
			('show_on_front', 'unexpected', 'now'),
			('page_on_front', 'not-an-id', 'now')
	`); err != nil {
		t.Fatal(err)
	}
	settings, err := Load(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if settings.Title != Defaults().Title || settings.ShowOnFront != FrontPosts || settings.PageOnFront != 0 {
		t.Fatalf("invalid stored settings did not use safe defaults: %+v", settings)
	}
}
