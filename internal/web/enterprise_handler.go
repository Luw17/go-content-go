package web

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Luw17/go-content-go/internal/auth"
	"github.com/Luw17/go-content-go/internal/config"
	"github.com/Luw17/go-content-go/internal/content"
	"github.com/Luw17/go-content-go/internal/database"
	"github.com/Luw17/go-content-go/internal/media"
	"github.com/Luw17/go-content-go/internal/siteconfig"
)

func (h *handler) userList(w http.ResponseWriter, r *http.Request) {
	user, ok := h.requireAdmin(w, r)
	if !ok {
		return
	}
	users, err := auth.Users(r.Context(), h.db)
	if err != nil {
		log.Printf("list users: %v", err)
		http.Error(w, "failed to load users", http.StatusInternalServerError)
		return
	}
	token, err := issueCSRFToken(w, r)
	if err != nil {
		http.Error(w, "failed to prepare user management", http.StatusInternalServerError)
		return
	}
	page := adminPage{
		Mode: "users", Title: "Usuários", Username: user.Username, Role: user.Role, CSRFToken: token, Users: users,
	}
	switch r.URL.Query().Get("status") {
	case "created":
		page.Notice = "Usuário criado."
	case "updated":
		page.Notice = "Usuário atualizado."
	case "password_updated":
		page.Notice = "Senha atualizada e sessões do usuário revogadas."
	case "sessions_revoked":
		page.Notice = "Sessões ativas do usuário revogadas."
	case "deleted":
		page.Notice = "Usuário excluído."
	case "last_admin":
		page.Error = "Não é possível remover, desativar ou rebaixar o último administrador ativo."
	case "invalid":
		page.Error = "Dados inválidos. Verifique o nome, a senha (mínimo 12 bytes) e o papel."
	case "exists":
		page.Error = "Esse nome de usuário já está em uso."
	}
	h.renderAdmin(w, page)
}

func (h *handler) createUser(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	if err := parseForm(w, r); err != nil || !validCSRFToken(r) {
		http.Error(w, "invalid form submission", http.StatusForbidden)
		return
	}
	_, err := auth.CreateUser(r.Context(), h.db, r.FormValue("username"), r.FormValue("password"), r.FormValue("role"))
	switch {
	case err == nil:
		http.Redirect(w, r, "/admin/users?status=created", http.StatusSeeOther)
	case errors.Is(err, auth.ErrInvalidUsername), errors.Is(err, auth.ErrInvalidPassword), errors.Is(err, auth.ErrInvalidRole):
		http.Redirect(w, r, "/admin/users?status=invalid", http.StatusSeeOther)
	default:
		if isUniqueConstraint(err) {
			http.Redirect(w, r, "/admin/users?status=exists", http.StatusSeeOther)
			return
		}
		log.Printf("create user: %v", err)
		http.Error(w, "failed to create user", http.StatusInternalServerError)
	}
}

func isUniqueConstraint(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "unique constraint")
}

func (h *handler) settings(w http.ResponseWriter, r *http.Request) {
	user, ok := h.requireAdmin(w, r)
	if !ok {
		return
	}
	token, err := issueCSRFToken(w, r)
	if err != nil {
		http.Error(w, "failed to prepare settings", http.StatusInternalServerError)
		return
	}
	settings, err := siteconfig.Load(r.Context(), h.db)
	if err != nil {
		log.Printf("load global settings: %v", err)
		http.Error(w, "failed to load settings", http.StatusInternalServerError)
		return
	}
	pages, err := content.PublishedPages(r.Context(), h.db)
	if err != nil {
		log.Printf("load published pages for settings: %v", err)
		http.Error(w, "failed to load settings", http.StatusInternalServerError)
		return
	}
	mediaItems, err := h.media.List(r.Context())
	if err != nil {
		log.Printf("load media for site identity settings: %v", err)
		http.Error(w, "failed to load settings", http.StatusInternalServerError)
		return
	}
	auditFindings := config.AuditPermissions(h.auditTargets())
	apiKeys, _ := auth.ListAPIKeys(r.Context(), h.db)
	var newAPIKey string
	if sessToken, err := sessionToken(r); err == nil {
		newAPIKey = h.consumeAPIKeyFlash(sessToken)
	}
	page := adminPage{
		Mode: "settings", Title: "Configurações", Username: user.Username, Role: user.Role, CSRFToken: token,
		SiteSettings: settings, Items: pages, MediaItems: mediaItems, PermissionFindings: auditFindings,
		APIKeys: apiKeys, NewAPIKey: newAPIKey,
	}
	if r.URL.Query().Get("backup") == "created" {
		page.Notice = "Backup criado em backups/."
	} else if r.URL.Query().Get("optimized") == "1" {
		page.Notice = "Banco de dados otimizado."
	} else if r.URL.Query().Get("saved") == "1" {
		page.Notice = "Configurações do site salvas."
	} else if r.URL.Query().Get("apikey") == "created" {
		page.Notice = "Chave de API criada. Copie agora — ela não será exibida novamente."
	} else if r.URL.Query().Get("apikey") == "deleted" {
		page.Notice = "Chave de API revogada."
	} else if r.URL.Query().Get("permissions") == "fixed" {
		page.Notice = "Permissões dos diretórios ajustadas com segurança."
	} else if r.URL.Query().Get("error") == "permissions" {
		page.Error = "Não foi possível alterar as permissões automaticamente (verifique o dono do diretório no sistema)."
	} else if r.URL.Query().Get("error") == "invalid" {
		page.Error = "Revise os campos: escolha uma página publicada e um logotipo de imagem válido."
	}
	h.renderAdmin(w, page)
}

