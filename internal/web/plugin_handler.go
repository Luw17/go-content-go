package web

import (
	"errors"
	"html/template"
	"log"
	"net/http"
	"sort"

	"github.com/Luw17/go-content-go/internal/plugins"
	"github.com/microcosm-cc/bluemonday"
)

type pluginSettingRow struct {
	Key   string
	Value string
}

var pluginAdminHTMLPolicy = bluemonday.UGCPolicy()

func (h *handler) pluginList(w http.ResponseWriter, r *http.Request) {
	user, ok := h.requireAdmin(w, r)
	if !ok {
		return
	}
	items, err := h.plugins.List(r.Context(), h.db)
	if err != nil {
		log.Printf("list plugins: %v", err)
		http.Error(w, "Não foi possível carregar os plugins.", http.StatusInternalServerError)
		return
	}
	token, err := issueCSRFToken(w, r)
	if err != nil {
		http.Error(w, "Não foi possível preparar a página de plugins.", http.StatusInternalServerError)
		return
	}
	page := adminPage{
		Mode:      "plugins",
		Title:     "Plugins",
		Username:  user.Username,
		Role:      user.Role,
		CSRFToken: token,
		Plugins:   items,
	}
	switch r.URL.Query().Get("updated") {
	case "activated":
		page.Notice = "Plugin ativado."
	case "deactivated":
		page.Notice = "Plugin desativado."
	}
	if r.URL.Query().Get("installed") == "1" {
		page.Notice = "Plugin instalado e disponível para ativação."
	}
	page.Error = packageInstallErrorMessage(r.URL.Query().Get("install_error"))
	h.renderAdmin(w, page)
}

func (h *handler) activatePlugin(w http.ResponseWriter, r *http.Request) {
	h.updatePlugin(w, r, true)
}

func (h *handler) deactivatePlugin(w http.ResponseWriter, r *http.Request) {
	h.updatePlugin(w, r, false)
}

func (h *handler) updatePlugin(w http.ResponseWriter, r *http.Request, activate bool) {
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
	id := r.PathValue("id")
	if !plugins.ValidID(id) {
		http.NotFound(w, r)
		return
	}
	if err := h.plugins.SetActive(r.Context(), h.db, id, activate); err != nil {
		if errors.Is(err, plugins.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		log.Printf("change plugin %q activation: %v", id, err)
		http.Error(w, "Não foi possível alterar o status do plugin. Verifique o manifesto e o script.", http.StatusUnprocessableEntity)
		return
	}
	h.cache.Clear()
	status := "deactivated"
	if activate {
		status = "activated"
	}
	http.Redirect(w, r, "/admin/plugins?updated="+status, http.StatusSeeOther)
}

func (h *handler) pluginAdminPage(w http.ResponseWriter, r *http.Request) {
	user, ok := h.requireAdmin(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	if !plugins.ValidID(id) {
		http.NotFound(w, r)
		return
	}
	items, err := h.plugins.List(r.Context(), h.db)
	if err != nil {
		log.Printf("list plugins for admin page %q: %v", id, err)
		http.Error(w, "Não foi possível carregar o plugin.", http.StatusInternalServerError)
		return
	}
	var target plugins.Manifest
	found := false
	for _, item := range items {
		if item.ID == id {
			target = item
			found = true
			break
		}
	}
	if !found {
		http.NotFound(w, r)
		return
	}

	pageTitle := target.Name
	if pageTitle == "" {
		pageTitle = target.ID
	}
	var customHTML template.HTML
	if target.Active {
		view, registered, err := h.plugins.RenderAdminPage(r.Context(), id)
		if err != nil {
			log.Printf("render plugin admin page %q: %v", id, err)
		} else if registered {
			if view.Title != "" {
				pageTitle = view.Title
			}
			customHTML = template.HTML(pluginAdminHTMLPolicy.Sanitize(view.HTML))
		}
	}

	rawSettings, err := plugins.AllSettings(r.Context(), h.db, id)
	if err != nil {
		log.Printf("load settings for plugin %q: %v", id, err)
		http.Error(w, "Não foi possível carregar as configurações do plugin.", http.StatusInternalServerError)
		return
	}
	keys := make([]string, 0, len(rawSettings))
	for k := range rawSettings {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	settingRows := make([]pluginSettingRow, 0, len(keys))
	for _, k := range keys {
		settingRows = append(settingRows, pluginSettingRow{Key: k, Value: rawSettings[k]})
	}

	token, err := issueCSRFToken(w, r)
	if err != nil {
		http.Error(w, "Não foi possível preparar a página do plugin.", http.StatusInternalServerError)
		return
	}
	page := adminPage{
		Mode:            "plugin-page",
		Title:           pageTitle,
		Username:        user.Username,
		Role:            user.Role,
		CSRFToken:       token,
		PluginManifest:  target,
		PluginPageHTML:  customHTML,
		PluginSettings:  settingRows,
	}
	switch r.URL.Query().Get("notice") {
	case "saved":
		page.Notice = "Configuração do plugin salva."
	case "deleted":
		page.Notice = "Configuração do plugin removida."
	}
	if r.URL.Query().Get("error") == "invalid" {
		page.Error = "Chave ou valor inválido para a configuração do plugin."
	}
	h.renderAdmin(w, page)
}

func (h *handler) savePluginSetting(w http.ResponseWriter, r *http.Request) {
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
	id := r.PathValue("id")
	if !plugins.ValidID(id) {
		http.NotFound(w, r)
		return
	}
	if err := plugins.SetSetting(r.Context(), h.db, id, r.FormValue("setting_key"), r.FormValue("setting_value")); errors.Is(err, plugins.ErrInvalidSetting) {
		http.Redirect(w, r, "/admin/plugin/"+id+"?error=invalid", http.StatusSeeOther)
		return
	} else if err != nil {
		log.Printf("save plugin setting for %q: %v", id, err)
		http.Error(w, "Não foi possível salvar a configuração do plugin.", http.StatusInternalServerError)
		return
	}
	h.cache.Clear()
	http.Redirect(w, r, "/admin/plugin/"+id+"?notice=saved", http.StatusSeeOther)
}

func (h *handler) deletePluginSetting(w http.ResponseWriter, r *http.Request) {
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
	id := r.PathValue("id")
	if !plugins.ValidID(id) {
		http.NotFound(w, r)
		return
	}
	if err := plugins.DeleteSetting(r.Context(), h.db, id, r.FormValue("setting_key")); errors.Is(err, plugins.ErrInvalidSetting) {
		http.Redirect(w, r, "/admin/plugin/"+id+"?error=invalid", http.StatusSeeOther)
		return
	} else if err != nil {
		log.Printf("delete plugin setting for %q: %v", id, err)
		http.Error(w, "Não foi possível remover a configuração do plugin.", http.StatusInternalServerError)
		return
	}
	h.cache.Clear()
	http.Redirect(w, r, "/admin/plugin/"+id+"?notice=deleted", http.StatusSeeOther)
}
