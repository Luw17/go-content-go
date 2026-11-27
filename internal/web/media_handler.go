package web

import (
	"errors"
	"log"
	"mime"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/Luw17/go-content-go/internal/media"
)

const multipartMemoryLimit = 1 << 20

func (h *handler) mediaLibrary(w http.ResponseWriter, r *http.Request) {
	user, ok := h.requireAdmin(w, r)
	if !ok {
		return
	}
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	pageNum, ok := parsePageParam(r)
	if !ok {
		pageNum = 1
	}
	const mediaPerPage = 12
	items, total, err := h.media.ListFilteredPage(r.Context(), query, pageNum, mediaPerPage)
	if err != nil {
		log.Printf("list media library: %v", err)
		http.Error(w, "Não foi possível carregar a biblioteca de mídias.", http.StatusInternalServerError)
		return
	}
	token, err := issueCSRFToken(w, r)
	if err != nil {
		http.Error(w, "Não foi possível preparar a biblioteca de mídias.", http.StatusInternalServerError)
		return
	}
	extra := url.Values{}
	if query != "" {
		extra.Set("q", query)
	}
	mediaPagination := buildPagination("/admin/media", extra, pageNum, mediaPerPage, total)
	page := adminPage{
		Mode:            "media",
		Title:           "Mídias",
		Username:        user.Username,
		Role:            user.Role,
		CSRFToken:       token,
		MediaItems:      items,
		MaxUploadSize:   h.media.MaxSize(),
		FilterQuery:     query,
		AdminPagination: mediaPagination,
	}
	if r.URL.Query().Get("uploaded") == "1" {
		page.Notice = "Arquivo enviado com sucesso."
	} else if r.URL.Query().Get("updated") == "1" {
		page.Notice = "Metadados da mídia atualizados."
	} else if r.URL.Query().Get("error") == "invalid" {
		page.Error = "Os metadados excedem o limite permitido."
	}
	h.renderAdmin(w, page)
}

func (h *handler) uploadMedia(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	bodyLimit := h.media.MaxSize() + (64 << 10)
	r.Body = http.MaxBytesReader(w, r.Body, bodyLimit)
	if err := r.ParseMultipartForm(multipartMemoryLimit); err != nil {
		if isRequestTooLarge(err) {
			h.mediaActionError(w, r, http.StatusRequestEntityTooLarge, "O arquivo excede o limite de tamanho.")
			return
		}
		h.mediaActionError(w, r, http.StatusBadRequest, "Envie um formulário multipart válido.")
		return
	}
	if !validCSRFToken(r) {
		h.mediaActionError(w, r, http.StatusForbidden, "A sessão do formulário expirou. Atualize a página e tente novamente.")
		return
	}
	files := r.MultipartForm.File["file"]
	if len(files) != 1 || len(r.MultipartForm.File) != 1 {
		h.mediaActionError(w, r, http.StatusBadRequest, "Selecione um único arquivo.")
		return
	}
	header := files[0]
	if header.Size > h.media.MaxSize() {
		h.mediaActionError(w, r, http.StatusRequestEntityTooLarge, "O arquivo excede o limite de tamanho.")
		return
	}
	file, err := header.Open()
	if err != nil {
		log.Printf("open media upload: %v", err)
		h.mediaActionError(w, r, http.StatusBadRequest, "Não foi possível ler o arquivo enviado.")
		return
	}
	defer file.Close()

	description := r.FormValue("description")
	folderIDStr := r.FormValue("folder_id")
	var folderID int64
	if folderIDStr != "" {
		folderID, _ = strconv.ParseInt(folderIDStr, 10, 64)
	}
	_, err = h.media.Save(r.Context(), header.Filename, r.FormValue("alt_text"), description, folderID, file)
	if errors.Is(err, media.ErrTooLarge) {
		h.mediaActionError(w, r, http.StatusRequestEntityTooLarge, "O arquivo excede o limite de tamanho.")
		return
	}
	if errors.Is(err, media.ErrInvalidFile) {
		h.mediaActionError(w, r, http.StatusUnprocessableEntity, "Tipo de arquivo inválido. Use JPG, PNG, WebP, GIF, SVG ou PDF válido.")
		return
	}
	if err != nil {
		log.Printf("save media upload: %v", err)
		h.mediaActionError(w, r, http.StatusInternalServerError, "Não foi possível salvar o arquivo. Verifique as permissões e tente novamente.")
		return
	}
	if r.Header.Get("HX-Request") == "true" && r.Header.Get("HX-Boosted") != "true" {
		items, err := h.media.List(r.Context())
		if err != nil {
			log.Printf("refresh media library after upload: %v", err)
			h.mediaActionError(w, r, http.StatusInternalServerError, "Arquivo enviado, mas não foi possível atualizar a biblioteca.")
			return
		}
		token, err := issueCSRFToken(w, r)
		if err != nil {
			h.mediaActionError(w, r, http.StatusInternalServerError, "Arquivo enviado, mas não foi possível atualizar a biblioteca.")
			return
		}
		h.renderFragment(w, "media-gallery", adminPage{
			CSRFToken:     token,
			MediaItems:    items,
			MaxUploadSize: h.media.MaxSize(),
			Notice:        "Arquivo enviado com sucesso.",
		})
		return
	}
	http.Redirect(w, r, "/admin/media?uploaded=1", http.StatusSeeOther)
}

