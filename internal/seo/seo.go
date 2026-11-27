package seo

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"html/template"
	"net/http"
	"strings"

	"github.com/Luw17/go-content-go/internal/content"
	"github.com/Luw17/go-content-go/internal/siteconfig"
)

type PageType string

const (
	TypeHome    PageType = "home"
	TypePost    PageType = "post"
	TypePage    PageType = "page"
	TypeArchive PageType = "archive"
	TypeSearch  PageType = "search"
	Type404     PageType = "404"
)

type Params struct {
	Type            PageType
	Site            siteconfig.Settings
	Item            *content.Item
	FallbackSummary string
	Request         *http.Request
	Title           string
	Description     string
	CanonicalPath   string
	BreadcrumbName  string
	PrevURL         string
	NextURL         string
}

type Metadata struct {
	Title           string
	MetaDescription string
	CanonicalURL    string
	Robots          string
	OGTitle         string
	OGDescription   string
	OGURL           string
	OGType          string
	OGSiteName      string
	OGImage         string
	TwitterCard     string
	TwitterTitle    string
	TwitterDescription string
	TwitterImage    string
	TwitterSite     string
	JSONLD          []string
}

// BaseURL returns the canonical site URL from settings or infers it from the HTTP request.
func BaseURL(site siteconfig.Settings, r *http.Request) string {
	if site.SiteURL != "" {
		return strings.TrimRight(site.SiteURL, "/")
	}
	if r == nil {
		return "http://localhost:8080"
	}
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	host := r.Host
	if host == "" {
		host = "localhost:8080"
	}
	return scheme + "://" + host
}

// AbsoluteURL transforms a path into a full URL using the given base URL.
func AbsoluteURL(baseURL, raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://") {
		return raw
	}
	if !strings.HasPrefix(raw, "/") {
		raw = "/" + raw
	}
	return baseURL + raw
}