func (h *handler) auditTargets() []config.AuditTarget {
	return []config.AuditTarget{
		{Label: "Diretório de uploads", Path: h.uploadDir},
		{Label: "Diretório de temas", Path: h.themeDir},
		{Label: "Diretório de plugins", Path: h.pluginDir},
		{Label: "Diretório de backups", Path: h.backupDir},
	}
}

func (h *handler) fixPermissions(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	if err := parseForm(w, r); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	if !validCSRFToken(r) {
		http.Error(w, "invalid form token", http.StatusForbidden)
		return
	}
	requestedPath := strings.TrimSpace(r.FormValue("path"))
	targets := h.auditTargets()
	if requestedPath != "" {
		cleanReq := filepath.Clean(requestedPath)
		filtered := make([]config.AuditTarget, 0, 1)
		for _, t := range targets {
			if t.Path != "" && filepath.Clean(t.Path) == cleanReq {
				filtered = append(filtered, t)
			}
		}
		targets = filtered
	}
	if _, err := config.FixPermissions(targets); err != nil {
		log.Printf("fix permissions: %v", err)
		http.Redirect(w, r, "/admin/settings?error=permissions", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/admin/settings?permissions=fixed", http.StatusSeeOther)
}

func (h *handler) saveSiteSettings(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	if err := parseForm(w, r); err != nil {
		http.Error(w, "invalid settings form", http.StatusBadRequest)
		return
	}
	if !validCSRFToken(r) {
		http.Error(w, "invalid form token", http.StatusForbidden)
		return
	}
	existingSettings, _ := siteconfig.Load(r.Context(), h.db)
	smtpPass := r.FormValue("smtp_pass")
	if smtpPass == "" && r.FormValue("clear_smtp_pass") != "1" {
		smtpPass = existingSettings.SMTPPass
	}
	settings := siteconfig.Settings{
		Title:                  r.FormValue("site_title"),
		Tagline:                r.FormValue("site_tagline"),
		Logo:                   r.FormValue("site_logo"),
		Favicon:                r.FormValue("site_favicon"),
		AppleTouchIcon:         r.FormValue("site_apple_touch_icon"),
		ShowOnFront:            r.FormValue("show_on_front"),
		HomeIntroTitle:         r.FormValue("home_intro_title"),
		HomePostsTitle:         r.FormValue("home_posts_title"),
		SiteURL:                r.FormValue("site_url"),
		SEODefaultDescription:  r.FormValue("seo_default_description"),
		SEODefaultOGImage:      r.FormValue("seo_default_og_image"),
		SEOTwitterHandle:       r.FormValue("seo_twitter_handle"),
		SEORobotsIndexing:      r.FormValue("seo_robots_indexing"),
		AutoRedirectSlugChange: r.FormValue("auto_redirect_slug_change") == "1" || r.FormValue("auto_redirect_slug_change") == "true",
		FormWebhookURL:         r.FormValue("form_webhook_url"),
		FormNotifyEmail:        r.FormValue("form_notify_email"),
		SMTPHost:               r.FormValue("smtp_host"),
		SMTPPort:               r.FormValue("smtp_port"),
		SMTPUser:               r.FormValue("smtp_user"),
		SMTPPass:               smtpPass,
		SMTPFrom:               r.FormValue("smtp_from"),
	}
	if _, hasNotifFields := r.PostForm["form_webhook_url"]; !hasNotifFields {
		settings.FormWebhookURL = existingSettings.FormWebhookURL
		settings.FormNotifyEmail = existingSettings.FormNotifyEmail
		settings.SMTPHost = existingSettings.SMTPHost
		settings.SMTPPort = existingSettings.SMTPPort
		settings.SMTPUser = existingSettings.SMTPUser
		settings.SMTPPass = existingSettings.SMTPPass
		settings.SMTPFrom = existingSettings.SMTPFrom
	}
	if settings.ShowOnFront == siteconfig.FrontPage {
		pageID, err := strconv.ParseInt(r.FormValue("page_on_front"), 10, 64)
		if err != nil || pageID <= 0 {
			http.Redirect(w, r, "/admin/settings?error=invalid", http.StatusSeeOther)
			return
		}
		settings.PageOnFront = pageID
	} else if settings.ShowOnFront != siteconfig.FrontPosts {
		http.Redirect(w, r, "/admin/settings?error=invalid", http.StatusSeeOther)
		return
	}
	for _, imgPath := range []struct {
		label string
		path  string
	}{
		{"site logo", settings.Logo},
		{"site favicon", settings.Favicon},
		{"apple touch icon", settings.AppleTouchIcon},
		{"default og image", settings.SEODefaultOGImage},
	} {
		if imgPath.path == "" {
			continue
		}
		asset, err := h.media.FindByPath(r.Context(), imgPath.path)
		if err != nil || !strings.HasPrefix(strings.ToLower(asset.FileType), "image/") {
			if err != nil && !errors.Is(err, media.ErrNotFound) {
				log.Printf("validate %s: %v", imgPath.label, err)
				http.Error(w, "failed to validate "+imgPath.label, http.StatusInternalServerError)
				return
			}
			http.Redirect(w, r, "/admin/settings?error=invalid", http.StatusSeeOther)
			return
		}
	}
	if settings.ShowOnFront == siteconfig.FrontPage {
		if _, err := content.PublishedPageByID(r.Context(), h.db, settings.PageOnFront); err != nil {
			if !errors.Is(err, content.ErrNotFound) {
				log.Printf("validate front page: %v", err)
				http.Error(w, "failed to validate front page", http.StatusInternalServerError)
				return
			}
			http.Redirect(w, r, "/admin/settings?error=invalid", http.StatusSeeOther)
			return
		}
	}
	if err := siteconfig.Save(r.Context(), h.db, settings); err != nil {
		if errors.Is(err, siteconfig.ErrInvalidSettings) {
			http.Redirect(w, r, "/admin/settings?error=invalid", http.StatusSeeOther)
			return
		}
		log.Printf("save global settings: %v", err)
		http.Error(w, "failed to save settings", http.StatusInternalServerError)
		return
	}
	h.cache.Clear()
	http.Redirect(w, r, "/admin/settings?saved=1", http.StatusSeeOther)
}

func (h *handler) createBackup(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	if err := parseForm(w, r); err != nil || !validCSRFToken(r) {
		http.Error(w, "invalid form submission", http.StatusForbidden)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
	defer cancel()
	if _, err := database.HotBackup(ctx, h.db, h.backupDir); err != nil {
		log.Printf("create database backup: %v", err)
		http.Error(w, "failed to create database backup", http.StatusInternalServerError)
		return
	}
	if _, err := database.PruneBackups(h.backupDir, h.backupRetention); err != nil {
		log.Printf("prune database backups: %v", err)
	}
	http.Redirect(w, r, "/admin/settings?backup=created", http.StatusSeeOther)
}

func (h *handler) downloadBackup(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	tmpDir, err := os.MkdirTemp("", "gocg-backup-download-*")
	if err != nil {
		log.Printf("create temporary backup directory for download: %v", err)
		http.Error(w, "failed to create database backup", http.StatusInternalServerError)
		return
	}
	defer os.RemoveAll(tmpDir)
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
	defer cancel()
	path, err := database.HotBackup(ctx, h.db, tmpDir)
	if err != nil {
		log.Printf("create database backup for download: %v", err)
		http.Error(w, "failed to create database backup", http.StatusInternalServerError)
		return
	}
	if err := copyBackupToResponse(w, filepath.Clean(path)); err != nil {
		log.Printf("download database backup: %v", err)
	}
}

func (h *handler) optimizeDatabase(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	if err := parseForm(w, r); err != nil || !validCSRFToken(r) {
		http.Error(w, "invalid form submission", http.StatusForbidden)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
	defer cancel()
	if err := database.Optimize(ctx, h.db); err != nil {
		log.Printf("optimize database: %v", err)
		http.Error(w, "failed to optimize database", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/admin/settings?optimized=1", http.StatusSeeOther)
}


func (h *handler) securityPage(w http.ResponseWriter, r *http.Request) {
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
		http.Error(w, "failed to prepare security page", http.StatusInternalServerError)
		return
	}
	var secret, uri string
	var remainingCodes int
	var flashedCodes []string
	if user.TOTPEnabled {
		remainingCodes, err = auth.RemainingRecoveryCodes(r.Context(), h.db, user.ID)
		if err != nil {
			log.Printf("count recovery codes for user %d: %v", user.ID, err)
		}
		if sessToken, err := sessionToken(r); err == nil {
			flashedCodes = h.consumeRecoveryFlash(sessToken)
		}
	} else {
		secret, err = auth.GenerateTOTPSecret()
		if err != nil {
			log.Printf("generate TOTP secret: %v", err)
			http.Error(w, "Não foi possível preparar a configuração de 2FA.", http.StatusInternalServerError)
			return
		}
		settings, _ := siteconfig.Load(r.Context(), h.db)
		uri = auth.TOTPProvisioningURI(settings.Title, user.Username, secret)
	}
	page := adminPage{
		Mode:                   "security",
		Title:                  "Segurança da Conta (2FA)",
		Username:               user.Username,
		Role:                   user.Role,
		CSRFToken:              token,
		TOTPEnabled:            user.TOTPEnabled,
		TOTPSecret:             secret,
		TOTPURI:                uri,
		RecoveryCodes:          flashedCodes,
		RemainingRecoveryCodes: remainingCodes,
	}
	switch r.URL.Query().Get("notice") {
	case "enabled":
		page.Notice = "Autenticação de dois fatores (2FA) ativada com sucesso. Guarde os códigos de recuperação abaixo em local seguro."
	case "disabled":
		page.Notice = "Autenticação de dois fatores (2FA) desativada."
	case "recovery":
		page.Notice = "Novos códigos de recuperação gerados. Os códigos anteriores foram invalidados."
	}
	if r.URL.Query().Get("error") == "invalid" {
		page.Error = "Código de 6 dígitos inválido. Confira o aplicativo autenticador e tente novamente."
	}
	h.renderAdmin(w, page)
}

func (h *handler) enableTOTP(w http.ResponseWriter, r *http.Request) {
	user, authenticated, err := h.currentUser(r)
	if err != nil {
		http.Error(w, "failed to validate session", http.StatusInternalServerError)
		return
	}
	if !authenticated {
		http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
		return
	}
	if err := parseForm(w, r); err != nil {
		http.Error(w, "Formulário inválido.", http.StatusBadRequest)
		return
	}
	if !validCSRFToken(r) {
		http.Error(w, "A sessão do formulário expirou.", http.StatusForbidden)
		return
	}
	if err := auth.EnableTOTP(r.Context(), h.db, user.ID, r.FormValue("totp_secret"), r.FormValue("totp_code")); errors.Is(err, auth.ErrInvalidTOTP) {
		http.Redirect(w, r, "/admin/security?error=invalid", http.StatusSeeOther)
		return
	} else if err != nil {
		log.Printf("enable TOTP for user %d: %v", user.ID, err)
		http.Error(w, "Não foi possível ativar o 2FA.", http.StatusInternalServerError)
		return
	}
	codes, err := auth.RegenerateRecoveryCodes(r.Context(), h.db, user.ID)
	if err != nil {
		log.Printf("generate recovery codes for user %d: %v", user.ID, err)
	} else if sessToken, err := sessionToken(r); err == nil {
		h.setRecoveryFlash(sessToken, codes)
	}
	http.Redirect(w, r, "/admin/security?notice=enabled", http.StatusSeeOther)
}

func (h *handler) regenerateRecoveryCodes(w http.ResponseWriter, r *http.Request) {
	user, authenticated, err := h.currentUser(r)
	if err != nil {
		http.Error(w, "failed to validate session", http.StatusInternalServerError)
		return
	}
	if !authenticated {
		http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
		return
	}
	if err := parseForm(w, r); err != nil {
		http.Error(w, "Formulário inválido.", http.StatusBadRequest)
		return
	}
	if !validCSRFToken(r) {
		http.Error(w, "A sessão do formulário expirou.", http.StatusForbidden)
		return
	}
	if !user.TOTPEnabled {
		http.Redirect(w, r, "/admin/security", http.StatusSeeOther)
		return
	}
	codes, err := auth.RegenerateRecoveryCodes(r.Context(), h.db, user.ID)
	if err != nil {
		log.Printf("regenerate recovery codes for user %d: %v", user.ID, err)
		http.Error(w, "Não foi possível gerar novos códigos de recuperação.", http.StatusInternalServerError)
		return
	}
	if sessToken, err := sessionToken(r); err == nil {
		h.setRecoveryFlash(sessToken, codes)
	}
	http.Redirect(w, r, "/admin/security?notice=recovery", http.StatusSeeOther)
}

func (h *handler) disableTOTP(w http.ResponseWriter, r *http.Request) {
	user, authenticated, err := h.currentUser(r)
	if err != nil {
		http.Error(w, "failed to validate session", http.StatusInternalServerError)
		return
	}
	if !authenticated {
		http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
		return
	}
	if err := parseForm(w, r); err != nil {
		http.Error(w, "Formulário inválido.", http.StatusBadRequest)
		return
	}
	if !validCSRFToken(r) {
		http.Error(w, "A sessão do formulário expirou.", http.StatusForbidden)
		return
	}
	if err := auth.DisableTOTP(r.Context(), h.db, user.ID); err != nil {
		log.Printf("disable TOTP for user %d: %v", user.ID, err)
		http.Error(w, "Não foi possível desativar o 2FA.", http.StatusInternalServerError)
		return
	}
	if sessToken, err := sessionToken(r); err == nil {
		h.consumeRecoveryFlash(sessToken)
	}
	http.Redirect(w, r, "/admin/security?notice=disabled", http.StatusSeeOther)
}

func (h *handler) setRecoveryFlash(sessionKey string, codes []string) {
	if sessionKey == "" || len(codes) == 0 {
		return
	}
	h.recoveryFlashMu.Lock()
	h.recoveryFlash[sessionKey] = append([]string(nil), codes...)
	h.recoveryFlashMu.Unlock()
}

func (h *handler) consumeRecoveryFlash(sessionKey string) []string {
	if sessionKey == "" {
		return nil
	}
	h.recoveryFlashMu.Lock()
	codes := h.recoveryFlash[sessionKey]
	delete(h.recoveryFlash, sessionKey)
	h.recoveryFlashMu.Unlock()
	return codes
}

func (h *handler) createAPIKey(w http.ResponseWriter, r *http.Request) {
	user, ok := h.requireAdmin(w, r)
	if !ok {
		return
	}
	_, plaintext, err := auth.CreateAPIKey(r.Context(), h.db, r.FormValue("key_name"), user.ID)
	if errors.Is(err, auth.ErrInvalidAPIKeyName) {
		http.Redirect(w, r, "/admin/settings?error=invalid", http.StatusSeeOther)
		return
	}
	if err != nil {
		log.Printf("create API key: %v", err)
		http.Error(w, "failed to create API key", http.StatusInternalServerError)
		return
	}
	if sessToken, err := sessionToken(r); err == nil {
		h.setAPIKeyFlash(sessToken, plaintext)
	}
	http.Redirect(w, r, "/admin/settings?apikey=created", http.StatusSeeOther)
}

func (h *handler) deleteAPIKey(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		http.NotFound(w, r)
		return
	}
	if err := auth.DeleteAPIKey(r.Context(), h.db, id); errors.Is(err, auth.ErrAPIKeyNotFound) {
		http.NotFound(w, r)
		return
	} else if err != nil {
		log.Printf("delete API key %d: %v", id, err)
		http.Error(w, "failed to delete API key", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/admin/settings?apikey=deleted", http.StatusSeeOther)
}

func (h *handler) setAPIKeyFlash(sessionKey, plaintext string) {
	if sessionKey == "" || plaintext == "" {
		return
	}
	h.apiKeyFlashMu.Lock()
	h.apiKeyFlash[sessionKey] = plaintext
	h.apiKeyFlashMu.Unlock()
}

func (h *handler) consumeAPIKeyFlash(sessionKey string) string {
	if sessionKey == "" {
		return ""
	}
	h.apiKeyFlashMu.Lock()
	val := h.apiKeyFlash[sessionKey]
	delete(h.apiKeyFlash, sessionKey)
	h.apiKeyFlashMu.Unlock()
	return val
}
