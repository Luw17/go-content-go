package web

import (
	"errors"
	"log"
	"net/http"

	"github.com/Luw17/go-content-go/internal/plugins"
)

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
