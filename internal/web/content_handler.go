package web

import (
	"context"
	"errors"
	"fmt"
	stdhtml "html"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Luw17/go-content-go/internal/auth"
	"github.com/Luw17/go-content-go/internal/builder"
	"github.com/Luw17/go-content-go/internal/config"
	"github.com/Luw17/go-content-go/internal/content"
	"github.com/Luw17/go-content-go/internal/media"
	"github.com/Luw17/go-content-go/internal/navigation"
	"github.com/Luw17/go-content-go/internal/plugins"
	"github.com/Luw17/go-content-go/internal/redirects"
	"github.com/Luw17/go-content-go/internal/seo"
	"github.com/Luw17/go-content-go/internal/siteconfig"
	themeengine "github.com/Luw17/go-content-go/themes"
	"github.com/microcosm-cc/bluemonday"
)

const homePostsPerPage = 10

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
	Layouts            []builder.Layout
	Layout             builder.Layout
	BuilderPresets     []builder.StarterPreset
	BuilderPreview     template.HTML
	FormSubmissions    []builder.FormSubmission
	SiteSections       []builder.SiteSection
	SelectedSection    builder.SiteSection
	PluginMenus        []plugins.AdminMenuItem
	PluginManifest     plugins.Manifest
	PluginPageHTML     template.HTML
	PluginSettings     []pluginSettingRow
	TOTPEnabled            bool
	TOTPSecret             string
	TOTPURI                string
	RecoveryCodes          []string
	RemainingRecoveryCodes int
	PermissionFindings     []config.PermissionFinding
	Redirects              []redirects.Redirect
	KPIs                   dashboardKPIs
	HealthChecks           []healthCheckItem
	Revisions              []content.Revision
	APIKeys                []auth.APIKey
	NewAPIKey              string
	FilterQuery            string
	FilterStatus           string
	AdminPagination        *publicPagination
}

type publicItem struct {
	content.Item
	HTML                 template.HTML
	Summary              string
	FormattedPublishedAt string
	ReadingTimeMinutes   int
	TagLinks             []content.Tag
}

var publicHTMLTagPattern = regexp.MustCompile(`<[^>]*>`)

type publicPagination struct {
	Page       int
	TotalPages int
	TotalItems int64
	HasPrev    bool
	HasNext    bool
	PrevURL    string
	NextURL    string
}

type publicHomeData struct {
	Theme              string
	Site               siteconfig.Settings
	Posts              []publicItem
	HeadTags           template.HTML
	BuilderHTML        template.HTML
	ArchiveEyebrow     string
	ArchiveTitle       string
	ArchiveDescription string
	SearchQuery        string
	IsSearch           bool
	Pagination         *publicPagination
}

