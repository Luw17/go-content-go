package web

import (
	"errors"
	"log"
	"net/http"
	"strconv"

	"github.com/Luw17/go-content-go/internal/redirects"
)

func (h *handler) redirectList(w http.ResponseWriter, r *http.Request) {
	user, ok := h.requireAdmin(w, r)
	if !ok {
		return
	}
	items, err := redirects.List(r.Context(), h.db)
	if err != nil {
		log.Printf("list redirects: %v", err)
		http.Error(w, "failed to load redirects", http.StatusInternalServerError)
		return
	}
	token, err := issueCSRFToken(w, r)
	if err != nil {
		http.Error(w, "failed to prepare redirects", http.StatusInternalServerError)
		return
	}
	page := adminPage{
		Mode:      "redirects",
		Title:     "Redirecionamentos",
		Username:  user.Username,
		Role:      user.Role,
		CSRFToken: token,
		Redirects: items,
	}
	switch r.URL.Query().Get("status") {
	case "saved":
		page.Notice = "Redirecionamento salvo."
	case "deleted":
		page.Notice = "Redirecionamento removido."
	case "invalid":
		page.Error = "Dados inválidos. Informe um caminho de origem local (ex: /antigo), destino diferente e código 301 ou 302."
	}
	h.renderAdmin(w, page)
}

func (h *handler) saveRedirect(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	if err := parseForm(w, r); err != nil || !validCSRFToken(r) {
		http.Error(w, "invalid form submission", http.StatusForbidden)
		return
	}
	statusCode, err := strconv.Atoi(r.FormValue("status_code"))
	if err != nil {
		statusCode = http.StatusMovedPermanently
	}
	if _, err := redirects.Save(r.Context(), h.db, r.FormValue("source_path"), r.FormValue("target_path"), statusCode); err != nil {
		if errors.Is(err, redirects.ErrInvalidRedirect) {
			http.Redirect(w, r, "/admin/redirects?status=invalid", http.StatusSeeOther)
			return
		}
		log.Printf("save redirect: %v", err)
		http.Error(w, "failed to save redirect", http.StatusInternalServerError)
		return
	}
	h.cache.Clear()
	http.Redirect(w, r, "/admin/redirects?status=saved", http.StatusSeeOther)
}

func (h *handler) deleteRedirect(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	if err := parseForm(w, r); err != nil || !validCSRFToken(r) {
		http.Error(w, "invalid form submission", http.StatusForbidden)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		http.NotFound(w, r)
		return
	}
	if err := redirects.Delete(r.Context(), h.db, id); errors.Is(err, redirects.ErrNotFound) {
		http.NotFound(w, r)
		return
	} else if err != nil {
		log.Printf("delete redirect %d: %v", id, err)
		http.Error(w, "failed to delete redirect", http.StatusInternalServerError)
		return
	}
	h.cache.Clear()
	http.Redirect(w, r, "/admin/redirects?status=deleted", http.StatusSeeOther)
}
