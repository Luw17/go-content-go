package seo

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Luw17/go-content-go/internal/content"
	"github.com/Luw17/go-content-go/internal/siteconfig"
)

func TestBaseURLResolution(t *testing.T) {
	siteWithURL := siteconfig.Settings{SiteURL: "https://mysite.com/"}
	if got := BaseURL(siteWithURL, nil); got != "https://mysite.com" {
		t.Errorf("BaseURL(siteWithURL) = %q, want https://mysite.com", got)
	}

	siteEmpty := siteconfig.Settings{}
	req := httptest.NewRequest(http.MethodGet, "http://myhost:9000/hello", nil)
	if got := BaseURL(siteEmpty, req); got != "http://myhost:9000" {
		t.Errorf("BaseURL(siteEmpty, req) = %q, want http://myhost:9000", got)
	}

	reqTLS := httptest.NewRequest(http.MethodGet, "https://secure.org/path", nil)
	reqTLS.TLS = &tls.ConnectionState{}
	if got := BaseURL(siteEmpty, reqTLS); got != "https://secure.org" {
		t.Errorf("BaseURL with TLS = %q, want https://secure.org", got)
	}

	reqProxy := httptest.NewRequest(http.MethodGet, "http://proxy.org/path", nil)
	reqProxy.Header.Set("X-Forwarded-Proto", "https")
	if got := BaseURL(siteEmpty, reqProxy); got != "https://proxy.org" {
		t.Errorf("BaseURL with proxy = %q, want https://proxy.org", got)
	}
}

func TestBuildHomeSEO(t *testing.T) {
	site := siteconfig.Settings{
		Title:                 "Meu Blog",
		Tagline:               "Pensamentos sobre tecnologia",
		Logo:                  "/uploads/logo.png",
		SiteURL:               "https://meublog.com",
		SEODefaultDescription: "Descrição padrão de SEO",
		SEODefaultOGImage:     "/uploads/og-home.png",
		SEOTwitterHandle:      "@meublog",
		SEORobotsIndexing:     siteconfig.RobotsIndex,
	}

	req := httptest.NewRequest(http.MethodGet, "https://meublog.com/", nil)
	m := Build(Params{
		Type:    TypeHome,
		Site:    site,
		Request: req,
	})

	if m.Title != "Meu Blog" {
		t.Errorf("Title = %q, want 'Meu Blog'", m.Title)
	}
	if m.MetaDescription != "Descrição padrão de SEO" {
		t.Errorf("MetaDescription = %q, want %q", m.MetaDescription, "Descrição padrão de SEO")
	}
	if m.CanonicalURL != "https://meublog.com/" {
		t.Errorf("CanonicalURL = %q, want https://meublog.com/", m.CanonicalURL)
	}
	if m.Robots != "index, follow" {
		t.Errorf("Robots = %q, want 'index, follow'", m.Robots)
	}
	if m.OGImage != "https://meublog.com/uploads/og-home.png" {
		t.Errorf("OGImage = %q, want https://meublog.com/uploads/og-home.png", m.OGImage)
	}
	if m.TwitterSite != "@meublog" {
		t.Errorf("TwitterSite = %q, want @meublog", m.TwitterSite)
	}
	if len(m.JSONLD) < 2 {
		t.Fatalf("expected at least 2 JSON-LD schemas (WebSite, Organization), got %d", len(m.JSONLD))
	}
	if !strings.Contains(m.JSONLD[0], `"@type":"WebSite"`) && !strings.Contains(m.JSONLD[1], `"@type":"WebSite"`) {
		t.Errorf("expected WebSite in JSON-LD, got %v", m.JSONLD)
	}
}

