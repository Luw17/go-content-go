package web

import (
	"errors"
	"fmt"
	stdhtml "html"
	"html/template"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Luw17/go-content-go/internal/auth"
	"github.com/Luw17/go-content-go/internal/content"
	"github.com/Luw17/go-content-go/internal/media"
	"github.com/Luw17/go-content-go/internal/navigation"
	"github.com/Luw17/go-content-go/internal/plugins"
	"github.com/Luw17/go-content-go/internal/siteconfig"
	themeengine "github.com/Luw17/go-content-go/themes"
)

type adminPage struct {
	Mode               string
	Title              string
	Username           string
	CSRFToken          string
	ContentType        string
	ContentTypeLabel   string
	Items              []content.Item
	Item               content.Item
	Categories         []content.Category
	SelectedCategories map[int64]bool
	TagInput           string
	NewCategory        string
	Error              string
	Notice             string
	Trashed            bool
	MarkdownPreview    template.HTML
	MediaItems         []media.Item
	MaxUploadSize      int64
	Themes             []themeengine.Theme
	ActiveTheme        string
	Plugins            []plugins.Manifest
	Users              []auth.User
	Role               string
	SiteSettings       siteconfig.Settings
	Menus              []navigation.Menu
	MenuItems          []navigation.Item
	SelectedMenu       navigation.Menu
	MenuPages          []content.Item
	MenuID             int64
	PickerMode         string
}

type publicItem struct {
	content.Item
	HTML                 template.HTML
	Summary              string
	FormattedPublishedAt string
}

var publicHTMLTagPattern = regexp.MustCompile(`<[^>]*>`)

type publicHomeData struct {
	Theme    string
	Site     siteconfig.Settings
	Posts    []publicItem
	HeadTags template.HTML
}

type publicContentData struct {
	Theme    string
	Site     siteconfig.Settings
	Item     publicItem
	HeadTags template.HTML
}

type publicNotFoundData struct {
	Theme    string
	Site     siteconfig.Settings
	HeadTags template.HTML
}

func (h *handler) listPosts(w http.ResponseWriter, r *http.Request) {
	h.contentList(w, r, content.TypePost)
}

func (h *handler) listPages(w http.ResponseWriter, r *http.Request) {
	h.contentList(w, r, content.TypePage)
}

func (h *handler) contentList(w http.ResponseWriter, r *http.Request, contentType string) {
	user, ok := h.requireContentAccess(w, r)
	if !ok {
		return
	}
	trashed := r.URL.Query().Get("trashed") == "1"
	if raw := r.URL.Query().Get("trashed"); raw != "" && raw != "1" {
		http.Error(w, "invalid trash filter", http.StatusBadRequest)
		return
	}
	var items []content.Item
	var err error
	if user.Role == "author" {
		items, err = content.ListByAuthor(r.Context(), h.db, contentType, trashed, user.ID)
	} else {
		items, err = content.List(r.Context(), h.db, contentType, trashed)
	}
	if err != nil {
		log.Printf("list %s: %v", contentType, err)
		http.Error(w, "failed to load content", http.StatusInternalServerError)
		return
	}
	token, err := issueCSRFToken(w, r)
	if err != nil {
		http.Error(w, "failed to prepare content list", http.StatusInternalServerError)
		return
	}
	label := "Posts"
	if contentType == content.TypePage {
		label = "Páginas"
	}
	page := adminPage{
		Mode:             "list",
		Title:            label,
		Username:         user.Username,
		Role:             user.Role,
		CSRFToken:        token,
		ContentType:      contentType,
		ContentTypeLabel: label,
		Items:            items,
		Trashed:          trashed,
	}
	if r.URL.Query().Get("saved") == "1" {
		page.Notice = "Conteúdo salvo."
	}
	if r.Header.Get("HX-Request") == "true" && r.Header.Get("HX-Boosted") != "true" {
		h.renderFragment(w, "content-list-table", page)
		return
	}
	h.renderAdmin(w, page)
}

func (h *handler) newPost(w http.ResponseWriter, r *http.Request) {
	h.editForm(w, r, content.TypePost, 0)
}

func (h *handler) newPage(w http.ResponseWriter, r *http.Request) {
	h.editForm(w, r, content.TypePage, 0)
}

func (h *handler) editPost(w http.ResponseWriter, r *http.Request) {
	h.editExisting(w, r, content.TypePost)
}

func (h *handler) editPage(w http.ResponseWriter, r *http.Request) {
	h.editExisting(w, r, content.TypePage)
}

