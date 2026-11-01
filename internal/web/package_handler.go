package web

import (
	"errors"
	"io"
	"log"
	"net/http"

	"github.com/Luw17/go-content-go/internal/packagezip"
)

func (h *handler) themeInstallPage(w http.ResponseWriter, r *http.Request) {
	h.packageInstallPage(w, r, "theme-install", "Instalar tema")
}

func (h *handler) pluginInstallPage(w http.ResponseWriter, r *http.Request) {
	h.packageInstallPage(w, r, "plugin-install", "Instalar plugin")
}

func (h *handler) packageInstallPage(w http.ResponseWriter, r *http.Request, mode, title string) {
	user, ok := h.requireAdmin(w, r)
	if !ok {
		return
	}
	token, err := issueCSRFToken(w, r)
	if err != nil {
		http.Error(w, "Não foi possível preparar a página de instalação.", http.StatusInternalServerError)
		return
	}
	h.renderAdmin(w, adminPage{
		Mode:      mode,
		Title:     title,
		Username:  user.Username,
		CSRFToken: token,
	})
}

func (h *handler) installTheme(w http.ResponseWriter, r *http.Request) {
	h.installPackageUpload(w, r, "theme", h.themes.InstallZip)
}

func (h *handler) installPlugin(w http.ResponseWriter, r *http.Request) {
	h.installPackageUpload(w, r, "plugin", h.plugins.InstallZip)
}

func (h *handler) installPackageUpload(w http.ResponseWriter, r *http.Request, kind string, install func(io.Reader, string) (string, error)) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	listURL := "/admin/appearance"
	if kind == "plugin" {
		listURL = "/admin/plugins"
	}
	r.Body = http.MaxBytesReader(w, r.Body, packagezip.MaxArchiveSize+(64<<10))
	if err := r.ParseMultipartForm(multipartMemoryLimit); err != nil {
		result := "invalid"
		if isRequestTooLarge(err) {
			result = "too-large"
		}
		http.Redirect(w, r, listURL+"?install_error="+result, http.StatusSeeOther)
		return
	}
	if r.MultipartForm == nil || !validCSRFToken(r) ||
		len(r.MultipartForm.Value) != 1 ||
		len(r.MultipartForm.Value["csrf_token"]) != 1 {
		http.Redirect(w, r, listURL+"?install_error=csrf", http.StatusSeeOther)
		return
	}
	files := r.MultipartForm.File["package"]
	if len(files) != 1 || len(r.MultipartForm.File) != 1 {
		http.Redirect(w, r, listURL+"?install_error=invalid", http.StatusSeeOther)
		return
	}
	header := files[0]
	if header.Size > packagezip.MaxArchiveSize {
		http.Redirect(w, r, listURL+"?install_error=too-large", http.StatusSeeOther)
		return
	}
	file, err := header.Open()
	if err != nil {
		log.Printf("open uploaded %s package: %v", kind, err)
		http.Redirect(w, r, listURL+"?install_error=invalid", http.StatusSeeOther)
		return
	}
	defer func() {
		if err := file.Close(); err != nil {
			log.Printf("close uploaded %s package: %v", kind, err)
		}
	}()
	id, err := install(file, header.Filename)
	switch {
	case err == nil:
		log.Printf("installed %s package %q", kind, id)
		http.Redirect(w, r, listURL+"?installed=1", http.StatusSeeOther)
	case errors.Is(err, packagezip.ErrTooLarge):
		http.Redirect(w, r, listURL+"?install_error=too-large", http.StatusSeeOther)
	case errors.Is(err, packagezip.ErrAlreadyExists):
		http.Redirect(w, r, listURL+"?install_error=exists", http.StatusSeeOther)
	case errors.Is(err, packagezip.ErrInvalidPackage):
		http.Redirect(w, r, listURL+"?install_error=invalid", http.StatusSeeOther)
	default:
		log.Printf("install %s package failed: %v", kind, err)
		http.Redirect(w, r, listURL+"?install_error=failed", http.StatusSeeOther)
	}
}

func packageInstallErrorMessage(code string) string {
	switch code {
	case "csrf":
		return "A sessão do formulário expirou. Atualize a página e tente novamente."
	case "too-large":
		return "O pacote excede o limite de 20 MB."
	case "exists":
		return "Já existe um pacote instalado com esse identificador."
	case "invalid":
		return "Pacote inválido. Verifique o arquivo ZIP e os arquivos obrigatórios."
	case "failed":
		return "Não foi possível instalar o pacote. Verifique as permissões do diretório."
	default:
		return ""
	}
}
