package web

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/Luw17/go-content-go/internal/auth"
	"github.com/Luw17/go-content-go/internal/content"
	"github.com/Luw17/go-content-go/internal/database"
)

func TestHandlerServesEmbeddedPagesAndAssets(t *testing.T) {
	db := newTestDB(t)
	handler, err := NewHandler(db)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("RoleBasedContentAuthorization", func(t *testing.T) {
		ctx := context.Background()
		db := newTestDB(t)
		admin, err := auth.CreateInitialAdmin(ctx, db, "role_admin", "a-very-secure-password")
		if err != nil {
			t.Fatal(err)
		}
		author, err := auth.CreateUser(ctx, db, "role_author", "a-very-secure-password", "author")
		if err != nil {
			t.Fatal(err)
		}
		otherAuthor, err := auth.CreateUser(ctx, db, "other_author", "a-very-secure-password", "author")
		if err != nil {
			t.Fatal(err)
		}
		editor, err := auth.CreateUser(ctx, db, "role_editor", "a-very-secure-password", "editor")
		if err != nil {
			t.Fatal(err)
		}
		ownContent, err := content.Save(ctx, db, content.Input{
			Type: content.TypePost, Title: "Own draft", Content: "own", Status: content.StatusDraft,
		}, author.ID)
		if err != nil {
			t.Fatal(err)
		}
		otherContent, err := content.Save(ctx, db, content.Input{
			Type: content.TypePost, Title: "Other draft", Content: "other", Status: content.StatusDraft,
		}, otherAuthor.ID)
		if err != nil {
			t.Fatal(err)
		}
		handler, err := NewHandler(db)
		if err != nil {
			t.Fatal(err)
		}
		sessionCookie := func(user auth.User) *http.Cookie {
			t.Helper()
			token, expiresAt, err := auth.CreateSession(ctx, db, user.ID)
			if err != nil {
				t.Fatal(err)
			}
			return &http.Cookie{Name: sessionCookieName, Value: token, Path: "/", Expires: expiresAt}
		}
		authorCookie := sessionCookie(author)
		list := getWithCookies(handler, "/admin/posts", authorCookie)
		if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), "Own draft") ||
			strings.Contains(list.Body.String(), "Other draft") {
			t.Fatalf("author content list = %d, %q", list.Code, list.Body.String())
		}
		foreignEdit := getWithCookies(handler, "/admin/posts/"+strconv.FormatInt(otherContent.ID, 10)+"/edit", authorCookie)
		if foreignEdit.Code != http.StatusNotFound {
			t.Fatalf("author foreign edit status = %d, want 404", foreignEdit.Code)
		}
		ownEdit := getWithCookies(handler, "/admin/posts/"+strconv.FormatInt(ownContent.ID, 10)+"/edit", authorCookie)
		if ownEdit.Code != http.StatusOK {
			t.Fatalf("author own edit status = %d, want 200", ownEdit.Code)
		}
		adminOnly := getWithCookies(handler, "/admin/media", authorCookie)
		if adminOnly.Code != http.StatusForbidden {
			t.Fatalf("author admin-only media status = %d, want 403", adminOnly.Code)
		}
		editorEdit := getWithCookies(handler, "/admin/posts/"+strconv.FormatInt(otherContent.ID, 10)+"/edit", sessionCookie(editor))
		if editorEdit.Code != http.StatusOK {
			t.Fatalf("editor foreign edit status = %d, want 200", editorEdit.Code)
		}
		csrfCookie := cookieNamed(t, list, csrfCookieName)
		publish := postForm(handler, "/admin/content/"+strconv.FormatInt(ownContent.ID, 10)+"/status", url.Values{
			"csrf_token":   {csrfFromBody(t, list.Body.String())},
			"content_type": {content.TypePost},
			"status":       {content.StatusPublished},
		}, authorCookie, csrfCookie)
		if publish.Code != http.StatusForbidden {
			t.Fatalf("author publish action = %d, want 403", publish.Code)
		}
		createPublished := postForm(handler, "/admin/posts/save", url.Values{
			"csrf_token": {csrfFromBody(t, list.Body.String())},
			"title":      {"Author submission"},
			"content":    {"saved as draft"},
			"status":     {content.StatusPublished},
		}, authorCookie, csrfCookie)
		if createPublished.Code != http.StatusSeeOther {
			t.Fatalf("author published submission = %d, want redirect after saving as draft", createPublished.Code)
		}
		var savedStatus string
		if err := db.QueryRowContext(ctx, "SELECT status FROM posts WHERE slug = 'author-submission'").Scan(&savedStatus); err != nil {
			t.Fatal(err)
		}
		if savedStatus != content.StatusDraft {
			t.Fatalf("author submission status = %q, want draft", savedStatus)
		}
		adminPage := getWithCookies(handler, "/admin/settings", authorCookie)
		if adminPage.Code != http.StatusForbidden {
			t.Fatalf("author settings status = %d, want 403", adminPage.Code)
		}
		adminSettings := getWithCookies(handler, "/admin/settings", sessionCookie(admin))
		if adminSettings.Code != http.StatusOK {
			t.Fatalf("administrator settings status = %d, want 200", adminSettings.Code)
		}
	})

	tests := []struct {
		path       string
		statusCode int
		body       string
	}{
		{path: "/", statusCode: http.StatusOK, body: "Go Content Go!"},
		{path: "/healthz", statusCode: http.StatusOK, body: "ok"},
		{path: "/static/style.css", statusCode: http.StatusOK, body: "system-ui"},
		{path: "/static/admin.js", statusCode: http.StatusOK, body: "data-copy-url"},
		{path: "/themes/default/style.css", statusCode: http.StatusOK, body: "system-ui"},
		{path: "/missing", statusCode: http.StatusNotFound, body: "404"},
	}
	for _, test := range tests {
		t.Run(test.path, func(t *testing.T) {
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, test.path, nil))
			if response.Code != test.statusCode {
				t.Errorf("status = %d, want %d", response.Code, test.statusCode)
			}
			if !strings.Contains(response.Body.String(), test.body) {
				t.Errorf("body %q does not contain %q", response.Body.String(), test.body)
			}
		})
	}
}

func TestPublicAPIIsRateLimited(t *testing.T) {
	handler, err := NewHandler(newTestDB(t))
	if err != nil {
		t.Fatal(err)
	}
	var response *httptest.ResponseRecorder
	for range 31 {
		response = httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/api/v1/posts", nil)
		request.Header.Set("Origin", "https://example.test")
		handler.ServeHTTP(response, request)
	}
	if response.Code != http.StatusTooManyRequests || response.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Fatalf("throttled API response = %d, CORS origin %q; want 429",
			response.Code, response.Header().Get("Access-Control-Allow-Origin"))
	}
}

