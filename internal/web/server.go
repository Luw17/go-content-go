package web

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Luw17/go-content-go/internal/auth"
	"github.com/Luw17/go-content-go/internal/cache"
	"github.com/Luw17/go-content-go/internal/content"
	"github.com/Luw17/go-content-go/internal/media"
	"github.com/Luw17/go-content-go/internal/navigation"
	"github.com/Luw17/go-content-go/internal/plugins"
	"github.com/Luw17/go-content-go/internal/siteconfig"
	themeengine "github.com/Luw17/go-content-go/themes"
	embeddedweb "github.com/Luw17/go-content-go/web"
)

const (
	sessionCookieName  = "gocg_session"
	csrfCookieName     = "gocg_csrf"
	maxFormSize        = 16 << 10
	maxContentFormSize = 3*(1<<20) + (64 << 10)
)

type handler struct {
	db              *sql.DB
	templates       *template.Template
	media           *media.Store
	menus           *navigation.Store
	themes          *themeengine.Manager
	plugins         *plugins.Manager
	cache           *cache.Cache
	loginProtection *cache.LoginProtection
	backupDir       string
}

type Options struct {
	UploadDir     string
	MaxUploadSize int64
	ThemeDir      string
	PluginDir     string
	BackupDir     string
	APIRateLimit  int
}

type formData struct {
	CSRFToken string
	Username  string
	Error     string
}

