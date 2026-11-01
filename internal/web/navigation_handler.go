package web

import (
	"database/sql"
	"errors"
	"log"
	"net/http"
	"net/url"
	"strconv"

	"github.com/Luw17/go-content-go/internal/content"
	"github.com/Luw17/go-content-go/internal/media"
	"github.com/Luw17/go-content-go/internal/navigation"
)

func (h *handler) menuManagement(w http.ResponseWriter, r *http.Request) {
	user, ok := h.requireAdmin(w, r)
	if !ok {
		return
	}
	menus, err := h.menus.List(r.Context())
	if err != nil {
		log.Printf("list navigation menus: %v", err)
		http.Error(w, "Não foi possível carregar os menus.", http.StatusInternalServerError)
		return
	}
	pages, err := content.PublishedPages(r.Context(), h.db)
	if err != nil {
		log.Printf("list pages for navigation menus: %v", err)
		http.Error(w, "Não foi possível carregar as páginas.", http.StatusInternalServerError)
		return
	}
	var selected navigation.Menu
	if rawID := r.URL.Query().Get("menu"); rawID != "" {
		id, err := parseID(rawID)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		for _, menu := range menus {
			if menu.ID == id {
				selected = menu
				break
			}
		}
		if selected.ID == 0 {
			http.NotFound(w, r)
			return
		}
	} else if len(menus) > 0 {
		selected = menus[0]
	}
	var items []navigation.Item
	if selected.ID > 0 {
		items, err = h.menus.Items(r.Context(), selected.ID)
		if err != nil {
			log.Printf("list items for navigation menu %d: %v", selected.ID, err)
			http.Error(w, "Não foi possível carregar os itens do menu.", http.StatusInternalServerError)
			return
		}
	}
	token, err := issueCSRFToken(w, r)
	if err != nil {
		http.Error(w, "Não foi possível preparar a página de menus.", http.StatusInternalServerError)
		return
	}
	page := adminPage{
		Mode: "menus", Title: "Menus", Username: user.Username, CSRFToken: token,
		Menus: menus, MenuItems: items, SelectedMenu: selected, MenuPages: pages, MenuID: selected.ID,
	}
	switch r.URL.Query().Get("status") {
	case "created":
		page.Notice = "Menu criado."
	case "saved":
		page.Notice = "Menu atualizado."
	case "deleted":
		page.Notice = "Menu excluído."
	case "item-added":
		page.Notice = "Item adicionado ao menu."
	case "item-deleted":
		page.Notice = "Item removido do menu."
	case "moved":
		page.Notice = "Ordem do menu atualizada."
	}
	switch r.URL.Query().Get("error") {
	case "invalid":
		page.Error = "Revise os campos informados e tente novamente."
	case "location":
		page.Error = "Já existe um menu nessa localização."
	}
	h.renderAdmin(w, page)
}

func (h *handler) createMenu(w http.ResponseWriter, r *http.Request) {
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
	menu, err := h.menus.Create(r.Context(), r.FormValue("name"), r.FormValue("location"))
	if errors.Is(err, navigation.ErrInvalidInput) {
		http.Redirect(w, r, "/admin/appearance/menus?error=invalid", http.StatusSeeOther)
		return
	}
	if isUniqueConstraint(err) {
		http.Redirect(w, r, "/admin/appearance/menus?error=location", http.StatusSeeOther)
		return
	}
	if err != nil {
		log.Printf("create navigation menu: %v", err)
		http.Error(w, "Não foi possível criar o menu.", http.StatusInternalServerError)
		return
	}
	h.cache.Clear()
	http.Redirect(w, r, "/admin/appearance/menus?menu="+strconv.FormatInt(menu.ID, 10)+"&status=created", http.StatusSeeOther)
}

