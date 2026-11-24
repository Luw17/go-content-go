package builder

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Luw17/go-content-go/internal/content"
	"github.com/Luw17/go-content-go/internal/navigation"
	"github.com/Luw17/go-content-go/internal/siteconfig"
)

const (
	SectionHeader = "header"
	SectionFooter = "footer"

	ScopeAll   = "all"
	ScopeHome  = "home"
	ScopePosts = "posts"
	ScopePages = "pages"
	ScopeSlug  = "slug"
)

type FooterLink struct {
	Label string `json:"label"`
	URL   string `json:"url"`
}

type FooterColumn struct {
	Title string       `json:"title"`
	Links []FooterLink `json:"links"`
}

type SectionConfig struct {
	Layout           string         `json:"layout,omitempty"`
	Tone             string         `json:"tone,omitempty"`
	ShowLogo         bool           `json:"show_logo"`
	ShowSiteTitle    bool           `json:"show_site_title"`
	ShowTagline      bool           `json:"show_tagline,omitempty"`
	ShowMenu         bool           `json:"show_menu"`
	MenuLocation     string         `json:"menu_location,omitempty"`
	Sticky           bool           `json:"sticky,omitempty"`
	AnnouncementText string         `json:"announcement_text,omitempty"`
	AnnouncementURL  string         `json:"announcement_url,omitempty"`
	CTALabel         string         `json:"cta_label,omitempty"`
	CTAURL           string         `json:"cta_url,omitempty"`
	CustomText       string         `json:"custom_text,omitempty"`
	CopyrightText    string         `json:"copyright_text,omitempty"`
	Columns          []FooterColumn `json:"columns,omitempty"`
}

type SiteSection struct {
	ID          int64
	Name        string
	SectionType string
	TargetScope string
	TargetSlug  string
	Config      SectionConfig
	ConfigJSON  string
	Active      bool
	CreatedAt   string
	UpdatedAt   string
}

type SectionInput struct {
	ID          int64
	Name        string
	SectionType string
	TargetScope string
	TargetSlug  string
	Config      SectionConfig
	Active      bool
}

func DefaultHeaderConfig() SectionConfig {
	return SectionConfig{
		Layout:        "split",
		Tone:          "default",
		ShowLogo:      true,
		ShowSiteTitle: true,
		ShowMenu:      true,
		MenuLocation:  navigation.LocationPrimary,
	}
}

func DefaultFooterConfig() SectionConfig {
	return SectionConfig{
		Layout:        "columns",
		Tone:          "surface",
		ShowLogo:      true,
		ShowSiteTitle: true,
		ShowTagline:   true,
		ShowMenu:      true,
		MenuLocation:  navigation.LocationFooter,
		CopyrightText: "© Todos os direitos reservados.",
	}
}

func (s *Store) ListSections(ctx context.Context) ([]SiteSection, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, section_type, target_scope, target_slug, config_json, active, created_at, updated_at
		FROM site_sections
		ORDER BY section_type ASC, active DESC, updated_at DESC, id DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("list site sections: %w", err)
	}
	defer rows.Close()

	var sections []SiteSection
	for rows.Next() {
		sec, err := scanSection(rows)
		if err != nil {
			return nil, err
		}
		sections = append(sections, sec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read site sections: %w", err)
	}
	return sections, nil
}

func (s *Store) GetSection(ctx context.Context, id int64) (SiteSection, error) {
	if id <= 0 {
		return SiteSection{}, ErrNotFound
	}
	row := s.db.QueryRowContext(ctx, `
		SELECT id, name, section_type, target_scope, target_slug, config_json, active, created_at, updated_at
		FROM site_sections WHERE id = ?
	`, id)
	return scanSection(row)
}