func TestAdministratorBackupAndMaintenanceControls(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	user, err := auth.CreateInitialAdmin(ctx, db, "backup_admin", "a-very-secure-password")
	if err != nil {
		t.Fatal(err)
	}
	token, expiresAt, err := auth.CreateSession(ctx, db, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	sessionCookie := &http.Cookie{Name: sessionCookieName, Value: token, Path: "/", Expires: expiresAt}
	backupDir := t.TempDir()
	handler, err := NewHandler(db, Options{BackupDir: backupDir})
	if err != nil {
		t.Fatal(err)
	}
	settings := getWithCookies(handler, "/admin/settings", sessionCookie)
	if settings.Code != http.StatusOK || !strings.Contains(settings.Body.String(), "Baixar backup agora") {
		t.Fatalf("settings response = %d, %q", settings.Code, settings.Body.String())
	}
	csrfCookie := cookieNamed(t, settings, csrfCookieName)
	create := postForm(handler, "/admin/settings/backup", url.Values{
		"csrf_token": {csrfFromBody(t, settings.Body.String())},
	}, sessionCookie, csrfCookie)
	if create.Code != http.StatusSeeOther {
		t.Fatalf("create backup response = %d, want redirect", create.Code)
	}
	download := getWithCookies(handler, "/admin/settings/backup/download", sessionCookie)
	if download.Code != http.StatusOK || download.Header().Get("Content-Disposition") == "" || download.Body.Len() == 0 {
		t.Fatalf("download response = %d, disposition %q, bytes %d",
			download.Code, download.Header().Get("Content-Disposition"), download.Body.Len())
	}
	optimize := postForm(handler, "/admin/settings/optimize", url.Values{
		"csrf_token": {csrfFromBody(t, settings.Body.String())},
	}, sessionCookie, csrfCookie)
	if optimize.Code != http.StatusSeeOther {
		t.Fatalf("database optimization response = %d, want redirect: %s", optimize.Code, optimize.Body.String())
	}
	backups, err := os.ReadDir(backupDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(backups) != 2 || !strings.HasSuffix(backups[0].Name(), ".db") || !strings.HasSuffix(backups[1].Name(), ".db") {
		t.Fatalf("local backups = %+v, want the two backup files", backups)
	}
}

func TestAdminSetupLoginAndLogoutFlow(t *testing.T) {
	db := newTestDB(t)
	handler, err := NewHandler(db)
	if err != nil {
		t.Fatal(err)
	}

	setupPage := httptest.NewRecorder()
	handler.ServeHTTP(setupPage, httptest.NewRequest(http.MethodGet, "/admin/setup", nil))
	if setupPage.Code != http.StatusOK {
		t.Fatalf("setup GET status = %d, want %d", setupPage.Code, http.StatusOK)
	}
	csrfCookie := cookieNamed(t, setupPage, csrfCookieName)
	csrfToken := csrfFromBody(t, setupPage.Body.String())
	if !csrfCookie.HttpOnly || csrfCookie.SameSite != http.SameSiteStrictMode {
		t.Errorf("CSRF cookie flags = %+v, want HttpOnly and SameSite=Strict", csrfCookie)
	}

	setupResponse := postForm(handler, "/admin/setup", url.Values{
		"csrf_token":            {csrfToken},
		"username":              {"site_admin"},
		"password":              {"correct-horse-battery"},
		"password_confirmation": {"correct-horse-battery"},
	}, csrfCookie)
	if setupResponse.Code != http.StatusSeeOther || setupResponse.Header().Get("Location") != "/admin/" {
		t.Fatalf("setup POST = %d to %q, want redirect to /admin/", setupResponse.Code, setupResponse.Header().Get("Location"))
	}
	sessionCookie := cookieNamed(t, setupResponse, sessionCookieName)
	if !sessionCookie.HttpOnly || sessionCookie.SameSite != http.SameSiteStrictMode {
		t.Errorf("session cookie flags = %+v, want HttpOnly and SameSite=Strict", sessionCookie)
	}

	dashboard := httptest.NewRecorder()
	dashboardRequest := httptest.NewRequest(http.MethodGet, "/admin/", nil)
	dashboardRequest.AddCookie(sessionCookie)
	handler.ServeHTTP(dashboard, dashboardRequest)
	if dashboard.Code != http.StatusOK || !strings.Contains(dashboard.Body.String(), "Bem-vindo, site_admin") {
		t.Fatalf("dashboard response = %d, %q", dashboard.Code, dashboard.Body.String())
	}
	logoutCSRF := cookieNamed(t, dashboard, csrfCookieName)
	logoutToken := csrfFromBody(t, dashboard.Body.String())

	logout := postForm(handler, "/admin/logout", url.Values{"csrf_token": {logoutToken}}, sessionCookie, logoutCSRF)
	if logout.Code != http.StatusSeeOther || logout.Header().Get("Location") != "/admin/login" {
		t.Fatalf("logout POST = %d to %q, want redirect to /admin/login", logout.Code, logout.Header().Get("Location"))
	}

	protected := httptest.NewRecorder()
	protectedRequest := httptest.NewRequest(http.MethodGet, "/admin/", nil)
	protectedRequest.AddCookie(sessionCookie)
	handler.ServeHTTP(protected, protectedRequest)
	if protected.Code != http.StatusSeeOther || protected.Header().Get("Location") != "/admin/login" {
		t.Fatalf("protected page after logout = %d to %q, want redirect to login", protected.Code, protected.Header().Get("Location"))
	}

	loginPage := httptest.NewRecorder()
	handler.ServeHTTP(loginPage, httptest.NewRequest(http.MethodGet, "/admin/login", nil))
	loginCSRF := cookieNamed(t, loginPage, csrfCookieName)
	loginToken := csrfFromBody(t, loginPage.Body.String())
	invalidLogin := postForm(handler, "/admin/login", url.Values{
		"csrf_token": {loginToken},
		"username":   {"site_admin"},
		"password":   {"incorrect-password"},
	}, loginCSRF)
	if invalidLogin.Code != http.StatusUnauthorized || !strings.Contains(invalidLogin.Body.String(), "Usuário ou senha inválidos.") {
		t.Fatalf("invalid login response = %d, %q", invalidLogin.Code, invalidLogin.Body.String())
	}
	loginResponse := postForm(handler, "/admin/login", url.Values{
		"csrf_token": {loginToken},
		"username":   {"site_admin"},
		"password":   {"correct-horse-battery"},
	}, loginCSRF)
	if loginResponse.Code != http.StatusSeeOther || loginResponse.Header().Get("Location") != "/admin/" {
		t.Fatalf("login POST = %d to %q, want redirect to /admin/", loginResponse.Code, loginResponse.Header().Get("Location"))
	}
}

func TestSetupRequiresCSRFAndCannotBeReopened(t *testing.T) {
	db := newTestDB(t)
	handler, err := NewHandler(db)
	if err != nil {
		t.Fatal(err)
	}
	missingCSRF := httptest.NewRecorder()
	handler.ServeHTTP(missingCSRF, httptest.NewRequest(http.MethodPost, "/admin/setup", strings.NewReader("username=attacker")))
	if missingCSRF.Code != http.StatusForbidden {
		t.Fatalf("setup without CSRF status = %d, want %d", missingCSRF.Code, http.StatusForbidden)
	}

	setupPage := httptest.NewRecorder()
	handler.ServeHTTP(setupPage, httptest.NewRequest(http.MethodGet, "/admin/setup", nil))
	csrfCookie := cookieNamed(t, setupPage, csrfCookieName)
	setup := postForm(handler, "/admin/setup", url.Values{
		"csrf_token":            {csrfFromBody(t, setupPage.Body.String())},
		"username":              {"first_admin"},
		"password":              {"correct-horse-battery"},
		"password_confirmation": {"correct-horse-battery"},
	}, csrfCookie)
	if setup.Code != http.StatusSeeOther {
		t.Fatalf("first setup status = %d, want redirect", setup.Code)
	}

	reopened := httptest.NewRecorder()
	handler.ServeHTTP(reopened, httptest.NewRequest(http.MethodGet, "/admin/setup", nil))
	if reopened.Code != http.StatusSeeOther || reopened.Header().Get("Location") != "/admin/login" {
		t.Fatalf("setup after initialization = %d to %q, want redirect to login", reopened.Code, reopened.Header().Get("Location"))
	}
}

func TestAdminContentCRUDAndCleanPublicRendering(t *testing.T) {
	db := newTestDB(t)
	user, err := auth.CreateInitialAdmin(context.Background(), db, "content_admin", "a-very-secure-password")
	if err != nil {
		t.Fatal(err)
	}
	session, expiresAt, err := auth.CreateSession(context.Background(), db, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	sessionCookie := &http.Cookie{Name: sessionCookieName, Value: session, Path: "/", Expires: expiresAt}
	handler, err := NewHandler(db)
	if err != nil {
		t.Fatal(err)
	}

	newPost := getWithCookies(handler, "/admin/posts/new", sessionCookie)
	if newPost.Code != http.StatusOK {
		t.Fatalf("new post editor status = %d, want %d", newPost.Code, http.StatusOK)
	}
	csrfCookie := cookieNamed(t, newPost, csrfCookieName)
	if strings.Count(newPost.Body.String(), `href="/static/style.css"`) != 1 {
		t.Errorf("admin editor should load one CSS file")
	}
	if !strings.Contains(newPost.Body.String(), "/static/htmx.min.js") {
		t.Errorf("admin editor does not load locally bundled HTMX")
	}

	created := postForm(handler, "/admin/posts/save", url.Values{
		"csrf_token":   {csrfFromBody(t, newPost.Body.String())},
		"title":        {"Post seguro"},
		"content":      {"# Olá\n\n<script>alert(1)</script>"},
		"status":       {"published"},
		"new_category": {"Novidades"},
		"tags":         {"go, cms"},
	}, sessionCookie, csrfCookie)
	if created.Code != http.StatusSeeOther || created.Header().Get("Location") != "/admin/posts?saved=1" {
		t.Fatalf("create post = %d to %q, want redirect to list", created.Code, created.Header().Get("Location"))
	}

	postList := getWithCookies(handler, "/admin/posts", sessionCookie)
	if postList.Code != http.StatusOK || !strings.Contains(postList.Body.String(), "Post seguro") ||
		!strings.Contains(postList.Body.String(), "Publicado") {
		t.Fatalf("post listing response = %d, %q", postList.Code, postList.Body.String())
	}
	csrfCookie = cookieNamed(t, postList, csrfCookieName)
	if strings.Count(postList.Body.String(), `href="/static/style.css"`) != 1 {
		t.Errorf("admin list should load one CSS file")
	}
	draftResponse := postHTMXForm(handler, "/admin/content/1/status", url.Values{
		"csrf_token":   {csrfFromBody(t, postList.Body.String())},
		"content_type": {"post"},
		"status":       {"draft"},
	}, sessionCookie, csrfCookie)
	if draftResponse.Code != http.StatusOK || !strings.Contains(draftResponse.Body.String(), "Rascunho") {
		t.Fatalf("HTMX status update = %d, %s", draftResponse.Code, draftResponse.Body.String())
	}
	csrfCookie = cookieNamed(t, draftResponse, csrfCookieName)
	publishedResponse := postHTMXForm(handler, "/admin/content/1/status", url.Values{
		"csrf_token":   {csrfFromBody(t, draftResponse.Body.String())},
		"content_type": {"post"},
		"status":       {"published"},
	}, sessionCookie, csrfCookie)
	if publishedResponse.Code != http.StatusOK || !strings.Contains(publishedResponse.Body.String(), "Publicado") {
		t.Fatalf("HTMX publish update = %d, %s", publishedResponse.Code, publishedResponse.Body.String())
	}
	csrfCookie = cookieNamed(t, publishedResponse, csrfCookieName)
	preview := postHTMXForm(handler, "/admin/preview", url.Values{
		"csrf_token": {csrfFromBody(t, publishedResponse.Body.String())},
		"content":    {"## Prévia\n\n<script>alert(1)</script>"},
	}, sessionCookie, csrfCookie)
	if preview.Code != http.StatusOK || !strings.Contains(preview.Body.String(), "<h2>Prévia</h2>") ||
		strings.Contains(preview.Body.String(), "<script") {
		t.Fatalf("markdown preview = %d, %s", preview.Code, preview.Body.String())
	}

	publicHome := httptest.NewRecorder()
	handler.ServeHTTP(publicHome, httptest.NewRequest(http.MethodGet, "/", nil))
	if publicHome.Code != http.StatusOK || !strings.Contains(publicHome.Body.String(), "Post seguro") {
		t.Fatalf("public home = %d, %q", publicHome.Code, publicHome.Body.String())
	}
	if strings.Contains(publicHome.Body.String(), "/static/style.css") || strings.Contains(publicHome.Body.String(), "<script") {
		t.Errorf("public page should be unstyled by core and sanitized: %s", publicHome.Body.String())
	}
	publicPost := httptest.NewRecorder()
	handler.ServeHTTP(publicPost, httptest.NewRequest(http.MethodGet, "/post-seguro", nil))
	if publicPost.Code != http.StatusOK || !strings.Contains(publicPost.Body.String(), "<h1>Olá</h1>") ||
		strings.Contains(publicPost.Body.String(), "<script") {
		t.Fatalf("public post not safely rendered: %d, %s", publicPost.Code, publicPost.Body.String())
	}
	if strings.Contains(publicPost.Body.String(), "/static/style.css") {
		t.Errorf("public post should not receive core styles")
	}

	editPage := getWithCookies(handler, "/admin/pages/new", sessionCookie)
	pageCSRF := cookieNamed(t, editPage, csrfCookieName)
	createdPage := postForm(handler, "/admin/pages/save", url.Values{
		"csrf_token": {csrfFromBody(t, editPage.Body.String())},
		"title":      {"Sobre nós"},
		"content":    {"Página institucional"},
		"status":     {"published"},
	}, sessionCookie, pageCSRF)
	if createdPage.Code != http.StatusSeeOther || createdPage.Header().Get("Location") != "/admin/pages?saved=1" {
		t.Fatalf("create page = %d to %q, want redirect to pages", createdPage.Code, createdPage.Header().Get("Location"))
	}
	publicPage := httptest.NewRecorder()
	handler.ServeHTTP(publicPage, httptest.NewRequest(http.MethodGet, "/sobre-nos", nil))
	if publicPage.Code != http.StatusOK || !strings.Contains(publicPage.Body.String(), "Página institucional") {
		t.Fatalf("public page = %d, %s", publicPage.Code, publicPage.Body.String())
	}

	csrfToken := csrfFromBody(t, publishedResponse.Body.String())
	trash := postForm(handler, "/admin/content/1/trash", url.Values{
		"csrf_token":   {csrfToken},
		"content_type": {"post"},
	}, sessionCookie, csrfCookie)
	if trash.Code != http.StatusSeeOther || trash.Header().Get("Location") != "/admin/posts" {
		t.Fatalf("trash post = %d to %q, want redirect to posts", trash.Code, trash.Header().Get("Location"))
	}
	hiddenPost := httptest.NewRecorder()
	handler.ServeHTTP(hiddenPost, httptest.NewRequest(http.MethodGet, "/post-seguro", nil))
	if hiddenPost.Code != http.StatusNotFound {
		t.Errorf("trashed post public status = %d, want %d", hiddenPost.Code, http.StatusNotFound)
	}
}

func TestAdminContentRequiresSessionAndCSRF(t *testing.T) {
	db := newTestDB(t)
	handler, err := NewHandler(db)
	if err != nil {
		t.Fatal(err)
	}
	redirect := httptest.NewRecorder()
	handler.ServeHTTP(redirect, httptest.NewRequest(http.MethodGet, "/admin/posts", nil))
	if redirect.Code != http.StatusSeeOther || redirect.Header().Get("Location") != "/admin/login" {
		t.Fatalf("anonymous content list = %d to %q, want login redirect", redirect.Code, redirect.Header().Get("Location"))
	}
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/admin/posts/save", strings.NewReader("title=attacker&status=published"))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusSeeOther {
		t.Errorf("anonymous content submission = %d, want login redirect", response.Code)
	}
}

func TestBoostedContentNavigationKeepsAdminLayout(t *testing.T) {
	db := newTestDB(t)
	user, err := auth.CreateInitialAdmin(context.Background(), db, "layout_admin", "a-very-secure-password")
	if err != nil {
		t.Fatal(err)
	}
	session, expiresAt, err := auth.CreateSession(context.Background(), db, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewHandler(db)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/admin/posts", nil)
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: session, Expires: expiresAt})
	request.Header.Set("HX-Request", "true")
	request.Header.Set("HX-Boosted", "true")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("boosted post list status = %d, want %d", response.Code, http.StatusOK)
	}
	for _, expected := range []string{
		`class="admin-sidebar"`,
		`class="admin-topbar"`,
		`id="admin-content"`,
		`Nenhum conteúdo por aqui.`,
	} {
		if !strings.Contains(response.Body.String(), expected) {
			t.Errorf("boosted post list is missing %q", expected)
		}
	}
}

