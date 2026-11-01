package web

import (
	"bytes"
	"errors"
	"html/template"
	"io"
	"log"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	themeengine "github.com/Luw17/go-content-go/themes"
	"github.com/microcosm-cc/bluemonday"
)

func (h *handler) appearance(w http.ResponseWriter, r *http.Request) {
	user, ok := h.requireAdmin(w, r)
	if !ok {
		return
	}
	available, err := h.themes.List()
	if err != nil {
		log.Printf("list themes: %v", err)
		http.Error(w, "Não foi possível carregar os temas.", http.StatusInternalServerError)
		return
	}
	configured, err := h.themes.Active(r.Context(), h.db)
	if err != nil {
		log.Printf("load active theme: %v", err)
		http.Error(w, "Não foi possível carregar o tema ativo.", http.StatusInternalServerError)
		return
	}
	active, _, err := h.themes.Resolve(configured)
	if err != nil {
		log.Printf("resolve active theme: %v", err)
		http.Error(w, "Não foi possível carregar o tema ativo.", http.StatusInternalServerError)
		return
	}
	token, err := issueCSRFToken(w, r)
	if err != nil {
		http.Error(w, "Não foi possível preparar a página de aparência.", http.StatusInternalServerError)
		return
	}
	page := adminPage{
		Mode:        "appearance",
		Title:       "Aparência",
		Username:    user.Username,
		CSRFToken:   token,
		Themes:      available,
		ActiveTheme: active,
	}
	if configured != active {
		page.Error = "O tema configurado não está disponível ou é inválido. O tema padrão está sendo usado."
	}
	if r.URL.Query().Get("activated") == "1" {
		page.Notice = "Tema ativado."
	}
	h.renderAdmin(w, page)
}

func (h *handler) activateTheme(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
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
	name := r.PathValue("name")
	if !themeengine.ValidName(name) {
		http.NotFound(w, r)
		return
	}
	available, err := h.themes.List()
	if err != nil {
		log.Printf("list themes for activation: %v", err)
		http.Error(w, "Não foi possível ativar o tema.", http.StatusInternalServerError)
		return
	}
	found := false
	for _, theme := range available {
		if theme.ID == name {
			found = true
			break
		}
	}
	if !found {
		http.NotFound(w, r)
		return
	}
	if err := h.themes.Validate(name); err != nil {
		log.Printf("validate theme %q: %v", name, err)
		http.Error(w, "Os arquivos do tema não puderam ser carregados.", http.StatusUnprocessableEntity)
		return
	}
	if _, err := h.db.ExecContext(r.Context(), `
		INSERT INTO options (key, value, updated_at) VALUES ('active_theme', ?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at
	`, name, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		log.Printf("save active theme %q: %v", name, err)
		http.Error(w, "Não foi possível salvar o tema ativo.", http.StatusInternalServerError)
		return
	}
	h.cache.Clear()
	http.Redirect(w, r, "/admin/appearance?activated=1", http.StatusSeeOther)
}

func (h *handler) serveThemeAsset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	requested := strings.TrimPrefix(r.URL.Path, "/themes/")
	themeName, assetPath, found := strings.Cut(requested, "/")
	if !found || !themeengine.ValidName(themeName) {
		http.NotFound(w, r)
		return
	}
	switch strings.ToLower(filepath.Ext(assetPath)) {
	case ".css", ".js", ".mjs", ".png", ".jpg", ".jpeg", ".webp", ".gif",
		".svg", ".ico", ".woff", ".woff2", ".ttf", ".otf":
	default:
		http.NotFound(w, r)
		return
	}
	configured, err := h.themes.Active(r.Context(), h.db)
	if err != nil {
		log.Printf("load active theme for asset: %v", err)
		http.Error(w, "failed to load theme asset", http.StatusInternalServerError)
		return
	}
	active, _, err := h.themes.Resolve(configured)
	if err != nil {
		log.Printf("resolve active theme for asset: %v", err)
		http.Error(w, "failed to load theme asset", http.StatusInternalServerError)
		return
	}
	if themeName != active {
		http.NotFound(w, r)
		return
	}
	file, err := h.themes.OpenAsset(active, assetPath)
	if errors.Is(err, themeengine.ErrNotFound) || errors.Is(err, os.ErrNotExist) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		log.Printf("open theme asset %q: %v", assetPath, err)
		http.Error(w, "failed to load theme asset", http.StatusInternalServerError)
		return
	}
	defer func() {
		if err := file.Close(); err != nil {
			log.Printf("close theme asset %q: %v", assetPath, err)
		}
	}()
	info, err := file.Stat()
	if errors.Is(err, os.ErrNotExist) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		log.Printf("inspect theme asset %q: %v", assetPath, err)
		http.Error(w, "failed to load theme asset", http.StatusInternalServerError)
		return
	}
	if !info.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}
	contentType := mime.TypeByExtension(filepath.Ext(assetPath))
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Length", strconv.FormatInt(info.Size(), 10))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		if _, err := io.Copy(w, file); err != nil {
			log.Printf("stream theme asset %q: %v", assetPath, err)
		}
	}
}

func (h *handler) renderTheme(w http.ResponseWriter, r *http.Request, templateName string, status int, data func(string, template.HTML) any) {
	configured, err := h.themes.Active(r.Context(), h.db)
	if err != nil {
		log.Printf("load active theme for public page: %v", err)
		http.Error(w, "Não foi possível carregar o site.", http.StatusInternalServerError)
		return
	}
	active, templates, err := h.themes.Resolve(configured)
	if err != nil {
		log.Printf("resolve theme for public page: %v", err)
		http.Error(w, "Não foi possível renderizar o site.", http.StatusInternalServerError)
		return
	}
	headTags := h.filteredHeadTags(r)
	var rendered bytes.Buffer
	if err := templates.ExecuteTemplate(&rendered, templateName, data(active, headTags)); err != nil {
		log.Printf("render public theme %q template %s: %v", active, templateName, err)
		http.Error(w, "Não foi possível renderizar o site.", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_, _ = w.Write(rendered.Bytes())
}

func (h *handler) filteredHeadTags(r *http.Request) template.HTML {
	raw, ok := h.plugins.ApplyFilters(r.Context(), "head:inject_tags", "").(string)
	if !ok {
		log.Printf("plugin filter head:inject_tags returned a non-string value; discarding plugin tags")
		return ""
	}
	policy := bluemonday.NewPolicy()
	policy.AllowElements("meta", "link")
	policy.AllowAttrs("name", "content", "property", "rel", "href", "type", "charset", "hreflang", "media").
		OnElements("meta", "link")
	policy.AllowURLSchemes("http", "https")
	return template.HTML(policy.Sanitize(raw))
}
