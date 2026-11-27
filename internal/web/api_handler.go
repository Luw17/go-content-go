package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/Luw17/go-content-go/internal/auth"
	"github.com/Luw17/go-content-go/internal/content"
	"github.com/Luw17/go-content-go/internal/navigation"
	"github.com/Luw17/go-content-go/internal/siteconfig"
)

type apiSEO struct {
	Title        string `json:"title,omitempty"`
	Description  string `json:"description,omitempty"`
	CanonicalURL string `json:"canonical_url,omitempty"`
	OGImage      string `json:"og_image,omitempty"`
	Noindex      bool   `json:"noindex"`
}

type apiCategory struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Slug     string `json:"slug"`
	ParentID int64  `json:"parent_id,omitempty"`
}

type apiContent struct {
	ID                 int64         `json:"id"`
	Type               string        `json:"type"`
	Title              string        `json:"title"`
	Slug               string        `json:"slug"`
	Content            string        `json:"content"`
	ContentHTML        string        `json:"content_html"`
	Summary            string        `json:"summary"`
	Status             string        `json:"status"`
	FeaturedImage      string        `json:"featured_image,omitempty"`
	Author             string        `json:"author,omitempty"`
	Categories         []apiCategory `json:"categories,omitempty"`
	Tags               []string      `json:"tags,omitempty"`
	ReadingTimeMinutes int           `json:"reading_time_minutes"`
	PublishedAt        string        `json:"published_at"`
	UpdatedAt          string        `json:"updated_at,omitempty"`
	SEO                apiSEO        `json:"seo"`
}

type apiPostList struct {
	Posts      []apiContent `json:"posts"`
	Page       int          `json:"page"`
	Limit      int          `json:"limit"`
	Total      int64        `json:"total"`
	TotalPages int          `json:"total_pages"`
}

type apiPageList struct {
	Pages      []apiContent `json:"pages"`
	Page       int          `json:"page"`
	Limit      int          `json:"limit"`
	Total      int64        `json:"total"`
	TotalPages int          `json:"total_pages"`
}

func (h *handler) apiPosts(w http.ResponseWriter, r *http.Request) {
	page, limit, err := parsePagination(r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_pagination", err.Error())
		return
	}
	items, total, err := content.PublishedPage(r.Context(), h.db, page, limit)
	if err != nil {
		slog.Error("list API posts", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "failed to load posts")
		return
	}
	posts := make([]apiContent, 0, len(items))
	for _, item := range items {
		posts = append(posts, apiContentFrom(item))
	}
	totalPages := 0
	if total > 0 {
		totalPages = int(1 + (total-1)/int64(limit))
	}
	writeJSON(w, http.StatusOK, apiPostList{
		Posts:      posts,
		Page:       page,
		Limit:      limit,
		Total:      total,
		TotalPages: totalPages,
	})
}

func (h *handler) apiPages(w http.ResponseWriter, r *http.Request) {
	page, limit, err := parsePagination(r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_pagination", err.Error())
		return
	}
	items, total, err := content.PublishedPagesPage(r.Context(), h.db, page, limit)
	if err != nil {
		slog.Error("list API pages", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "failed to load pages")
		return
	}
	pages := make([]apiContent, 0, len(items))
	for _, item := range items {
		pages = append(pages, apiContentFrom(item))
	}
	totalPages := 0
	if total > 0 {
		totalPages = int(1 + (total-1)/int64(limit))
	}
	writeJSON(w, http.StatusOK, apiPageList{
		Pages:      pages,
		Page:       page,
		Limit:      limit,
		Total:      total,
		TotalPages: totalPages,
	})
}

func (h *handler) apiPost(w http.ResponseWriter, r *http.Request) {
	h.apiContentBySlug(w, r, content.TypePost)
}

func (h *handler) apiPage(w http.ResponseWriter, r *http.Request) {
	h.apiContentBySlug(w, r, content.TypePage)
}