// NewHandler creates the public HTTP handler using embedded templates and assets.
func NewHandler(db *sql.DB, options ...Options) (http.Handler, error) {
	if db == nil {
		return nil, fmt.Errorf("database must not be nil")
	}
	settings := Options{
		UploadDir:     "uploads",
		MaxUploadSize: media.DefaultMaxUploadSize,
		ThemeDir:      "themes",
		PluginDir:     "plugins",
		BackupDir:     "backups",
		APIRateLimit:  60,
	}
	if len(options) > 1 {
		return nil, fmt.Errorf("only one web options value may be supplied")
	}
	if len(options) == 1 {
		if options[0].UploadDir != "" {
			settings.UploadDir = options[0].UploadDir
		}
		if options[0].MaxUploadSize != 0 {
			settings.MaxUploadSize = options[0].MaxUploadSize
		}
		if options[0].ThemeDir != "" {
			settings.ThemeDir = options[0].ThemeDir
		}
		if options[0].PluginDir != "" {
			settings.PluginDir = options[0].PluginDir
		}
		if options[0].BackupDir != "" {
			settings.BackupDir = options[0].BackupDir
		}
		if options[0].APIRateLimit != 0 {
			settings.APIRateLimit = options[0].APIRateLimit
		}
	}
	if settings.APIRateLimit < 1 || settings.APIRateLimit > 10000 {
		return nil, fmt.Errorf("API rate limit must be between 1 and 10000 requests per minute")
	}
	mediaStore, err := media.NewStore(db, settings.UploadDir, settings.MaxUploadSize)
	if err != nil {
		return nil, fmt.Errorf("initialize media store: %w", err)
	}
	menuStore, err := navigation.NewStore(db)
	if err != nil {
		return nil, fmt.Errorf("initialize navigation menus: %w", err)
	}
	themeManager, err := themeengine.New(settings.ThemeDir)
	if err != nil {
		return nil, fmt.Errorf("initialize theme manager: %w", err)
	}
	pluginManager, err := plugins.New(settings.PluginDir, plugins.DefaultTimeout)
	if err != nil {
		return nil, fmt.Errorf("initialize plugin manager: %w", err)
	}
	if err := pluginManager.Reload(context.Background(), db); err != nil {
		return nil, fmt.Errorf("load active plugins: %w", err)
	}
	staticFiles, err := embeddedweb.StaticFiles()
	if err != nil {
		return nil, err
	}
	staticVersions := make(map[string]string, 4)
	for _, name := range []string{"admin.css", "style.css", "admin.js", "htmx.min.js"} {
		version, err := embeddedweb.StaticVersion(name)
		if err != nil {
			return nil, fmt.Errorf("version static asset %q: %w", name, err)
		}
		staticVersions[name] = version
	}
	templateFiles, err := embeddedweb.Templates()
	if err != nil {
		return nil, err
	}
	templates, err := template.New("admin.html").Funcs(template.FuncMap{
		"markdown": func(source string) (template.HTML, error) {
			rendered, err := content.RenderMarkdown(source)
			return template.HTML(rendered), err
		},
		"repeat":    strings.Repeat,
		"hasPrefix": strings.HasPrefix,
		"staticVersion": func(name string) (string, error) {
			version, ok := staticVersions[name]
			if !ok {
				return "", fmt.Errorf("unknown static asset %q", name)
			}
			return version, nil
		},
		"formatBytes": func(size int64) string {
			if size >= 1<<20 {
				return fmt.Sprintf("%.1f MB", float64(size)/(1<<20))
			}
			if size >= 1<<10 {
				return fmt.Sprintf("%.1f KB", float64(size)/(1<<10))
			}
			return fmt.Sprintf("%d bytes", size)
		},
	}).ParseFS(templateFiles, "*.html")
	if err != nil {
		return nil, fmt.Errorf("parse embedded templates: %w", err)
	}
	responseCache, err := cache.New(512, 30*time.Second)
	if err != nil {
		return nil, fmt.Errorf("initialize public response cache: %w", err)
	}
	loginLimiter, err := cache.NewLimiter(5, time.Minute, 5, 10000)
	if err != nil {
		return nil, fmt.Errorf("initialize login rate limiter: %w", err)
	}
	loginProtection, err := cache.NewLoginProtection(5, time.Minute, 15*time.Minute, 10000)
	if err != nil {
		return nil, fmt.Errorf("initialize login cooldown: %w", err)
	}
	uploadLimiter, err := cache.NewLimiter(10, time.Minute, 3, 10000)
	if err != nil {
		return nil, fmt.Errorf("initialize upload rate limiter: %w", err)
	}
	apiBurst := min(30, settings.APIRateLimit)
	apiLimiter, err := cache.NewLimiter(settings.APIRateLimit, time.Minute, apiBurst, 10000)
	if err != nil {
		return nil, fmt.Errorf("initialize API rate limiter: %w", err)
	}
	h := &handler{
		db: db, templates: templates, media: mediaStore, menus: menuStore, themes: themeManager, plugins: pluginManager,
		cache: responseCache, loginProtection: loginProtection, backupDir: settings.BackupDir,
	}
	mux := http.NewServeMux()
	staticHandler := http.FileServer(http.FS(staticFiles))
	mux.Handle("/static/", http.StripPrefix("/static/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/")
		if version, ok := staticVersions[name]; ok && r.URL.Query().Get("v") == version {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		staticHandler.ServeHTTP(w, r)
	})))
	mux.HandleFunc("/uploads/", h.serveUpload)
	mux.HandleFunc("/themes/", h.serveThemeAsset)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("GET /admin", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/admin/", http.StatusSeeOther)
	})
	mux.HandleFunc("GET /admin/setup", h.setupForm)
	mux.HandleFunc("POST /admin/setup", h.setup)
	mux.HandleFunc("GET /admin/login", h.loginForm)
	loginHandler := loginLimiter.Middleware("login", http.HandlerFunc(h.login))
	mux.Handle("POST /admin/login", h.loginProtectionMiddleware(loginHandler))
	mux.HandleFunc("GET /admin/", h.dashboard)
	mux.HandleFunc("POST /admin/logout", h.logout)
	mux.HandleFunc("GET /admin/posts", h.listPosts)
	mux.HandleFunc("GET /admin/pages", h.listPages)
	mux.HandleFunc("GET /admin/media", h.mediaLibrary)
	mux.HandleFunc("GET /admin/media/picker", h.mediaPicker)
	mux.HandleFunc("GET /admin/appearance", h.appearance)
	mux.HandleFunc("GET /admin/appearance/install", h.themeInstallPage)
	mux.HandleFunc("POST /admin/appearance/install", h.installTheme)
	mux.HandleFunc("GET /admin/themes/install", h.themeInstallPage)
	mux.HandleFunc("POST /admin/themes/install", h.installTheme)
	mux.HandleFunc("GET /admin/appearance/menus", h.menuManagement)
	mux.HandleFunc("POST /admin/appearance/menus", h.createMenu)
	mux.HandleFunc("POST /admin/appearance/menus/{id}/items", h.addMenuItem)
	mux.HandleFunc("POST /admin/appearance/menus/{id}/delete", h.deleteMenu)
	mux.HandleFunc("POST /admin/appearance/menu-items/{id}/move", h.moveMenuItem)
	mux.HandleFunc("POST /admin/appearance/menu-items/{id}/delete", h.deleteMenuItem)
	mux.HandleFunc("POST /admin/appearance/{name}/activate", h.activateTheme)
	mux.HandleFunc("GET /admin/plugins", h.pluginList)
	mux.HandleFunc("GET /admin/plugins/install", h.pluginInstallPage)
	mux.HandleFunc("POST /admin/plugins/install", h.installPlugin)
	mux.HandleFunc("POST /admin/plugins/{id}/activate", h.activatePlugin)
	mux.HandleFunc("POST /admin/plugins/{id}/deactivate", h.deactivatePlugin)
	mux.HandleFunc("GET /admin/users", h.userList)
	mux.HandleFunc("POST /admin/users", h.createUser)
	mux.HandleFunc("GET /admin/settings", h.settings)
	mux.HandleFunc("POST /admin/settings/save", h.saveSiteSettings)
	mux.HandleFunc("POST /admin/settings/backup", h.createBackup)
	mux.HandleFunc("POST /admin/settings/optimize", h.optimizeDatabase)
	mux.HandleFunc("GET /admin/settings/backup/download", h.downloadBackup)
	mux.HandleFunc("GET /admin/posts/new", h.newPost)
	mux.HandleFunc("GET /admin/pages/new", h.newPage)
	mux.HandleFunc("GET /admin/posts/{id}/edit", h.editPost)
	mux.HandleFunc("GET /admin/pages/{id}/edit", h.editPage)
	mux.HandleFunc("POST /admin/posts/save", h.savePost)
	mux.HandleFunc("POST /admin/pages/save", h.savePage)
	mux.HandleFunc("POST /admin/content/{id}/status", h.changeContentStatus)
	mux.HandleFunc("POST /admin/content/{id}/trash", h.trashContent)
	mux.HandleFunc("POST /admin/content/{id}/restore", h.restoreContent)
	mux.HandleFunc("POST /admin/content/{id}/delete", h.deleteContent)
	mux.HandleFunc("POST /admin/preview", h.previewMarkdown)
	mux.HandleFunc("GET /admin/{section}", h.adminPlaceholder)
	mux.HandleFunc("POST /admin/media/upload", h.uploadMedia)
	mux.HandleFunc("POST /admin/media/{id}/delete", h.deleteMedia)
	api := http.NewServeMux()
	api.HandleFunc("GET /api/v1/posts", h.apiPosts)
	api.HandleFunc("GET /api/v1/posts/{slug}", h.apiPost)
	api.HandleFunc("GET /api/v1/pages/{slug}", h.apiPage)
	api.HandleFunc("/", h.apiNotFound)
	apiWithLimit := apiLimiter.Middleware("api", responseCache.Middleware(api))
	mux.Handle("/api/v1/", publicAPICORS(apiWithLimit))
	mux.Handle("/", responseCache.MiddlewareWithKey(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if r.URL.Path != "/" {
			h.publicContent(w, r)
			return
		}
		h.publicHome(w, r)
	}), h.publicCacheKey))
	adminHandler := h.csrfMiddleware(mux)
	uploadHandler := uploadLimiter.Middleware("upload", adminHandler)
	securedHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isUploadRequest(r) {
			uploadHandler.ServeHTTP(w, r)
			return
		}
		adminHandler.ServeHTTP(w, r)
	})
	return securityHeaders(securedHandler), nil
}

