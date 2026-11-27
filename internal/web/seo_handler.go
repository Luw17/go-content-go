package web

import (
	"bytes"
	"encoding/xml"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Luw17/go-content-go/internal/content"
	"github.com/Luw17/go-content-go/internal/siteconfig"
)

type sitemapURLSet struct {
	XMLName xml.Name     `xml:"urlset"`
	XMLNS   string       `xml:"xmlns,attr"`
	URLs    []sitemapURL `xml:"url"`
}

type sitemapURL struct {
	Loc     string `xml:"loc"`
	LastMod string `xml:"lastmod,omitempty"`
}

type rssFeed struct {
	XMLName xml.Name   `xml:"rss"`
	Version string     `xml:"version,attr"`
	AtomNS  string     `xml:"xmlns:atom,attr"`
	Channel rssChannel `xml:"channel"`
}

type rssChannel struct {
	Title         string       `xml:"title"`
	Link          string       `xml:"link"`
	Description   string       `xml:"description"`
	Language      string       `xml:"language"`
	LastBuildDate string       `xml:"lastBuildDate,omitempty"`
	AtomLink      rssAtomLink  `xml:"atom:link"`
	Items         []rssItem    `xml:"item"`
}

type rssAtomLink struct {
	Href string `xml:"href,attr"`
	Rel  string `xml:"rel,attr"`
	Type string `xml:"type,attr"`
}

type rssItem struct {
	Title       string `xml:"title"`
	Link        string `xml:"link"`
	GUID        string `xml:"guid"`
	PubDate     string `xml:"pubDate,omitempty"`
	Description string `xml:"description,omitempty"`
}

func (h *handler) publicBaseURL(settings siteconfig.Settings, r *http.Request) string {
	if settings.SiteURL != "" {
		return strings.TrimRight(settings.SiteURL, "/")
	}
	if r == nil {
		return "http://localhost:8080"
	}
	scheme := "http"
	if h.isHTTPS(r) {
		scheme = "https"
	}
	host := r.Host
	if host == "" {
		host = "localhost:8080"
	}
	return scheme + "://" + host
}

func (h *handler) publicSitemap(w http.ResponseWriter, r *http.Request) {
	settings, err := siteconfig.Load(r.Context(), h.db)
	if err != nil {
		log.Printf("load site settings for sitemap: %v", err)
		http.Error(w, "failed to build sitemap", http.StatusInternalServerError)
		return
	}
	urlSet := sitemapURLSet{
		XMLNS: "http://www.sitemaps.org/schemas/sitemap/0.9",
	}
	if settings.SEORobotsIndexing != siteconfig.RobotsNoindex {
		baseURL := h.publicBaseURL(settings, r)
		items, err := content.PublishedIndexable(r.Context(), h.db)
		if err != nil {
			log.Printf("load indexable content for sitemap: %v", err)
			http.Error(w, "failed to build sitemap", http.StatusInternalServerError)
			return
		}
		categories, err := content.IndexableCategories(r.Context(), h.db)
		if err != nil {
			log.Printf("load indexable categories for sitemap: %v", err)
			http.Error(w, "failed to build sitemap", http.StatusInternalServerError)
			return
		}
		tags, err := content.IndexableTags(r.Context(), h.db)
		if err != nil {
			log.Printf("load indexable tags for sitemap: %v", err)
			http.Error(w, "failed to build sitemap", http.StatusInternalServerError)
			return
		}

		var latestHome time.Time
		entries := make([]sitemapURL, 0, len(items)+len(categories)+len(tags)+1)
		for _, item := range items {
			if item.Slug == "" {
				continue
			}
			ts, ok := parseSitemapTime(item.UpdatedAt, item.PublishedAt)
			if ok && ts.After(latestHome) {
				latestHome = ts
			}
			if settings.ShowOnFront == siteconfig.FrontPage && settings.PageOnFront > 0 && item.ID == settings.PageOnFront {
				continue
			}
			entry := sitemapURL{
				Loc: baseURL + "/" + item.Slug,
			}
			if ok {
				entry.LastMod = ts.UTC().Format(time.RFC3339)
			}
			entries = append(entries, entry)
		}
		for _, cat := range categories {
			if cat.Slug == "" {
				continue
			}
			entry := sitemapURL{Loc: baseURL + "/categoria/" + cat.Slug}
			if ts, ok := parseSitemapTime(cat.LastMod, ""); ok {
				entry.LastMod = ts.UTC().Format(time.RFC3339)
			}
			entries = append(entries, entry)
		}
		for _, tag := range tags {
			if tag.Slug == "" {
				continue
			}
			entry := sitemapURL{Loc: baseURL + "/tag/" + tag.Slug}
			if ts, ok := parseSitemapTime(tag.LastMod, ""); ok {
				entry.LastMod = ts.UTC().Format(time.RFC3339)
			}
			entries = append(entries, entry)
		}
		home := sitemapURL{Loc: baseURL + "/"}
		if !latestHome.IsZero() {
			home.LastMod = latestHome.UTC().Format(time.RFC3339)
		}
		urlSet.URLs = append([]sitemapURL{home}, entries...)
	}

	var buf bytes.Buffer
	buf.WriteString(xml.Header)
	encoder := xml.NewEncoder(&buf)
	encoder.Indent("", "  ")
	if err := encoder.Encode(urlSet); err != nil {
		log.Printf("encode sitemap XML: %v", err)
		http.Error(w, "failed to build sitemap", http.StatusInternalServerError)
		return
	}
	buf.WriteByte('\n')

	payload := buf.Bytes()
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(payload)
	}
}