func (h *handler) apiContentBySlug(w http.ResponseWriter, r *http.Request, contentType string) {
	preview := r.URL.Query().Get("preview") == "1" || r.URL.Query().Get("preview") == "true"
	if preview {
		rawKey := extractAPIKey(r)
		valid, err := auth.ValidateAPIKey(r.Context(), h.db, rawKey)
		if err != nil {
			slog.Error("validate API key", "error", err)
			writeAPIError(w, http.StatusInternalServerError, "internal_error", "failed to validate API key")
			return
		}
		if !valid {
			writeAPIError(w, http.StatusUnauthorized, "unauthorized", "valid API key required for draft preview")
			return
		}
		item, err := content.BySlugAndTypeAnyStatus(r.Context(), h.db, r.PathValue("slug"), contentType)
		if errors.Is(err, content.ErrNotFound) {
			writeAPIError(w, http.StatusNotFound, "not_found", "content was not found")
			return
		}
		if err != nil {
			slog.Error("load preview API content", "type", contentType, "error", err)
			writeAPIError(w, http.StatusInternalServerError, "internal_error", "failed to load content")
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSONUncached(w, http.StatusOK, apiContentFrom(item))
		return
	}

	item, err := content.PublishedBySlugAndType(r.Context(), h.db, r.PathValue("slug"), contentType)
	if errors.Is(err, content.ErrNotFound) {
		writeAPIError(w, http.StatusNotFound, "not_found", "published content was not found")
		return
	}
	if err != nil {
		slog.Error("load API content", "type", contentType, "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "failed to load content")
		return
	}
	writeJSON(w, http.StatusOK, apiContentFrom(item))
}

func (h *handler) apiCategories(w http.ResponseWriter, r *http.Request) {
	cats, err := content.Categories(r.Context(), h.db)
	if err != nil {
		slog.Error("list API categories", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "failed to load categories")
		return
	}
	out := make([]apiCategory, 0, len(cats))
	for _, c := range cats {
		out = append(out, apiCategory{ID: c.ID, Name: c.Name, Slug: c.Slug, ParentID: c.ParentID})
	}
	writeJSON(w, http.StatusOK, map[string]any{"categories": out})
}

func (h *handler) apiMenuByLocation(w http.ResponseWriter, r *http.Request) {
	location := strings.TrimSpace(r.PathValue("location"))
	if location != navigation.LocationPrimary && location != navigation.LocationFooter {
		writeAPIError(w, http.StatusNotFound, "not_found", "menu location was not found")
		return
	}
	menus, err := h.menus.List(r.Context())
	if err != nil {
		slog.Error("list API menus", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "failed to load menu")
		return
	}
	for _, menu := range menus {
		if menu.Location == location {
			items, err := h.menus.Items(r.Context(), menu.ID)
			if err != nil {
				slog.Error("load API menu items", "menu_id", menu.ID, "error", err)
				writeAPIError(w, http.StatusInternalServerError, "internal_error", "failed to load menu items")
				return
			}
			outItems := make([]map[string]any, 0, len(items))
			for _, it := range items {
				outItems = append(outItems, map[string]any{
					"id":        it.ID,
					"label":     it.Label,
					"type":      it.Type,
					"url":       it.URL,
					"object_id": it.ObjectID,
					"order":     it.Order,
				})
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"id":       menu.ID,
				"name":     menu.Name,
				"location": menu.Location,
				"items":    outItems,
			})
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"location": location,
		"items":    []any{},
	})
}

func (h *handler) apiSiteInfo(w http.ResponseWriter, r *http.Request) {
	settings, err := siteconfig.Load(r.Context(), h.db)
	if err != nil {
		slog.Error("load API site info", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "failed to load site info")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"title":            settings.Title,
		"tagline":          settings.Tagline,
		"logo":             settings.Logo,
		"favicon":          settings.Favicon,
		"apple_touch_icon": settings.AppleTouchIcon,
		"site_url":         settings.SiteURL,
		"show_on_front":    settings.ShowOnFront,
		"seo": map[string]any{
			"default_description": settings.SEODefaultDescription,
			"default_og_image":    settings.SEODefaultOGImage,
			"twitter_handle":      settings.SEOTwitterHandle,
			"robots_indexing":     settings.SEORobotsIndexing,
		},
	})
}

func (h *handler) apiNotFound(w http.ResponseWriter, _ *http.Request) {
	writeAPIError(w, http.StatusNotFound, "not_found", "API endpoint was not found")
}

func extractAPIKey(r *http.Request) string {
	if key := strings.TrimSpace(r.Header.Get("X-API-Key")); key != "" {
		return key
	}
	authz := strings.TrimSpace(r.Header.Get("Authorization"))
	if strings.HasPrefix(strings.ToLower(authz), "bearer ") {
		return strings.TrimSpace(authz[7:])
	}
	return ""
}

func parsePagination(r *http.Request) (int, int, error) {
	page := 1
	limit := 10
	var err error
	if values, exists := r.URL.Query()["page"]; exists {
		if len(values) != 1 || values[0] == "" {
			return 0, 0, fmt.Errorf("page must be a single positive integer")
		}
		page, err = strconv.Atoi(values[0])
		if err != nil || page < 1 {
			return 0, 0, fmt.Errorf("page must be a positive integer")
		}
	}
	if values, exists := r.URL.Query()["limit"]; exists {
		if len(values) != 1 || values[0] == "" {
			return 0, 0, fmt.Errorf("limit must be a single integer between 1 and 100")
		}
		limit, err = strconv.Atoi(values[0])
		if err != nil || limit < 1 || limit > 100 {
			return 0, 0, fmt.Errorf("limit must be an integer between 1 and 100")
		}
	}
	if page-1 > math.MaxInt/limit {
		return 0, 0, fmt.Errorf("requested page is too large")
	}
	return page, limit, nil
}

func apiContentFrom(item content.Item) apiContent {
	rendered, err := content.RenderMarkdown(item.Content)
	if err != nil {
		rendered = ""
	}
	pub := newPublicItem(item, rendered)
	cats := make([]apiCategory, 0, len(item.Categories))
	for _, c := range item.Categories {
		cats = append(cats, apiCategory{ID: c.ID, Name: c.Name, Slug: c.Slug, ParentID: c.ParentID})
	}
	return apiContent{
		ID:                 item.ID,
		Type:               item.Type,
		Title:              item.Title,
		Slug:               item.Slug,
		Content:            item.Content,
		ContentHTML:        rendered,
		Summary:            pub.Summary,
		Status:             item.Status,
		FeaturedImage:      item.FeaturedImage,
		Author:             item.Author,
		Categories:         cats,
		Tags:               item.Tags,
		ReadingTimeMinutes: pub.ReadingTimeMinutes,
		PublishedAt:        item.PublishedAt,
		UpdatedAt:          item.UpdatedAt,
		SEO: apiSEO{
			Title:        item.SEOTitle,
			Description:  item.SEODescription,
			CanonicalURL: item.CanonicalURL,
			OGImage:      item.OGImage,
			Noindex:      item.Noindex,
		},
	}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "public, max-age=30")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		slog.Error("encode API response", "error", err)
	}
}

func writeJSONUncached(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		slog.Error("encode uncached API response", "error", err)
	}
}

func writeAPIError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}{
		Error: struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		}{Code: code, Message: message},
	})
}

func publicAPICORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Origin") != "" {
			w.Header().Set("Access-Control-Allow-Origin", "*")
		}
		w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-API-Key")
		w.Header().Set("Access-Control-Max-Age", "600")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