// Build generates all SEO metadata according to the site settings and current page context.
func Build(p Params) Metadata {
	baseURL := BaseURL(p.Site, p.Request)
	m := Metadata{
		OGSiteName: p.Site.Title,
	}

	// 1. Title
	switch p.Type {
	case TypeHome:
		if p.Title != "" {
			m.Title = p.Title
		} else {
			m.Title = p.Site.Title
		}
	case TypeArchive, TypeSearch:
		if p.Title != "" {
			m.Title = p.Title + " | " + p.Site.Title
		} else {
			m.Title = p.Site.Title
		}
	case Type404:
		m.Title = "Página não encontrada | " + p.Site.Title
	default:
		if p.Item != nil && p.Item.SEOTitle != "" {
			m.Title = p.Item.SEOTitle
		} else if p.Item != nil && p.Item.Title != "" {
			m.Title = p.Item.Title + " | " + p.Site.Title
		} else {
			m.Title = p.Site.Title
		}
	}

	// 2. Meta Description
	if p.Item != nil && p.Item.SEODescription != "" {
		m.MetaDescription = p.Item.SEODescription
	} else if p.Description != "" {
		m.MetaDescription = p.Description
	} else if p.FallbackSummary != "" {
		m.MetaDescription = p.FallbackSummary
	} else if p.Site.SEODefaultDescription != "" {
		m.MetaDescription = p.Site.SEODefaultDescription
	} else {
		m.MetaDescription = p.Site.Tagline
	}

	// 3. Canonical URL
	if p.Item != nil && p.Item.CanonicalURL != "" {
		m.CanonicalURL = AbsoluteURL(baseURL, p.Item.CanonicalURL)
	} else if p.CanonicalPath != "" {
		m.CanonicalURL = AbsoluteURL(baseURL, p.CanonicalPath)
	} else if p.Type == TypeHome {
		m.CanonicalURL = baseURL + "/"
	} else if p.Item != nil && p.Item.Slug != "" {
		m.CanonicalURL = baseURL + "/" + p.Item.Slug
	} else if p.Request != nil {
		m.CanonicalURL = baseURL + p.Request.URL.Path
	} else {
		m.CanonicalURL = baseURL + "/"
	}

	// 4. Robots
	if p.Site.SEORobotsIndexing == siteconfig.RobotsNoindex || (p.Item != nil && p.Item.Noindex) || p.Type == Type404 {
		m.Robots = "noindex, nofollow"
	} else if p.Type == TypeSearch {
		m.Robots = "noindex, follow"
	} else {
		m.Robots = "index, follow"
	}

	// 5. Open Graph
	m.OGTitle = m.Title
	if p.Item != nil && p.Item.OGTitle != "" {
		m.OGTitle = p.Item.OGTitle
	}
	m.OGDescription = m.MetaDescription
	if p.Item != nil && p.Item.OGDescription != "" {
		m.OGDescription = p.Item.OGDescription
	}
	m.OGURL = m.CanonicalURL

	if p.Type == TypePost {
		m.OGType = "article"
	} else {
		m.OGType = "website"
	}

	// OG Image resolution: Item.OGImage -> Item.FeaturedImage -> Site.SEODefaultOGImage -> Site.Logo
	if p.Item != nil && p.Item.OGImage != "" {
		m.OGImage = AbsoluteURL(baseURL, p.Item.OGImage)
	} else if p.Item != nil && p.Item.FeaturedImage != "" {
		m.OGImage = AbsoluteURL(baseURL, p.Item.FeaturedImage)
	} else if p.Site.SEODefaultOGImage != "" {
		m.OGImage = AbsoluteURL(baseURL, p.Site.SEODefaultOGImage)
	} else if p.Site.Logo != "" {
		m.OGImage = AbsoluteURL(baseURL, p.Site.Logo)
	}

	// 6. Twitter Card
	if p.Item != nil && p.Item.TwitterCard != "" {
		m.TwitterCard = p.Item.TwitterCard
	} else if m.OGImage != "" {
		m.TwitterCard = "summary_large_image"
	} else {
		m.TwitterCard = "summary"
	}
	m.TwitterTitle = m.OGTitle
	m.TwitterDescription = m.OGDescription
	m.TwitterImage = m.OGImage
	m.TwitterSite = p.Site.SEOTwitterHandle

	// 7. Structured Data (JSON-LD)
	m.JSONLD = buildJSONLD(p, baseURL, m)

	return m
}

