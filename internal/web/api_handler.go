package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"net/http"
	"strconv"

	"github.com/Luw17/go-content-go/internal/content"
)

type apiContent struct {
	ID          int64  `json:"id"`
	Title       string `json:"title"`
	Slug        string `json:"slug"`
	Content     string `json:"content"`
	Status      string `json:"status"`
	PublishedAt string `json:"published_at"`
}

type apiPostList struct {
	Posts      []apiContent `json:"posts"`
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
		log.Printf("list API posts: %v", err)
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

func (h *handler) apiPost(w http.ResponseWriter, r *http.Request) {
	h.apiContentBySlug(w, r, content.TypePost)
}

func (h *handler) apiPage(w http.ResponseWriter, r *http.Request) {
	h.apiContentBySlug(w, r, content.TypePage)
}

func (h *handler) apiContentBySlug(w http.ResponseWriter, r *http.Request, contentType string) {
	item, err := content.PublishedBySlugAndType(r.Context(), h.db, r.PathValue("slug"), contentType)
	if errors.Is(err, content.ErrNotFound) {
		writeAPIError(w, http.StatusNotFound, "not_found", "published content was not found")
		return
	}
	if err != nil {
		log.Printf("load API %s: %v", contentType, err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "failed to load content")
		return
	}
	writeJSON(w, http.StatusOK, apiContentFrom(item))
}

func (h *handler) apiNotFound(w http.ResponseWriter, _ *http.Request) {
	writeAPIError(w, http.StatusNotFound, "not_found", "API endpoint was not found")
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
	return apiContent{
		ID:          item.ID,
		Title:       item.Title,
		Slug:        item.Slug,
		Content:     item.Content,
		Status:      item.Status,
		PublishedAt: item.PublishedAt,
	}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "public, max-age=30")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		log.Printf("encode API response: %v", err)
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
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		w.Header().Set("Access-Control-Max-Age", "600")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
