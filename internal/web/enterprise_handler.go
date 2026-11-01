package web

import (
	"context"
	"errors"
	"log"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Luw17/go-content-go/internal/auth"
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
		Mode: "users", Title: "Usuários", Username: user.Username, CSRFToken: token, Users: users,
	}
	switch r.URL.Query().Get("status") {
	case "created":
		page.Notice = "Usuário criado."
	case "invalid":
		page.Error = "Dados inválidos. Verifique o nome, a senha e o papel."
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
	page := adminPage{
		Mode: "settings", Title: "Configurações", Username: user.Username, CSRFToken: token,
		SiteSettings: settings, Items: pages, MediaItems: mediaItems,
	}
	if r.URL.Query().Get("backup") == "created" {
		page.Notice = "Backup criado em backups/."
	} else if r.URL.Query().Get("optimized") == "1" {
		page.Notice = "Banco de dados otimizado."
	} else if r.URL.Query().Get("saved") == "1" {
		page.Notice = "Configurações do site salvas."
	} else if r.URL.Query().Get("error") == "invalid" {
		page.Error = "Revise os campos: escolha uma página publicada e um logotipo de imagem válido."
	}
	h.renderAdmin(w, page)
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
	settings := siteconfig.Settings{
		Title:          r.FormValue("site_title"),
		Tagline:        r.FormValue("site_tagline"),
		Logo:           r.FormValue("site_logo"),
		ShowOnFront:    r.FormValue("show_on_front"),
		HomeIntroTitle: r.FormValue("home_intro_title"),
		HomePostsTitle: r.FormValue("home_posts_title"),
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
	if settings.Logo != "" {
		logo, err := h.media.FindByPath(r.Context(), settings.Logo)
		if err != nil || !strings.HasPrefix(strings.ToLower(logo.FileType), "image/") {
			if err != nil && !errors.Is(err, media.ErrNotFound) {
				log.Printf("validate site logo: %v", err)
				http.Error(w, "failed to validate site logo", http.StatusInternalServerError)
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
	http.Redirect(w, r, "/admin/settings?backup=created", http.StatusSeeOther)
}

func (h *handler) downloadBackup(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
	defer cancel()
	path, err := database.HotBackup(ctx, h.db, h.backupDir)
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
