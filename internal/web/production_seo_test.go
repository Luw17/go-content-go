package web

import (
	"context"
	"encoding/xml"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Luw17/go-content-go/internal/auth"
	"github.com/Luw17/go-content-go/internal/content"
	"github.com/Luw17/go-content-go/internal/siteconfig"
)

func TestPublicSitemapAndRobots(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	admin, err := auth.CreateInitialAdmin(ctx, db, "seo_admin", "a-very-secure-password")
	if err != nil {
		t.Fatal(err)
	}
	settings := siteconfig.Defaults()
	settings.SiteURL = "https://blog.exemplo.com"
	if err := siteconfig.Save(ctx, db, settings); err != nil {
		t.Fatal(err)
	}
	if _, err := content.Save(ctx, db, content.Input{
		Type:    content.TypePost,
		Title:   "Post Publicado",
		Slug:    "post-publicado",
		Content: "Conteúdo indexável.",
		Status:  content.StatusPublished,
	}, admin.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := content.Save(ctx, db, content.Input{
		Type:    content.TypePage,
		Title:   "Sobre",
		Slug:    "sobre",
		Content: "Página sobre.",
		Status:  content.StatusPublished,
	}, admin.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := content.Save(ctx, db, content.Input{
		Type:    content.TypePost,
		Title:   "Post Oculto Noindex",
		Slug:    "post-noindex",
		Content: "Não indexar.",
		Status:  content.StatusPublished,
		Noindex: true,
	}, admin.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := content.Save(ctx, db, content.Input{
		Type:    content.TypePost,
		Title:   "Rascunho",
		Slug:    "rascunho",
		Content: "Ainda em rascunho.",
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

	// 1. Check /sitemap.xml
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/sitemap.xml", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /sitemap.xml status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/xml") {
		t.Fatalf("sitemap Content-Type = %q, want application/xml", ct)
	}
	if rec.Header().Get("ETag") == "" {
		t.Fatalf("expected /sitemap.xml to be cached with ETag")
	}
	var parsed sitemapURLSet
	if err := xml.Unmarshal(rec.Body.Bytes(), &parsed); err != nil {
		t.Fatalf("unmarshal sitemap XML: %v\n%s", err, rec.Body.String())
	}
	locs := make(map[string]string, len(parsed.URLs))
	for _, u := range parsed.URLs {
		locs[u.Loc] = u.LastMod
		if u.LastMod != "" {
			if _, err := time.Parse(time.RFC3339, u.LastMod); err != nil {
				t.Errorf("invalid RFC3339 lastmod %q for %s: %v", u.LastMod, u.Loc, err)
			}
		}
	}
	for _, wantLoc := range []string{
		"https://blog.exemplo.com/",
		"https://blog.exemplo.com/post-publicado",
		"https://blog.exemplo.com/sobre",
	} {
		if _, ok := locs[wantLoc]; !ok {
			t.Errorf("sitemap missing expected URL %q (got %v)", wantLoc, locs)
		}
	}
	for _, forbidden := range []string{
		"https://blog.exemplo.com/post-noindex",
		"https://blog.exemplo.com/rascunho",
	} {
		if _, ok := locs[forbidden]; ok {
			t.Errorf("sitemap unexpectedly included %q", forbidden)
		}
	}

	// 2. Check /robots.txt
	robotsRec := httptest.NewRecorder()
	h.ServeHTTP(robotsRec, httptest.NewRequest(http.MethodGet, "/robots.txt", nil))
	if robotsRec.Code != http.StatusOK {
		t.Fatalf("GET /robots.txt status = %d, want 200", robotsRec.Code)
	}
	robotsBody := robotsRec.Body.String()
	for _, want := range []string{
		"User-agent: *",
		"Allow: /",
		"Disallow: /admin/",
		"Disallow: /api/",
		"Sitemap: https://blog.exemplo.com/sitemap.xml",
	} {
		if !strings.Contains(robotsBody, want) {
			t.Errorf("robots.txt missing %q:\n%s", want, robotsBody)
		}
	}

	// 3. Switch site to noindex and verify robots.txt + sitemap.xml update
	settings.SEORobotsIndexing = siteconfig.RobotsNoindex
	if err := siteconfig.Save(ctx, db, settings); err != nil {
		t.Fatal(err)
	}

	robotsNoindex := httptest.NewRecorder()
	h.ServeHTTP(robotsNoindex, httptest.NewRequest(http.MethodGet, "/robots.txt", nil))
	if !strings.Contains(robotsNoindex.Body.String(), "Disallow: /") || strings.Contains(robotsNoindex.Body.String(), "Sitemap:") {
		t.Fatalf("noindex robots.txt unexpected:\n%s", robotsNoindex.Body.String())
	}

	sitemapNoindex := httptest.NewRecorder()
	h.ServeHTTP(sitemapNoindex, httptest.NewRequest(http.MethodGet, "/sitemap.xml", nil))
	var emptySet sitemapURLSet
	if err := xml.Unmarshal(sitemapNoindex.Body.Bytes(), &emptySet); err != nil {
		t.Fatalf("unmarshal noindex sitemap: %v", err)
	}
	if len(emptySet.URLs) != 0 {
		t.Fatalf("noindex sitemap URLs = %+v, want empty", emptySet.URLs)
	}
}

func TestPublicRoutesSupportHEAD(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	admin, err := auth.CreateInitialAdmin(ctx, db, "head_admin", "a-very-secure-password")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := content.Save(ctx, db, content.Input{
		Type:    content.TypePost,
		Title:   "Hello HEAD",
		Slug:    "hello-head",
		Content: "Body text.",
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

	paths := []string{"/", "/hello-head", "/sitemap.xml", "/robots.txt"}
	for _, p := range paths {
		// HEAD before cache warm
		headCold := httptest.NewRecorder()
		h.ServeHTTP(headCold, httptest.NewRequest(http.MethodHead, p, nil))
		if headCold.Code != http.StatusOK {
			t.Fatalf("cold HEAD %s = %d, want 200", p, headCold.Code)
		}
		if headCold.Body.Len() != 0 {
			t.Fatalf("cold HEAD %s returned %d body bytes, want 0", p, headCold.Body.Len())
		}
		if headCold.Header().Get("Content-Length") == "" || headCold.Header().Get("Content-Length") == "0" {
			t.Fatalf("cold HEAD %s missing non-zero Content-Length: %q", p, headCold.Header().Get("Content-Length"))
		}

		// Warm cache with GET
		getRec := httptest.NewRecorder()
		h.ServeHTTP(getRec, httptest.NewRequest(http.MethodGet, p, nil))
		if getRec.Code != http.StatusOK || getRec.Body.Len() == 0 {
			t.Fatalf("GET %s = %d (len %d), want 200 with body", p, getRec.Code, getRec.Body.Len())
		}

		// HEAD after cache warm
		headWarm := httptest.NewRecorder()
		h.ServeHTTP(headWarm, httptest.NewRequest(http.MethodHead, p, nil))
		if headWarm.Code != http.StatusOK {
			t.Fatalf("warm HEAD %s = %d, want 200", p, headWarm.Code)
		}
		if headWarm.Body.Len() != 0 {
			t.Fatalf("warm HEAD %s returned %d body bytes, want 0", p, headWarm.Body.Len())
		}
		if headWarm.Header().Get("ETag") == "" {
			t.Fatalf("warm HEAD %s missing ETag", p)
		}
	}

	// Unsupported method still returns 405 with Allow: GET, HEAD
	postRec := httptest.NewRecorder()
	h.ServeHTTP(postRec, httptest.NewRequest(http.MethodPost, "/", nil))
	if postRec.Code != http.StatusMethodNotAllowed || postRec.Header().Get("Allow") != "GET, HEAD" {
		t.Fatalf("POST / = %d Allow=%q, want 405 Allow='GET, HEAD'", postRec.Code, postRec.Header().Get("Allow"))
	}
}

func TestTrustedProxyRealIPAndSecureCookies(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	if _, err := auth.CreateInitialAdmin(ctx, db, "proxy_admin", "a-very-secure-password"); err != nil {
		t.Fatal(err)
	}

	h, err := NewHandler(db, Options{
		UploadDir:      t.TempDir(),
		ThemeDir:       t.TempDir(),
		PluginDir:      t.TempDir(),
		BackupDir:      t.TempDir(),
		TrustedProxies: []string{"10.0.0.0/8"},
	})
	if err != nil {
		t.Fatal(err)
	}

	// 1. Trusted proxy with X-Forwarded-Proto: https -> Secure cookies + HSTS
	req := httptest.NewRequest(http.MethodGet, "/admin/login", nil)
	req.RemoteAddr = "10.0.0.5:44321"
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("X-Forwarded-For", "203.0.113.10")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /admin/login via proxy = %d", rec.Code)
	}
	if rec.Header().Get("Strict-Transport-Security") == "" {
		t.Fatalf("expected Strict-Transport-Security header behind trusted HTTPS proxy")
	}
	csrfCookie := cookieNamed(t, rec, csrfCookieName)
	if !csrfCookie.Secure {
		t.Fatalf("expected gocg_csrf cookie to have Secure=true behind trusted HTTPS proxy")
	}

	// 2. Untrusted peer spoofing X-Forwarded-Proto: https -> must NOT set Secure or HSTS
	untrustedReq := httptest.NewRequest(http.MethodGet, "/admin/login", nil)
	untrustedReq.RemoteAddr = "198.51.100.9:12345"
	untrustedReq.Header.Set("X-Forwarded-Proto", "https")
	untrustedReq.Header.Set("X-Forwarded-For", "203.0.113.99")
	untrustedRec := httptest.NewRecorder()
	h.ServeHTTP(untrustedRec, untrustedReq)
	if untrustedRec.Header().Get("Strict-Transport-Security") != "" {
		t.Fatalf("untrusted peer must not trigger Strict-Transport-Security")
	}
	if cookieNamed(t, untrustedRec, csrfCookieName).Secure {
		t.Fatalf("untrusted peer must not set Secure cookie without TLS")
	}

	// 3. Per-client login cooldown behind shared proxy IP
	csrfToken := csrfFromBody(t, rec.Body.String())
	for i := 0; i < 5; i++ {
		badLogin := httptest.NewRequest(http.MethodPost, "/admin/login", strings.NewReader(url.Values{
			"csrf_token": {csrfToken},
			"username":   {"proxy_admin"},
			"password":   {"wrong-password-123"},
		}.Encode()))
		badLogin.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		badLogin.RemoteAddr = "10.0.0.5:50000"
		badLogin.Header.Set("X-Forwarded-For", "203.0.113.10")
		badLogin.AddCookie(csrfCookie)
		r := httptest.NewRecorder()
		h.ServeHTTP(r, badLogin)
		if r.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d for 203.0.113.10 = %d, want 401", i+1, r.Code)
		}
	}
	// 6th attempt from 203.0.113.10 is blocked (429)
	blockedReq := httptest.NewRequest(http.MethodPost, "/admin/login", strings.NewReader(url.Values{
		"csrf_token": {csrfToken},
		"username":   {"proxy_admin"},
		"password":   {"a-very-secure-password"},
	}.Encode()))
	blockedReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	blockedReq.RemoteAddr = "10.0.0.5:50001"
	blockedReq.Header.Set("X-Forwarded-For", "203.0.113.10")
	blockedReq.AddCookie(csrfCookie)
	blockedRec := httptest.NewRecorder()
	h.ServeHTTP(blockedRec, blockedReq)
	if blockedRec.Code != http.StatusTooManyRequests {
		t.Fatalf("blocked IP 203.0.113.10 = %d, want 429", blockedRec.Code)
	}

	// Different real client IP behind the exact same proxy 10.0.0.5 logs in successfully with Secure session cookie
	otherClient := httptest.NewRequest(http.MethodPost, "/admin/login", strings.NewReader(url.Values{
		"csrf_token": {csrfToken},
		"username":   {"proxy_admin"},
		"password":   {"a-very-secure-password"},
	}.Encode()))
	otherClient.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	otherClient.RemoteAddr = "10.0.0.5:50002"
	otherClient.Header.Set("X-Forwarded-Proto", "https")
	otherClient.Header.Set("X-Forwarded-For", "203.0.113.20")
	otherClient.AddCookie(csrfCookie)
	otherRec := httptest.NewRecorder()
	h.ServeHTTP(otherRec, otherClient)
	if otherRec.Code != http.StatusSeeOther || otherRec.Header().Get("Location") != "/admin/" {
		t.Fatalf("other client 203.0.113.20 behind same proxy = %d (%s), want 303 /admin/", otherRec.Code, otherRec.Header().Get("Location"))
	}
	if !cookieNamed(t, otherRec, sessionCookieName).Secure {
		t.Fatalf("expected gocg_session cookie to have Secure=true behind trusted HTTPS proxy")
	}
}

func TestTOTPHardeningNoStep1PenaltyReplayAndRecoveryCodes(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	admin, err := auth.CreateInitialAdmin(ctx, db, "totp_admin", "strong-password-123")
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

	// 1. Enable 2FA and collect one-time recovery codes from redirected /admin/security?notice=enabled
	secPage := getWithCookies(h, "/admin/security", sessionCookie)
	secBody := secPage.Body.String()
	const secretMarker = `name="totp_secret" value="`
	idx := strings.Index(secBody, secretMarker)
	if idx < 0 {
		t.Fatal("missing totp_secret")
	}
	rest := secBody[idx+len(secretMarker):]
	secret := rest[:strings.Index(rest, `"`)]

	code, err := auth.TOTPCode(secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	secCSRF := cookieNamed(t, secPage, csrfCookieName)
	enableResp := postForm(h, "/admin/security/totp/enable", url.Values{
		"csrf_token":  {csrfFromBody(t, secBody)},
		"totp_secret": {secret},
		"totp_code":   {code},
	}, sessionCookie, secCSRF)
	if enableResp.Code != http.StatusSeeOther {
		t.Fatalf("enable 2FA = %d, want 303", enableResp.Code)
	}

	afterEnable := getWithCookies(h, enableResp.Header().Get("Location"), sessionCookie)
	recoveryPattern := regexp.MustCompile(`<code>([A-Z2-9]{5}-[A-Z2-9]{5})</code>`)
	matches := recoveryPattern.FindAllStringSubmatch(afterEnable.Body.String(), -1)
	if len(matches) != 8 {
		t.Fatalf("found %d recovery codes on /admin/security, want 8", len(matches))
	}
	recoveryCode := matches[0][1]

	// Reloading /admin/security must NOT show the recovery codes a second time
	reloaded := getWithCookies(h, "/admin/security", sessionCookie)
	if len(recoveryPattern.FindAllStringSubmatch(reloaded.Body.String(), -1)) != 0 {
		t.Fatalf("recovery codes must only be displayed once")
	}

	// 2. Step 1 without TOTP code must NOT trigger cooldown (try 4 times from same IP)
	loginPage := httptest.NewRecorder()
	h.ServeHTTP(loginPage, httptest.NewRequest(http.MethodGet, "/admin/login", nil))
	loginCSRF := cookieNamed(t, loginPage, csrfCookieName)
	loginToken := csrfFromBody(t, loginPage.Body.String())

	for i := 0; i < 4; i++ {
		req := httptest.NewRequest(http.MethodPost, "/admin/login", strings.NewReader(url.Values{
			"csrf_token": {loginToken},
			"username":   {"totp_admin"},
			"password":   {"strong-password-123"},
		}.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.RemoteAddr = "198.51.100.50:12345"
		req.AddCookie(loginCSRF)
		r := httptest.NewRecorder()
		h.ServeHTTP(r, req)
		if r.Code != http.StatusUnauthorized {
			t.Fatalf("step 1 attempt %d = %d, want 401 (no cooldown)", i+1, r.Code)
		}
	}

	// 3. Login with valid TOTP code succeeds on first use
	firstLogin := httptest.NewRequest(http.MethodPost, "/admin/login", strings.NewReader(url.Values{
		"csrf_token": {loginToken},
		"username":   {"totp_admin"},
		"password":   {"strong-password-123"},
		"totp_code":  {code},
	}.Encode()))
	firstLogin.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	firstLogin.RemoteAddr = "198.51.100.51:12345"
	firstLogin.AddCookie(loginCSRF)
	firstRec := httptest.NewRecorder()
	h.ServeHTTP(firstRec, firstLogin)
	if firstRec.Code != http.StatusSeeOther || firstRec.Header().Get("Location") != "/admin/" {
		t.Fatalf("first TOTP login = %d (%s), want 303 /admin/", firstRec.Code, firstRec.Header().Get("Location"))
	}

	// 4. Replaying the exact same TOTP code immediately must fail (401)
	replayLogin := httptest.NewRequest(http.MethodPost, "/admin/login", strings.NewReader(url.Values{
		"csrf_token": {loginToken},
		"username":   {"totp_admin"},
		"password":   {"strong-password-123"},
		"totp_code":  {code},
	}.Encode()))
	replayLogin.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	replayLogin.RemoteAddr = "198.51.100.52:12345"
	replayLogin.AddCookie(loginCSRF)
	replayRec := httptest.NewRecorder()
	h.ServeHTTP(replayRec, replayLogin)
	if replayRec.Code != http.StatusUnauthorized {
		t.Fatalf("replayed TOTP login = %d, want 401", replayRec.Code)
	}

	// 5. Login with recovery code succeeds once and cannot be reused
	recoveryLogin := httptest.NewRequest(http.MethodPost, "/admin/login", strings.NewReader(url.Values{
		"csrf_token": {loginToken},
		"username":   {"totp_admin"},
		"password":   {"strong-password-123"},
		"totp_code":  {recoveryCode},
	}.Encode()))
	recoveryLogin.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	recoveryLogin.RemoteAddr = "198.51.100.53:12345"
	recoveryLogin.AddCookie(loginCSRF)
	recoveryRec := httptest.NewRecorder()
	h.ServeHTTP(recoveryRec, recoveryLogin)
	if recoveryRec.Code != http.StatusSeeOther || recoveryRec.Header().Get("Location") != "/admin/" {
		t.Fatalf("recovery code login = %d (%s), want 303 /admin/", recoveryRec.Code, recoveryRec.Header().Get("Location"))
	}

	recoveryReplay := httptest.NewRequest(http.MethodPost, "/admin/login", strings.NewReader(url.Values{
		"csrf_token": {loginToken},
		"username":   {"totp_admin"},
		"password":   {"strong-password-123"},
		"totp_code":  {recoveryCode},
	}.Encode()))
	recoveryReplay.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	recoveryReplay.RemoteAddr = "198.51.100.54:12345"
	recoveryReplay.AddCookie(loginCSRF)
	recoveryReplayRec := httptest.NewRecorder()
	h.ServeHTTP(recoveryReplayRec, recoveryReplay)
	if recoveryReplayRec.Code != http.StatusUnauthorized {
		t.Fatalf("reused recovery code login = %d, want 401", recoveryReplayRec.Code)
	}
}

func TestBackupRetentionPolicyOnManualCreate(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	user, err := auth.CreateInitialAdmin(ctx, db, "retention_admin", "a-very-secure-password")
	if err != nil {
		t.Fatal(err)
	}
	token, expiresAt, err := auth.CreateSession(ctx, db, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	sessionCookie := &http.Cookie{Name: sessionCookieName, Value: token, Path: "/", Expires: expiresAt}
	backupDir := t.TempDir()
	h, err := NewHandler(db, Options{
		UploadDir:       t.TempDir(),
		ThemeDir:        t.TempDir(),
		PluginDir:       t.TempDir(),
		BackupDir:       backupDir,
		BackupRetention: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	settings := getWithCookies(h, "/admin/settings", sessionCookie)
	csrfCookie := cookieNamed(t, settings, csrfCookieName)
	csrfToken := csrfFromBody(t, settings.Body.String())

	for i := 0; i < 4; i++ {
		resp := postForm(h, "/admin/settings/backup", url.Values{
			"csrf_token": {csrfToken},
		}, sessionCookie, csrfCookie)
		if resp.Code != http.StatusSeeOther {
			t.Fatalf("backup %d response = %d, want 303", i+1, resp.Code)
		}
		time.Sleep(2 * time.Millisecond)
	}

	entries, err := os.ReadDir(backupDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("retained backups = %d (%v), want 2", len(entries), entries)
	}
}