func (h *handler) publicRobots(w http.ResponseWriter, r *http.Request) {
	settings, err := siteconfig.Load(r.Context(), h.db)
	if err != nil {
		log.Printf("load site settings for robots.txt: %v", err)
		http.Error(w, "failed to build robots.txt", http.StatusInternalServerError)
		return
	}
	var body string
	if settings.SEORobotsIndexing == siteconfig.RobotsNoindex {
		body = "User-agent: *\nDisallow: /\n"
	} else {
		baseURL := h.publicBaseURL(settings, r)
		body = "User-agent: *\nAllow: /\nDisallow: /admin/\nDisallow: /api/\n\nSitemap: " + baseURL + "/sitemap.xml\n"
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write([]byte(body))
	}
}

func (h *handler) publicRSS(w http.ResponseWriter, r *http.Request) {
	settings, err := siteconfig.Load(r.Context(), h.db)
	if err != nil {
		log.Printf("load site settings for RSS: %v", err)
		http.Error(w, "failed to build RSS feed", http.StatusInternalServerError)
		return
	}
	posts, _, err := content.PublishedPage(r.Context(), h.db, 1, 20)
	if err != nil {
		log.Printf("load posts for RSS: %v", err)
		http.Error(w, "failed to build RSS feed", http.StatusInternalServerError)
		return
	}
	baseURL := h.publicBaseURL(settings, r)
	description := settings.SEODefaultDescription
	if description == "" {
		description = settings.Tagline
	}
	var latest time.Time
	rssItems := make([]rssItem, 0, len(posts))
	for _, post := range posts {
		if post.Noindex {
			continue
		}
		rendered, renderErr := content.RenderMarkdown(post.Content)
		if renderErr != nil {
			rendered = post.Content
		}
		pub := newPublicItem(post, rendered)
		summary := post.SEODescription
		if summary == "" {
			summary = pub.Summary
		}
		link := baseURL + "/" + post.Slug
		entry := rssItem{
			Title:       post.Title,
			Link:        link,
			GUID:        link,
			Description: summary,
		}
		if ts, ok := parseSitemapTime(post.PublishedAt, post.UpdatedAt); ok {
			entry.PubDate = ts.UTC().Format(time.RFC1123Z)
			if ts.After(latest) {
				latest = ts
			}
		}
		rssItems = append(rssItems, entry)
	}
	channel := rssChannel{
		Title:       settings.Title,
		Link:        baseURL + "/",
		Description: description,
		Language:    "pt-BR",
		AtomLink: rssAtomLink{
			Href: baseURL + "/rss.xml",
			Rel:  "self",
			Type: "application/rss+xml",
		},
		Items: rssItems,
	}
	if !latest.IsZero() {
		channel.LastBuildDate = latest.UTC().Format(time.RFC1123Z)
	}
	feed := rssFeed{
		Version: "2.0",
		AtomNS:  "http://www.w3.org/2005/Atom",
		Channel: channel,
	}

	var buf bytes.Buffer
	buf.WriteString(xml.Header)
	encoder := xml.NewEncoder(&buf)
	encoder.Indent("", "  ")
	if err := encoder.Encode(feed); err != nil {
		log.Printf("encode RSS XML: %v", err)
		http.Error(w, "failed to build RSS feed", http.StatusInternalServerError)
		return
	}
	buf.WriteByte('\n')

	payload := buf.Bytes()
	w.Header().Set("Content-Type", "application/rss+xml; charset=utf-8")
	w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(payload)
	}
}

func (h *handler) publicFavicon(w http.ResponseWriter, r *http.Request) {
	settings, err := siteconfig.Load(r.Context(), h.db)
	if err != nil {
		log.Printf("load site settings for favicon: %v", err)
		http.Error(w, "failed to load favicon", http.StatusInternalServerError)
		return
	}
	if settings.Favicon != "" {
		http.Redirect(w, r, settings.Favicon, http.StatusFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func parseSitemapTime(primary, fallback string) (time.Time, bool) {
	for _, raw := range []string{primary, fallback} {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05"} {
			if parsed, err := time.Parse(layout, raw); err == nil {
				return parsed.UTC(), true
			}
		}
	}
	return time.Time{}, false
}
