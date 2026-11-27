package portability

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/Luw17/go-content-go/internal/content"
)

type ExportBundle struct {
	Version    string       `json:"version"`
	ExportedAt string       `json:"exported_at"`
	Items      []ExportItem `json:"items"`
}

type ExportItem struct {
	Type           string   `json:"type"`
	Title          string   `json:"title"`
	Slug           string   `json:"slug"`
	Content        string   `json:"content"`
	Status         string   `json:"status"`
	FeaturedImage  string   `json:"featured_image,omitempty"`
	SEOTitle       string   `json:"seo_title,omitempty"`
	SEODescription string   `json:"seo_description,omitempty"`
	CanonicalURL   string   `json:"canonical_url,omitempty"`
	Noindex        bool     `json:"noindex,omitempty"`
	Tags           []string `json:"tags,omitempty"`
	Categories     []string `json:"categories,omitempty"`
	PublishedAt    string   `json:"published_at,omitempty"`
}

type ImportSummary struct {
	Imported int
	Skipped  int
}

func ExportJSON(ctx context.Context, db *sql.DB) ([]byte, error) {
	items, err := collectAllContent(ctx, db)
	if err != nil {
		return nil, err
	}
	bundle := ExportBundle{
		Version:    "1",
		ExportedAt: time.Now().UTC().Format(time.RFC3339),
		Items:      items,
	}
	return json.MarshalIndent(bundle, "", "  ")
}

func ExportMarkdown(ctx context.Context, db *sql.DB) ([]byte, error) {
	items, err := collectAllContent(ctx, db)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	for i, item := range items {
		if i > 0 {
			buf.WriteString("\n\n<!-- gocg-item-separator -->\n\n")
		}
		buf.WriteString("---\n")
		fmt.Fprintf(&buf, "title: %s\n", sanitizeFrontmatterValue(item.Title))
		fmt.Fprintf(&buf, "slug: %s\n", sanitizeFrontmatterValue(item.Slug))
		fmt.Fprintf(&buf, "type: %s\n", item.Type)
		fmt.Fprintf(&buf, "status: %s\n", item.Status)
		if len(item.Tags) > 0 {
			fmt.Fprintf(&buf, "tags: %s\n", strings.Join(item.Tags, ", "))
		}
		buf.WriteString("---\n\n")
		buf.WriteString(item.Content)
		buf.WriteString("\n")
	}
	return buf.Bytes(), nil
}

func ImportJSON(ctx context.Context, db *sql.DB, r io.Reader, authorID int64) (ImportSummary, error) {
	var bundle ExportBundle
	if err := json.NewDecoder(io.LimitReader(r, 20<<20)).Decode(&bundle); err != nil {
		return ImportSummary{}, fmt.Errorf("decode JSON bundle: %w", err)
	}
	return importItems(ctx, db, bundle.Items, authorID)
}

func ImportMarkdown(ctx context.Context, db *sql.DB, r io.Reader, authorID int64) (ImportSummary, error) {
	raw, err := io.ReadAll(io.LimitReader(r, 20<<20))
	if err != nil {
		return ImportSummary{}, fmt.Errorf("read markdown import: %w", err)
	}
	chunks := strings.Split(string(raw), "<!-- gocg-item-separator -->")
	var items []ExportItem
	for _, chunk := range chunks {
		chunk = strings.TrimSpace(chunk)
		if chunk == "" {
			continue
		}
		item := parseMarkdownItem(chunk)
		if item.Title != "" || item.Content != "" {
			items = append(items, item)
		}
	}
	return importItems(ctx, db, items, authorID)
}

type wxrDocument struct {
	Channel struct {
		Items []wxrItem `xml:"item"`
	} `xml:"channel"`
}

type wxrItem struct {
	Title      string        `xml:"title"`
	PostName   string        `xml:"post_name"`
	PostType   string        `xml:"post_type"`
	Status     string        `xml:"status"`
	Encoded    []string      `xml:"encoded"`
	Categories []wxrCategory `xml:"category"`
}

type wxrCategory struct {
	Domain string `xml:"domain,attr"`
	Value  string `xml:",chardata"`
}

func ImportWordPressWXR(ctx context.Context, db *sql.DB, r io.Reader, authorID int64) (ImportSummary, error) {
	var doc wxrDocument
	if err := xml.NewDecoder(io.LimitReader(r, 20<<20)).Decode(&doc); err != nil {
		return ImportSummary{}, fmt.Errorf("decode WordPress WXR XML: %w", err)
	}
	var items []ExportItem
	for _, entry := range doc.Channel.Items {
		itemType := content.TypePost
		switch strings.ToLower(strings.TrimSpace(entry.PostType)) {
		case "post", "":
			itemType = content.TypePost
		case "page":
			itemType = content.TypePage
		default:
			continue
		}
		status := content.StatusDraft
		if strings.EqualFold(strings.TrimSpace(entry.Status), "publish") {
			status = content.StatusPublished
		}
		body := ""
		if len(entry.Encoded) > 0 {
			body = strings.TrimSpace(entry.Encoded[0])
		}
		var tags []string
		for _, c := range entry.Categories {
			if c.Domain == "post_tag" && strings.TrimSpace(c.Value) != "" {
				tags = append(tags, strings.TrimSpace(c.Value))
			}
		}
		title := strings.TrimSpace(entry.Title)
		if title == "" {
			title = "Sem título"
		}
		items = append(items, ExportItem{
			Type:    itemType,
			Title:   title,
			Slug:    strings.TrimSpace(entry.PostName),
			Content: body,
			Status:  status,
			Tags:    tags,
		})
	}
	return importItems(ctx, db, items, authorID)
}

