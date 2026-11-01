package siteconfig

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	FrontPosts = "posts"
	FrontPage  = "page"
)

var ErrInvalidSettings = errors.New("invalid site settings")

type Settings struct {
	Title          string
	Tagline        string
	Logo           string
	ShowOnFront    string
	PageOnFront    int64
	HomeIntroTitle string
	HomePostsTitle string
}

func Defaults() Settings {
	return Settings{
		Title:          "Go Content Go!",
		Tagline:        "Um espaço para compartilhar ideias.",
		ShowOnFront:    FrontPosts,
		HomeIntroTitle: "Ideias para ler com calma.",
		HomePostsTitle: "Publicações recentes",
	}
}

func Load(ctx context.Context, db *sql.DB) (Settings, error) {
	settings := Defaults()
	rows, err := db.QueryContext(ctx, `
		SELECT key, value FROM options
		WHERE key IN (
			'site_title', 'site_tagline', 'site_logo', 'show_on_front', 'page_on_front',
			'home_intro_title', 'home_posts_title'
		)
	`)
	if err != nil {
		return Settings{}, fmt.Errorf("load site settings: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return Settings{}, fmt.Errorf("scan site setting: %w", err)
		}
		switch key {
		case "site_title":
			if strings.TrimSpace(value) != "" {
				settings.Title = value
			}
		case "site_tagline":
			settings.Tagline = value
		case "site_logo":
			settings.Logo = value
		case "show_on_front":
			if value == FrontPage {
				settings.ShowOnFront = FrontPage
			}
		case "page_on_front":
			id, err := strconv.ParseInt(value, 10, 64)
			if err == nil && id > 0 {
				settings.PageOnFront = id
			}
		case "home_intro_title":
			if strings.TrimSpace(value) != "" {
				settings.HomeIntroTitle = value
			}
		case "home_posts_title":
			if strings.TrimSpace(value) != "" {
				settings.HomePostsTitle = value
			}
		}
	}
	if err := rows.Err(); err != nil {
		return Settings{}, fmt.Errorf("read site settings: %w", err)
	}
	if err := rows.Close(); err != nil {
		return Settings{}, fmt.Errorf("close site settings: %w", err)
	}
	return settings, nil
}

func Save(ctx context.Context, db *sql.DB, settings Settings) error {
	settings.Title = strings.TrimSpace(settings.Title)
	settings.Tagline = strings.TrimSpace(settings.Tagline)
	settings.Logo = strings.TrimSpace(settings.Logo)
	settings.HomeIntroTitle = strings.TrimSpace(settings.HomeIntroTitle)
	settings.HomePostsTitle = strings.TrimSpace(settings.HomePostsTitle)
	if settings.Title == "" || len([]rune(settings.Title)) > 120 ||
		len([]rune(settings.Tagline)) > 250 ||
		settings.HomeIntroTitle == "" || len([]rune(settings.HomeIntroTitle)) > 120 ||
		settings.HomePostsTitle == "" || len([]rune(settings.HomePostsTitle)) > 120 ||
		(settings.ShowOnFront != FrontPosts && settings.ShowOnFront != FrontPage) ||
		(settings.ShowOnFront == FrontPage && settings.PageOnFront <= 0) ||
		(settings.ShowOnFront == FrontPosts && settings.PageOnFront != 0) {
		return ErrInvalidSettings
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin site settings update: %w", err)
	}
	defer tx.Rollback()

	now := time.Now().UTC().Format(time.RFC3339Nano)
	values := []struct {
		key   string
		value string
	}{
		{"site_title", settings.Title},
		{"site_tagline", settings.Tagline},
		{"site_logo", settings.Logo},
		{"show_on_front", settings.ShowOnFront},
		{"page_on_front", strconv.FormatInt(settings.PageOnFront, 10)},
		{"home_intro_title", settings.HomeIntroTitle},
		{"home_posts_title", settings.HomePostsTitle},
	}
	for _, setting := range values {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO options (key, value, updated_at) VALUES (?, ?, ?)
			ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at
		`, setting.key, setting.value, now); err != nil {
			return fmt.Errorf("save site setting %q: %w", setting.key, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit site settings: %w", err)
	}
	return nil
}