type publicContentData struct {
	Theme        string
	Site         siteconfig.Settings
	Item         publicItem
	HeadTags     template.HTML
	BuilderHTML  template.HTML
	RelatedPosts []publicItem
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
	filterQuery := strings.TrimSpace(r.URL.Query().Get("q"))
	filterStatus := strings.TrimSpace(r.URL.Query().Get("status"))
	if filterStatus != "" && filterStatus != content.StatusDraft && filterStatus != content.StatusPublished {
		filterStatus = ""
	}
	pageNum, ok := parsePageParam(r)
	if !ok {
		pageNum = 1
	}
	var authorFilter int64
	if user.Role == "author" {
		authorFilter = user.ID
	}
	items, total, err := content.ListFiltered(r.Context(), h.db, content.AdminFilter{
		ContentType: contentType,
		Trashed:     trashed,
		AuthorID:    authorFilter,
		Query:       filterQuery,
		Status:      filterStatus,
		Page:        pageNum,
		PerPage:     20,
	})
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
	basePath := "/admin/posts"
	if contentType == content.TypePage {
		label = "Páginas"
		basePath = "/admin/pages"
	}
	extra := url.Values{}
	if trashed {
		extra.Set("trashed", "1")
	}
	if filterQuery != "" {
		extra.Set("q", filterQuery)
	}
	if filterStatus != "" {
		extra.Set("status", filterStatus)
	}
	adminPagination := buildPagination(basePath, extra, pageNum, 20, total)
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
		FilterQuery:      filterQuery,
		FilterStatus:     filterStatus,
		AdminPagination:  adminPagination,
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
	var layouts []builder.Layout
	if contentType == content.TypePage {
		layouts, err = h.builder.ListLayouts(r.Context())
		if err != nil {
			log.Printf("load page layouts: %v", err)
			http.Error(w, "failed to load layouts", http.StatusInternalServerError)
			return
		}
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
	var revisions []content.Revision
	if id > 0 {
		revisions, _ = content.ListRevisions(r.Context(), h.db, id, 20)
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
		Layouts:            layouts,
		Revisions:          revisions,
	}
	if r.URL.Query().Get("restored") == "1" {
		page.Notice = "Revisão restaurada com sucesso."
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
	layoutID, err := optionalID(r.FormValue("layout_id"))
	if err != nil {
		http.Error(w, "invalid layout selection", http.StatusBadRequest)
		return
	}
	if contentType != content.TypePage {
		layoutID = 0
	}
	input := content.Input{
		ID:                  id,
		Type:                contentType,
		Title:               r.FormValue("title"),
		Slug:                r.FormValue("slug"),
		Content:             r.FormValue("content"),
		Status:              status,
		FeaturedImage:       r.FormValue("featured_image"),
		SEOTitle:            r.FormValue("seo_title"),
		SEODescription:      r.FormValue("seo_description"),
		CanonicalURL:        r.FormValue("canonical_url"),
		OGTitle:             r.FormValue("og_title"),
		OGDescription:       r.FormValue("og_description"),
		OGImage:             r.FormValue("og_image"),
		TwitterCard:         r.FormValue("twitter_card"),
		SchemaType:          r.FormValue("schema_type"),
		Noindex:             r.FormValue("noindex") == "1" || r.FormValue("noindex") == "true",
		LayoutID:            layoutID,
		ScheduledAt:         r.FormValue("scheduled_at"),
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
	if strings.HasPrefix(input.OGImage, "/uploads/") {
		image, err := h.media.FindByPath(r.Context(), input.OGImage)
		if err != nil || !strings.HasPrefix(strings.ToLower(image.FileType), "image/") {
			if err != nil && !errors.Is(err, media.ErrNotFound) {
				log.Printf("validate og image: %v", err)
				http.Error(w, "failed to validate og image", http.StatusInternalServerError)
				return
			}
			h.renderValidationError(w, r, input, "Selecione uma imagem social existente na biblioteca de mídias.")
			return
		}
	}
	var previousItem content.Item
	var hasPrevious bool
	if input.ID > 0 {
		if existing, getErr := content.Get(r.Context(), h.db, input.ID, contentType); getErr == nil {
			previousItem = existing
			hasPrevious = true
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
	if hasPrevious &&
		previousItem.Status == content.StatusPublished &&
		item.Status == content.StatusPublished &&
		previousItem.Slug != "" && item.Slug != "" && previousItem.Slug != item.Slug {
		if settings, cfgErr := siteconfig.Load(r.Context(), h.db); cfgErr == nil && settings.AutoRedirectSlugChange {
			if _, rErr := redirects.Save(r.Context(), h.db, "/"+previousItem.Slug, "/"+item.Slug, http.StatusMovedPermanently); rErr != nil {
				log.Printf("auto-create redirect %s -> %s: %v", previousItem.Slug, item.Slug, rErr)
			}
		}
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
		"seo_title": input.SEOTitle, "seo_description": input.SEODescription, "canonical_url": input.CanonicalURL,
		"og_title": input.OGTitle, "og_description": input.OGDescription, "og_image": input.OGImage,
		"twitter_card": input.TwitterCard, "schema_type": input.SchemaType, "noindex": input.Noindex,
		"type": input.Type, "category_ids": input.CategoryIDs, "tags": input.Tags,
	}
}

var publicAfterRenderPolicy = bluemonday.UGCPolicy()

func (h *handler) filteredMarkdown(r *http.Request, source string) string {
	current := source
	if before, ok := h.plugins.ApplyFilters(r.Context(), "content:before_render", current).(string); ok {
		current = before
	} else {
		log.Printf("plugin filter content:before_render returned a non-string value; preserving previous Markdown")
	}
	filtered, ok := h.plugins.ApplyFilters(r.Context(), "content:render", current).(string)
	if !ok {
		log.Printf("plugin filter content:render returned a non-string value; preserving previous Markdown")
		return current
	}
	return filtered
}

func (h *handler) renderPublicMarkdown(r *http.Request, source string) (string, error) {
	md := h.filteredMarkdown(r, source)
	rendered, err := content.RenderMarkdown(md)
	if err != nil {
		return "", err
	}
	after, ok := h.plugins.ApplyFilters(r.Context(), "content:after_render", rendered).(string)
	if !ok {
		log.Printf("plugin filter content:after_render returned a non-string value; preserving rendered HTML")
		return rendered, nil
	}
	if after != rendered {
		return publicAfterRenderPolicy.Sanitize(after), nil
	}
	return rendered, nil
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
	if input.FeaturedImage != "" || input.OGImage != "" {
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
		ID:             input.ID,
		Type:           input.Type,
		Title:          input.Title,
		Slug:           input.Slug,
		Content:        input.Content,
		Status:         input.Status,
		FeaturedImage:  input.FeaturedImage,
		SEOTitle:       input.SEOTitle,
		SEODescription: input.SEODescription,
		CanonicalURL:   input.CanonicalURL,
		OGTitle:        input.OGTitle,
		OGDescription:  input.OGDescription,
		OGImage:        input.OGImage,
		TwitterCard:    input.TwitterCard,
		SchemaType:     input.SchemaType,
		Noindex:        input.Noindex,
		LayoutID:       input.LayoutID,
	}
	var layouts []builder.Layout
	if input.Type == content.TypePage {
		layouts, _ = h.builder.ListLayouts(r.Context())
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
		Layouts:            layouts,
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

func (h *handler) publicHome(w http.ResponseWriter, r *http.Request) {
	settings, err := siteconfig.Load(r.Context(), h.db)
	if err != nil {
		log.Printf("load site settings for home: %v", err)
		http.Error(w, "failed to load site", http.StatusInternalServerError)
		return
	}
	if homeLayout, hasHomeLayout, err := h.builder.ActiveHomepageLayout(r.Context()); err != nil {
		log.Printf("load active homepage layout: %v", err)
		http.Error(w, "failed to load site", http.StatusInternalServerError)
		return
	} else if hasHomeLayout {
		builderHTML, err := builder.RenderBlocks(r.Context(), homeLayout.Blocks, builder.RenderContext{
			DB:              h.db,
			PagePath:        "/",
			SubmittedFormID: r.URL.Query().Get("form_submitted"),
			ErrorFormID:     r.URL.Query().Get("form_error"),
			FilterMarkdown: func(md string) string {
				return h.filteredMarkdown(r, md)
			},
		})
		if err != nil {
			log.Printf("render active homepage layout %d: %v", homeLayout.ID, err)
			http.Error(w, "failed to render site", http.StatusInternalServerError)
			return
		}
		seoParams := seo.Params{
			Type: seo.TypeHome,
		}
		h.renderTheme(w, r, "index.html", http.StatusOK, seoParams, func(themeName string, headTags template.HTML, site siteconfig.Settings) any {
			return publicHomeData{Theme: themeName, Site: site, HeadTags: headTags, BuilderHTML: builderHTML}
		})
		return
	}
	if settings.ShowOnFront == siteconfig.FrontPage {
		frontPage, err := content.PublishedPageByID(r.Context(), h.db, settings.PageOnFront)
		if err == nil {
			rendered, err := h.renderPublicMarkdown(r, frontPage.Content)
			if err != nil {
				log.Printf("render front page %d: %v", frontPage.ID, err)
				http.Error(w, "failed to render site", http.StatusInternalServerError)
				return
			}
			var builderHTML template.HTML
			if frontPage.LayoutID > 0 {
				if layout, lerr := h.builder.GetLayout(r.Context(), frontPage.LayoutID); lerr == nil {
					builderHTML, err = builder.RenderBlocks(r.Context(), layout.Blocks, builder.RenderContext{
						DB:              h.db,
						PagePath:        "/",
						SubmittedFormID: r.URL.Query().Get("form_submitted"),
						ErrorFormID:     r.URL.Query().Get("form_error"),
						FilterMarkdown: func(md string) string {
							return h.filteredMarkdown(r, md)
						},
					})
					if err != nil {
						log.Printf("render front page layout %d: %v", frontPage.LayoutID, err)
						http.Error(w, "failed to render site", http.StatusInternalServerError)
						return
					}
				}
			}
			public := newPublicItem(frontPage, rendered)
			seoParams := seo.Params{
				Type:            seo.TypePage,
				Item:            &frontPage,
				FallbackSummary: public.Summary,
			}
			h.renderTheme(w, r, "page.html", http.StatusOK, seoParams, func(themeName string, headTags template.HTML, site siteconfig.Settings) any {
				return publicContentData{Theme: themeName, Site: site, Item: public, HeadTags: headTags, BuilderHTML: builderHTML}
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
	page, ok := parsePageParam(r)
	if !ok {
		h.publicNotFound(w, r)
		return
	}
	items, total, err := content.PublishedPage(r.Context(), h.db, page, homePostsPerPage)
	if err != nil {
		log.Printf("load public home: %v", err)
		http.Error(w, "failed to load site", http.StatusInternalServerError)
		return
	}
	pagination := buildPagination("/", nil, page, homePostsPerPage, total)
	if page > 1 && page > pagination.TotalPages {
		h.publicNotFound(w, r)
		return
	}
	posts, err := h.renderPublicItemList(r, items)
	if err != nil {
		log.Printf("render public home posts: %v", err)
		http.Error(w, "failed to render site", http.StatusInternalServerError)
		return
	}
	canonicalPath := "/"
	pageTitle := ""
	if page > 1 {
		canonicalPath = fmt.Sprintf("/?page=%d", page)
		pageTitle = fmt.Sprintf("%s — Página %d", settings.Title, page)
	}
	seoParams := seo.Params{
		Type:          seo.TypeHome,
		Title:         pageTitle,
		CanonicalPath: canonicalPath,
		PrevURL:       pagination.PrevURL,
		NextURL:       pagination.NextURL,
	}
	h.renderTheme(w, r, "index.html", http.StatusOK, seoParams, func(themeName string, headTags template.HTML, site siteconfig.Settings) any {
		return publicHomeData{Theme: themeName, Site: site, Posts: posts, HeadTags: headTags, Pagination: pagination}
	})
}

func (h *handler) publicCategoryArchive(w http.ResponseWriter, r *http.Request, slug string) {
	category, err := content.CategoryBySlug(r.Context(), h.db, slug)
	if errors.Is(err, content.ErrNotFound) {
		h.publicNotFound(w, r)
		return
	}
	if err != nil {
		log.Printf("load category %q: %v", slug, err)
		http.Error(w, "failed to load category", http.StatusInternalServerError)
		return
	}
	page, ok := parsePageParam(r)
	if !ok {
		h.publicNotFound(w, r)
		return
	}
	items, total, err := content.PublishedByCategoryPage(r.Context(), h.db, category.Slug, page, homePostsPerPage)
	if err != nil {
		log.Printf("load category %q posts: %v", category.Slug, err)
		http.Error(w, "failed to load category", http.StatusInternalServerError)
		return
	}
	basePath := "/categoria/" + category.Slug
	pagination := buildPagination(basePath, nil, page, homePostsPerPage, total)
	if page > 1 && page > pagination.TotalPages {
		h.publicNotFound(w, r)
		return
	}
	posts, err := h.renderPublicItemList(r, items)
	if err != nil {
		log.Printf("render category %q posts: %v", category.Slug, err)
		http.Error(w, "failed to render category", http.StatusInternalServerError)
		return
	}
	canonicalPath := basePath
	title := "Categoria: " + category.Name
	if page > 1 {
		canonicalPath = fmt.Sprintf("%s?page=%d", basePath, page)
		title = fmt.Sprintf("%s — Página %d", title, page)
	}
	desc := fmt.Sprintf("Artigos publicados na categoria %s.", category.Name)
	seoParams := seo.Params{
		Type:           seo.TypeArchive,
		Title:          title,
		Description:    desc,
		CanonicalPath:  canonicalPath,
		BreadcrumbName: category.Name,
		PrevURL:        pagination.PrevURL,
		NextURL:        pagination.NextURL,
	}
	h.renderTheme(w, r, "index.html", http.StatusOK, seoParams, func(themeName string, headTags template.HTML, site siteconfig.Settings) any {
		return publicHomeData{
			Theme:              themeName,
			Site:               site,
			Posts:              posts,
			HeadTags:           headTags,
			ArchiveEyebrow:     "Categoria",
			ArchiveTitle:       category.Name,
			ArchiveDescription: desc,
			Pagination:         pagination,
		}
	})
}

func (h *handler) publicTagArchive(w http.ResponseWriter, r *http.Request, slug string) {
	tag, err := content.TagBySlug(r.Context(), h.db, slug)
	if errors.Is(err, content.ErrNotFound) {
		h.publicNotFound(w, r)
		return
	}
	if err != nil {
		log.Printf("load tag %q: %v", slug, err)
		http.Error(w, "failed to load tag", http.StatusInternalServerError)
		return
	}
	page, ok := parsePageParam(r)
	if !ok {
		h.publicNotFound(w, r)
		return
	}
	items, total, err := content.PublishedByTagPage(r.Context(), h.db, tag.Slug, page, homePostsPerPage)
	if err != nil {
		log.Printf("load tag %q posts: %v", tag.Slug, err)
		http.Error(w, "failed to load tag", http.StatusInternalServerError)
		return
	}
	basePath := "/tag/" + tag.Slug
	pagination := buildPagination(basePath, nil, page, homePostsPerPage, total)
	if page > 1 && page > pagination.TotalPages {
		h.publicNotFound(w, r)
		return
	}
	posts, err := h.renderPublicItemList(r, items)
	if err != nil {
		log.Printf("render tag %q posts: %v", tag.Slug, err)
		http.Error(w, "failed to render tag", http.StatusInternalServerError)
		return
	}
	canonicalPath := basePath
	title := "Tag: #" + tag.Name
	if page > 1 {
		canonicalPath = fmt.Sprintf("%s?page=%d", basePath, page)
		title = fmt.Sprintf("%s — Página %d", title, page)
	}
	desc := fmt.Sprintf("Artigos marcados com a tag %s.", tag.Name)
	seoParams := seo.Params{
		Type:           seo.TypeArchive,
		Title:          title,
		Description:    desc,
		CanonicalPath:  canonicalPath,
		BreadcrumbName: "#" + tag.Name,
		PrevURL:        pagination.PrevURL,
		NextURL:        pagination.NextURL,
	}
	h.renderTheme(w, r, "index.html", http.StatusOK, seoParams, func(themeName string, headTags template.HTML, site siteconfig.Settings) any {
		return publicHomeData{
			Theme:              themeName,
			Site:               site,
			Posts:              posts,
			HeadTags:           headTags,
			ArchiveEyebrow:     "Tag",
			ArchiveTitle:       "#" + tag.Name,
			ArchiveDescription: desc,
			Pagination:         pagination,
		}
	})
}

func (h *handler) publicSearch(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	page, ok := parsePageParam(r)
	if !ok {
		h.publicNotFound(w, r)
		return
	}
	var items []content.Item
	var total int64
	if query != "" {
		var err error
		items, total, err = content.SearchPublishedPage(r.Context(), h.db, query, page, homePostsPerPage)
		if err != nil {
			log.Printf("search published content for %q: %v", query, err)
			http.Error(w, "failed to search content", http.StatusInternalServerError)
			return
		}
	}
	extraParams := url.Values{}
	if query != "" {
		extraParams.Set("q", query)
	}
	pagination := buildPagination("/search", extraParams, page, homePostsPerPage, total)
	if page > 1 && page > pagination.TotalPages {
		h.publicNotFound(w, r)
		return
	}
	posts, err := h.renderPublicItemList(r, items)
	if err != nil {
		log.Printf("render search results for %q: %v", query, err)
		http.Error(w, "failed to render search results", http.StatusInternalServerError)
		return
	}
	archiveTitle := "Buscar no site"
	desc := "Digite um termo para pesquisar publicações."
	if query != "" {
		archiveTitle = fmt.Sprintf("Resultados para %q", query)
		if total == 1 {
			desc = "1 resultado encontrado."
		} else {
			desc = fmt.Sprintf("%d resultados encontrados.", total)
		}
	}
	seoParams := seo.Params{
		Type:          seo.TypeSearch,
		Title:         archiveTitle,
		Description:   desc,
		CanonicalPath: "/search",
		PrevURL:       pagination.PrevURL,
		NextURL:       pagination.NextURL,
	}
	h.renderTheme(w, r, "index.html", http.StatusOK, seoParams, func(themeName string, headTags template.HTML, site siteconfig.Settings) any {
		return publicHomeData{
			Theme:              themeName,
			Site:               site,
			Posts:              posts,
			HeadTags:           headTags,
			ArchiveEyebrow:     "Busca",
			ArchiveTitle:       archiveTitle,
			ArchiveDescription: desc,
			SearchQuery:        query,
			IsSearch:           true,
			Pagination:         pagination,
		}
	})
}

func (h *handler) renderPublicItemList(r *http.Request, items []content.Item) ([]publicItem, error) {
	posts := make([]publicItem, 0, len(items))
	for _, item := range items {
		rendered, err := h.renderPublicMarkdown(r, item.Content)
		if err != nil {
			return nil, err
		}
		posts = append(posts, newPublicItem(item, rendered))
	}
	return posts, nil
}

func parsePageParam(r *http.Request) (int, bool) {
	raw := strings.TrimSpace(r.URL.Query().Get("page"))
	if raw == "" {
		return 1, true
	}
	page, err := strconv.Atoi(raw)
	if err != nil || page < 1 {
		return 0, false
	}
	return page, true
}

func buildPagination(basePath string, extra url.Values, page, perPage int, total int64) *publicPagination {
	totalPages := int((total + int64(perPage) - 1) / int64(perPage))
	if totalPages < 1 {
		totalPages = 1
	}
	buildURL := func(targetPage int) string {
		q := url.Values{}
		for k, vals := range extra {
			for _, v := range vals {
				q.Add(k, v)
			}
		}
		if targetPage > 1 {
			q.Set("page", strconv.Itoa(targetPage))
		}
		encoded := q.Encode()
		if encoded == "" {
			return basePath
		}
		return basePath + "?" + encoded
	}
	p := &publicPagination{
		Page:       page,
		TotalPages: totalPages,
		TotalItems: total,
		HasPrev:    page > 1,
		HasNext:    page < totalPages,
	}
	if p.HasPrev {
		p.PrevURL = buildURL(page - 1)
	}
	if p.HasNext {
		p.NextURL = buildURL(page + 1)
	}
	return p
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
	rendered, err := h.renderPublicMarkdown(r, item.Content)
	if err != nil {
		log.Printf("render public content %d: %v", item.ID, err)
		http.Error(w, "failed to render content", http.StatusInternalServerError)
		return
	}
	templateName := "page.html"
	pageType := seo.TypePage
	if item.Type == content.TypePost {
		templateName = "single.html"
		pageType = seo.TypePost
	}
	var builderHTML template.HTML
	if item.Type == content.TypePage && item.LayoutID > 0 {
		if layout, lerr := h.builder.GetLayout(r.Context(), item.LayoutID); lerr == nil {
			builderHTML, err = builder.RenderBlocks(r.Context(), layout.Blocks, builder.RenderContext{
				DB:              h.db,
				PagePath:        "/" + item.Slug,
				SubmittedFormID: r.URL.Query().Get("form_submitted"),
				ErrorFormID:     r.URL.Query().Get("form_error"),
				FilterMarkdown: func(md string) string {
					return h.filteredMarkdown(r, md)
				},
			})
			if err != nil {
				log.Printf("render page layout %d: %v", item.LayoutID, err)
				http.Error(w, "failed to render content", http.StatusInternalServerError)
				return
			}
		}
	}
	public := newPublicItem(item, rendered)
	if tagLinks, tagErr := content.TagsForItem(r.Context(), h.db, item.ID); tagErr == nil {
		public.TagLinks = tagLinks
	}
	var relatedPosts []publicItem
	if item.Type == content.TypePost {
		if relItems, relErr := content.RelatedPosts(r.Context(), h.db, item.ID, 3); relErr == nil {
			if renderedRel, rErr := h.renderPublicItemList(r, relItems); rErr == nil {
				relatedPosts = renderedRel
			}
		}
	}
	seoParams := seo.Params{
		Type:            pageType,
		Item:            &item,
		FallbackSummary: public.Summary,
	}
	h.renderTheme(w, r, templateName, http.StatusOK, seoParams, func(themeName string, headTags template.HTML, site siteconfig.Settings) any {
		return publicContentData{
			Theme:        themeName,
			Site:         site,
			Item:         public,
			HeadTags:     headTags,
			BuilderHTML:  builderHTML,
			RelatedPosts: relatedPosts,
		}
	})
}

func newPublicItem(item content.Item, rendered string) publicItem {
	plainText := stdhtml.UnescapeString(publicHTMLTagPattern.ReplaceAllString(rendered, " "))
	plainText = strings.Join(strings.Fields(plainText), " ")
	readingMinutes := content.ReadingTimeMinutes(plainText)
	summary := plainText
	runes := []rune(summary)
	if len(runes) > 200 {
		summary = strings.TrimSpace(string(runes[:200])) + "…"
	}
	publishedDate := item.PublishedAt
	if parsed, err := time.Parse(time.RFC3339Nano, item.PublishedAt); err == nil {
		publishedDate = parsed.Format("02/01/2006")
	}
	return publicItem{
		Item: item, HTML: template.HTML(rendered), Summary: summary,
		FormattedPublishedAt: publishedDate, ReadingTimeMinutes: readingMinutes,
	}
}

func (h *handler) renderAdmin(w http.ResponseWriter, page adminPage) {
	h.renderAdminStatus(w, page, http.StatusOK)
}

func (h *handler) renderAdminStatus(w http.ResponseWriter, page adminPage, status int) {
	if len(page.PluginMenus) == 0 && h.plugins != nil {
		page.PluginMenus = h.plugins.AdminMenus(context.Background())
	}
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
	seoParams := seo.Params{
		Type: seo.Type404,
	}
	h.renderTheme(w, r, "404.html", http.StatusNotFound, seoParams, func(themeName string, headTags template.HTML, site siteconfig.Settings) any {
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

func (h *handler) restoreContentRevision(w http.ResponseWriter, r *http.Request) {
	user, ok := h.requireContentAccess(w, r)
	if !ok {
		return
	}
	postID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || postID <= 0 {
		http.NotFound(w, r)
		return
	}
	revID, err := strconv.ParseInt(r.PathValue("revID"), 10, 64)
	if err != nil || revID <= 0 {
		http.NotFound(w, r)
		return
	}
	contentType := r.FormValue("content_type")
	if contentType != content.TypePage {
		contentType = content.TypePost
	}
	if !h.canAccessContent(w, r, user, postID, contentType) {
		return
	}
	item, err := content.RestoreRevision(r.Context(), h.db, postID, revID, user.ID)
	if errors.Is(err, content.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		log.Printf("restore content revision %d/%d: %v", postID, revID, err)
		http.Error(w, "failed to restore revision", http.StatusInternalServerError)
		return
	}
	h.cache.Clear()
	if item.Type == content.TypePage {
		http.Redirect(w, r, fmt.Sprintf("/admin/pages/%d/edit?restored=1", item.ID), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/admin/posts/%d/edit?restored=1", item.ID), http.StatusSeeOther)
}