func (s *Store) SaveSection(ctx context.Context, input SectionInput) (SiteSection, error) {
	input.Name = strings.TrimSpace(input.Name)
	input.SectionType = strings.TrimSpace(input.SectionType)
	input.TargetScope = strings.TrimSpace(input.TargetScope)
	input.TargetSlug = content.Slugify(input.TargetSlug)

	if input.ID < 0 || input.Name == "" || utf8.RuneCountInString(input.Name) > 120 {
		return SiteSection{}, fmt.Errorf("%w: invalid section name", ErrInvalidInput)
	}
	if input.SectionType != SectionHeader && input.SectionType != SectionFooter {
		return SiteSection{}, fmt.Errorf("%w: invalid section type", ErrInvalidInput)
	}
	switch input.TargetScope {
	case ScopeAll, ScopeHome, ScopePosts, ScopePages:
		input.TargetSlug = ""
	case ScopeSlug:
		if input.TargetSlug == "" || len(input.TargetSlug) > 100 {
			return SiteSection{}, fmt.Errorf("%w: target slug is required for slug scope", ErrInvalidInput)
		}
	default:
		return SiteSection{}, fmt.Errorf("%w: invalid target scope", ErrInvalidInput)
	}

	cfg, err := sanitizeSectionConfig(input.SectionType, input.Config)
	if err != nil {
		return SiteSection{}, err
	}
	encoded, err := json.Marshal(cfg)
	if err != nil {
		return SiteSection{}, fmt.Errorf("encode section config: %w", err)
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	activeInt := 0
	if input.Active {
		activeInt = 1
	}

	var id int64
	if input.ID == 0 {
		res, err := s.db.ExecContext(ctx, `
			INSERT INTO site_sections (name, section_type, target_scope, target_slug, config_json, active, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		`, input.Name, input.SectionType, input.TargetScope, input.TargetSlug, string(encoded), activeInt, now, now)
		if err != nil {
			return SiteSection{}, fmt.Errorf("insert site section: %w", err)
		}
		id, err = res.LastInsertId()
		if err != nil {
			return SiteSection{}, fmt.Errorf("get inserted section ID: %w", err)
		}
	} else {
		id = input.ID
		res, err := s.db.ExecContext(ctx, `
			UPDATE site_sections
			SET name = ?, section_type = ?, target_scope = ?, target_slug = ?, config_json = ?, active = ?, updated_at = ?
			WHERE id = ?
		`, input.Name, input.SectionType, input.TargetScope, input.TargetSlug, string(encoded), activeInt, now, id)
		if err != nil {
			return SiteSection{}, fmt.Errorf("update site section: %w", err)
		}
		affected, err := res.RowsAffected()
		if err != nil {
			return SiteSection{}, fmt.Errorf("check updated section: %w", err)
		}
		if affected == 0 {
			return SiteSection{}, ErrNotFound
		}
	}
	return s.GetSection(ctx, id)
}

func (s *Store) DeleteSection(ctx context.Context, id int64) error {
	if id <= 0 {
		return ErrNotFound
	}
	res, err := s.db.ExecContext(ctx, "DELETE FROM site_sections WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("delete site section: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("check deleted section: %w", err)
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

// ResolveSection selects the most specific active visual header or footer for the given scope and slug.
// Precedence: exact slug > specific section scope (home/posts/pages) > global (all).
func (s *Store) ResolveSection(ctx context.Context, sectionType, scope, slug string) (SiteSection, bool, error) {
	if sectionType != SectionHeader && sectionType != SectionFooter {
		return SiteSection{}, false, nil
	}
	slug = strings.TrimSpace(slug)
	if slug != "" {
		row := s.db.QueryRowContext(ctx, `
			SELECT id, name, section_type, target_scope, target_slug, config_json, active, created_at, updated_at
			FROM site_sections
			WHERE section_type = ? AND active = 1 AND target_scope = 'slug' AND target_slug = ?
			ORDER BY updated_at DESC, id DESC LIMIT 1
		`, sectionType, slug)
		sec, err := scanSection(row)
		if err == nil {
			return sec, true, nil
		}
		if !errors.Is(err, ErrNotFound) {
			return SiteSection{}, false, err
		}
	}
	if scope == ScopeHome || scope == ScopePosts || scope == ScopePages {
		row := s.db.QueryRowContext(ctx, `
			SELECT id, name, section_type, target_scope, target_slug, config_json, active, created_at, updated_at
			FROM site_sections
			WHERE section_type = ? AND active = 1 AND target_scope = ?
			ORDER BY updated_at DESC, id DESC LIMIT 1
		`, sectionType, scope)
		sec, err := scanSection(row)
		if err == nil {
			return sec, true, nil
		}
		if !errors.Is(err, ErrNotFound) {
			return SiteSection{}, false, err
		}
	}
	row := s.db.QueryRowContext(ctx, `
		SELECT id, name, section_type, target_scope, target_slug, config_json, active, created_at, updated_at
		FROM site_sections
		WHERE section_type = ? AND active = 1 AND target_scope = 'all'
		ORDER BY updated_at DESC, id DESC LIMIT 1
	`, sectionType)
	sec, err := scanSection(row)
	if errors.Is(err, ErrNotFound) {
		return SiteSection{}, false, nil
	}
	if err != nil {
		return SiteSection{}, false, err
	}
	return sec, true, nil
}

func sanitizeSectionConfig(sectionType string, cfg SectionConfig) (SectionConfig, error) {
	cfg.AnnouncementText = trimMax(cfg.AnnouncementText, 200)
	cfg.AnnouncementURL = strings.TrimSpace(cfg.AnnouncementURL)
	cfg.CTALabel = trimMax(cfg.CTALabel, 60)
	cfg.CTAURL = strings.TrimSpace(cfg.CTAURL)
	cfg.CustomText = trimMax(cfg.CustomText, 400)
	cfg.CopyrightText = trimMax(cfg.CopyrightText, 200)

	if cfg.AnnouncementURL != "" && !ValidLinkURL(cfg.AnnouncementURL) {
		return SectionConfig{}, fmt.Errorf("%w: invalid announcement URL", ErrInvalidInput)
	}
	if cfg.CTAURL != "" && !ValidLinkURL(cfg.CTAURL) {
		return SectionConfig{}, fmt.Errorf("%w: invalid CTA URL", ErrInvalidInput)
	}
	switch cfg.Tone {
	case "surface", "accent", "dark":
	default:
		cfg.Tone = "default"
	}
	switch cfg.MenuLocation {
	case navigation.LocationPrimary, navigation.LocationFooter:
	default:
		if sectionType == SectionFooter {
			cfg.MenuLocation = navigation.LocationFooter
		} else {
			cfg.MenuLocation = navigation.LocationPrimary
		}
	}

	if sectionType == SectionHeader {
		switch cfg.Layout {
		case "centered", "minimal":
		default:
			cfg.Layout = "split"
		}
		cfg.Columns = nil
	} else {
		switch cfg.Layout {
		case "simple", "centered":
		default:
			cfg.Layout = "columns"
		}
		if len(cfg.Columns) > 4 {
			return SectionConfig{}, fmt.Errorf("%w: footer supports at most 4 link columns", ErrInvalidInput)
		}
		columns := make([]FooterColumn, 0, len(cfg.Columns))
		for _, col := range cfg.Columns {
			col.Title = trimMax(col.Title, 80)
			links := make([]FooterLink, 0, len(col.Links))
			if len(col.Links) > 10 {
				return SectionConfig{}, fmt.Errorf("%w: footer column supports at most 10 links", ErrInvalidInput)
			}
			for _, link := range col.Links {
				link.Label = trimMax(link.Label, 80)
				link.URL = strings.TrimSpace(link.URL)
				if link.Label == "" && link.URL == "" {
					continue
				}
				if link.Label == "" || !ValidLinkURL(link.URL) {
					return SectionConfig{}, fmt.Errorf("%w: invalid footer link", ErrInvalidInput)
				}
				links = append(links, link)
			}
			if col.Title == "" && len(links) == 0 {
				continue
			}
			col.Links = links
			columns = append(columns, col)
		}
		cfg.Columns = columns
	}
	return cfg, nil
}

func scanSection(row rowScanner) (SiteSection, error) {
	var sec SiteSection
	var activeInt int
	err := row.Scan(
		&sec.ID, &sec.Name, &sec.SectionType, &sec.TargetScope, &sec.TargetSlug,
		&sec.ConfigJSON, &activeInt, &sec.CreatedAt, &sec.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return SiteSection{}, ErrNotFound
	}
	if err != nil {
		return SiteSection{}, fmt.Errorf("scan site section: %w", err)
	}
	sec.Active = activeInt == 1
	if sec.SectionType == SectionFooter {
		sec.Config = DefaultFooterConfig()
	} else {
		sec.Config = DefaultHeaderConfig()
	}
	if strings.TrimSpace(sec.ConfigJSON) != "" {
		_ = json.Unmarshal([]byte(sec.ConfigJSON), &sec.Config)
	}
	return sec, nil
}

var sectionTemplates = template.Must(template.New("sections").Parse(`
{{define "visual-header"}}
<div class="visual-header-wrap visual-tone-{{.Config.Tone}}{{if .Config.Sticky}} visual-header-sticky{{end}}">
  {{if .Config.AnnouncementText}}
    <div class="visual-announcement">
      <div class="visual-container">
        {{if .Config.AnnouncementURL}}
          <a href="{{.Config.AnnouncementURL}}">{{.Config.AnnouncementText}}</a>
        {{else}}
          <span>{{.Config.AnnouncementText}}</span>
        {{end}}
      </div>
    </div>
  {{end}}
  <header class="site-header visual-header visual-header-{{.Config.Layout}}" data-section-id="{{.Section.ID}}">
    <a class="brand" href="/" aria-label="{{.Site.Title}} — início">
      {{if and .Config.ShowLogo .Site.Logo}}
        <img class="brand-logo" src="{{.Site.Logo}}" alt="{{.Site.Title}}">
      {{end}}
      {{if or .Config.ShowSiteTitle (not (and .Config.ShowLogo .Site.Logo))}}
        <span>{{.Site.Title}}</span>
      {{end}}
    </a>
    {{if and .Config.ShowMenu .MenuHTML}}
      <div class="site-nav">{{.MenuHTML}}</div>
    {{end}}
    {{if and .Config.CTALabel .Config.CTAURL}}
      <a class="visual-header-cta" href="{{.Config.CTAURL}}">{{.Config.CTALabel}}</a>
    {{end}}
  </header>
</div>
{{end}}

{{define "visual-footer"}}
<div class="visual-footer-wrap visual-tone-{{.Config.Tone}}">
  <footer class="site-footer visual-footer visual-footer-{{.Config.Layout}}" data-section-id="{{.Section.ID}}">
    <div class="visual-footer-brand">
      <a class="brand brand-small" href="/">
        {{if and .Config.ShowLogo .Site.Logo}}
          <img class="brand-logo" src="{{.Site.Logo}}" alt="{{.Site.Title}}">
        {{end}}
        {{if or .Config.ShowSiteTitle (not (and .Config.ShowLogo .Site.Logo))}}
          <span>{{.Site.Title}}</span>
        {{end}}
      </a>
      {{if .Config.CustomText}}
        <p class="visual-footer-copy">{{.Config.CustomText}}</p>
      {{else if and .Config.ShowTagline .Site.Tagline}}
        <p class="visual-footer-copy">{{.Site.Tagline}}</p>
      {{end}}
      {{if and .Config.CTALabel .Config.CTAURL}}
        <a class="visual-footer-cta" href="{{.Config.CTAURL}}">{{.Config.CTALabel}}</a>
      {{end}}
    </div>
    {{if .Config.Columns}}
      <div class="visual-footer-columns">
        {{range .Config.Columns}}
          <div class="visual-footer-col">
            {{if .Title}}<h3>{{.Title}}</h3>{{end}}
            {{if .Links}}
              <ul>
                {{range .Links}}
                  <li><a href="{{.URL}}">{{.Label}}</a></li>
                {{end}}
              </ul>
            {{end}}
          </div>
        {{end}}
      </div>
    {{end}}
    <div class="visual-footer-bottom">
      {{if and .Config.ShowMenu .MenuHTML}}
        <div class="visual-footer-menu">{{.MenuHTML}}</div>
      {{end}}
      {{if .Config.CopyrightText}}
        <p class="visual-footer-legal">{{.Config.CopyrightText}}</p>
      {{end}}
    </div>
  </footer>
</div>
{{end}}
`))

func RenderHeaderHTML(sec SiteSection, site siteconfig.Settings, menuHTML template.HTML) (template.HTML, error) {
	var out bytes.Buffer
	err := sectionTemplates.ExecuteTemplate(&out, "visual-header", struct {
		Section  SiteSection
		Config   SectionConfig
		Site     siteconfig.Settings
		MenuHTML template.HTML
	}{
		Section:  sec,
		Config:   sec.Config,
		Site:     site,
		MenuHTML: menuHTML,
	})
	if err != nil {
		return "", fmt.Errorf("render visual header: %w", err)
	}
	return template.HTML(out.String()), nil
}

func RenderFooterHTML(sec SiteSection, site siteconfig.Settings, menuHTML template.HTML) (template.HTML, error) {
	var out bytes.Buffer
	err := sectionTemplates.ExecuteTemplate(&out, "visual-footer", struct {
		Section  SiteSection
		Config   SectionConfig
		Site     siteconfig.Settings
		MenuHTML template.HTML
	}{
		Section:  sec,
		Config:   sec.Config,
		Site:     site,
		MenuHTML: menuHTML,
	})
	if err != nil {
		return "", fmt.Errorf("render visual footer: %w", err)
	}
	return template.HTML(out.String()), nil
}