func buildJSONLD(p Params, baseURL string, m Metadata) []string {
	var schemas []string

	org := map[string]any{
		"@type": "Organization",
		"name":  p.Site.Title,
		"url":   baseURL + "/",
	}
	if p.Site.Logo != "" {
		org["logo"] = AbsoluteURL(baseURL, p.Site.Logo)
	}

	switch p.Type {
	case TypeHome:
		website := map[string]any{
			"@context": "https://schema.org",
			"@type":    "WebSite",
			"name":     p.Site.Title,
			"url":      baseURL + "/",
		}
		if p.Site.Tagline != "" {
			website["description"] = p.Site.Tagline
		}
		if raw, err := json.Marshal(website); err == nil {
			schemas = append(schemas, string(raw))
		}

		orgSchema := map[string]any{
			"@context": "https://schema.org",
			"@type":    org["@type"],
			"name":     org["name"],
			"url":      org["url"],
		}
		if logo, ok := org["logo"]; ok {
			orgSchema["logo"] = logo
		}
		if raw, err := json.Marshal(orgSchema); err == nil {
			schemas = append(schemas, string(raw))
		}

	case TypePost:
		if p.Item != nil {
			schemaType := "BlogPosting"
			if p.Item.SchemaType != "" {
				schemaType = p.Item.SchemaType
			}
			postSchema := map[string]any{
				"@context": "https://schema.org",
				"@type":    schemaType,
				"mainEntityOfPage": map[string]any{
					"@type": "WebPage",
					"@id":   m.CanonicalURL,
				},
				"headline":      p.Item.Title,
				"description":   m.MetaDescription,
				"datePublished": p.Item.PublishedAt,
				"dateModified":  p.Item.UpdatedAt,
				"publisher":     org,
			}
			if m.OGImage != "" {
				postSchema["image"] = []string{m.OGImage}
			}
			if p.Item.Author != "" {
				postSchema["author"] = map[string]any{
					"@type": "Person",
					"name":  p.Item.Author,
				}
			}
			if raw, err := json.Marshal(postSchema); err == nil {
				schemas = append(schemas, string(raw))
			}

			// Breadcrumbs
			breadcrumbs := map[string]any{
				"@context": "https://schema.org",
				"@type":    "BreadcrumbList",
				"itemListElement": []map[string]any{
					{
						"@type":    "ListItem",
						"position": 1,
						"name":     "Início",
						"item":     baseURL + "/",
					},
					{
						"@type":    "ListItem",
						"position": 2,
						"name":     p.Item.Title,
						"item":     m.CanonicalURL,
					},
				},
			}
			if raw, err := json.Marshal(breadcrumbs); err == nil {
				schemas = append(schemas, string(raw))
			}
		}

	case TypePage:
		if p.Item != nil {
			pageSchema := map[string]any{
				"@context":    "https://schema.org",
				"@type":       "WebPage",
				"name":        p.Item.Title,
				"url":         m.CanonicalURL,
				"description": m.MetaDescription,
				"publisher":   org,
			}
			if m.OGImage != "" {
				pageSchema["image"] = []string{m.OGImage}
			}
			if raw, err := json.Marshal(pageSchema); err == nil {
				schemas = append(schemas, string(raw))
			}
		}

	case TypeArchive:
		collectionSchema := map[string]any{
			"@context":    "https://schema.org",
			"@type":       "CollectionPage",
			"name":        m.Title,
			"url":         m.CanonicalURL,
			"description": m.MetaDescription,
			"publisher":   org,
		}
		if raw, err := json.Marshal(collectionSchema); err == nil {
			schemas = append(schemas, string(raw))
		}
		crumbName := p.BreadcrumbName
		if crumbName == "" {
			crumbName = p.Title
		}
		if crumbName != "" {
			breadcrumbs := map[string]any{
				"@context": "https://schema.org",
				"@type":    "BreadcrumbList",
				"itemListElement": []map[string]any{
					{
						"@type":    "ListItem",
						"position": 1,
						"name":     "Início",
						"item":     baseURL + "/",
					},
					{
						"@type":    "ListItem",
						"position": 2,
						"name":     crumbName,
						"item":     m.CanonicalURL,
					},
				},
			}
			if raw, err := json.Marshal(breadcrumbs); err == nil {
				schemas = append(schemas, string(raw))
			}
		}
	}

	return schemas
}