func (h *handler) deleteMedia(w http.ResponseWriter, r *http.Request) {
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
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		http.NotFound(w, r)
		return
	}
	if err := h.media.Delete(r.Context(), id); err != nil {
		if errors.Is(err, media.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		log.Printf("delete media %d: %v", id, err)
		h.mediaActionError(w, r, http.StatusInternalServerError, "Não foi possível excluir a mídia.")
		return
	}
	if r.Header.Get("HX-Request") == "true" && r.Header.Get("HX-Boosted") != "true" {
		items, err := h.media.List(r.Context())
		if err != nil {
			log.Printf("refresh media library after deletion: %v", err)
			http.Error(w, "Não foi possível atualizar a biblioteca.", http.StatusInternalServerError)
			return
		}
		token, err := issueCSRFToken(w, r)
		if err != nil {
			http.Error(w, "Não foi possível atualizar a biblioteca.", http.StatusInternalServerError)
			return
		}
		h.renderFragment(w, "media-gallery", adminPage{
			CSRFToken:     token,
			MediaItems:    items,
			MaxUploadSize: h.media.MaxSize(),
			Notice:        "Mídia excluída.",
		})
		return
	}
	http.Redirect(w, r, "/admin/media", http.StatusSeeOther)
}

func (h *handler) serveUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	publicPath := r.URL.Path
	if _, err := h.media.ResolvePublicPath(publicPath); err != nil {
		http.NotFound(w, r)
		return
	}
	item, isThumb, err := h.media.FindByPublicPath(r.Context(), publicPath)
	if errors.Is(err, media.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		log.Printf("lookup public upload: %v", err)
		http.Error(w, "failed to load file", http.StatusInternalServerError)
		return
	}
	targetPublicPath := item.Filepath
	contentType := item.FileType
	if isThumb {
		targetPublicPath = item.ThumbnailPath
		contentType = "image/jpeg"
	}
	path, err := h.media.ResolvePublicPath(targetPublicPath)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			http.NotFound(w, r)
			return
		}
		log.Printf("open public upload: %v", err)
		http.Error(w, "failed to load file", http.StatusInternalServerError)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Length", strconv.FormatInt(info.Size(), 10))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("inline", map[string]string{"filename": item.Filename}))
	if contentType == "image/svg+xml" {
		w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	}
	http.ServeContent(w, r, item.Filename, info.ModTime(), file)
}

func (h *handler) mediaActionError(w http.ResponseWriter, r *http.Request, status int, message string) {
	if r.Header.Get("HX-Request") != "true" || r.Header.Get("HX-Boosted") == "true" {
		http.Error(w, message, status)
		return
	}
	user, ok := h.requireAdmin(w, r)
	if !ok {
		return
	}
	items, err := h.media.List(r.Context())
	if err != nil {
		log.Printf("load media library after error: %v", err)
		http.Error(w, message, status)
		return
	}
	token, err := r.Cookie(csrfCookieName)
	if err != nil {
		http.Error(w, message, status)
		return
	}
	h.renderFragmentStatus(w, "media-gallery", adminPage{
		Username:      user.Username,
		CSRFToken:     token.Value,
		MediaItems:    items,
		MaxUploadSize: h.media.MaxSize(),
		Error:         message,
	}, status)
}

func isRequestTooLarge(err error) bool {
	var maxBytesError *http.MaxBytesError
	return errors.As(err, &maxBytesError) || (err != nil && strings.Contains(err.Error(), "request body too large"))
}

func (h *handler) updateMediaMetadata(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		http.NotFound(w, r)
		return
	}
	err = h.media.UpdateMetadata(r.Context(), id, r.FormValue("alt_text"), r.FormValue("description"))
	if errors.Is(err, media.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if errors.Is(err, media.ErrInvalidFile) {
		http.Redirect(w, r, "/admin/media?error=invalid", http.StatusSeeOther)
		return
	}
	if err != nil {
		log.Printf("update media metadata %d: %v", id, err)
		http.Error(w, "failed to update media metadata", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/admin/media?updated=1", http.StatusSeeOther)
}