func (h *handler) addMenuItem(w http.ResponseWriter, r *http.Request) {
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
	menuID, err := parseID(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	var addErr error
	switch r.FormValue("type") {
	case navigation.TypePage:
		pageID, parseErr := parseID(r.FormValue("page_id"))
		if parseErr != nil {
			addErr = navigation.ErrInvalidInput
			break
		}
		label := r.FormValue("label")
		if label == "" {
			page, pageErr := content.PublishedPageByID(r.Context(), h.db, pageID)
			if pageErr != nil {
				addErr = pageErr
				break
			}
			label = page.Title
		}
		addErr = h.menus.AddPage(r.Context(), menuID, pageID, label)
	case navigation.TypeCustom:
		addErr = h.menus.AddLink(r.Context(), menuID, r.FormValue("label"), r.FormValue("url"))
	case "home":
		addErr = h.menus.AddLink(r.Context(), menuID, r.FormValue("label"), "/")
	default:
		addErr = navigation.ErrInvalidInput
	}
	if errors.Is(addErr, navigation.ErrInvalidInput) {
		http.Redirect(w, r, menuReturnURL(menuID, "error=invalid"), http.StatusSeeOther)
		return
	}
	if errors.Is(addErr, content.ErrNotFound) || errors.Is(addErr, navigation.ErrNotFound) {
		http.Redirect(w, r, menuReturnURL(menuID, "error=invalid"), http.StatusSeeOther)
		return
	}
	if addErr != nil {
		log.Printf("add item to navigation menu %d: %v", menuID, addErr)
		http.Error(w, "Não foi possível adicionar o item ao menu.", http.StatusInternalServerError)
		return
	}
	h.cache.Clear()
	http.Redirect(w, r, menuReturnURL(menuID, "status=item-added"), http.StatusSeeOther)
}

func (h *handler) moveMenuItem(w http.ResponseWriter, r *http.Request) {
	h.updateMenuItem(w, r, func(itemID int64) error {
		return h.menus.Move(r.Context(), itemID, r.FormValue("direction"))
	}, "status=moved")
}

func (h *handler) deleteMenuItem(w http.ResponseWriter, r *http.Request) {
	h.updateMenuItem(w, r, func(itemID int64) error {
		return h.menus.DeleteItem(r.Context(), itemID)
	}, "status=item-deleted")
}

func (h *handler) updateMenuItem(w http.ResponseWriter, r *http.Request, action func(int64) error, result string) {
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
	itemID, err := parseID(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	var menuID int64
	if err := h.db.QueryRowContext(r.Context(), "SELECT menu_id FROM menu_items WHERE id = ?", itemID).Scan(&menuID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		log.Printf("load menu for item %d: %v", itemID, err)
		http.Error(w, "Não foi possível carregar o menu.", http.StatusInternalServerError)
		return
	}
	if err := action(itemID); errors.Is(err, navigation.ErrNotFound) {
		http.NotFound(w, r)
		return
	} else if errors.Is(err, navigation.ErrInvalidInput) {
		http.Error(w, "Ação de menu inválida.", http.StatusBadRequest)
		return
	} else if err != nil {
		log.Printf("update navigation menu item %d: %v", itemID, err)
		http.Error(w, "Não foi possível atualizar o menu.", http.StatusInternalServerError)
		return
	}
	h.cache.Clear()
	http.Redirect(w, r, menuReturnURL(menuID, result), http.StatusSeeOther)
}

func (h *handler) deleteMenu(w http.ResponseWriter, r *http.Request) {
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
	menuID, err := parseID(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := h.menus.DeleteMenu(r.Context(), menuID); errors.Is(err, navigation.ErrNotFound) {
		http.NotFound(w, r)
		return
	} else if err != nil {
		log.Printf("delete navigation menu %d: %v", menuID, err)
		http.Error(w, "Não foi possível excluir o menu.", http.StatusInternalServerError)
		return
	}
	h.cache.Clear()
	http.Redirect(w, r, "/admin/appearance/menus?status=deleted", http.StatusSeeOther)
}

func menuReturnURL(menuID int64, query string) string {
	return "/admin/appearance/menus?menu=" + url.QueryEscape(strconv.FormatInt(menuID, 10)) + "&" + query
}

func (h *handler) mediaPicker(w http.ResponseWriter, r *http.Request) {
	user, ok := h.requireContentAccess(w, r)
	if !ok {
		return
	}
	mode := r.URL.Query().Get("mode")
	if mode != "featured" && mode != "inline" {
		http.Error(w, "Modo de seleção de mídia inválido.", http.StatusBadRequest)
		return
	}
	items, err := h.media.List(r.Context())
	if err != nil {
		log.Printf("list media for editor picker: %v", err)
		http.Error(w, "Não foi possível carregar as mídias.", http.StatusInternalServerError)
		return
	}
	images := make([]media.Item, 0, len(items))
	for _, item := range items {
		if len(item.FileType) >= len("image/") && item.FileType[:len("image/")] == "image/" {
			images = append(images, item)
		}
	}
	h.renderFragment(w, "media-picker", adminPage{
		Username: user.Username, MediaItems: images, PickerMode: mode,
	})
}