func (h *handler) publicCacheKey(r *http.Request) string {
	key := r.Host + r.URL.RequestURI()
	var activeTheme string
	if err := h.db.QueryRowContext(r.Context(),
		"SELECT COALESCE((SELECT value FROM options WHERE key = 'active_theme'), '')",
	).Scan(&activeTheme); err != nil {
		log.Printf("read active theme for public cache: %v", err)
		return fmt.Sprintf("%s|uncacheable=%d", key, time.Now().UnixNano())
	}
	settings, err := siteconfig.Load(r.Context(), h.db)
	if err != nil {
		log.Printf("read site settings for public cache: %v", err)
		return fmt.Sprintf("%s|uncacheable=%d", key, time.Now().UnixNano())
	}
	return fmt.Sprintf("%s|theme=%q|title=%q|tagline=%q|logo=%q|front=%q:%d|intro=%q|posts=%q",
		key, activeTheme, settings.Title, settings.Tagline, settings.Logo, settings.ShowOnFront,
		settings.PageOnFront, settings.HomeIntroTitle, settings.HomePostsTitle)
}

func (h *handler) setupForm(w http.ResponseWriter, r *http.Request) {
	available, err := auth.SetupAvailable(r.Context(), h.db)
	if err != nil {
		http.Error(w, "failed to check initial setup", http.StatusInternalServerError)
		return
	}
	if !available {
		http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
		return
	}
	token, err := issueCSRFToken(w, r)
	if err != nil {
		http.Error(w, "failed to prepare setup form", http.StatusInternalServerError)
		return
	}
	h.render(w, "setup.html", formData{CSRFToken: token}, http.StatusOK)
}

