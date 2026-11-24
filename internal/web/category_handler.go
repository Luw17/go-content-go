package web

import (
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/Luw17/go-content-go/internal/content"
)

// categoryList renders the category manager page (GET /admin/categories).
func (h *handler) categoryList(w http.ResponseWriter, r *http.Request) {
	user, ok := h.requireAdmin(w, r)
	if !ok {
		return
	}
	cats, err := content.CategoriesWithParents(r.Context(), h.db)
	if err != nil {
		log.Printf("list categories: %v", err)
		http.Error(w, "Não foi possível carregar as categorias.", http.StatusInternalServerError)
		return
	}
	token, err := issueCSRFToken(w, r)
	if err != nil {
		http.Error(w, "Falha ao preparar a página.", http.StatusInternalServerError)
		return
	}
	page := adminPage{
		Mode:       "categories",
		Title:      "Categorias",
		Username:   user.Username,
		Role:       user.Role,
		CSRFToken:  token,
		Categories: cats,
	}
	if r.URL.Query().Get("saved") == "1" {
		page.Notice = "Categoria salva com sucesso."
	}
	if r.URL.Query().Get("deleted") == "1" {
		page.Notice = "Categoria excluída."
	}
	if r.URL.Query().Get("error") == "invalid" {
		page.Error = "Informe um nome válido para a categoria."
	}
	h.renderAdmin(w, page)
}

// saveCategory handles POST /admin/categories/save (standard HTML form or JSON).
func (h *handler) saveCategory(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	if !validCSRFToken(r) {
		http.Error(w, "CSRF inválido.", http.StatusForbidden)
		return
	}

	isJSON := strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "application/json")
	var input content.CategoryInput
	if isJSON {
		var body struct {
			ID        int64   `json:"id"`
			Name      string  `json:"name"`
			ParentIDs []int64 `json:"parent_ids"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "JSON inválido.", http.StatusBadRequest)
			return
		}
		input = content.CategoryInput{ID: body.ID, Name: body.Name, ParentIDs: body.ParentIDs}
	} else {
		if err := parseForm(w, r); err != nil {
			http.Error(w, "Formulário inválido.", http.StatusBadRequest)
			return
		}
		id, err := optionalID(r.FormValue("id"))
		if err != nil {
			http.Redirect(w, r, "/admin/categories?error=invalid", http.StatusSeeOther)
			return
		}
		parentIDs, err := parseCategoryIDs(r.Form["parent_ids"])
		if err != nil {
			http.Redirect(w, r, "/admin/categories?error=invalid", http.StatusSeeOther)
			return
		}
		input = content.CategoryInput{
			ID:        id,
			Name:      r.FormValue("name"),
			ParentIDs: parentIDs,
		}
	}

	cat, err := content.SaveCategory(r.Context(), h.db, input)
	if err != nil {
		log.Printf("save category: %v", err)
		if isJSON {
			http.Error(w, "Não foi possível salvar a categoria.", http.StatusUnprocessableEntity)
			return
		}
		http.Redirect(w, r, "/admin/categories?error=invalid", http.StatusSeeOther)
		return
	}
	h.cache.Clear()
	if isJSON {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(cat)
		return
	}
	http.Redirect(w, r, "/admin/categories?saved=1", http.StatusSeeOther)
}

// deleteCategory handles POST /admin/categories/{id}/delete.
func (h *handler) deleteCategory(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	if err := parseForm(w, r); err != nil {
		http.Error(w, "Formulário inválido.", http.StatusBadRequest)
		return
	}
	if !validCSRFToken(r) {
		http.Error(w, "CSRF inválido.", http.StatusForbidden)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		http.NotFound(w, r)
		return
	}
	if err := content.DeleteCategory(r.Context(), h.db, id); err != nil {
		log.Printf("delete category %d: %v", id, err)
		http.Error(w, "Não foi possível excluir a categoria.", http.StatusInternalServerError)
		return
	}
	h.cache.Clear()
	http.Redirect(w, r, "/admin/categories?deleted=1", http.StatusSeeOther)
}

// reorderCategories handles POST /admin/categories/reorder.
func (h *handler) reorderCategories(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	if !validCSRFToken(r) {
		http.Error(w, "CSRF inválido.", http.StatusForbidden)
		return
	}
	if strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "application/json") {
		var body struct {
			Order []int64 `json:"order"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "JSON inválido.", http.StatusBadRequest)
			return
		}
	} else {
		if err := parseForm(w, r); err != nil {
			http.Error(w, "Formulário inválido.", http.StatusBadRequest)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}