func TestBuildPostSEOWithOverrides(t *testing.T) {
	site := siteconfig.Settings{
		Title:   "Meu Blog",
		SiteURL: "https://meublog.com",
	}

	post := content.Item{
		ID:             1,
		Type:           content.TypePost,
		Title:          "Título Normal",
		Slug:           "post-slug",
		SEOTitle:       "Título Otimizado para o Google",
		SEODescription: "Meta descrição dedicada para mecanismos de busca.",
		CanonicalURL:   "https://externo.com/post-original",
		OGTitle:        "Título Social",
		OGDescription:  "Descrição Social",
		OGImage:        "/uploads/social.png",
		TwitterCard:    "summary",
		SchemaType:     "NewsArticle",
		Noindex:        true,
		PublishedAt:    "2026-11-18T10:00:00Z",
		UpdatedAt:      "2026-11-18T11:00:00Z",
		Author:         "Luiz",
		Categories:     []content.Category{{Name: "Tecnologia"}},
		Tags:           []string{"Go", "SQLite"},
	}

	req := httptest.NewRequest(http.MethodGet, "https://meublog.com/post-slug", nil)
	m := Build(Params{
		Type:    TypePost,
		Site:    site,
		Item:    &post,
		Request: req,
	})

	if m.Title != "Título Otimizado para o Google" {
		t.Errorf("Title = %q, want SEO Title override", m.Title)
	}
	if m.MetaDescription != "Meta descrição dedicada para mecanismos de busca." {
		t.Errorf("MetaDescription = %q", m.MetaDescription)
	}
	if m.CanonicalURL != "https://externo.com/post-original" {
		t.Errorf("CanonicalURL = %q, want custom canonical", m.CanonicalURL)
	}
	if m.Robots != "noindex, nofollow" {
		t.Errorf("Robots = %q, want noindex", m.Robots)
	}
	if m.OGTitle != "Título Social" {
		t.Errorf("OGTitle = %q", m.OGTitle)
	}
	if m.OGDescription != "Descrição Social" {
		t.Errorf("OGDescription = %q", m.OGDescription)
	}
	if m.OGImage != "https://meublog.com/uploads/social.png" {
		t.Errorf("OGImage = %q", m.OGImage)
	}
	if m.TwitterCard != "summary" {
		t.Errorf("TwitterCard = %q, want summary", m.TwitterCard)
	}

	htmlContent := string(RenderHTML(Params{
		Type:    TypePost,
		Site:    site,
		Item:    &post,
		Request: req,
	}))

	if !strings.Contains(htmlContent, `<link rel="canonical" href="https://externo.com/post-original">`) {
		t.Errorf("missing canonical link in rendered HTML: %s", htmlContent)
	}
	if !strings.Contains(htmlContent, `<meta name="robots" content="noindex, nofollow">`) {
		t.Errorf("missing robots meta tag in rendered HTML: %s", htmlContent)
	}
	if !strings.Contains(htmlContent, `<meta property="og:type" content="article">`) {
		t.Errorf("missing og:type in rendered HTML: %s", htmlContent)
	}
	if !strings.Contains(htmlContent, `<meta property="article:author" content="Luiz">`) {
		t.Errorf("missing article:author in rendered HTML: %s", htmlContent)
	}
	if !strings.Contains(htmlContent, `<script type="application/ld+json">`) {
		t.Errorf("missing application/ld+json script in rendered HTML: %s", htmlContent)
	}
	if !strings.Contains(htmlContent, `"@type":"NewsArticle"`) {
		t.Errorf("missing NewsArticle schema in rendered HTML: %s", htmlContent)
	}
	if !strings.Contains(htmlContent, `"@type":"BreadcrumbList"`) {
		t.Errorf("missing BreadcrumbList schema in rendered HTML: %s", htmlContent)
	}
}

func TestBuild404SEO(t *testing.T) {
	site := siteconfig.Settings{Title: "Meu Site"}
	m := Build(Params{Type: Type404, Site: site})
	if m.Robots != "noindex, nofollow" {
		t.Errorf("Robots for 404 = %q, want noindex, nofollow", m.Robots)
	}
	if !strings.Contains(m.Title, "Página não encontrada") {
		t.Errorf("Title for 404 = %q", m.Title)
	}
}
