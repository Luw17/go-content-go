package web

import (
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/Luw17/go-content-go/internal/portability"
)

func (h *handler) toolsPage(w http.ResponseWriter, r *http.Request) {
	user, ok := h.requireAdmin(w, r)
	if !ok {
		return
	}
	token, err := issueCSRFToken(w, r)
	if err != nil {
		http.Error(w, "failed to prepare tools page", http.StatusInternalServerError)
		return
	}
	page := adminPage{
		Mode:      "tools",
		Title:     "Importar e Exportar",
		Username:  user.Username,
		Role:      user.Role,
		CSRFToken: token,
	}
	if imported := r.URL.Query().Get("imported"); imported != "" {
		page.Notice = fmt.Sprintf("Importação concluída: %s item(ns) importado(s).", imported)
	} else if r.URL.Query().Get("error") == "invalid" {
		page.Error = "Não foi possível importar o arquivo. Verifique o formato selecionado (JSON, Markdown ou WordPress WXR)."
	}
	h.renderAdmin(w, page)
}

func (h *handler) exportContentJSON(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	data, err := portability.ExportJSON(r.Context(), h.db)
	if err != nil {
		slog.Error("export content JSON", "error", err)
		http.Error(w, "failed to export JSON", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="go-content-go-export.json"`)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func (h *handler) exportContentMarkdown(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	data, err := portability.ExportMarkdown(r.Context(), h.db)
	if err != nil {
		slog.Error("export content Markdown", "error", err)
		http.Error(w, "failed to export Markdown", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="go-content-go-export.md"`)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func (h *handler) importContent(w http.ResponseWriter, r *http.Request) {
	user, ok := h.requireAdmin(w, r)
	if !ok {
		return
	}
	format := strings.ToLower(strings.TrimSpace(r.FormValue("format")))
	file, _, err := r.FormFile("file")
	if err != nil {
		http.Redirect(w, r, "/admin/tools?error=invalid", http.StatusSeeOther)
		return
	}
	defer file.Close()

	var summary portability.ImportSummary
	switch format {
	case "json":
		summary, err = portability.ImportJSON(r.Context(), h.db, file, user.ID)
	case "markdown", "md":
		summary, err = portability.ImportMarkdown(r.Context(), h.db, file, user.ID)
	case "wxr", "wordpress":
		summary, err = portability.ImportWordPressWXR(r.Context(), h.db, file, user.ID)
	default:
		http.Redirect(w, r, "/admin/tools?error=invalid", http.StatusSeeOther)
		return
	}
	if err != nil {
		slog.Warn("content import failed", "format", format, "error", err)
		http.Redirect(w, r, "/admin/tools?error=invalid", http.StatusSeeOther)
		return
	}
	h.cache.Clear()
	http.Redirect(w, r, fmt.Sprintf("/admin/tools?imported=%d", summary.Imported), http.StatusSeeOther)
}
