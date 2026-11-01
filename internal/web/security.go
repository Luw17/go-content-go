package web

import (
	"log/slog"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/Luw17/go-content-go/internal/packagezip"
)

const securityContentPolicy = "default-src 'self'; base-uri 'self'; object-src 'none'; frame-ancestors 'self'; form-action 'self'; img-src 'self' data: https:; style-src 'self'; script-src 'self'; connect-src 'self'; font-src 'self' data:"

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", securityContentPolicy)
		w.Header().Set("X-Frame-Options", "SAMEORIGIN")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		if internalRequestPath(r.URL.Path) {
			http.NotFound(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func internalRequestPath(requestPath string) bool {
	cleaned := path.Clean("/" + requestPath)
	segments := strings.Split(strings.Trim(strings.ToLower(cleaned), "/"), "/")
	for index, segment := range segments {
		switch {
		case segment == ".git":
			return true
		case index == 0 && (segment == "data" || segment == "backups" || segment == "internal" || segment == "logs" || segment == "plugins"):
			return true
		}
	}
	lower := strings.ToLower(cleaned)
	for _, suffix := range []string{".db", ".sqlite", ".sqlite3", ".log", ".db-wal", ".db-shm", ".db-journal"} {
		if strings.HasSuffix(lower, suffix) {
			return true
		}
	}
	return false
}

func (h *handler) csrfMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/admin/") || !isMutatingMethod(r.Method) {
			next.ServeHTTP(w, r)
			return
		}
		if r.URL.Path != "/admin/login" && r.URL.Path != "/admin/setup" {
			_, authenticated, err := h.currentUser(r)
			if err != nil {
				slog.Error("validate session before administrative mutation", "path", r.URL.Path, "error", err)
				http.Error(w, "Não foi possível validar a sessão.", http.StatusInternalServerError)
				return
			}
			if !authenticated {
				http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
				return
			}
		}
		r.Body = http.MaxBytesReader(w, r.Body, adminRequestBodyLimit(r.URL.Path, h.media.MaxSize()))
		var err error
		if strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "multipart/form-data") {
			err = r.ParseMultipartForm(multipartMemoryLimit)
			if r.MultipartForm != nil {
				defer removeMultipartTempFiles(r)
			}
		} else {
			err = r.ParseForm()
		}
		if err != nil {
			if isRequestTooLarge(err) {
				http.Error(w, "A requisição excede o limite permitido.", http.StatusRequestEntityTooLarge)
				return
			}
			http.Error(w, "Formulário inválido.", http.StatusBadRequest)
			return
		}
		if !validCSRFToken(r) {
			slog.Warn("administrative request rejected by CSRF protection", "method", r.Method, "path", r.URL.Path, "remote_addr", clientAddress(r))
			http.Error(w, "A sessão do formulário expirou. Atualize a página e tente novamente.", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func removeMultipartTempFiles(r *http.Request) {
	if r.MultipartForm == nil {
		return
	}
	if err := r.MultipartForm.RemoveAll(); err != nil {
		slog.Error("remove rejected multipart temporary files", "error", err)
	}
}

func isMutatingMethod(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

func isUploadRequest(r *http.Request) bool {
	if r.Method != http.MethodPost {
		return false
	}
	switch r.URL.Path {
	case "/admin/media/upload", "/admin/appearance/install", "/admin/themes/install", "/admin/plugins/install":
		return true
	default:
		return false
	}
}

func adminRequestBodyLimit(requestPath string, mediaMax int64) int64 {
	switch requestPath {
	case "/admin/media/upload":
		return mediaMax + (64 << 10)
	case "/admin/appearance/install", "/admin/themes/install", "/admin/plugins/install":
		return packagezip.MaxArchiveSize + (64 << 10)
	case "/admin/posts/save", "/admin/pages/save", "/admin/preview":
		return maxContentFormSize
	default:
		return maxFormSize
	}
}

func (h *handler) loginProtectionMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := clientAddress(r)
		blocked, remaining := h.loginProtection.Blocked(ip, time.Now())
		if blocked {
			retryAfter := int(remaining.Seconds())
			if retryAfter < 1 {
				retryAfter = 1
			}
			w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
			slog.Warn("login temporarily blocked", "remote_addr", ip, "retry_after_seconds", retryAfter)
			http.Error(w, "Muitas tentativas de login. Tente novamente mais tarde.", http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}