func (h *handler) editExisting(w http.ResponseWriter, r *http.Request, contentType string) {
	user, ok := h.requireContentAccess(w, r)
	if !ok {
		return
	}
	id, err := parseID(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	item, err := content.Get(r.Context(), h.db, id, contentType)
	if errors.Is(err, content.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		log.Printf("load content editor: %v", err)
		http.Error(w, "failed to load content", http.StatusInternalServerError)
		return
	}
	if !h.canAccessContent(w, r, user, item.ID, contentType) {
		return
	}
	h.editForm(w, r, contentType, id, item)
}

func (h *handler) editForm(w http.ResponseWriter, r *http.Request, contentType string, id int64, items ...content.Item) {
	user, ok := h.requireContentAccess(w, r)
	if !ok {
		return
	}
	categories, err := content.Categories(r.Context(), h.db)
	if err != nil {
		log.Printf("load categories: %v", err)
		http.Error(w, "failed to load categories", http.StatusInternalServerError)
		return
	}
	token, err := issueCSRFToken(w, r)
	if err != nil {
		http.Error(w, "failed to prepare editor", http.StatusInternalServerError)
		return
	}
	label := "Post"
	if contentType == content.TypePage {
		label = "Página"
	}
	item := content.Item{Type: contentType, Status: content.StatusDraft}
	if id > 0 {
		item.ID = id
		item = items[0]
	}
	selected := make(map[int64]bool, len(item.Categories))
	for _, category := range item.Categories {
		selected[category.ID] = true
	}
	page := adminPage{
		Mode:               "form",
		Title:              label,
		Username:           user.Username,
		Role:               user.Role,
		CSRFToken:          token,
		ContentType:        contentType,
		ContentTypeLabel:   label,
		Item:               item,
		Categories:         categories,
		SelectedCategories: selected,
		TagInput:           strings.Join(item.Tags, ", "),
	}
	h.renderAdmin(w, page)
}

func (h *handler) savePost(w http.ResponseWriter, r *http.Request) {
	h.saveContent(w, r, content.TypePost)
}

func (h *handler) savePage(w http.ResponseWriter, r *http.Request) {
	h.saveContent(w, r, content.TypePage)
}

func (h *handler) saveContent(w http.ResponseWriter, r *http.Request, contentType string) {
	user, ok := h.requireContentAccess(w, r)
	if !ok {
		return
	}
	if err := parseContentForm(w, r); err != nil {
		http.Error(w, "formulário inválido ou muito grande", http.StatusBadRequest)
		return
	}
	if !validCSRFToken(r) {
		http.Error(w, "invalid form token", http.StatusForbidden)
		return
	}
	id, err := optionalID(r.FormValue("id"))
	if err != nil {
		http.Error(w, "invalid content ID", http.StatusBadRequest)
		return
	}
	if id > 0 && !h.canAccessContent(w, r, user, id, contentType) {
		return
	}
	status := r.FormValue("status")
	if user.Role == "author" && status == content.StatusPublished {
		status = content.StatusDraft
	}
	categoryIDs, err := parseCategoryIDs(r.Form["category_ids"])
	if err != nil {
		http.Error(w, "invalid category selection", http.StatusBadRequest)
		return
	}
	parentID, err := optionalID(r.FormValue("new_category_parent_id"))
	if err != nil {
		http.Error(w, "invalid parent category", http.StatusBadRequest)
		return
	}
	input := content.Input{
		ID:                  id,
		Type:                contentType,
		Title:               r.FormValue("title"),
		Slug:                r.FormValue("slug"),
		Content:             r.FormValue("content"),
		Status:              status,
		FeaturedImage:       r.FormValue("featured_image"),
		CategoryIDs:         categoryIDs,
		NewCategory:         r.FormValue("new_category"),
		NewCategoryParentID: parentID,
		Tags:                r.FormValue("tags"),
	}
	if strings.HasPrefix(input.FeaturedImage, "/uploads/") {
		image, err := h.media.FindByPath(r.Context(), input.FeaturedImage)
		if err != nil || !strings.HasPrefix(strings.ToLower(image.FileType), "image/") {
			if err != nil && !errors.Is(err, media.ErrNotFound) {
				log.Printf("validate featured image: %v", err)
				http.Error(w, "failed to validate featured image", http.StatusInternalServerError)
				return
			}
			h.renderValidationError(w, r, input, "Selecione uma imagem existente na biblioteca de mídias.")
			return
		}
	}
	if contentType == content.TypePost {
		h.plugins.DispatchAction(r.Context(), "post:before_save", contentHookPayload(input, user.ID))
	}
	item, err := content.Save(r.Context(), h.db, input, user.ID)
	if errors.Is(err, content.ErrInvalidInput) || errors.Is(err, content.ErrSlugTaken) {
		h.renderValidationError(w, r, input, err.Error())
		return
	}
	h.cache.Clear()
	if errors.Is(err, content.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		log.Printf("save %s: %v", contentType, err)
		http.Error(w, "failed to save content", http.StatusInternalServerError)
		return
	}
	if item.Type == content.TypePost {
		h.plugins.DispatchAction(r.Context(), "post:after_save", map[string]any{
			"id": item.ID, "title": item.Title, "slug": item.Slug,
			"type": item.Type, "status": item.Status,
		})
	}
	redirectURL := "/admin/posts?saved=1"
	if item.Type == content.TypePage {
		redirectURL = "/admin/pages?saved=1"
	}

	http.Redirect(w, r, redirectURL, http.StatusSeeOther)
}

func contentHookPayload(input content.Input, authorID int64) map[string]any {
	return map[string]any{
		"id": input.ID, "author_id": authorID, "title": input.Title, "slug": input.Slug,
		"content": input.Content, "status": input.Status, "featured_image": input.FeaturedImage,
		"type": input.Type, "category_ids": input.CategoryIDs, "tags": input.Tags,
	}
}

func (h *handler) filteredMarkdown(r *http.Request, source string) string {
	filtered, ok := h.plugins.ApplyFilters(r.Context(), "content:render", source).(string)
	if !ok {
		log.Printf("plugin filter content:render returned a non-string value; preserving original Markdown")
		return source
	}
	return filtered
}

func (h *handler) renderValidationError(w http.ResponseWriter, r *http.Request, input content.Input, message string) {
	user, ok := h.requireContentAccess(w, r)
	if !ok {
		return
	}
	categories, err := content.Categories(r.Context(), h.db)
	if err != nil {
		log.Printf("load categories after invalid content: %v", err)
		http.Error(w, "failed to load categories", http.StatusInternalServerError)
		return
	}
	var mediaItems []media.Item
	if input.FeaturedImage != "" {
		mediaItems, err = h.media.List(r.Context())
		if err != nil {
			log.Printf("load media after invalid content: %v", err)
			http.Error(w, "failed to reload content editor", http.StatusInternalServerError)
			return
		}
	}
	token, err := r.Cookie(csrfCookieName)
	if err != nil {
		http.Error(w, "missing form token", http.StatusForbidden)
		return
	}
	label := "Post"
	if input.Type == content.TypePage {
		label = "Página"
	}
	item := content.Item{
		ID:            input.ID,
		Type:          input.Type,
		Title:         input.Title,
		Slug:          input.Slug,
		Content:       input.Content,
		Status:        input.Status,
		FeaturedImage: input.FeaturedImage,
	}
	selected := make(map[int64]bool)
	for _, id := range input.CategoryIDs {
		selected[id] = true
	}
	h.renderAdminStatus(w, adminPage{
		Mode:               "form",
		Title:              label,
		Username:           user.Username,
		Role:               user.Role,
		CSRFToken:          token.Value,
		ContentType:        input.Type,
		ContentTypeLabel:   label,
		Item:               item,
		Categories:         categories,
		MediaItems:         mediaItems,
		SelectedCategories: selected,
		TagInput:           input.Tags,
		NewCategory:        input.NewCategory,
		Error:              message,
	}, http.StatusUnprocessableEntity)
}

func (h *handler) changeContentStatus(w http.ResponseWriter, r *http.Request) {
	user, ok := h.requireContentAccess(w, r)
	if !ok {
		return
	}
	if err := parseForm(w, r); err != nil || !validCSRFToken(r) {
		http.Error(w, "invalid form submission", http.StatusForbidden)
		return
	}
	contentType, err := formContentType(r)
	if err != nil {
		http.Error(w, "invalid content type", http.StatusBadRequest)
		return
	}
	id, err := parseID(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if !h.canAccessContent(w, r, user, id, contentType) {
		return
	}
	status := r.FormValue("status")
	if user.Role == "author" && status == content.StatusPublished {
		http.Error(w, "authors cannot publish content", http.StatusForbidden)
		return
	}
	if err := content.UpdateStatus(r.Context(), h.db, id, contentType, status); err != nil {
		h.contentActionError(w, r, err)
		return
	}
	h.finishContentAction(w, r, contentType, false)
}

func (h *handler) trashContent(w http.ResponseWriter, r *http.Request) {
	user, ok := h.requireContentAccess(w, r)
	if !ok {
		return
	}
	if err := parseForm(w, r); err != nil || !validCSRFToken(r) {
		http.Error(w, "invalid form submission", http.StatusForbidden)
		return
	}
	contentType, err := formContentType(r)
	if err != nil {
		http.Error(w, "invalid content type", http.StatusBadRequest)
		return
	}
	id, err := parseID(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if !h.canAccessContent(w, r, user, id, contentType) {
		return
	}
	if err := content.Trash(r.Context(), h.db, id, contentType); err != nil {
		h.contentActionError(w, r, err)
		return
	}
	h.finishContentAction(w, r, contentType, false)
}

func (h *handler) restoreContent(w http.ResponseWriter, r *http.Request) {
	user, ok := h.requireContentAccess(w, r)
	if !ok {
		return
	}
	if err := parseForm(w, r); err != nil || !validCSRFToken(r) {
		http.Error(w, "invalid form submission", http.StatusForbidden)
		return
	}
	contentType, err := formContentType(r)
	if err != nil {
		http.Error(w, "invalid content type", http.StatusBadRequest)
		return
	}
	id, err := parseID(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if !h.canAccessContent(w, r, user, id, contentType) {
		return
	}
	if err := content.Restore(r.Context(), h.db, id, contentType); err != nil {
		h.contentActionError(w, r, err)
		return
	}
	h.finishContentAction(w, r, contentType, true)
}

func (h *handler) deleteContent(w http.ResponseWriter, r *http.Request) {
	user, ok := h.requireContentAccess(w, r)
	if !ok {
		return
	}
	if err := parseForm(w, r); err != nil || !validCSRFToken(r) {
		http.Error(w, "invalid form submission", http.StatusForbidden)
		return
	}
	contentType, err := formContentType(r)
	if err != nil {
		http.Error(w, "invalid content type", http.StatusBadRequest)
		return
	}
	id, err := parseID(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if !h.canAccessContent(w, r, user, id, contentType) {
		return
	}
	if err := content.Delete(r.Context(), h.db, id, contentType); err != nil {
		h.contentActionError(w, r, err)
		return
	}
	h.finishContentAction(w, r, contentType, true)
}

func (h *handler) finishContentAction(w http.ResponseWriter, r *http.Request, contentType string, trashed bool) {
	h.cache.Clear()
	if r.Header.Get("HX-Request") != "true" {
		path := "/admin/posts"
		if contentType == content.TypePage {
			path = "/admin/pages"
		}
		if trashed {
			path += "?trashed=1"
		}
		http.Redirect(w, r, path, http.StatusSeeOther)
		return
	}
	user, authenticated, err := h.currentUser(r)
	if err != nil || !authenticated {
		http.Error(w, "failed to validate session", http.StatusInternalServerError)
		return
	}
	var items []content.Item
	if user.Role == "author" {
		items, err = content.ListByAuthor(r.Context(), h.db, contentType, trashed, user.ID)
	} else {
		items, err = content.List(r.Context(), h.db, contentType, trashed)
	}
	if err != nil {
		log.Printf("refresh content list after action: %v", err)
		http.Error(w, "failed to refresh content list", http.StatusInternalServerError)
		return
	}
	token, err := issueCSRFToken(w, r)
	if err != nil {
		http.Error(w, "failed to refresh content list", http.StatusInternalServerError)
		return
	}
	label := "Posts"
	if contentType == content.TypePage {
		label = "Páginas"
	}
	h.renderFragment(w, "content-list-table", adminPage{
		Mode:             "list",
		Title:            label,
		CSRFToken:        token,
		Role:             user.Role,
		ContentType:      contentType,
		ContentTypeLabel: label,
		Items:            items,
		Trashed:          trashed,
	})
}

func (h *handler) contentActionError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, content.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if errors.Is(err, content.ErrInvalidInput) {
		http.Error(w, "invalid content action", http.StatusBadRequest)
		return
	}
	log.Printf("content action: %v", err)
	http.Error(w, "failed to update content", http.StatusInternalServerError)
}

func (h *handler) previewMarkdown(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireContentAccess(w, r); !ok {
		return
	}
	if err := parseContentForm(w, r); err != nil || !validCSRFToken(r) {
		http.Error(w, "invalid preview request", http.StatusForbidden)
		return
	}
	rendered, err := content.RenderMarkdown(h.filteredMarkdown(r, r.FormValue("content")))
	if err != nil {
		http.Error(w, "failed to render preview", http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = fmt.Fprint(w, rendered)
}

func (h *handler) adminPlaceholder(w http.ResponseWriter, r *http.Request) {
	user, ok := h.requireAdmin(w, r)
	if !ok {
		return
	}
	sections := map[string]string{
		"appearance": "Aparência",
		"settings":   "Configurações",
	}
	title, exists := sections[r.PathValue("section")]
	if !exists {
		http.NotFound(w, r)
		return
	}
	token, err := issueCSRFToken(w, r)
	if err != nil {
		http.Error(w, "failed to prepare administration", http.StatusInternalServerError)
		return
	}
	h.renderAdmin(w, adminPage{
		Mode:      "placeholder",
		Title:     title,
		Username:  user.Username,
		CSRFToken: token,
	})
}

func (h *handler) publicHome(w http.ResponseWriter, r *http.Request) {
	settings, err := siteconfig.Load(r.Context(), h.db)
	if err != nil {
		log.Printf("load site settings for home: %v", err)
		http.Error(w, "failed to load site", http.StatusInternalServerError)
		return
	}
	if settings.ShowOnFront == siteconfig.FrontPage {
		frontPage, err := content.PublishedPageByID(r.Context(), h.db, settings.PageOnFront)
		if err == nil {
			markdown := h.filteredMarkdown(r, frontPage.Content)
			rendered, err := content.RenderMarkdown(markdown)
			if err != nil {
				log.Printf("render front page %d: %v", frontPage.ID, err)
				http.Error(w, "failed to render site", http.StatusInternalServerError)
				return
			}
			public := newPublicItem(frontPage, rendered)
			h.renderTheme(w, r, "page.html", http.StatusOK, func(themeName string, headTags template.HTML, site siteconfig.Settings) any {
				return publicContentData{Theme: themeName, Site: site, Item: public, HeadTags: headTags}
			})
			return
		}
		if !errors.Is(err, content.ErrNotFound) {
			log.Printf("load configured front page %d: %v", settings.PageOnFront, err)
			http.Error(w, "failed to load site", http.StatusInternalServerError)
			return
		}
		log.Printf("configured front page %d is unavailable; showing published posts", settings.PageOnFront)
	}
	items, err := content.Published(r.Context(), h.db, 20)
	if err != nil {
		log.Printf("load public home: %v", err)
		http.Error(w, "failed to load site", http.StatusInternalServerError)
		return
	}
	var posts []publicItem
	for _, item := range items {
		if item.Type != content.TypePost {
			continue
		}
		markdown := h.filteredMarkdown(r, item.Content)
		rendered, err := content.RenderMarkdown(markdown)
		if err != nil {
			log.Printf("render public post %d: %v", item.ID, err)
			http.Error(w, "failed to render site", http.StatusInternalServerError)
			return
		}
		posts = append(posts, newPublicItem(item, rendered))
	}
	h.renderTheme(w, r, "index.html", http.StatusOK, func(themeName string, headTags template.HTML, site siteconfig.Settings) any {
		return publicHomeData{Theme: themeName, Site: site, Posts: posts, HeadTags: headTags}
	})
}

func (h *handler) publicContent(w http.ResponseWriter, r *http.Request) {
	slug := strings.TrimPrefix(r.URL.Path, "/")
	if slug == "" || strings.Contains(slug, "/") {
		h.publicNotFound(w, r)
		return
	}
	item, err := content.PublishedBySlug(r.Context(), h.db, slug)
	if errors.Is(err, content.ErrNotFound) {
		h.publicNotFound(w, r)
		return
	}
	if err != nil {
		log.Printf("load public content: %v", err)
		http.Error(w, "failed to load content", http.StatusInternalServerError)
		return
	}
	markdown := h.filteredMarkdown(r, item.Content)
	rendered, err := content.RenderMarkdown(markdown)
	if err != nil {
		log.Printf("render public content %d: %v", item.ID, err)
		http.Error(w, "failed to render content", http.StatusInternalServerError)
		return
	}
	templateName := "page.html"
	if item.Type == content.TypePost {
		templateName = "single.html"
	}
	public := newPublicItem(item, rendered)
	h.renderTheme(w, r, templateName, http.StatusOK, func(themeName string, headTags template.HTML, site siteconfig.Settings) any {
		return publicContentData{Theme: themeName, Site: site, Item: public, HeadTags: headTags}
	})
}

func newPublicItem(item content.Item, rendered string) publicItem {
	plainText := stdhtml.UnescapeString(publicHTMLTagPattern.ReplaceAllString(rendered, " "))
	plainText = strings.Join(strings.Fields(plainText), " ")
	runes := []rune(plainText)
	if len(runes) > 200 {
		plainText = strings.TrimSpace(string(runes[:200])) + "…"
	}
	publishedDate := item.PublishedAt
	if parsed, err := time.Parse(time.RFC3339Nano, item.PublishedAt); err == nil {
		publishedDate = parsed.Format("02/01/2006")
	}
	return publicItem{
		Item: item, HTML: template.HTML(rendered), Summary: plainText, FormattedPublishedAt: publishedDate,
	}
}

func (h *handler) renderAdmin(w http.ResponseWriter, page adminPage) {
	h.renderAdminStatus(w, page, http.StatusOK)
}

func (h *handler) renderAdminStatus(w http.ResponseWriter, page adminPage, status int) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if err := h.templates.ExecuteTemplate(w, "admin.html", page); err != nil {
		log.Printf("render admin page: %v", err)
	}
}

func (h *handler) renderFragment(w http.ResponseWriter, name string, page adminPage) {
	h.renderFragmentStatus(w, name, page, http.StatusOK)
}

func (h *handler) renderFragmentStatus(w http.ResponseWriter, name string, page adminPage, status int) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if err := h.templates.ExecuteTemplate(w, name, page); err != nil {
		log.Printf("render %s: %v", name, err)
	}
}

func (h *handler) publicNotFound(w http.ResponseWriter, r *http.Request) {
	h.renderTheme(w, r, "404.html", http.StatusNotFound, func(themeName string, headTags template.HTML, site siteconfig.Settings) any {
		return publicNotFoundData{Theme: themeName, Site: site, HeadTags: headTags}
	})
}

func (h *handler) requireAdmin(w http.ResponseWriter, r *http.Request) (auth.User, bool) {
	user, authenticated, err := h.currentUser(r)
	if err != nil {
		http.Error(w, "failed to validate session", http.StatusInternalServerError)
		return auth.User{}, false
	}
	if !authenticated {
		http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
		return auth.User{}, false
	}
	if user.Role != "admin" {
		http.Error(w, "forbidden", http.StatusForbidden)
		return auth.User{}, false
	}
	return user, true
}

func (h *handler) requireContentAccess(w http.ResponseWriter, r *http.Request) (auth.User, bool) {
	user, authenticated, err := h.currentUser(r)
	if err != nil {
		http.Error(w, "failed to validate session", http.StatusInternalServerError)
		return auth.User{}, false
	}
	if !authenticated {
		http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
		return auth.User{}, false
	}
	if !auth.ValidRole(user.Role) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return auth.User{}, false
	}
	return user, true
}

func (h *handler) canAccessContent(w http.ResponseWriter, r *http.Request, user auth.User, id int64, contentType string) bool {
	if user.Role != "author" {
		return true
	}
	authorID, err := content.AuthorID(r.Context(), h.db, id, contentType)
	if errors.Is(err, content.ErrNotFound) {
		http.NotFound(w, r)
		return false
	}
	if err != nil {
		log.Printf("check content ownership: %v", err)
		http.Error(w, "failed to authorize content", http.StatusInternalServerError)
		return false
	}
	if authorID != user.ID {
		http.NotFound(w, r)
		return false
	}
	return true
}

func parseID(value string) (int64, error) {
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("invalid ID")
	}
	return id, nil
}

func optionalID(value string) (int64, error) {
	if value == "" || value == "0" {
		return 0, nil
	}
	return parseID(value)
}

func parseCategoryIDs(values []string) ([]int64, error) {
	if len(values) > 100 {
		return nil, fmt.Errorf("too many categories")
	}
	ids := make([]int64, 0, len(values))
	for _, value := range values {
		id, err := parseID(value)
		if err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, nil
}

func formContentType(r *http.Request) (string, error) {
	contentType := r.FormValue("content_type")
	if contentType != content.TypePage && contentType != content.TypePost {
		return "", fmt.Errorf("invalid content type")
	}
	return contentType, nil
}

func parseContentForm(w http.ResponseWriter, r *http.Request) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxContentFormSize)
	if err := r.ParseForm(); err != nil {
		return fmt.Errorf("parse content form: %w", err)
	}
	return nil
}
