package web

import (
	"errors"
	"log"
	"mime"
	"net/http"
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
	items, err := h.media.List(r.Context())
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
	page := adminPage{
		Mode:          "media",
		Title:         "Mídias",
		Username:      user.Username,
		CSRFToken:     token,
		MediaItems:    items,
		MaxUploadSize: h.media.MaxSize(),
	}
	if r.URL.Query().Get("uploaded") == "1" {
		page.Notice = "Arquivo enviado com sucesso."
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

	_, err = h.media.Save(r.Context(), header.Filename, r.FormValue("alt_text"), file)
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
	item, err := h.media.FindByPath(r.Context(), publicPath)
	if errors.Is(err, media.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		log.Printf("lookup public upload: %v", err)
		http.Error(w, "failed to load file", http.StatusInternalServerError)
		return
	}
	path, err := h.media.ResolvePublicPath(item.Filepath)
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

	w.Header().Set("Content-Type", item.FileType)
	w.Header().Set("Content-Length", strconv.FormatInt(item.FileSize, 10))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("inline", map[string]string{"filename": item.Filename}))
	if item.FileType == "image/svg+xml" {
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