// RenderHTML generates the complete <head> tags for SEO, Open Graph, Twitter Cards, and JSON-LD.
func RenderHTML(p Params) template.HTML {
	m := Build(p)
	baseURL := BaseURL(p.Site, p.Request)
	var buf bytes.Buffer

	// Canonical link
	if m.CanonicalURL != "" {
		fmt.Fprintf(&buf, "<link rel=\"canonical\" href=\"%s\">\n", html.EscapeString(m.CanonicalURL))
	}

	// Pagination links
	if p.PrevURL != "" {
		fmt.Fprintf(&buf, "<link rel=\"prev\" href=\"%s\">\n", html.EscapeString(AbsoluteURL(baseURL, p.PrevURL)))
	}
	if p.NextURL != "" {
		fmt.Fprintf(&buf, "<link rel=\"next\" href=\"%s\">\n", html.EscapeString(AbsoluteURL(baseURL, p.NextURL)))
	}

	// RSS 2.0 autodiscovery
	rssTitle := strings.TrimSpace(p.Site.Title)
	if rssTitle == "" {
		rssTitle = "RSS"
	} else {
		rssTitle += " — RSS"
	}
	fmt.Fprintf(&buf, "<link rel=\"alternate\" type=\"application/rss+xml\" title=\"%s\" href=\"%s\">\n",
		html.EscapeString(rssTitle), html.EscapeString(baseURL+"/rss.xml"))

	// Favicon & Apple Touch Icon
	if p.Site.Favicon != "" {
		fmt.Fprintf(&buf, "<link rel=\"icon\" href=\"%s\">\n", html.EscapeString(p.Site.Favicon))
	}
	if p.Site.AppleTouchIcon != "" {
		fmt.Fprintf(&buf, "<link rel=\"apple-touch-icon\" href=\"%s\">\n", html.EscapeString(p.Site.AppleTouchIcon))
	}

	// Robots
	if m.Robots != "" {
		fmt.Fprintf(&buf, "<meta name=\"robots\" content=\"%s\">\n", html.EscapeString(m.Robots))
	}

	// Open Graph
	if m.OGSiteName != "" {
		fmt.Fprintf(&buf, "<meta property=\"og:site_name\" content=\"%s\">\n", html.EscapeString(m.OGSiteName))
	}
	if m.OGType != "" {
		fmt.Fprintf(&buf, "<meta property=\"og:type\" content=\"%s\">\n", html.EscapeString(m.OGType))
	}
	if m.OGTitle != "" {
		fmt.Fprintf(&buf, "<meta property=\"og:title\" content=\"%s\">\n", html.EscapeString(m.OGTitle))
	}
	if m.OGDescription != "" {
		fmt.Fprintf(&buf, "<meta property=\"og:description\" content=\"%s\">\n", html.EscapeString(m.OGDescription))
	}
	if m.OGURL != "" {
		fmt.Fprintf(&buf, "<meta property=\"og:url\" content=\"%s\">\n", html.EscapeString(m.OGURL))
	}
	if m.OGImage != "" {
		fmt.Fprintf(&buf, "<meta property=\"og:image\" content=\"%s\">\n", html.EscapeString(m.OGImage))
	}
	if p.Type == TypePost && p.Item != nil {
		if p.Item.PublishedAt != "" {
			fmt.Fprintf(&buf, "<meta property=\"article:published_time\" content=\"%s\">\n", html.EscapeString(p.Item.PublishedAt))
		}
		if p.Item.UpdatedAt != "" {
			fmt.Fprintf(&buf, "<meta property=\"article:modified_time\" content=\"%s\">\n", html.EscapeString(p.Item.UpdatedAt))
		}
		if p.Item.Author != "" {
			fmt.Fprintf(&buf, "<meta property=\"article:author\" content=\"%s\">\n", html.EscapeString(p.Item.Author))
		}
		if len(p.Item.Categories) > 0 {
			fmt.Fprintf(&buf, "<meta property=\"article:section\" content=\"%s\">\n", html.EscapeString(p.Item.Categories[0].Name))
		}
		for _, tag := range p.Item.Tags {
			fmt.Fprintf(&buf, "<meta property=\"article:tag\" content=\"%s\">\n", html.EscapeString(tag))
		}
	}

	// Twitter Card
	if m.TwitterCard != "" {
		fmt.Fprintf(&buf, "<meta name=\"twitter:card\" content=\"%s\">\n", html.EscapeString(m.TwitterCard))
	}
	if m.TwitterTitle != "" {
		fmt.Fprintf(&buf, "<meta name=\"twitter:title\" content=\"%s\">\n", html.EscapeString(m.TwitterTitle))
	}
	if m.TwitterDescription != "" {
		fmt.Fprintf(&buf, "<meta name=\"twitter:description\" content=\"%s\">\n", html.EscapeString(m.TwitterDescription))
	}
	if m.TwitterImage != "" {
		fmt.Fprintf(&buf, "<meta name=\"twitter:image\" content=\"%s\">\n", html.EscapeString(m.TwitterImage))
	}
	if m.TwitterSite != "" {
		fmt.Fprintf(&buf, "<meta name=\"twitter:site\" content=\"%s\">\n", html.EscapeString(m.TwitterSite))
	}

	// JSON-LD Scripts
	for _, jsonld := range m.JSONLD {
		fmt.Fprintf(&buf, "<script type=\"application/ld+json\">%s</script>\n", jsonld)
	}

	return template.HTML(buf.String())
}
