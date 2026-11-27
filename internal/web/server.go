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
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Luw17/go-content-go/internal/auth"
	"github.com/Luw17/go-content-go/internal/builder"
	"github.com/Luw17/go-content-go/internal/cache"
	"github.com/Luw17/go-content-go/internal/content"
	"github.com/Luw17/go-content-go/internal/database"
	"github.com/Luw17/go-content-go/internal/media"
	"github.com/Luw17/go-content-go/internal/navigation"
	"github.com/Luw17/go-content-go/internal/plugins"
	"github.com/Luw17/go-content-go/internal/redirects"
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

type httpsContextKey struct{}

type handler struct {
	db              *sql.DB
	templates       *template.Template
	media           *media.Store
	menus           *navigation.Store
	builder         *builder.Store
	themes          *themeengine.Manager
	plugins         *plugins.Manager
	cache           *cache.Cache
	loginProtection *cache.LoginProtection
	uploadDir       string
	themeDir        string
	pluginDir       string
	backupDir       string
	backupRetention int
	trustedProxies  []netip.Prefix
	recoveryFlashMu sync.Mutex
	recoveryFlash   map[string][]string
	apiKeyFlashMu   sync.Mutex
	apiKeyFlash     map[string]string
}

type Options struct {
	UploadDir       string
	MaxUploadSize   int64
	ThemeDir        string
	PluginDir       string
	BackupDir       string
	BackupRetention int
	APIRateLimit    int
	TrustedProxies  []string
}

type formData struct {
	CSRFToken   string
	Username    string
	Error       string
	RequireTOTP bool
}

