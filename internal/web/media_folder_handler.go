package web

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
)

// MediaFolder represents a logical folder for media organisation.
type MediaFolder struct {
	ID       int64         `json:"id"`
	Name     string        `json:"name"`
	ParentID int64         `json:"parent_id"`
	Children []MediaFolder `json:"children,omitempty"`
}

func (h *handler) listMediaFolders(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	folders, err := loadFolders(r.Context(), h.db)
	if err != nil {
		log.Printf("list media folders: %v", err)
		http.Error(w, "Não foi possível carregar as pastas.", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(folders)
}

func (h *handler) createMediaFolder(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	if !validCSRFToken(r) {
		http.Error(w, "CSRF inválido.", http.StatusForbidden)
		return
	}
	var body struct {
		Name     string `json:"name"`
		ParentID int64  `json:"parent_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "JSON inválido.", http.StatusBadRequest)
		return
	}
	body.Name = strings.TrimSpace(body.Name)
	if body.Name == "" || len(body.Name) > 100 {
		http.Error(w, "Nome de pasta inválido.", http.StatusUnprocessableEntity)
		return
	}
	folder, err := createFolder(r.Context(), h.db, body.Name, body.ParentID)
	if err != nil {
		log.Printf("create media folder: %v", err)
		http.Error(w, "Não foi possível criar a pasta.", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(folder)
}

func (h *handler) deleteMediaFolder(w http.ResponseWriter, r *http.Request) {
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
	if _, err := h.db.ExecContext(r.Context(), "DELETE FROM media_folders WHERE id = ?", id); err != nil {
		log.Printf("delete media folder %d: %v", id, err)
		http.Error(w, "Não foi possível excluir a pasta.", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ── DB helpers ───────────────────────────────────────────────────────────────

func loadFolders(ctx context.Context, db *sql.DB) ([]MediaFolder, error) {
	rows, err := db.QueryContext(ctx, "SELECT id, name, COALESCE(parent_id,0) FROM media_folders ORDER BY name")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var flat []MediaFolder
	for rows.Next() {
		var f MediaFolder
		if err := rows.Scan(&f.ID, &f.Name, &f.ParentID); err != nil {
			return nil, err
		}
		flat = append(flat, f)
	}
	return buildFolderTree(flat), rows.Err()
}

func createFolder(ctx context.Context, db *sql.DB, name string, parentID int64) (MediaFolder, error) {
	var pid interface{}
	if parentID > 0 {
		pid = parentID
	}
	res, err := db.ExecContext(ctx, "INSERT INTO media_folders (name, parent_id) VALUES (?, ?)", name, pid)
	if err != nil {
		return MediaFolder{}, fmt.Errorf("insert folder: %w", err)
	}
	id, _ := res.LastInsertId()
	return MediaFolder{ID: id, Name: name, ParentID: parentID}, nil
}

func buildFolderTree(flat []MediaFolder) []MediaFolder {
	byID := make(map[int64]*MediaFolder, len(flat))
	for i := range flat {
		byID[flat[i].ID] = &flat[i]
	}
	var roots []MediaFolder
	for _, f := range flat {
		if f.ParentID == 0 {
			roots = append(roots, f)
		} else if parent, ok := byID[f.ParentID]; ok {
			parent.Children = append(parent.Children, f)
		}
	}
	return roots
}
