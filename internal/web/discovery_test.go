package web

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/Luw17/go-content-go/internal/auth"
	"github.com/Luw17/go-content-go/internal/content"
	"github.com/Luw17/go-content-go/internal/media"
	"github.com/Luw17/go-content-go/internal/siteconfig"
)

func TestHomePaginationWithPrevNext(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	admin, err := auth.CreateInitialAdmin(ctx, db, "pag_admin", "a-very-secure-password")
	if err != nil {
		t.Fatal(err)
	}
	settings := siteconfig.Defaults()
	settings.SiteURL = "https://pag.exemplo.com"
	if err := siteconfig.Save(ctx, db, settings); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 12; i++ {
		if _, err := content.Save(ctx, db, content.Input{
			Type:    content.TypePost,
			Title:   fmt.Sprintf("Artigo %02d", i),
			Slug:    fmt.Sprintf("artigo-%02d", i),
			Content: "Parágrafo de teste para paginação na home.",
			Status:  content.StatusPublished,
		}, admin.ID); err != nil {
			t.Fatal(err)
		}
	}

	h, err := NewHandler(db, Options{
		UploadDir: t.TempDir(),
		ThemeDir:  t.TempDir(),
		PluginDir: t.TempDir(),
		BackupDir: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}

	// Page 1: has rel="next", no rel="prev", shows 10 posts
	p1 := httptest.NewRecorder()
	h.ServeHTTP(p1, httptest.NewRequest(http.MethodGet, "/", nil))
	if p1.Code != http.StatusOK {
		t.Fatalf("GET / = %d, want 200", p1.Code)
	}
	body1 := p1.Body.String()
	if !strings.Contains(body1, `<link rel="next" href="https://pag.exemplo.com/?page=2">`) {
		t.Fatalf("page 1 missing rel=next link")
	}
	if strings.Contains(body1, `<link rel="prev"`) {
		t.Fatalf("page 1 unexpectedly contains rel=prev")
	}
	if !strings.Contains(body1, "Página 1 de 2") {
		t.Fatalf("page 1 missing pagination status")
	}

	// Page 2: has rel="prev", no rel="next", canonical /?page=2
	p2 := httptest.NewRecorder()
	h.ServeHTTP(p2, httptest.NewRequest(http.MethodGet, "/?page=2", nil))
	if p2.Code != http.StatusOK {
		t.Fatalf("GET /?page=2 = %d, want 200", p2.Code)
	}
	body2 := p2.Body.String()
	if !strings.Contains(body2, `<link rel="prev" href="https://pag.exemplo.com/">`) {
		t.Fatalf("page 2 missing rel=prev link")
	}
	if strings.Contains(body2, `<link rel="next"`) {
		t.Fatalf("page 2 unexpectedly contains rel=next")
	}
	if !strings.Contains(body2, `<link rel="canonical" href="https://pag.exemplo.com/?page=2">`) {
		t.Fatalf("page 2 missing canonical /?page=2")
	}

	// Page 3: out of range -> 404
	p3 := httptest.NewRecorder()
	h.ServeHTTP(p3, httptest.NewRequest(http.MethodGet, "/?page=3", nil))
	if p3.Code != http.StatusNotFound {
		t.Fatalf("GET /?page=3 = %d, want 404", p3.Code)
	}
}

func TestCategoryTagArchivesAndSitemap(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	admin, err := auth.CreateInitialAdmin(ctx, db, "arch_admin", "a-very-secure-password")
	if err != nil {
		t.Fatal(err)
	}
	settings := siteconfig.Defaults()
	settings.SiteURL = "https://arch.exemplo.com"
	if err := siteconfig.Save(ctx, db, settings); err != nil {
		t.Fatal(err)
	}
	if _, err := content.Save(ctx, db, content.Input{
		Type:        content.TypePost,
		Title:       "Guia de Go e SQLite",
		Slug:        "guia-go-sqlite",
		Content:     "Conteúdo técnico completo sobre Go e SQLite.",
		Status:      content.StatusPublished,
		NewCategory: "Engenharia",
		Tags:        "golang, sqlite",
	}, admin.ID); err != nil {
		t.Fatal(err)
	}

	h, err := NewHandler(db, Options{
		UploadDir: t.TempDir(),
		ThemeDir:  t.TempDir(),
		PluginDir: t.TempDir(),
		BackupDir: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}

	// Category archive
	catRec := httptest.NewRecorder()
	h.ServeHTTP(catRec, httptest.NewRequest(http.MethodGet, "/categoria/engenharia", nil))
	if catRec.Code != http.StatusOK {
		t.Fatalf("GET /categoria/engenharia = %d, want 200", catRec.Code)
	}
	if !strings.Contains(catRec.Body.String(), "Guia de Go e SQLite") || !strings.Contains(catRec.Body.String(), "BreadcrumbList") {
		t.Fatalf("category archive missing post or BreadcrumbList JSON-LD")
	}

	// Tag archive
	tagRec := httptest.NewRecorder()
	h.ServeHTTP(tagRec, httptest.NewRequest(http.MethodGet, "/tag/golang", nil))
	if tagRec.Code != http.StatusOK {
		t.Fatalf("GET /tag/golang = %d, want 200", tagRec.Code)
	}
	if !strings.Contains(tagRec.Body.String(), "Guia de Go e SQLite") {
		t.Fatalf("tag archive missing post")
	}

	// Unknown archive returns 404
	notFound := httptest.NewRecorder()
	h.ServeHTTP(notFound, httptest.NewRequest(http.MethodGet, "/categoria/inexistente", nil))
	if notFound.Code != http.StatusNotFound {
		t.Fatalf("GET /categoria/inexistente = %d, want 404", notFound.Code)
	}

	// Sitemap includes category and tag archive URLs
	smRec := httptest.NewRecorder()
	h.ServeHTTP(smRec, httptest.NewRequest(http.MethodGet, "/sitemap.xml", nil))
	if smRec.Code != http.StatusOK {
		t.Fatalf("GET /sitemap.xml = %d", smRec.Code)
	}
	smBody := smRec.Body.String()
	for _, want := range []string{
		"https://arch.exemplo.com/categoria/engenharia",
		"https://arch.exemplo.com/tag/golang",
		"https://arch.exemplo.com/tag/sqlite",
	} {
		if !strings.Contains(smBody, want) {
			t.Errorf("sitemap missing archive URL %q", want)
		}
	}
}

func TestRSSFeedAndAutodiscovery(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	admin, err := auth.CreateInitialAdmin(ctx, db, "rss_admin", "a-very-secure-password")
	if err != nil {
		t.Fatal(err)
	}
	settings := siteconfig.Defaults()
	settings.SiteURL = "https://rss.exemplo.com"
	if err := siteconfig.Save(ctx, db, settings); err != nil {
		t.Fatal(err)
	}
	if _, err := content.Save(ctx, db, content.Input{
		Type:    content.TypePost,
		Title:   "Post no Feed",
		Slug:    "post-no-feed",
		Content: "Resumo do post no RSS 2.0.",
		Status:  content.StatusPublished,
	}, admin.ID); err != nil {
		t.Fatal(err)
	}

	h, err := NewHandler(db, Options{
		UploadDir: t.TempDir(),
		ThemeDir:  t.TempDir(),
		PluginDir: t.TempDir(),
		BackupDir: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}

	// Home <head> has RSS autodiscovery
	homeRec := httptest.NewRecorder()
	h.ServeHTTP(homeRec, httptest.NewRequest(http.MethodGet, "/", nil))
	if !strings.Contains(homeRec.Body.String(), `<link rel="alternate" type="application/rss+xml"`) ||
		!strings.Contains(homeRec.Body.String(), `href="https://rss.exemplo.com/rss.xml"`) {
		t.Fatalf("home missing RSS autodiscovery link")
	}

	// GET /rss.xml returns valid RSS 2.0
	rssRec := httptest.NewRecorder()
	h.ServeHTTP(rssRec, httptest.NewRequest(http.MethodGet, "/rss.xml", nil))
	if rssRec.Code != http.StatusOK {
		t.Fatalf("GET /rss.xml = %d, want 200", rssRec.Code)
	}
	if ct := rssRec.Header().Get("Content-Type"); !strings.Contains(ct, "application/rss+xml") {
		t.Fatalf("RSS Content-Type = %q", ct)
	}
	var parsed rssFeed
	if err := xml.Unmarshal(rssRec.Body.Bytes(), &parsed); err != nil {
		t.Fatalf("unmarshal RSS XML: %v\n%s", err, rssRec.Body.String())
	}
	if parsed.Version != "2.0" || len(parsed.Channel.Items) != 1 || parsed.Channel.Items[0].Link != "https://rss.exemplo.com/post-no-feed" {
		t.Fatalf("unexpected RSS feed: %+v", parsed)
	}

	// HEAD /rss.xml returns 200 with empty body
	headRSS := httptest.NewRecorder()
	h.ServeHTTP(headRSS, httptest.NewRequest(http.MethodHead, "/rss.xml", nil))
	if headRSS.Code != http.StatusOK || headRSS.Body.Len() != 0 {
		t.Fatalf("HEAD /rss.xml = %d (len %d)", headRSS.Code, headRSS.Body.Len())
	}
}

func TestRedirectsAdminAndAutoSlugChange(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	admin, err := auth.CreateInitialAdmin(ctx, db, "redir_admin", "a-very-secure-password")
	if err != nil {
		t.Fatal(err)
	}
	settings := siteconfig.Defaults()
	settings.AutoRedirectSlugChange = true
	if err := siteconfig.Save(ctx, db, settings); err != nil {
		t.Fatal(err)
	}
	post, err := content.Save(ctx, db, content.Input{
		Type:    content.TypePost,
		Title:   "Post Original",
		Slug:    "slug-original",
		Content: "Conteúdo original.",
		Status:  content.StatusPublished,
	}, admin.ID)
	if err != nil {
		t.Fatal(err)
	}

	token, expiresAt, err := auth.CreateSession(ctx, db, admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	sessionCookie := &http.Cookie{Name: sessionCookieName, Value: token, Path: "/", Expires: expiresAt}

	h, err := NewHandler(db, Options{
		UploadDir: t.TempDir(),
		ThemeDir:  t.TempDir(),
		PluginDir: t.TempDir(),
		BackupDir: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}

	// 1. Create manual 302 redirect via /admin/redirects
	redirPage := getWithCookies(h, "/admin/redirects", sessionCookie)
	if redirPage.Code != http.StatusOK {
		t.Fatalf("GET /admin/redirects = %d", redirPage.Code)
	}
	csrfCookie := cookieNamed(t, redirPage, csrfCookieName)
	saveResp := postForm(h, "/admin/redirects", url.Values{
		"csrf_token":  {csrfFromBody(t, redirPage.Body.String())},
		"source_path": {"/campanha-antiga"},
		"target_path": {"/slug-original"},
		"status_code": {"302"},
	}, sessionCookie, csrfCookie)
	if saveResp.Code != http.StatusSeeOther {
		t.Fatalf("POST /admin/redirects = %d, want 303", saveResp.Code)
	}

	manualRec := httptest.NewRecorder()
	h.ServeHTTP(manualRec, httptest.NewRequest(http.MethodGet, "/campanha-antiga", nil))
	if manualRec.Code != http.StatusFound || manualRec.Header().Get("Location") != "/slug-original" {
		t.Fatalf("GET /campanha-antiga = %d (%s), want 302 /slug-original", manualRec.Code, manualRec.Header().Get("Location"))
	}

	// 2. Rename slug of published post -> automatic 301 redirect created
	editResp := postForm(h, "/admin/posts/save", url.Values{
		"csrf_token": {csrfFromBody(t, redirPage.Body.String())},
		"id":         {fmt.Sprint(post.ID)},
		"title":      {"Post Renomeado"},
		"slug":       {"slug-renomeado"},
		"content":    {"Conteúdo atualizado."},
		"status":     {content.StatusPublished},
	}, sessionCookie, csrfCookie)
	if editResp.Code != http.StatusSeeOther {
		t.Fatalf("POST /admin/posts/save = %d", editResp.Code)
	}

	autoRec := httptest.NewRecorder()
	h.ServeHTTP(autoRec, httptest.NewRequest(http.MethodGet, "/slug-original", nil))
	if autoRec.Code != http.StatusMovedPermanently || autoRec.Header().Get("Location") != "/slug-renomeado" {
		t.Fatalf("GET /slug-original = %d (%s), want 301 /slug-renomeado", autoRec.Code, autoRec.Header().Get("Location"))
	}
}

func TestFaviconAndAppleTouchIcon(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	if _, err := auth.CreateInitialAdmin(ctx, db, "fav_admin", "a-very-secure-password"); err != nil {
		t.Fatal(err)
	}
	uploadDir := t.TempDir()
	store, err := media.NewStore(db, uploadDir, media.DefaultMaxUploadSize)
	if err != nil {
		t.Fatal(err)
	}
	var imgBuf bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 16, 16))
	img.Set(0, 0, color.RGBA{R: 10, G: 20, B: 30, A: 255})
	if err := png.Encode(&imgBuf, img); err != nil {
		t.Fatal(err)
	}
	uploaded, err := store.Save(ctx, "favicon.png", "Favicon", "", 0, bytes.NewReader(imgBuf.Bytes()))
	if err != nil {
		t.Fatal(err)
	}

	h, err := NewHandler(db, Options{
		UploadDir: uploadDir,
		ThemeDir:  t.TempDir(),
		PluginDir: t.TempDir(),
		BackupDir: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}

	// Unconfigured /favicon.ico returns 204
	favEmpty := httptest.NewRecorder()
	h.ServeHTTP(favEmpty, httptest.NewRequest(http.MethodGet, "/favicon.ico", nil))
	if favEmpty.Code != http.StatusNoContent {
		t.Fatalf("unconfigured /favicon.ico = %d, want 204", favEmpty.Code)
	}

	// Configure favicon and apple touch icon
	settings := siteconfig.Defaults()
	settings.Favicon = uploaded.Filepath
	settings.AppleTouchIcon = uploaded.Filepath
	if err := siteconfig.Save(ctx, db, settings); err != nil {
		t.Fatal(err)
	}

	favConfigured := httptest.NewRecorder()
	h.ServeHTTP(favConfigured, httptest.NewRequest(http.MethodGet, "/favicon.ico", nil))
	if favConfigured.Code != http.StatusFound || favConfigured.Header().Get("Location") != uploaded.Filepath {
		t.Fatalf("configured /favicon.ico = %d (%s), want 302 %s", favConfigured.Code, favConfigured.Header().Get("Location"), uploaded.Filepath)
	}

	homeRec := httptest.NewRecorder()
	h.ServeHTTP(homeRec, httptest.NewRequest(http.MethodGet, "/", nil))
	homeBody := homeRec.Body.String()
	if !strings.Contains(homeBody, `<link rel="icon" href="`+uploaded.Filepath+`">`) ||
		!strings.Contains(homeBody, `<link rel="apple-touch-icon" href="`+uploaded.Filepath+`">`) {
		t.Fatalf("home <head> missing favicon or apple-touch-icon link")
	}
}

func TestPublicSearchFTS5(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	admin, err := auth.CreateInitialAdmin(ctx, db, "search_admin", "a-very-secure-password")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := content.Save(ctx, db, content.Input{
		Type:    content.TypePost,
		Title:   "Performance com SQLite WAL",
		Slug:    "sqlite-wal",
		Content: "Como otimizar leituras concorrentes com modo WAL.",
		Status:  content.StatusPublished,
	}, admin.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := content.Save(ctx, db, content.Input{
		Type:    content.TypePost,
		Title:   "Rascunho secreto SQLite",
		Slug:    "rascunho-sqlite",
		Content: "Não deve aparecer na busca pública.",
		Status:  content.StatusDraft,
	}, admin.ID); err != nil {
		t.Fatal(err)
	}

	h, err := NewHandler(db, Options{
		UploadDir: t.TempDir(),
		ThemeDir:  t.TempDir(),
		PluginDir: t.TempDir(),
		BackupDir: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/search?q=SQLite+WAL", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /search = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Performance com SQLite WAL") {
		t.Fatalf("search did not find published matching post")
	}
	if strings.Contains(body, "Rascunho secreto SQLite") {
		t.Fatalf("search leaked draft post")
	}
	if !strings.Contains(body, `<meta name="robots" content="noindex, follow">`) {
		t.Fatalf("search page missing noindex, follow robots meta")
	}

	// Special FTS5 syntax chars must not cause 500
	special := httptest.NewRecorder()
	h.ServeHTTP(special, httptest.NewRequest(http.MethodGet, "/search?q="+url.QueryEscape(`"test" OR (sqlite-wal*)`), nil))
	if special.Code != http.StatusOK {
		t.Fatalf("GET /search with special chars = %d, want 200", special.Code)
	}
}

func TestRelatedPostsAndReadingTime(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	admin, err := auth.CreateInitialAdmin(ctx, db, "rel_admin", "a-very-secure-password")
	if err != nil {
		t.Fatal(err)
	}
	longBody := strings.Repeat("palavra ", 420) // ~420 words -> 3 min de leitura
	if _, err := content.Save(ctx, db, content.Input{
		Type:        content.TypePost,
		Title:       "Artigo Principal",
		Slug:        "artigo-principal",
		Content:     longBody,
		Status:      content.StatusPublished,
		NewCategory: "Arquitetura",
		Tags:        "go, performance",
	}, admin.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := content.Save(ctx, db, content.Input{
		Type:    content.TypePost,
		Title:   "Artigo Relacionado",
		Slug:    "artigo-relacionado",
		Content: "Outro artigo sobre performance em Go.",
		Status:  content.StatusPublished,
		Tags:    "performance",
	}, admin.ID); err != nil {
		t.Fatal(err)
	}

	h, err := NewHandler(db, Options{
		UploadDir: t.TempDir(),
		ThemeDir:  t.TempDir(),
		PluginDir: t.TempDir(),
		BackupDir: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/artigo-principal", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /artigo-principal = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "3 min de leitura") {
		t.Fatalf("single post missing reading time (expected '3 min de leitura')")
	}
	if !strings.Contains(body, "Artigos relacionados") || !strings.Contains(body, "Artigo Relacionado") {
		t.Fatalf("single post missing related posts section")
	}
	if !strings.Contains(body, `href="/categoria/arquitetura"`) || !strings.Contains(body, `href="/tag/performance"`) {
		t.Fatalf("single post missing clickable category or tag links")
	}
}