// NewHandler creates the public HTTP handler using embedded templates and assets.
func NewHandler(db *sql.DB, options ...Options) (http.Handler, error) {
	if db == nil {
		return nil, fmt.Errorf("database must not be nil")
	}
	settings := Options{
		UploadDir:       "uploads",
		MaxUploadSize:   media.DefaultMaxUploadSize,
		ThemeDir:        "themes",
		PluginDir:       "plugins",
		BackupDir:       "backups",
		BackupRetention: database.DefaultBackupRetention,
		APIRateLimit:    60,
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
		if options[0].BackupRetention != 0 {
			settings.BackupRetention = options[0].BackupRetention
		}
		if options[0].APIRateLimit != 0 {
			settings.APIRateLimit = options[0].APIRateLimit
		}
		if len(options[0].TrustedProxies) > 0 {
			settings.TrustedProxies = options[0].TrustedProxies
		}
	}
	if settings.APIRateLimit < 1 || settings.APIRateLimit > 10000 {
		return nil, fmt.Errorf("API rate limit must be between 1 and 10000 requests per minute")
	}
	if settings.BackupRetention < 1 || settings.BackupRetention > 1000 {
		return nil, fmt.Errorf("backup retention must be between 1 and 1000 files")
	}
	trustedPrefixes, err := parseTrustedProxies(settings.TrustedProxies)
	if err != nil {
		return nil, fmt.Errorf("initialize trusted proxies: %w", err)
	}
	mediaStore, err := media.NewStore(db, settings.UploadDir, settings.MaxUploadSize)
	if err != nil {
		return nil, fmt.Errorf("initialize media store: %w", err)
	}
	menuStore, err := navigation.NewStore(db)
	if err != nil {
		return nil, fmt.Errorf("initialize navigation menus: %w", err)
	}
	builderStore, err := builder.NewStore(db)
	if err != nil {
		return nil, fmt.Errorf("initialize page builder store: %w", err)
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
		"formatDateTime": func(raw string) string {
			raw = strings.TrimSpace(raw)
			if raw == "" {
				return "—"
			}
			for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05"} {
				if parsed, err := time.Parse(layout, raw); err == nil {
					return parsed.In(time.Local).Format("02/01/2006 15:04")
				}
			}
			return raw
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
	formLimiter, err := cache.NewLimiter(15, time.Minute, 5, 10000)
	if err != nil {
		return nil, fmt.Errorf("initialize form rate limiter: %w", err)
	}
	h := &handler{
		db: db, templates: templates, media: mediaStore, menus: menuStore, builder: builderStore, themes: themeManager, plugins: pluginManager,
		cache: responseCache, loginProtection: loginProtection,
		uploadDir: settings.UploadDir, themeDir: settings.ThemeDir, pluginDir: settings.PluginDir, backupDir: settings.BackupDir,
		backupRetention: settings.BackupRetention, trustedProxies: trustedPrefixes,
		recoveryFlash: make(map[string][]string),
		apiKeyFlash:   make(map[string]string),
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
	loginHandler := loginLimiter.MiddlewareWithIP("login", h.clientAddress, http.HandlerFunc(h.login))
	mux.Handle("POST /admin/login", h.loginProtectionMiddleware(loginHandler))
	mux.HandleFunc("GET /admin/", h.dashboard)
	mux.HandleFunc("POST /admin/logout", h.logout)
	mux.HandleFunc("GET /admin/posts", h.listPosts)
	mux.HandleFunc("GET /admin/pages", h.listPages)
	mux.HandleFunc("GET /admin/categories", h.categoryList)
	mux.HandleFunc("POST /admin/categories/save", h.saveCategory)
	mux.HandleFunc("POST /admin/categories/reorder", h.reorderCategories)
	mux.HandleFunc("POST /admin/categories/{id}/delete", h.deleteCategory)
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
	mux.HandleFunc("GET /admin/appearance/sections", h.sectionsPage)
	mux.HandleFunc("POST /admin/appearance/sections/save", h.saveSection)
	mux.HandleFunc("POST /admin/appearance/sections/{id}/delete", h.deleteSection)
	mux.HandleFunc("POST /admin/appearance/{name}/activate", h.activateTheme)
	mux.HandleFunc("GET /admin/builder", h.builderList)
	mux.HandleFunc("GET /admin/builder/new", h.builderNew)
	mux.HandleFunc("GET /admin/builder/{id}/edit", h.builderEdit)
	mux.HandleFunc("POST /admin/builder/save", h.saveBuilderLayout)
	mux.HandleFunc("POST /admin/builder/preview", h.previewBuilderBlocks)
	mux.HandleFunc("POST /admin/builder/homepage/clear", h.clearHomepageLayout)
	mux.HandleFunc("POST /admin/builder/{id}/homepage", h.setHomepageLayout)
	mux.HandleFunc("POST /admin/builder/{id}/duplicate", h.duplicateBuilderLayout)
	mux.HandleFunc("POST /admin/builder/{id}/delete", h.deleteBuilderLayout)
	mux.HandleFunc("GET /admin/builder/submissions/export.csv", h.exportFormSubmissionsCSV)
	mux.HandleFunc("POST /admin/builder/submissions/{id}/delete", h.deleteFormSubmission)
	mux.Handle("POST /forms/submit", formLimiter.MiddlewareWithIP("form", h.clientAddress, http.HandlerFunc(h.submitPublicForm)))
	mux.HandleFunc("GET /admin/plugins", h.pluginList)
	mux.HandleFunc("GET /admin/plugins/install", h.pluginInstallPage)
	mux.HandleFunc("POST /admin/plugins/install", h.installPlugin)
	mux.HandleFunc("POST /admin/plugins/{id}/activate", h.activatePlugin)
	mux.HandleFunc("POST /admin/plugins/{id}/deactivate", h.deactivatePlugin)
	mux.HandleFunc("GET /admin/plugin/{id}", h.pluginAdminPage)
	mux.HandleFunc("POST /admin/plugin/{id}/settings", h.savePluginSetting)
	mux.HandleFunc("POST /admin/plugin/{id}/settings/delete", h.deletePluginSetting)
	mux.HandleFunc("GET /admin/security", h.securityPage)
	mux.HandleFunc("POST /admin/security/totp/enable", h.enableTOTP)
	mux.HandleFunc("POST /admin/security/totp/disable", h.disableTOTP)
	mux.HandleFunc("POST /admin/security/totp/recovery", h.regenerateRecoveryCodes)
	mux.HandleFunc("GET /admin/users", h.userList)
	mux.HandleFunc("POST /admin/users", h.createUser)
	mux.HandleFunc("POST /admin/users/{id}/role", h.updateUserRole)
	mux.HandleFunc("POST /admin/users/{id}/password", h.updateUserPassword)
	mux.HandleFunc("POST /admin/users/{id}/toggle", h.toggleUserActive)
	mux.HandleFunc("POST /admin/users/{id}/revoke-sessions", h.revokeUserSessions)
	mux.HandleFunc("POST /admin/users/{id}/delete", h.deleteUser)
	mux.HandleFunc("GET /admin/redirects", h.redirectList)
	mux.HandleFunc("POST /admin/redirects", h.saveRedirect)
	mux.HandleFunc("POST /admin/redirects/{id}/delete", h.deleteRedirect)
	mux.HandleFunc("GET /admin/tools", h.toolsPage)
	mux.HandleFunc("GET /admin/tools/export.json", h.exportContentJSON)
	mux.HandleFunc("GET /admin/tools/export.md", h.exportContentMarkdown)
	mux.HandleFunc("POST /admin/tools/import", h.importContent)
	mux.HandleFunc("GET /admin/settings", h.settings)
	mux.HandleFunc("POST /admin/settings/save", h.saveSiteSettings)
	mux.HandleFunc("POST /admin/settings/api-keys", h.createAPIKey)
	mux.HandleFunc("POST /admin/settings/api-keys/{id}/delete", h.deleteAPIKey)
	mux.HandleFunc("POST /admin/settings/backup", h.createBackup)
	mux.HandleFunc("POST /admin/settings/optimize", h.optimizeDatabase)
	mux.HandleFunc("GET /admin/settings/backup/download", h.downloadBackup)
	mux.HandleFunc("POST /admin/settings/permissions/fix", h.fixPermissions)
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
	mux.HandleFunc("POST /admin/content/{id}/revisions/{revID}/restore", h.restoreContentRevision)
	mux.HandleFunc("POST /admin/preview", h.previewMarkdown)
	mux.HandleFunc("GET /admin/media/folders", h.listMediaFolders)
	mux.HandleFunc("POST /admin/media/folders", h.createMediaFolder)
	mux.HandleFunc("POST /admin/media/folders/{id}/delete", h.deleteMediaFolder)
	mux.HandleFunc("POST /admin/media/upload", h.uploadMedia)
	mux.HandleFunc("POST /admin/media/{id}/metadata", h.updateMediaMetadata)
	mux.HandleFunc("POST /admin/media/{id}/delete", h.deleteMedia)
	api := http.NewServeMux()
	api.HandleFunc("GET /api/v1/posts", h.apiPosts)
	api.HandleFunc("GET /api/v1/posts/{slug}", h.apiPost)
	api.HandleFunc("GET /api/v1/pages", h.apiPages)
	api.HandleFunc("GET /api/v1/pages/{slug}", h.apiPage)
	api.HandleFunc("GET /api/v1/categories", h.apiCategories)
	api.HandleFunc("GET /api/v1/menus/{location}", h.apiMenuByLocation)
	api.HandleFunc("GET /api/v1/site", h.apiSiteInfo)
	api.HandleFunc("/", h.apiNotFound)
	apiWithLimit := apiLimiter.MiddlewareWithIP("api", h.clientAddress, responseCache.Middleware(api))
	mux.Handle("/api/v1/", publicAPICORS(apiWithLimit))
	mux.Handle("/", responseCache.MiddlewareWithKey(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		switch {
		case r.URL.Path == "/sitemap.xml":
			h.publicSitemap(w, r)
		case r.URL.Path == "/robots.txt":
			h.publicRobots(w, r)
		case r.URL.Path == "/rss.xml":
			h.publicRSS(w, r)
		case r.URL.Path == "/favicon.ico":
			h.publicFavicon(w, r)
		case r.URL.Path == "/search":
			h.publicSearch(w, r)
		case r.URL.Path == "/":
			h.publicHome(w, r)
		default:
			if redir, ok, err := redirects.Find(r.Context(), h.db, r.URL.Path); err == nil && ok {
				http.Redirect(w, r, redir.TargetPath, redir.StatusCode)
				return
			}
			if slug, ok := strings.CutPrefix(r.URL.Path, "/categoria/"); ok && slug != "" && !strings.Contains(slug, "/") {
				h.publicCategoryArchive(w, r, slug)
				return
			}
			if slug, ok := strings.CutPrefix(r.URL.Path, "/tag/"); ok && slug != "" && !strings.Contains(slug, "/") {
				h.publicTagArchive(w, r, slug)
				return
			}
			h.publicContent(w, r)
		}
	}), h.publicCacheKey))
	adminHandler := h.csrfMiddleware(mux)
	uploadHandler := uploadLimiter.MiddlewareWithIP("upload", h.clientAddress, adminHandler)
	securedHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isUploadRequest(r) {
			uploadHandler.ServeHTTP(w, r)
			return
		}
		adminHandler.ServeHTTP(w, r)
	})
	return &serverHandler{
		Handler:    h.securityHeaders(gzipMiddleware(securedHandler)),
		clearCache: responseCache.Clear,
	}, nil
}