func collectAllContent(ctx context.Context, db *sql.DB) ([]ExportItem, error) {
	var out []ExportItem
	for _, contentType := range []string{content.TypePost, content.TypePage} {
		list, err := content.List(ctx, db, contentType, false)
		if err != nil {
			return nil, err
		}
		for _, item := range list {
			full, err := content.Get(ctx, db, item.ID, contentType)
			if err != nil {
				continue
			}
			cats := make([]string, 0, len(full.Categories))
			for _, c := range full.Categories {
				cats = append(cats, c.Name)
			}
			out = append(out, ExportItem{
				Type:           full.Type,
				Title:          full.Title,
				Slug:           full.Slug,
				Content:        full.Content,
				Status:         full.Status,
				FeaturedImage:  full.FeaturedImage,
				SEOTitle:       full.SEOTitle,
				SEODescription: full.SEODescription,
				CanonicalURL:   full.CanonicalURL,
				Noindex:        full.Noindex,
				Tags:           full.Tags,
				Categories:     cats,
				PublishedAt:    full.PublishedAt,
			})
		}
	}
	return out, nil
}

func importItems(ctx context.Context, db *sql.DB, items []ExportItem, authorID int64) (ImportSummary, error) {
	var summary ImportSummary
	for _, entry := range items {
		itemType := entry.Type
		if itemType != content.TypePost && itemType != content.TypePage {
			itemType = content.TypePost
		}
		status := entry.Status
		if status != content.StatusDraft && status != content.StatusPublished {
			status = content.StatusDraft
		}
		title := strings.TrimSpace(entry.Title)
		if title == "" {
			summary.Skipped++
			continue
		}
		newCategory := ""
		if len(entry.Categories) > 0 {
			newCategory = entry.Categories[0]
		}
		_, err := content.Save(ctx, db, content.Input{
			Type:           itemType,
			Title:          title,
			Slug:           "", // auto-dedupe slug safely on import
			Content:        entry.Content,
			Status:         status,
			FeaturedImage:  entry.FeaturedImage,
			SEOTitle:       entry.SEOTitle,
			SEODescription: entry.SEODescription,
			CanonicalURL:   entry.CanonicalURL,
			Noindex:        entry.Noindex,
			NewCategory:    newCategory,
			Tags:           strings.Join(entry.Tags, ", "),
		}, authorID)
		if err != nil {
			summary.Skipped++
			continue
		}
		summary.Imported++
	}
	return summary, nil
}

func parseMarkdownItem(raw string) ExportItem {
	item := ExportItem{
		Type:   content.TypePost,
		Status: content.StatusDraft,
	}
	body := raw
	if strings.HasPrefix(raw, "---\n") || strings.HasPrefix(raw, "---\r\n") {
		rest := strings.TrimPrefix(strings.TrimPrefix(raw, "---\r\n"), "---\n")
		if idx := strings.Index(rest, "\n---"); idx >= 0 {
			header := rest[:idx]
			body = strings.TrimSpace(rest[idx+4:])
			for _, line := range strings.Split(header, "\n") {
				key, val, ok := strings.Cut(line, ":")
				if !ok {
					continue
				}
				key = strings.ToLower(strings.TrimSpace(key))
				val = strings.Trim(strings.TrimSpace(val), `"'`)
				switch key {
				case "title":
					item.Title = val
				case "slug":
					item.Slug = val
				case "type":
					if val == content.TypePage {
						item.Type = content.TypePage
					}
				case "status":
					if val == content.StatusPublished {
						item.Status = content.StatusPublished
					}
				case "tags":
					for _, t := range strings.Split(val, ",") {
						if trimmed := strings.TrimSpace(t); trimmed != "" {
							item.Tags = append(item.Tags, trimmed)
						}
					}
				}
			}
		}
	}
	if item.Title == "" {
		lines := strings.Split(body, "\n")
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "# ") {
				item.Title = strings.TrimSpace(strings.TrimPrefix(line, "# "))
				break
			}
		}
		if item.Title == "" {
			item.Title = "Conteúdo importado"
		}
	}
	item.Content = body
	return item
}

func sanitizeFrontmatterValue(v string) string {
	v = strings.ReplaceAll(v, "\r", " ")
	v = strings.ReplaceAll(v, "\n", " ")
	return strings.TrimSpace(v)
}