func (h *handler) setup(w http.ResponseWriter, r *http.Request) {
	if err := parseForm(w, r); err != nil {
		http.Error(w, "invalid form submission", http.StatusBadRequest)
		return
	}
	if !validCSRFToken(r) {
		http.Error(w, "invalid form token", http.StatusForbidden)
		return
	}
	available, err := auth.SetupAvailable(r.Context(), h.db)
	if err != nil {
		http.Error(w, "failed to check initial setup", http.StatusInternalServerError)
		return
	}
	if !available {
		http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
		return
	}

	username := r.FormValue("username")
	password := r.FormValue("password")
	if password != r.FormValue("password_confirmation") {
		h.renderWithCSRF(w, r, "setup.html", formData{
			Username: username,
			Error:    "As senhas não coincidem.",
		}, http.StatusUnprocessableEntity)
		return
	}
	user, err := auth.CreateInitialAdmin(r.Context(), h.db, username, password)
	if errors.Is(err, auth.ErrSetupComplete) {
		http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
		return
	}
	if errors.Is(err, auth.ErrInvalidUsername) || errors.Is(err, auth.ErrInvalidPassword) {
		message := "Nome de usuário inválido."
		if errors.Is(err, auth.ErrInvalidPassword) {
			message = "A senha deve ter entre 12 e 72 bytes."
		}
		h.renderWithCSRF(w, r, "setup.html", formData{
			Username: username,
			Error:    message,
		}, http.StatusUnprocessableEntity)
		return
	}
	if err != nil {
		http.Error(w, "failed to create administrator", http.StatusInternalServerError)
		return
	}
	if err := h.startSession(w, r, user.ID); err != nil {
		http.Error(w, "administrator created, but failed to start session", http.StatusInternalServerError)
		return
	}
	clearCookie(w, r, csrfCookieName)
	http.Redirect(w, r, "/admin/", http.StatusSeeOther)
}

