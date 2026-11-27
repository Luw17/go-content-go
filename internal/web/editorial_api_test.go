package web

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Luw17/go-content-go/internal/auth"
	"github.com/Luw17/go-content-go/internal/builder"
	"github.com/Luw17/go-content-go/internal/content"
	"github.com/Luw17/go-content-go/internal/media"
	"github.com/Luw17/go-content-go/internal/siteconfig"
)

func TestDashboardKPIsHealthChecklistAndRBACSidebar(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	admin, err := auth.CreateInitialAdmin(ctx, db, "dash_admin", "a-very-secure-password")
	if err != nil {
		t.Fatal(err)
	}
	author, err := auth.CreateUser(ctx, db, "dash_author", "a-very-secure-password", "author")
	if err != nil {
		t.Fatal(err)
	}
	adminTok, adminExp, _ := auth.CreateSession(ctx, db, admin.ID)
	authorTok, authorExp, _ := auth.CreateSession(ctx, db, author.ID)
	adminCookie := &http.Cookie{Name: sessionCookieName, Value: adminTok, Path: "/", Expires: adminExp}
	authorCookie := &http.Cookie{Name: sessionCookieName, Value: authorTok, Path: "/", Expires: authorExp}

	h, err := NewHandler(db, Options{
		UploadDir: t.TempDir(),
		ThemeDir:  t.TempDir(),
		PluginDir: t.TempDir(),
		BackupDir: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}

	// Admin sees KPIs, Health Checklist, and admin links
	adminDash := getWithCookies(h, "/admin/", adminCookie)
	if adminDash.Code != http.StatusOK {
		t.Fatalf("GET /admin/ (admin) = %d", adminDash.Code)
	}
	adminBody := adminDash.Body.String()
	for _, want := range []string{"Checklist de Saúde SEO", "Posts publicados", "/admin/users", "/admin/settings", "/admin/tools"} {
		if !strings.Contains(adminBody, want) {
			t.Errorf("admin dashboard missing %q", want)
		}
	}

	// Author sidebar hides admin-only links
	authorDash := getWithCookies(h, "/admin/", authorCookie)
	if authorDash.Code != http.StatusOK {
		t.Fatalf("GET /admin/ (author) = %d", authorDash.Code)
	}
	authorBody := authorDash.Body.String()
	for _, hidden := range []string{`href="/admin/users"`, `href="/admin/settings"`, `href="/admin/plugins"`, `href="/admin/tools"`} {
		if strings.Contains(authorBody, hidden) {
			t.Errorf("author sidebar unexpectedly exposed %q", hidden)
		}
	}
}

func TestUserLifecycleManagement(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	admin, err := auth.CreateInitialAdmin(ctx, db, "u_admin", "a-very-secure-password")
	if err != nil {
		t.Fatal(err)
	}
	editor, err := auth.CreateUser(ctx, db, "u_editor", "a-very-secure-password", "editor")
	if err != nil {
		t.Fatal(err)
	}
	adminTok, adminExp, _ := auth.CreateSession(ctx, db, admin.ID)
	editorTok, _, _ := auth.CreateSession(ctx, db, editor.ID)
	adminCookie := &http.Cookie{Name: sessionCookieName, Value: adminTok, Path: "/", Expires: adminExp}

	h, err := NewHandler(db, Options{
		UploadDir: t.TempDir(),
		ThemeDir:  t.TempDir(),
		PluginDir: t.TempDir(),
		BackupDir: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}

	usersPage := getWithCookies(h, "/admin/users", adminCookie)
	csrfCookie := cookieNamed(t, usersPage, csrfCookieName)
	csrf := csrfFromBody(t, usersPage.Body.String())

	// Cannot demote or delete the last active admin
	demoteAdmin := postForm(h, fmt.Sprintf("/admin/users/%d/role", admin.ID), url.Values{
		"csrf_token": {csrf},
		"role":       {"editor"},
	}, adminCookie, csrfCookie)
	if !strings.Contains(demoteAdmin.Header().Get("Location"), "status=last_admin") {
		t.Fatalf("demoting last admin redirected to %q, want status=last_admin", demoteAdmin.Header().Get("Location"))
	}

	// Change editor role to author
	roleResp := postForm(h, fmt.Sprintf("/admin/users/%d/role", editor.ID), url.Values{
		"csrf_token": {csrf},
		"role":       {"author"},
	}, adminCookie, csrfCookie)
	if !strings.Contains(roleResp.Header().Get("Location"), "status=updated") {
		t.Fatalf("update role = %d (%s)", roleResp.Code, roleResp.Header().Get("Location"))
	}

	// Change editor password -> revokes existing session
	passResp := postForm(h, fmt.Sprintf("/admin/users/%d/password", editor.ID), url.Values{
		"csrf_token": {csrf},
		"password":   {"new-super-secret-password"},
	}, adminCookie, csrfCookie)
	if !strings.Contains(passResp.Header().Get("Location"), "status=password_updated") {
		t.Fatalf("update password = %d (%s)", passResp.Code, passResp.Header().Get("Location"))
	}
	if _, valid, _ := auth.SessionUser(ctx, db, editorTok); valid {
		t.Fatalf("editor session was not revoked after password change")
	}

	// Deactivate editor -> cannot authenticate
	toggleResp := postForm(h, fmt.Sprintf("/admin/users/%d/toggle", editor.ID), url.Values{
		"csrf_token": {csrf},
		"active":     {"0"},
	}, adminCookie, csrfCookie)
	if toggleResp.Code != http.StatusSeeOther {
		t.Fatalf("toggle active = %d", toggleResp.Code)
	}
	if _, err := auth.Authenticate(ctx, db, "u_editor", "new-super-secret-password"); err == nil {
		t.Fatalf("deactivated user was allowed to authenticate")
	}

	// Delete editor
	delResp := postForm(h, fmt.Sprintf("/admin/users/%d/delete", editor.ID), url.Values{
		"csrf_token": {csrf},
	}, adminCookie, csrfCookie)
	if !strings.Contains(delResp.Header().Get("Location"), "status=deleted") {
		t.Fatalf("delete user = %d (%s)", delResp.Code, delResp.Header().Get("Location"))
	}
}

func TestScheduledPublishingAndContentRevisions(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	admin, err := auth.CreateInitialAdmin(ctx, db, "sched_admin", "a-very-secure-password")
	if err != nil {
		t.Fatal(err)
	}
	past := time.Now().UTC().Add(-2 * time.Minute).Format(time.RFC3339)
	item, err := content.Save(ctx, db, content.Input{
		Type:        content.TypePost,
		Title:       "Versão 1",
		Slug:        "post-agendado",
		Content:     "Conteúdo original da versão 1.",
		Status:      content.StatusDraft,
		ScheduledAt: past,
	}, admin.ID)
	if err != nil {
		t.Fatal(err)
	}

	// Scheduled publisher publishes due draft
	published, err := content.PublishDueScheduled(ctx, db, time.Now().UTC())
	if err != nil || published != 1 {
		t.Fatalf("PublishDueScheduled() = (%d, %v), want (1, nil)", published, err)
	}
	reloaded, err := content.Get(ctx, db, item.ID, content.TypePost)
	if err != nil || reloaded.Status != content.StatusPublished || reloaded.ScheduledAt != "" {
		t.Fatalf("reloaded scheduled post = %+v, err=%v", reloaded, err)
	}

	// Update post -> creates revision snapshot of Versão 1
	if _, err := content.Save(ctx, db, content.Input{
		ID:      item.ID,
		Type:    content.TypePost,
		Title:   "Versão 2",
		Slug:    "post-agendado",
		Content: "Conteúdo atualizado da versão 2.",
		Status:  content.StatusPublished,
	}, admin.ID); err != nil {
		t.Fatal(err)
	}
	revs, err := content.ListRevisions(ctx, db, item.ID, 10)
	if err != nil || len(revs) != 1 || revs[0].Title != "Versão 1" {
		t.Fatalf("ListRevisions() = (%+v, %v), want 1 revision titled 'Versão 1'", revs, err)
	}

	// Restore revision via HTTP endpoint
	tok, exp, _ := auth.CreateSession(ctx, db, admin.ID)
	sessionCookie := &http.Cookie{Name: sessionCookieName, Value: tok, Path: "/", Expires: exp}
	h, err := NewHandler(db, Options{
		UploadDir: t.TempDir(),
		ThemeDir:  t.TempDir(),
		PluginDir: t.TempDir(),
		BackupDir: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	editPage := getWithCookies(h, fmt.Sprintf("/admin/posts/%d/edit", item.ID), sessionCookie)
	csrfCookie := cookieNamed(t, editPage, csrfCookieName)
	restoreResp := postForm(h, fmt.Sprintf("/admin/content/%d/revisions/%d/restore", item.ID, revs[0].ID), url.Values{
		"csrf_token":   {csrfFromBody(t, editPage.Body.String())},
		"content_type": {content.TypePost},
	}, sessionCookie, csrfCookie)
	if restoreResp.Code != http.StatusSeeOther {
		t.Fatalf("restore revision = %d, want 303", restoreResp.Code)
	}
	restored, err := content.Get(ctx, db, item.ID, content.TypePost)
	if err != nil || restored.Title != "Versão 1" || !strings.Contains(restored.Content, "versão 1") {
		t.Fatalf("restored post = %+v", restored)
	}
}

func TestHeadlessAPIv2AndAPIKeyDraftPreview(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	admin, err := auth.CreateInitialAdmin(ctx, db, "api_admin", "a-very-secure-password")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := content.Save(ctx, db, content.Input{
		Type:        content.TypePost,
		Title:       "Post Headless",
		Slug:        "post-headless",
		Content:     "Conteúdo **rico** em Markdown.",
		Status:      content.StatusPublished,
		NewCategory: "API",
		Tags:        "headless, rest",
	}, admin.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := content.Save(ctx, db, content.Input{
		Type:    content.TypePost,
		Title:   "Rascunho Secreto",
		Slug:    "rascunho-secreto",
		Content: "Apenas via preview com API Key.",
		Status:  content.StatusDraft,
	}, admin.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := content.Save(ctx, db, content.Input{
		Type:    content.TypePage,
		Title:   "Sobre Nós",
		Slug:    "sobre-nos",
		Content: "Página institucional.",
		Status:  content.StatusPublished,
	}, admin.ID); err != nil {
		t.Fatal(err)
	}

	_, apiKey, err := auth.CreateAPIKey(ctx, db, "Frontend Preview", admin.ID)
	if err != nil {
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

	// Enriched post detail
	postRec := httptest.NewRecorder()
	h.ServeHTTP(postRec, httptest.NewRequest(http.MethodGet, "/api/v1/posts/post-headless", nil))
	if postRec.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/posts/post-headless = %d", postRec.Code)
	}
	var detail apiContent
	if err := json.Unmarshal(postRec.Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(detail.ContentHTML, "<strong>rico</strong>") || detail.Author != "api_admin" || len(detail.Categories) != 1 || len(detail.Tags) != 2 {
		t.Fatalf("enriched API post missing fields: %+v", detail)
	}

	// New endpoints: /pages, /categories, /menus/primary, /site
	for _, ep := range []string{"/api/v1/pages", "/api/v1/categories", "/api/v1/menus/primary", "/api/v1/site"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, ep, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s = %d, want 200", ep, rec.Code)
		}
	}

	// Draft preview without API key -> 401
	unauth := httptest.NewRecorder()
	h.ServeHTTP(unauth, httptest.NewRequest(http.MethodGet, "/api/v1/posts/rascunho-secreto?preview=1", nil))
	if unauth.Code != http.StatusUnauthorized {
		t.Fatalf("preview without key = %d, want 401", unauth.Code)
	}

	// Draft preview with valid API key -> 200
	authReq := httptest.NewRequest(http.MethodGet, "/api/v1/posts/rascunho-secreto?preview=1", nil)
	authReq.Header.Set("X-API-Key", apiKey)
	authRec := httptest.NewRecorder()
	h.ServeHTTP(authRec, authReq)
	if authRec.Code != http.StatusOK || !strings.Contains(authRec.Body.String(), "Rascunho Secreto") {
		t.Fatalf("preview with API key = %d (%s)", authRec.Code, authRec.Body.String())
	}
}

func TestGzipThemeCacheBustingMediaPipelineCSVAndTools(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	admin, err := auth.CreateInitialAdmin(ctx, db, "w3_admin", "a-very-secure-password")
	if err != nil {
		t.Fatal(err)
	}
	uploadDir := t.TempDir()
	store, err := media.NewStore(db, uploadDir, media.DefaultMaxUploadSize)
	if err != nil {
		t.Fatal(err)
	}
	// Upload a 640x320 PNG -> should generate a 480px wide thumbnail
	var imgBuf bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 640, 320))
	img.Set(10, 10, color.RGBA{R: 255, A: 255})
	if err := png.Encode(&imgBuf, img); err != nil {
		t.Fatal(err)
	}
	item, err := store.Save(ctx, "banner.png", "Alt inicial", "Desc inicial", 0, bytes.NewReader(imgBuf.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if item.ThumbnailPath == "" || !strings.Contains(item.ThumbnailPath, "/thumbs/") {
		t.Fatalf("expected generated thumbnail path, got %q", item.ThumbnailPath)
	}

	tok, exp, _ := auth.CreateSession(ctx, db, admin.ID)
	sessionCookie := &http.Cookie{Name: sessionCookieName, Value: tok, Path: "/", Expires: exp}
	h, err := NewHandler(db, Options{
		UploadDir: uploadDir,
		ThemeDir:  t.TempDir(),
		PluginDir: t.TempDir(),
		BackupDir: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}

	// 0. Thumbnail must be served with 200 OK image/jpeg (not 404!)
	thumbRec := httptest.NewRecorder()
	h.ServeHTTP(thumbRec, httptest.NewRequest(http.MethodGet, item.ThumbnailPath, nil))
	if thumbRec.Code != http.StatusOK || thumbRec.Header().Get("Content-Type") != "image/jpeg" || thumbRec.Body.Len() == 0 {
		t.Fatalf("GET thumbnail %q = %d ct=%q len=%d, want 200 image/jpeg", item.ThumbnailPath, thumbRec.Code, thumbRec.Header().Get("Content-Type"), thumbRec.Body.Len())
	}

	// 1. Update media metadata via POST /admin/media/{id}/metadata (admin only)
	mediaPage := getWithCookies(h, "/admin/media?q=banner", sessionCookie)
	if mediaPage.Code != http.StatusOK || !strings.Contains(mediaPage.Body.String(), "banner.png") {
		t.Fatalf("GET /admin/media?q=banner = %d", mediaPage.Code)
	}
	if strings.Contains(mediaPage.Body.String(), `style="`) {
		t.Fatalf("media library HTML must not contain inline style attributes blocked by CSP")
	}
	csrfCookie := cookieNamed(t, mediaPage, csrfCookieName)
	metaResp := postForm(h, fmt.Sprintf("/admin/media/%d/metadata", item.ID), url.Values{
		"csrf_token":  {csrfFromBody(t, mediaPage.Body.String())},
		"alt_text":    {"Alt atualizado"},
		"description": {"Descrição nova"},
	}, sessionCookie, csrfCookie)
	if metaResp.Code != http.StatusSeeOther {
		t.Fatalf("POST /admin/media/%d/metadata = %d", item.ID, metaResp.Code)
	}

	// Author must get 403 on updateMediaMetadata
	authorUser, err := auth.CreateUser(ctx, db, "media_author", "a-very-secure-password", "author")
	if err != nil {
		t.Fatal(err)
	}
	authorTok, authorExp, _ := auth.CreateSession(ctx, db, authorUser.ID)
	authorCookie := &http.Cookie{Name: sessionCookieName, Value: authorTok, Path: "/", Expires: authorExp}
	authorDash := getWithCookies(h, "/admin/", authorCookie)
	authorMetaResp := postForm(h, fmt.Sprintf("/admin/media/%d/metadata", item.ID), url.Values{
		"csrf_token":  {csrfFromBody(t, authorDash.Body.String())},
		"alt_text":    {"Tentativa indevida"},
		"description": {""},
	}, authorCookie, cookieNamed(t, authorDash, csrfCookieName))
	if authorMetaResp.Code != http.StatusForbidden {
		t.Fatalf("author POST /admin/media/%d/metadata = %d, want 403", item.ID, authorMetaResp.Code)
	}

	// Deleting media must also remove the thumbnail file from disk
	absThumb, err := store.ResolvePublicPath(item.ThumbnailPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Delete(ctx, item.ID); err != nil {
		t.Fatalf("store.Delete() error = %v", err)
	}
	if _, err := os.Stat(absThumb); !os.IsNotExist(err) {
		t.Fatalf("expected thumbnail file %q to be deleted with media item, stat err = %v", absThumb, err)
	}

	// 2. Gzip compression + theme asset cache-busting (?v=hash)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("GET / with gzip = %d encoding=%q", rec.Code, rec.Header().Get("Content-Encoding"))
	}
	gr, err := gzip.NewReader(rec.Body)
	if err != nil {
		t.Fatal(err)
	}
	unzipped, err := io.ReadAll(gr)
	_ = gr.Close()
	if err != nil {
		t.Fatal(err)
	}
	assetRe := regexp.MustCompile(`/themes/default/style\.css\?v=([0-9a-f]{12})`)
	match := assetRe.FindSubmatch(unzipped)
	if match == nil {
		t.Fatalf("home HTML missing cache-busted theme stylesheet link")
	}
	cssRec := httptest.NewRecorder()
	h.ServeHTTP(cssRec, httptest.NewRequest(http.MethodGet, string(match[0]), nil))
	if cssRec.Code != http.StatusOK || !strings.Contains(cssRec.Header().Get("Cache-Control"), "immutable") {
		t.Fatalf("versioned theme CSS = %d Cache-Control=%q", cssRec.Code, cssRec.Header().Get("Cache-Control"))
	}

	// 3. Export CSV of form submissions
	csvRec := getWithCookies(h, "/admin/builder/submissions/export.csv", sessionCookie)
	if csvRec.Code != http.StatusOK || !strings.Contains(csvRec.Header().Get("Content-Type"), "text/csv") {
		t.Fatalf("GET /admin/builder/submissions/export.csv = %d ct=%q", csvRec.Code, csvRec.Header().Get("Content-Type"))
	}

	// 4. Webhook SSRF protection + delivery on form submit
	webhookHit := make(chan bool, 1)
	hookServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		webhookHit <- true
		w.WriteHeader(http.StatusOK)
	}))
	defer hookServer.Close()

	// By default, loopback / cloud metadata webhook targets must be blocked (anti-SSRF)
	if err := sendFormWebhook(ctx, hookServer.URL, builder.FormSubmission{FormID: "ssrf"}); err == nil {
		t.Fatalf("expected SSRF protection to block loopback webhook URL %q", hookServer.URL)
	}
	if err := sendFormWebhook(ctx, "http://169.254.169.254/latest/meta-data/", builder.FormSubmission{FormID: "ssrf"}); err == nil {
		t.Fatalf("expected SSRF protection to block link-local metadata IP")
	}

	// CSV formula injection & SMTP header sanitization
	if got := sanitizeCSVCell("=SUM(A1:A2)"); got != "'=SUM(A1:A2)" {
		t.Fatalf("sanitizeCSVCell formula = %q, want prefixed with single quote", got)
	}
	if got := sanitizeEmailHeader("contato\r\nBcc: attacker@evil.example"); strings.ContainsAny(got, "\r\n") {
		t.Fatalf("sanitizeEmailHeader leaked CRLF: %q", got)
	}

	allowPrivateWebhookTargetsForTest = true
	t.Cleanup(func() { allowPrivateWebhookTargetsForTest = false })
	settings := siteconfig.Defaults()
	settings.FormWebhookURL = hookServer.URL
	if err := siteconfig.Save(ctx, db, settings); err != nil {
		t.Fatal(err)
	}
	formReq := httptest.NewRequest(http.MethodPost, "/forms/submit", strings.NewReader(url.Values{
		"form_id":   {"contato"},
		"page_path": {"/"},
		"name":      {"=Visitante"},
		"email":     {"visitante@exemplo.com"},
		"message":   {"Olá!"},
	}.Encode()))
	formReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	formRec := httptest.NewRecorder()
	h.ServeHTTP(formRec, formReq)
	if formRec.Code != http.StatusSeeOther {
		t.Fatalf("POST /forms/submit = %d", formRec.Code)
	}
	select {
	case <-webhookHit:
	case <-time.After(3 * time.Second):
		t.Fatalf("expected webhook notification after form submission")
	}
	// Verify CSV export neutralizes leading '='
	csvAfterSubmit := getWithCookies(h, "/admin/builder/submissions/export.csv", sessionCookie)
	if !strings.Contains(csvAfterSubmit.Body.String(), "'=Visitante") {
		t.Fatalf("CSV export did not sanitize formula cell:\n%s", csvAfterSubmit.Body.String())
	}

	// 5. Export JSON & Import Markdown via /admin/tools
	exportRec := getWithCookies(h, "/admin/tools/export.json", sessionCookie)
	if exportRec.Code != http.StatusOK || !strings.Contains(exportRec.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("GET /admin/tools/export.json = %d", exportRec.Code)
	}
	toolsPage := getWithCookies(h, "/admin/tools", sessionCookie)
	toolsCSRF := cookieNamed(t, toolsPage, csrfCookieName)
	var mpBuf bytes.Buffer
	mw := multipart.NewWriter(&mpBuf)
	_ = mw.WriteField("csrf_token", csrfFromBody(t, toolsPage.Body.String()))
	_ = mw.WriteField("format", "markdown")
	fw, _ := mw.CreateFormFile("file", "import.md")
	_, _ = fw.Write([]byte("---\ntitle: Importado via HTTP\nstatus: published\n---\n\nCorpo do artigo."))
	_ = mw.Close()
	impReq := httptest.NewRequest(http.MethodPost, "/admin/tools/import", &mpBuf)
	impReq.Header.Set("Content-Type", mw.FormDataContentType())
	impReq.AddCookie(sessionCookie)
	impReq.AddCookie(toolsCSRF)
	impRec := httptest.NewRecorder()
	h.ServeHTTP(impRec, impReq)
	if impRec.Code != http.StatusSeeOther || !strings.Contains(impRec.Header().Get("Location"), "imported=1") {
		t.Fatalf("POST /admin/tools/import = %d (%s)", impRec.Code, impRec.Header().Get("Location"))
	}
}