type serverHandler struct {
	http.Handler
	clearCache func()
}

// ClearCache invalidates all cached public responses (used by background tasks such as scheduled publishing).
func (s *serverHandler) ClearCache() {
	if s.clearCache != nil {
		s.clearCache()
	}
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
	builderRev, err := h.builder.Revision(r.Context())
	if err != nil {
		log.Printf("read builder revision for public cache: %v", err)
		return fmt.Sprintf("%s|uncacheable=%d", key, time.Now().UnixNano())
	}
	return fmt.Sprintf("%s|theme=%q|title=%q|tagline=%q|logo=%q|fav=%q|apple=%q|front=%q:%d|intro=%q|posts=%q|url=%q|desc=%q|og=%q|tw=%q|robots=%q|builder=%q",
		key, activeTheme, settings.Title, settings.Tagline, settings.Logo, settings.Favicon, settings.AppleTouchIcon,
		settings.ShowOnFront, settings.PageOnFront, settings.HomeIntroTitle, settings.HomePostsTitle,
		settings.SiteURL, settings.SEODefaultDescription, settings.SEODefaultOGImage,
		settings.SEOTwitterHandle, settings.SEORobotsIndexing, builderRev)
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
		ip := h.clientAddress(r)
		blocked := h.loginProtection.Failed(ip, time.Now())
		slog.Warn("authentication failed", "remote_addr", ip, "cooldown_started", blocked)
		h.renderWithCSRF(w, r, "login.html", formData{
			Username: username,
			Error:    "Usuário ou senha inválidos.",
		}, http.StatusUnauthorized)
		return
	}

	if err != nil {
		slog.Error("authenticate administrator", "remote_addr", h.clientAddress(r), "error", err)
		http.Error(w, "failed to authenticate", http.StatusInternalServerError)
		return
	}
	if user.TOTPEnabled {
		rawCode := strings.TrimSpace(r.FormValue("totp_code"))
		if rawCode == "" {
			h.renderWithCSRF(w, r, "login.html", formData{
				Username:    username,
				Error:       "Informe o código de 6 dígitos do seu aplicativo autenticador ou um código de recuperação.",
				RequireTOTP: true,
			}, http.StatusUnauthorized)
			return
		}
		totpOK, err := auth.VerifyUserTOTP(r.Context(), h.db, user.ID, rawCode)
		if err != nil {
			slog.Error("verify user TOTP", "remote_addr", h.clientAddress(r), "error", err)
			http.Error(w, "failed to authenticate", http.StatusInternalServerError)
			return
		}
		if !totpOK {
			ip := h.clientAddress(r)
			blocked := h.loginProtection.Failed(ip, time.Now())
			slog.Warn("2FA verification failed", "remote_addr", ip, "cooldown_started", blocked)
			h.renderWithCSRF(w, r, "login.html", formData{
				Username:    username,
				Error:       "Código de autenticação (2FA) ou de recuperação inválido.",
				RequireTOTP: true,
			}, http.StatusUnauthorized)
			return
		}
	}
	h.loginProtection.Clear(h.clientAddress(r))
	if err := h.startSession(w, r, user.ID); err != nil {
		slog.Error("start authenticated session", "remote_addr", h.clientAddress(r), "error", err)
		http.Error(w, "failed to start session", http.StatusInternalServerError)
		return
	}
	h.plugins.DispatchAction(r.Context(), "user:login", map[string]any{
		"user_id": user.ID, "username": user.Username,
	})
	clearCookie(w, r, csrfCookieName)
	http.Redirect(w, r, "/admin/", http.StatusSeeOther)
}