func (h *handler) loginForm(w http.ResponseWriter, r *http.Request) {
	available, err := auth.SetupAvailable(r.Context(), h.db)
	if err != nil {
		http.Error(w, "failed to check initial setup", http.StatusInternalServerError)
		return
	}
	if available {
		http.Redirect(w, r, "/admin/setup", http.StatusSeeOther)
		return
	}
	token, err := issueCSRFToken(w, r)
	if err != nil {
		http.Error(w, "failed to prepare login form", http.StatusInternalServerError)
		return
	}
	h.render(w, "login.html", formData{CSRFToken: token}, http.StatusOK)
}

func (h *handler) login(w http.ResponseWriter, r *http.Request) {
	if err := parseForm(w, r); err != nil {
		http.Error(w, "invalid form submission", http.StatusBadRequest)
		return
	}
	if !validCSRFToken(r) {
		http.Error(w, "invalid form token", http.StatusForbidden)
		return
	}
	available, err := auth.SetupAvailable(r.Context(), h.db)
	if err != nil {
		http.Error(w, "failed to check initial setup", http.StatusInternalServerError)
		return
	}
	if available {
		http.Redirect(w, r, "/admin/setup", http.StatusSeeOther)
		return
	}
	username := r.FormValue("username")
	user, err := auth.Authenticate(r.Context(), h.db, username, r.FormValue("password"))
	if errors.Is(err, auth.ErrInvalidCredentials) {
		ip := clientAddress(r)
		blocked := h.loginProtection.Failed(ip, time.Now())
		slog.Warn("authentication failed", "remote_addr", ip, "cooldown_started", blocked)
		h.renderWithCSRF(w, r, "login.html", formData{
			Username: username,
			Error:    "Usuário ou senha inválidos.",
		}, http.StatusUnauthorized)
		return
	}

	if err != nil {
		slog.Error("authenticate administrator", "remote_addr", clientAddress(r), "error", err)
		http.Error(w, "failed to authenticate", http.StatusInternalServerError)
		return
	}
	h.loginProtection.Clear(clientAddress(r))
	if err := h.startSession(w, r, user.ID); err != nil {
		slog.Error("start authenticated session", "remote_addr", clientAddress(r), "error", err)
		http.Error(w, "failed to start session", http.StatusInternalServerError)
		return
	}
	h.plugins.DispatchAction(r.Context(), "user:login", map[string]any{
		"user_id": user.ID, "username": user.Username,
	})
	clearCookie(w, r, csrfCookieName)
	http.Redirect(w, r, "/admin/", http.StatusSeeOther)
}

func clientAddress(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (h *handler) dashboard(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/admin/" {
		http.NotFound(w, r)
		return
	}
	user, authenticated, err := h.currentUser(r)
	if err != nil {
		http.Error(w, "failed to validate session", http.StatusInternalServerError)
		return
	}
	if !authenticated {
		http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
		return
	}
	token, err := issueCSRFToken(w, r)
	if err != nil {
		http.Error(w, "failed to prepare dashboard", http.StatusInternalServerError)
		return
	}
	h.renderAdmin(w, adminPage{
		Mode:      "dashboard",
		Username:  user.Username,
		CSRFToken: token,
		Title:     "Visão geral",
	})
}

func (h *handler) logout(w http.ResponseWriter, r *http.Request) {
	if err := parseForm(w, r); err != nil {
		http.Error(w, "invalid form submission", http.StatusBadRequest)
		return
	}
	if !validCSRFToken(r) {
		http.Error(w, "invalid form token", http.StatusForbidden)
		return
	}
	token, err := sessionToken(r)
	if err != nil {
		http.Error(w, "invalid session cookie", http.StatusBadRequest)
		return
	}
	if err := auth.DeleteSession(r.Context(), h.db, token); err != nil {
		http.Error(w, "failed to end session", http.StatusInternalServerError)
		return
	}
	clearCookie(w, r, sessionCookieName)
	clearCookie(w, r, csrfCookieName)
	http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
}

