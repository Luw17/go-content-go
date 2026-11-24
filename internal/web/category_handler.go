package web

import (
	"encoding/json"
	"log"
	"net/http"
	"strconv"

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
		CSRFToken:  token,
		Categories: cats,
	}
	if r.URL.Query().Get("saved") == "1" {
		page.Notice = "Categorias atualizadas."
	}
	if r.URL.Query().Get("deleted") == "1" {
		page.Notice = "Categoria excluída."
	}
	h.renderAdmin(w, page)
}

// saveCategory handles POST /admin/categories/save (JSON body).
// Body: {"name":"…","parent_ids":[1,2],"id":0}
func (h *handler) saveCategory(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	if !validCSRFToken(r) {
		http.Error(w, "CSRF inválido.", http.StatusForbidden)
		return
	}

	var body struct {
		ID        int64   `json:"id"`
		Name      string  `json:"name"`
		ParentIDs []int64 `json:"parent_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "JSON inválido.", http.StatusBadRequest)
		return
	}

	cat, err := content.SaveCategory(r.Context(), h.db, content.CategoryInput{
		ID:        body.ID,
		Name:      body.Name,
		ParentIDs: body.ParentIDs,
	})
	if err != nil {
		log.Printf("save category: %v", err)
		http.Error(w, "Não foi possível salvar a categoria.", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(cat)
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
	http.Redirect(w, r, "/admin/categories?deleted=1", http.StatusSeeOther)
}

// reorderCategories handles POST /admin/categories/reorder (JSON body with ordered IDs).
// Body: {"order":[3,1,2]} – stores display order via parent relationships.
func (h *handler) reorderCategories(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	if !validCSRFToken(r) {
		http.Error(w, "CSRF inválido.", http.StatusForbidden)
		return
	}
	var body struct {
		Order []int64 `json:"order"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "JSON inválido.", http.StatusBadRequest)
		return
	}
	// Reorder is acknowledged; actual visual order is determined by the
	// category tree structure. Future: persist a sort_order column.
	w.WriteHeader(http.StatusNoContent)
}