func TestPublicSSRAndHeadlessAPI(t *testing.T) {
	db := newTestDB(t)
	user, err := auth.CreateInitialAdmin(context.Background(), db, "api_admin", "a-very-secure-password")
	if err != nil {
		t.Fatal(err)
	}
	for index := 1; index <= 12; index++ {
		_, err := content.Save(context.Background(), db, content.Input{
			Type:    content.TypePost,
			Title:   "Public post " + strconv.Itoa(index),
			Content: "Public content " + strconv.Itoa(index),
			Status:  content.StatusPublished,
		}, user.ID)
		if err != nil {
			t.Fatal(err)
		}
	}
	latest, err := content.Save(context.Background(), db, content.Input{
		Type: content.TypePost, Title: "Latest post", Content: "Latest body", Status: content.StatusPublished,
	}, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	draft, err := content.Save(context.Background(), db, content.Input{
		Type: content.TypePost, Title: "Private draft", Status: content.StatusDraft,
	}, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	page, err := content.Save(context.Background(), db, content.Input{
		Type: content.TypePage, Title: "About", Content: "About content", Status: content.StatusPublished,
	}, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewHandler(db)
	if err != nil {
		t.Fatal(err)
	}

	listRequest := httptest.NewRequest(http.MethodGet, "/api/v1/posts?page=1&limit=10", nil)
	listRequest.Header.Set("Origin", "https://headless.example")
	listResponse := httptest.NewRecorder()
	handler.ServeHTTP(listResponse, listRequest)
	if listResponse.Code != http.StatusOK || listResponse.Header().Get("Content-Type") != "application/json; charset=utf-8" {
		t.Fatalf("API list response = %d %q", listResponse.Code, listResponse.Header().Get("Content-Type"))
	}
	if listResponse.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Errorf("CORS allow-origin = %q, want wildcard for public API", listResponse.Header().Get("Access-Control-Allow-Origin"))
	}
	var list apiPostList
	if err := json.Unmarshal(listResponse.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Posts) != 10 || list.Page != 1 || list.Limit != 10 || list.Total != 13 || list.TotalPages != 2 {
		t.Fatalf("API pagination = %+v, want 10/13 posts over 2 pages", list)
	}
	if list.Posts[0].ID != latest.ID {
		t.Errorf("newest post first ID = %d, want %d", list.Posts[0].ID, latest.ID)
	}
	if list.Posts[0].Content != "Latest body" || list.Posts[0].Status != content.StatusPublished || list.Posts[0].PublishedAt == "" {
		t.Errorf("API post fields missing: %+v", list.Posts[0])
	}

	pageRequest := httptest.NewRequest(http.MethodGet, "/api/v1/posts?page=2&limit=10", nil)
	pageResponse := httptest.NewRecorder()
	handler.ServeHTTP(pageResponse, pageRequest)
	if pageResponse.Code != http.StatusOK {
		t.Fatalf("second page status = %d, body %s", pageResponse.Code, pageResponse.Body.String())
	}
	var secondPage apiPostList
	if err := json.Unmarshal(pageResponse.Body.Bytes(), &secondPage); err != nil {
		t.Fatal(err)
	}
	if len(secondPage.Posts) != 3 || secondPage.Page != 2 {
		t.Errorf("second API page = %+v, want 3 records on page 2", secondPage)
	}

	detailRequest := httptest.NewRequest(http.MethodGet, "/api/v1/posts/"+latest.Slug, nil)
	detailResponse := httptest.NewRecorder()
	handler.ServeHTTP(detailResponse, detailRequest)
	var detail apiContent
	if err := json.Unmarshal(detailResponse.Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	if detailResponse.Code != http.StatusOK || detail.ID != latest.ID || detail.Content != "Latest body" {
		t.Errorf("post detail = %d %+v", detailResponse.Code, detail)
	}
	pageDetail := httptest.NewRecorder()
	handler.ServeHTTP(pageDetail, httptest.NewRequest(http.MethodGet, "/api/v1/pages/"+page.Slug, nil))
	if pageDetail.Code != http.StatusOK || !strings.Contains(pageDetail.Body.String(), "About content") {
		t.Errorf("page API detail = %d %s", pageDetail.Code, pageDetail.Body.String())
	}
	draftDetail := httptest.NewRecorder()
	handler.ServeHTTP(draftDetail, httptest.NewRequest(http.MethodGet, "/api/v1/posts/"+draft.Slug, nil))
	if draftDetail.Code != http.StatusNotFound {
		t.Errorf("draft API detail status = %d, want 404", draftDetail.Code)
	}
	wrongType := httptest.NewRecorder()
	handler.ServeHTTP(wrongType, httptest.NewRequest(http.MethodGet, "/api/v1/posts/"+page.Slug, nil))
	if wrongType.Code != http.StatusNotFound {
		t.Errorf("page through posts API status = %d, want 404", wrongType.Code)
	}
	invalidPage := httptest.NewRecorder()
	handler.ServeHTTP(invalidPage, httptest.NewRequest(http.MethodGet, "/api/v1/posts?page=0", nil))
	if invalidPage.Code != http.StatusBadRequest || !strings.Contains(invalidPage.Body.String(), "invalid_pagination") {
		t.Errorf("invalid pagination response = %d %s", invalidPage.Code, invalidPage.Body.String())
	}

	preflight := httptest.NewRecorder()
	preflightRequest := httptest.NewRequest(http.MethodOptions, "/api/v1/posts", nil)
	preflightRequest.Header.Set("Origin", "https://headless.example")
	preflightRequest.Header.Set("Access-Control-Request-Method", "GET")
	handler.ServeHTTP(preflight, preflightRequest)
	if preflight.Code != http.StatusNoContent || preflight.Header().Get("Access-Control-Allow-Methods") != "GET, OPTIONS" {
		t.Errorf("CORS preflight = %d %+v", preflight.Code, preflight.Header())
	}

	publicPage := httptest.NewRecorder()
	handler.ServeHTTP(publicPage, httptest.NewRequest(http.MethodGet, "/"+page.Slug, nil))
	if publicPage.Code != http.StatusOK || !strings.Contains(publicPage.Body.String(), "About content") {
		t.Errorf("public page SSR = %d %s", publicPage.Code, publicPage.Body.String())
	}
	publicHome := httptest.NewRecorder()
	handler.ServeHTTP(publicHome, httptest.NewRequest(http.MethodGet, "/", nil))
	latestPosition := strings.Index(publicHome.Body.String(), "Latest post")
	olderPosition := strings.Index(publicHome.Body.String(), "Public post 12")
	if publicHome.Code != http.StatusOK || latestPosition < 0 || olderPosition < 0 || latestPosition > olderPosition {
		t.Errorf("home posts are not reverse chronological: %d %s", publicHome.Code, publicHome.Body.String())
	}
	if body := publicHome.Body.String(); !strings.Contains(body, "/themes/default/style.css") ||
		!strings.Contains(body, "Por api_admin") || !strings.Contains(body, `datetime="`) ||
		!strings.Contains(body, "Latest body") || strings.Contains(body, "https://fonts.googleapis.com") {
		t.Errorf("default homepage is missing official theme styling or post metadata: %s", body)
	}
	publicPost := httptest.NewRecorder()
	handler.ServeHTTP(publicPost, httptest.NewRequest(http.MethodGet, "/"+latest.Slug, nil))
	if publicPost.Code != http.StatusOK || !strings.Contains(publicPost.Body.String(), "Por api_admin") ||
		!strings.Contains(publicPost.Body.String(), "/themes/default/style.css") {
		t.Errorf("default single post is missing author or theme styling: %d %s", publicPost.Code, publicPost.Body.String())
	}
	missing := httptest.NewRecorder()
	handler.ServeHTTP(missing, httptest.NewRequest(http.MethodGet, "/does-not-exist", nil))
	if missing.Code != http.StatusNotFound || !strings.Contains(missing.Body.String(), "404") ||
		!strings.Contains(missing.Header().Get("Content-Type"), "text/html") ||
		strings.Contains(missing.Body.String(), "/static/style.css") {
		t.Errorf("clean SSR 404 = %d %q %q", missing.Code, missing.Header().Get("Content-Type"), missing.Body.String())
	}
	missingDraft := httptest.NewRecorder()
	handler.ServeHTTP(missingDraft, httptest.NewRequest(http.MethodGet, "/"+draft.Slug, nil))
	if missingDraft.Code != http.StatusNotFound || !strings.Contains(missingDraft.Body.String(), "404") {
		t.Errorf("draft SSR response = %d %s", missingDraft.Code, missingDraft.Body.String())
	}
	methodNotAllowed := httptest.NewRecorder()
	handler.ServeHTTP(methodNotAllowed, httptest.NewRequest(http.MethodPost, "/"+latest.Slug, nil))
	if methodNotAllowed.Code != http.StatusMethodNotAllowed {
		t.Errorf("public non-GET method status = %d, want %d", methodNotAllowed.Code, http.StatusMethodNotAllowed)
	}
}

func TestMediaUploadGalleryPublicServingAndDelete(t *testing.T) {
	db := newTestDB(t)
	user, err := auth.CreateInitialAdmin(context.Background(), db, "media_admin", "a-very-secure-password")
	if err != nil {
		t.Fatal(err)
	}
	session, expiresAt, err := auth.CreateSession(context.Background(), db, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewHandler(db, Options{UploadDir: t.TempDir(), MaxUploadSize: 1024})
	if err != nil {
		t.Fatal(err)
	}
	sessionCookie := &http.Cookie{Name: sessionCookieName, Value: session, Expires: expiresAt}
	library := getWithCookies(handler, "/admin/media", sessionCookie)
	if library.Code != http.StatusOK || !strings.Contains(library.Body.String(), "Biblioteca") ||
		!strings.Contains(library.Body.String(), "media-file") || !strings.Contains(library.Body.String(), "Enviar arquivo") {
		t.Fatalf("media library response = %d, %s", library.Code, library.Body.String())
	}
	csrfCookie := cookieNamed(t, library, csrfCookieName)
	csrfToken := csrfFromBody(t, library.Body.String())
	imageBytes := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}
	upload := postMultipartForm(t, handler, "/admin/media/upload", csrfToken, "cover.png", imageBytes, "Cover image", sessionCookie, csrfCookie)
	if upload.Code != http.StatusOK || !strings.Contains(upload.Body.String(), "Arquivo enviado com sucesso.") ||
		!strings.Contains(upload.Body.String(), "Copiar URL") {
		t.Fatalf("media upload response = %d, %s", upload.Code, upload.Body.String())
	}
	urlMatch := regexp.MustCompile(`/uploads/[0-9]{4}/[0-9]{2}/[a-f0-9]{32}\.png`).FindString(upload.Body.String())
	if urlMatch == "" {
		t.Fatalf("uploaded media URL not found in gallery: %s", upload.Body.String())
	}
	if strings.Contains(upload.Body.String(), "alt=\"Cover image\"") == false {
		t.Errorf("uploaded image alt text missing from gallery")
	}

	publicFile := httptest.NewRecorder()
	handler.ServeHTTP(publicFile, httptest.NewRequest(http.MethodGet, urlMatch, nil))
	if publicFile.Code != http.StatusOK || publicFile.Header().Get("Content-Type") != "image/png" ||
		!bytes.Equal(publicFile.Body.Bytes(), imageBytes) {
		t.Fatalf("public upload response = %d, headers=%v, body=%v", publicFile.Code, publicFile.Header(), publicFile.Body.Bytes())
	}
	if publicFile.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("public upload missing nosniff header")
	}

	updatedCSRF := cookieNamed(t, upload, csrfCookieName)
	deleteIDMatch := regexp.MustCompile(`/admin/media/([0-9]+)/delete`).FindStringSubmatch(upload.Body.String())
	if len(deleteIDMatch) != 2 {
		t.Fatalf("media delete route missing from gallery: %s", upload.Body.String())
	}
	deleted := postHTMXForm(handler, "/admin/media/"+deleteIDMatch[1]+"/delete", url.Values{
		"csrf_token": {csrfFromBody(t, upload.Body.String())},
	}, sessionCookie, updatedCSRF)
	if deleted.Code != http.StatusOK || !strings.Contains(deleted.Body.String(), "Nenhuma mídia enviada") {
		t.Fatalf("delete media response = %d, %s", deleted.Code, deleted.Body.String())
	}
	deletedFile := httptest.NewRecorder()
	handler.ServeHTTP(deletedFile, httptest.NewRequest(http.MethodGet, urlMatch, nil))
	if deletedFile.Code != http.StatusNotFound {
		t.Errorf("deleted upload public status = %d, want 404", deletedFile.Code)
	}

	freshLibrary := getWithCookies(handler, "/admin/media", sessionCookie)
	freshCSRF := cookieNamed(t, freshLibrary, csrfCookieName)
	svg := []byte(`<svg xmlns="http://www.w3.org/2000/svg"><path d="M0 0"/></svg>`)
	svgUpload := postMultipartForm(t, handler, "/admin/media/upload", csrfFromBody(t, freshLibrary.Body.String()),
		"icon.svg", svg, "", sessionCookie, freshCSRF)
	if svgUpload.Code != http.StatusOK {
		t.Fatalf("SVG upload response = %d, %s", svgUpload.Code, svgUpload.Body.String())
	}
	svgURL := regexp.MustCompile(`/uploads/[0-9]{4}/[0-9]{2}/[a-f0-9]{32}\.svg`).FindString(svgUpload.Body.String())
	if svgURL == "" {
		t.Fatalf("uploaded SVG URL not found: %s", svgUpload.Body.String())
	}
	svgResponse := httptest.NewRecorder()
	handler.ServeHTTP(svgResponse, httptest.NewRequest(http.MethodGet, svgURL, nil))
	if svgResponse.Code != http.StatusOK || svgResponse.Header().Get("Content-Type") != "image/svg+xml" ||
		!strings.Contains(svgResponse.Header().Get("Content-Security-Policy"), "sandbox") {
		t.Errorf("SVG response headers/status = %d %+v", svgResponse.Code, svgResponse.Header())
	}
}

func TestMediaUploadRejectsMissingCSRFAndOversize(t *testing.T) {
	db := newTestDB(t)
	user, err := auth.CreateInitialAdmin(context.Background(), db, "upload_admin", "a-very-secure-password")
	if err != nil {
		t.Fatal(err)
	}
	session, expiresAt, err := auth.CreateSession(context.Background(), db, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewHandler(db, Options{UploadDir: t.TempDir(), MaxUploadSize: 8})
	if err != nil {
		t.Fatal(err)
	}
	sessionCookie := &http.Cookie{Name: sessionCookieName, Value: session, Expires: expiresAt}
	missingCSRF := postMultipartForm(t, handler, "/admin/media/upload", "", "image.png", []byte("12345678"), "", sessionCookie)
	if missingCSRF.Code != http.StatusForbidden {
		t.Errorf("upload without CSRF status = %d, want %d", missingCSRF.Code, http.StatusForbidden)
	}
	library := getWithCookies(handler, "/admin/media", sessionCookie)
	csrfCookie := cookieNamed(t, library, csrfCookieName)
	csrfToken := csrfFromBody(t, library.Body.String())
	tooLarge := postMultipartForm(t, handler, "/admin/media/upload", csrfToken, "image.png", bytes.Repeat([]byte{0}, 9), "", sessionCookie, csrfCookie)
	if tooLarge.Code != http.StatusRequestEntityTooLarge || !strings.Contains(tooLarge.Body.String(), "limite de tamanho") {
		t.Errorf("oversize upload response = %d, %s", tooLarge.Code, tooLarge.Body.String())
	}
}

func TestThemeAppearanceActivationRenderingAndAssetIsolation(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	user, err := auth.CreateInitialAdmin(ctx, db, "theme_admin", "a-very-secure-password")
	if err != nil {
		t.Fatal(err)
	}
	session, expiresAt, err := auth.CreateSession(ctx, db, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	customThemeDir := filepath.Join(root, "custom")
	if err := os.MkdirAll(customThemeDir, 0700); err != nil {
		t.Fatal(err)
	}
	themeFiles := map[string]string{
		"theme.json":  `{"name":"Custom Theme","author":"Test Author","version":"2.1"}`,
		"index.html":  `<h1>custom-home {{.Theme}}</h1>{{range .Posts}}<a href="/{{.Slug}}">{{.Title}}</a>{{end}}<link rel="stylesheet" href="/themes/{{.Theme}}/style.css">`,
		"single.html": `<h1>custom-single {{.Item.Title}}</h1>`,
		"page.html":   `<h1>custom-page {{.Item.Title}}</h1>`,
		"404.html":    `<h1>custom-404</h1>`,
		"style.css":   `body { color: red; }`,
	}
	for name, body := range themeFiles {
		if err := os.WriteFile(filepath.Join(customThemeDir, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	handler, err := NewHandler(db, Options{UploadDir: t.TempDir(), ThemeDir: root})
	if err != nil {
		t.Fatal(err)
	}
	sessionCookie := &http.Cookie{Name: sessionCookieName, Value: session, Expires: expiresAt}
	appearance := getWithCookies(handler, "/admin/appearance", sessionCookie)
	if appearance.Code != http.StatusOK || !strings.Contains(appearance.Body.String(), "Custom Theme") ||
		!strings.Contains(appearance.Body.String(), "Test Author") || !strings.Contains(appearance.Body.String(), "2.1") {
		t.Fatalf("appearance page = %d, %s", appearance.Code, appearance.Body.String())
	}
	csrfCookie := cookieNamed(t, appearance, csrfCookieName)
	activation := postForm(handler, "/admin/appearance/custom/activate", url.Values{
		"csrf_token": {csrfFromBody(t, appearance.Body.String())},
	}, sessionCookie, csrfCookie)
	if activation.Code != http.StatusSeeOther || activation.Header().Get("Location") != "/admin/appearance?activated=1" {
		t.Fatalf("theme activation = %d to %q, %s", activation.Code, activation.Header().Get("Location"), activation.Body.String())
	}
	var active string
	if err := db.QueryRowContext(ctx, "SELECT value FROM options WHERE key = 'active_theme'").Scan(&active); err != nil {
		t.Fatal(err)
	}
	if active != "custom" {
		t.Fatalf("active theme option = %q, want custom", active)
	}

	post, err := content.Save(ctx, db, content.Input{
		Type: content.TypePost, Title: "Theme post", Content: "post body", Status: content.StatusPublished,
	}, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	page, err := content.Save(ctx, db, content.Input{
		Type: content.TypePage, Title: "Theme page", Content: "page body", Status: content.StatusPublished,
	}, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	home := httptest.NewRecorder()
	handler.ServeHTTP(home, httptest.NewRequest(http.MethodGet, "/", nil))
	if home.Code != http.StatusOK || !strings.Contains(home.Body.String(), "custom-home custom") ||
		!strings.Contains(home.Body.String(), "/themes/custom/style.css") || !strings.Contains(home.Body.String(), post.Title) {
		t.Fatalf("custom homepage = %d %s", home.Code, home.Body.String())
	}
	single := httptest.NewRecorder()
	handler.ServeHTTP(single, httptest.NewRequest(http.MethodGet, "/"+post.Slug, nil))
	if single.Code != http.StatusOK || !strings.Contains(single.Body.String(), "custom-single Theme post") {
		t.Errorf("custom single page = %d %s", single.Code, single.Body.String())
	}
	publicPage := httptest.NewRecorder()
	handler.ServeHTTP(publicPage, httptest.NewRequest(http.MethodGet, "/"+page.Slug, nil))
	if publicPage.Code != http.StatusOK || !strings.Contains(publicPage.Body.String(), "custom-page Theme page") {
		t.Errorf("custom static page = %d %s", publicPage.Code, publicPage.Body.String())
	}
	notFound := httptest.NewRecorder()
	handler.ServeHTTP(notFound, httptest.NewRequest(http.MethodGet, "/missing", nil))
	if notFound.Code != http.StatusNotFound || !strings.Contains(notFound.Body.String(), "custom-404") {
		t.Errorf("custom 404 = %d %s", notFound.Code, notFound.Body.String())
	}
	asset := httptest.NewRecorder()
	handler.ServeHTTP(asset, httptest.NewRequest(http.MethodGet, "/themes/custom/style.css", nil))
	if asset.Code != http.StatusOK || !strings.Contains(asset.Body.String(), "color: red") {
		t.Errorf("active theme asset = %d %s", asset.Code, asset.Body.String())
	}
	themeMetadata := httptest.NewRecorder()
	handler.ServeHTTP(themeMetadata, httptest.NewRequest(http.MethodGet, "/themes/custom/theme.json", nil))
	if themeMetadata.Code != http.StatusNotFound {
		t.Errorf("theme metadata asset status = %d, want 404", themeMetadata.Code)
	}
	inactiveAsset := httptest.NewRecorder()
	handler.ServeHTTP(inactiveAsset, httptest.NewRequest(http.MethodGet, "/themes/default/style.css", nil))
	if inactiveAsset.Code != http.StatusNotFound {
		t.Errorf("inactive theme asset status = %d, want 404", inactiveAsset.Code)
	}
	traversal := httptest.NewRecorder()
	handler.ServeHTTP(traversal, httptest.NewRequest(http.MethodGet, "/themes/custom/%2e%2e/README.md", nil))
	if traversal.Code == http.StatusOK {
		t.Errorf("theme asset traversal returned content: %s", traversal.Body.String())
	}

	if _, err := db.ExecContext(ctx, "UPDATE options SET value = 'missing-theme' WHERE key = 'active_theme'"); err != nil {
		t.Fatal(err)
	}
	fallback := httptest.NewRecorder()
	handler.ServeHTTP(fallback, httptest.NewRequest(http.MethodGet, "/", nil))
	if fallback.Code != http.StatusOK || !strings.Contains(fallback.Body.String(), "/themes/default/style.css") {
		t.Errorf("missing active theme fallback = %d %s", fallback.Code, fallback.Body.String())
	}
}

func TestPluginAdministrationActivationAndContentFilter(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	user, err := auth.CreateInitialAdmin(ctx, db, "plugin_admin", "a-very-secure-password")
	if err != nil {
		t.Fatal(err)
	}
	session, expiresAt, err := auth.CreateSession(ctx, db, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	pluginDir := filepath.Join(root, "render-marker")
	if err := os.MkdirAll(pluginDir, 0700); err != nil {
		t.Fatal(err)
	}
	manifest := `{"id":"render-marker","name":"Render marker","version":"1.0","description":"Adds markers to rendered content","author":"Test","main_file":"main.lua","hooks":["post:before_save","post:after_save","user:login","content:render","head:inject_tags"]}`
	source := `return { hooks = {
  ["post:before_save"] = function(value) error("expected before-save failure") end,
  ["post:after_save"] = function(value) error("expected after-save failure") end,
  ["user:login"] = function(value) error("expected login failure") end,
  ["content:render"] = function(value) return value .. "\n\nPlugin marker" end,
  ["head:inject_tags"] = function(value) return value .. '<meta name="description" content="safe"><script>alert(1)</script>' end
} }`
	if err := os.WriteFile(filepath.Join(pluginDir, "plugin.json"), []byte(manifest), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginDir, "main.lua"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	handler, err := NewHandler(db, Options{UploadDir: t.TempDir(), PluginDir: root})
	if err != nil {
		t.Fatal(err)
	}
	sessionCookie := &http.Cookie{Name: sessionCookieName, Value: session, Expires: expiresAt}
	pluginPage := getWithCookies(handler, "/admin/plugins", sessionCookie)
	if pluginPage.Code != http.StatusOK || !strings.Contains(pluginPage.Body.String(), "Render marker") ||
		!strings.Contains(pluginPage.Body.String(), "Inativo") {
		t.Fatalf("plugin administration page = %d, %s", pluginPage.Code, pluginPage.Body.String())
	}
	csrfCookie := cookieNamed(t, pluginPage, csrfCookieName)
	activated := postForm(handler, "/admin/plugins/render-marker/activate", url.Values{
		"csrf_token": {csrfFromBody(t, pluginPage.Body.String())},
	}, sessionCookie, csrfCookie)
	if activated.Code != http.StatusSeeOther || activated.Header().Get("Location") != "/admin/plugins?updated=activated" {
		t.Fatalf("plugin activation = %d to %q, %s", activated.Code, activated.Header().Get("Location"), activated.Body.String())
	}
	loginPage := getWithCookies(handler, "/admin/login")
	loginCSRF := cookieNamed(t, loginPage, csrfCookieName)
	login := postForm(handler, "/admin/login", url.Values{
		"csrf_token": {csrfFromBody(t, loginPage.Body.String())},
		"username":   {"plugin_admin"},
		"password":   {"a-very-secure-password"},
	}, loginCSRF)
	if login.Code != http.StatusSeeOther || login.Header().Get("Location") != "/admin/" {
		t.Fatalf("login with failing plugin action = %d to %q, %s", login.Code, login.Header().Get("Location"), login.Body.String())
	}
	edit := getWithCookies(handler, "/admin/posts/new", sessionCookie)
	if edit.Code != http.StatusOK {
		t.Fatalf("post editor = %d, %s", edit.Code, edit.Body.String())
	}
	editCSRF := cookieNamed(t, edit, csrfCookieName)
	saved := postForm(handler, "/admin/posts/save", url.Values{
		"csrf_token": {csrfFromBody(t, edit.Body.String())},
		"title":      {"Plugin test post"},
		"content":    {"Original body"},
		"status":     {"published"},
	}, sessionCookie, editCSRF)
	if saved.Code != http.StatusSeeOther {
		t.Fatalf("save post with active plugin = %d, %s", saved.Code, saved.Body.String())
	}
	home := httptest.NewRecorder()
	handler.ServeHTTP(home, httptest.NewRequest(http.MethodGet, "/", nil))
	if home.Code != http.StatusOK || !strings.Contains(home.Body.String(), "Plugin marker") ||
		!strings.Contains(home.Body.String(), `<meta name="description" content="safe">`) ||
		strings.Contains(home.Body.String(), "<script") {
		t.Errorf("active content filter result = %d %s", home.Code, home.Body.String())
	}
	pluginPage = getWithCookies(handler, "/admin/plugins", sessionCookie)
	csrfCookie = cookieNamed(t, pluginPage, csrfCookieName)
	deactivated := postForm(handler, "/admin/plugins/render-marker/deactivate", url.Values{
		"csrf_token": {csrfFromBody(t, pluginPage.Body.String())},
	}, sessionCookie, csrfCookie)
	if deactivated.Code != http.StatusSeeOther || deactivated.Header().Get("Location") != "/admin/plugins?updated=deactivated" {
		t.Fatalf("plugin deactivation = %d to %q, %s", deactivated.Code, deactivated.Header().Get("Location"), deactivated.Body.String())
	}
	home = httptest.NewRecorder()
	handler.ServeHTTP(home, httptest.NewRequest(http.MethodGet, "/", nil))
	if home.Code != http.StatusOK || strings.Contains(home.Body.String(), "Plugin marker") {
		t.Errorf("deactivated content filter result = %d %s", home.Code, home.Body.String())
	}
}

func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := database.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(context.Background(), db); err != nil {
		db.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close test database: %v", err)
		}
	})
	return db
}

func getWithCookies(handler http.Handler, path string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, path, nil)
	for _, cookie := range cookies {
		request.AddCookie(cookie)
	}
	handler.ServeHTTP(response, request)
	return response
}

func cookieNamed(t *testing.T, response *httptest.ResponseRecorder, name string) *http.Cookie {
	t.Helper()
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == name {
			return cookie
		}
	}
	t.Fatalf("response did not set cookie %q", name)
	return nil
}

var csrfPattern = regexp.MustCompile(`name="csrf_token" value="([^"]+)"`)

func csrfFromBody(t *testing.T, body string) string {
	t.Helper()
	match := csrfPattern.FindStringSubmatch(body)
	if len(match) != 2 {
		t.Fatalf("response body does not contain a CSRF form token: %s", body)
	}
	return match[1]
}

func postForm(handler http.Handler, path string, values url.Values, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(values.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for _, cookie := range cookies {
		request.AddCookie(cookie)
	}
	handler.ServeHTTP(response, request)
	return response
}

func postHTMXForm(handler http.Handler, path string, values url.Values, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(values.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("HX-Request", "true")
	for _, cookie := range cookies {
		request.AddCookie(cookie)
	}
	handler.ServeHTTP(response, request)
	return response
}

func postMultipartForm(t *testing.T, handler http.Handler, path, csrfToken, filename string, fileData []byte, altText string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if csrfToken != "" {
		if err := writer.WriteField("csrf_token", csrfToken); err != nil {
			t.Fatal(err)
		}
	}
	if altText != "" {
		if err := writer.WriteField("alt_text", altText); err != nil {
			t.Fatal(err)
		}
	}
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(fileData); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, path, &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.Header.Set("HX-Request", "true")
	for _, cookie := range cookies {
		request.AddCookie(cookie)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