func parseTrustedProxies(raw []string) ([]netip.Prefix, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	prefixes := make([]netip.Prefix, 0, len(raw))
	for _, entry := range raw {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if strings.Contains(entry, "/") {
			prefix, err := netip.ParsePrefix(entry)
			if err != nil {
				return nil, fmt.Errorf("invalid trusted proxy CIDR %q: %w", entry, err)
			}
			prefixes = append(prefixes, prefix.Masked())
			continue
		}
		addr, err := netip.ParseAddr(entry)
		if err != nil {
			return nil, fmt.Errorf("invalid trusted proxy IP %q: %w", entry, err)
		}
		addr = addr.Unmap()
		prefixes = append(prefixes, netip.PrefixFrom(addr, addr.BitLen()))
	}
	return prefixes, nil
}

func peerAddress(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return strings.TrimSpace(r.RemoteAddr)
	}
	return strings.TrimSpace(host)
}

func (h *handler) isTrustedAddr(addr netip.Addr) bool {
	if len(h.trustedProxies) == 0 {
		return false
	}
	addr = addr.Unmap()
	for _, prefix := range h.trustedProxies {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

func (h *handler) isTrustedProxy(r *http.Request) bool {
	if len(h.trustedProxies) == 0 {
		return false
	}
	addr, err := netip.ParseAddr(peerAddress(r))
	if err != nil {
		return false
	}
	return h.isTrustedAddr(addr)
}

func (h *handler) clientAddress(r *http.Request) string {
	peer := peerAddress(r)
	if !h.isTrustedProxy(r) {
		return peer
	}
	forwarded := strings.TrimSpace(r.Header.Get("X-Forwarded-For"))
	if forwarded == "" {
		return peer
	}
	parts := strings.Split(forwarded, ",")
	var leftmostValid string
	for i := len(parts) - 1; i >= 0; i-- {
		candidate := strings.TrimSpace(parts[i])
		addr, err := netip.ParseAddr(candidate)
		if err != nil {
			continue
		}
		addr = addr.Unmap()
		leftmostValid = addr.String()
		if !h.isTrustedAddr(addr) {
			return addr.String()
		}
	}
	if leftmostValid != "" {
		return leftmostValid
	}
	return peer
}

func (h *handler) isHTTPS(r *http.Request) bool {
	if r == nil {
		return false
	}
	if r.TLS != nil {
		return true
	}
	if !h.isTrustedProxy(r) {
		return false
	}
	proto := r.Header.Get("X-Forwarded-Proto")
	if first, _, found := strings.Cut(proto, ","); found {
		proto = first
	}
	return strings.EqualFold(strings.TrimSpace(proto), "https")
}

func requestIsHTTPS(r *http.Request) bool {
	if r == nil {
		return false
	}
	if r.TLS != nil {
		return true
	}
	if https, ok := r.Context().Value(httpsContextKey{}).(bool); ok {
		return https
	}
	return false
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
	kpis, checks := h.loadDashboardOverview(r.Context(), user)
	h.renderAdmin(w, adminPage{
		Mode:         "dashboard",
		Username:     user.Username,
		Role:         user.Role,
		CSRFToken:    token,
		Title:        "Visão geral",
		KPIs:         kpis,
		HealthChecks: checks,
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
		Secure:   requestIsHTTPS(r),
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
		Secure:   requestIsHTTPS(r),
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
		Secure:   requestIsHTTPS(r),
		SameSite: http.SameSiteStrictMode,
	})
}