func (h *handler) currentUser(r *http.Request) (auth.User, bool, error) {
	token, err := sessionToken(r)
	if err != nil {
		return auth.User{}, false, nil
	}
	return auth.SessionUser(r.Context(), h.db, token)
}

func (h *handler) startSession(w http.ResponseWriter, r *http.Request, userID int64) error {
	token, expiresAt, err := auth.CreateSession(r.Context(), h.db, userID)
	if err != nil {
		return err
	}
	if previousToken, err := sessionToken(r); err == nil {
		if err := auth.DeleteSession(r.Context(), h.db, previousToken); err != nil {
			if cleanupErr := auth.DeleteSession(r.Context(), h.db, token); cleanupErr != nil {
				return fmt.Errorf("rotate session: %v; revoke new session: %w", err, cleanupErr)
			}
			return fmt.Errorf("rotate previous session: %w", err)
		}
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		Expires:  expiresAt,
		MaxAge:   int(time.Until(expiresAt).Seconds()),
		HttpOnly: true,
		Secure:   r.TLS != nil,
		SameSite: http.SameSiteStrictMode,
	})
	return nil
}

func (h *handler) renderWithCSRF(w http.ResponseWriter, r *http.Request, name string, data formData, status int) {
	cookie, err := r.Cookie(csrfCookieName)
	if err != nil {
		http.Error(w, "missing form token", http.StatusForbidden)
		return
	}
	data.CSRFToken = cookie.Value
	h.render(w, name, data, status)
}

func (h *handler) render(w http.ResponseWriter, name string, data formData, status int) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if err := h.templates.ExecuteTemplate(w, name, data); err != nil {
		// The response is already started; report the rendering failure for logs.
		log.Printf("render %s: %v", name, err)
	}
}

func copyBackupToResponse(w http.ResponseWriter, path string) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open backup for download: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("inspect backup for download: %w", err)
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filepath.Base(path)+`"`)
	w.Header().Set("Content-Length", fmt.Sprint(info.Size()))
	if _, err := io.Copy(w, file); err != nil {
		return fmt.Errorf("send backup download: %w", err)
	}
	return nil
}

func parseForm(w http.ResponseWriter, r *http.Request) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxFormSize)
	if err := r.ParseForm(); err != nil {
		return fmt.Errorf("parse form: %w", err)
	}
	return nil
}

func issueCSRFToken(w http.ResponseWriter, r *http.Request) (string, error) {
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return "", fmt.Errorf("generate form token: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(tokenBytes)
	http.SetCookie(w, &http.Cookie{
		Name:     csrfCookieName,
		Value:    token,
		Path:     "/admin",
		HttpOnly: true,
		Secure:   r.TLS != nil,
		SameSite: http.SameSiteStrictMode,
	})
	return token, nil
}

func validCSRFToken(r *http.Request) bool {
	cookie, err := r.Cookie(csrfCookieName)
	values := r.PostForm["csrf_token"]
	if err != nil || len(values) != 1 || cookie.Value == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(values[0])) == 1
}

func sessionToken(r *http.Request) (string, error) {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		return "", err
	}
	token, err := base64.RawURLEncoding.DecodeString(cookie.Value)
	if err != nil || len(token) != 32 {
		return "", fmt.Errorf("invalid session token")
	}
	return cookie.Value, nil
}

func clearCookie(w http.ResponseWriter, r *http.Request, name string) {
	path := "/"
	if name == csrfCookieName {
		path = "/admin"
	}
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    "",
		Path:     path,
		Expires:  time.Unix(1, 0),
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   r.TLS != nil,
		SameSite: http.SameSiteStrictMode,
	})
}
