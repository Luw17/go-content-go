package content

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

type Revision struct {
	ID        int64
	PostID    int64
	Author    string
	Title     string
	Slug      string
	Content   string
	Status    string
	CreatedAt string
}

type AdminFilter struct {
	ContentType string
	Trashed     bool
	AuthorID    int64
	Query       string
	Status      string
	Page        int
	PerPage     int
}

func normalizeScheduledAt(status, raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if status == StatusPublished || raw == "" {
		return "", nil
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if parsed, err := time.Parse(layout, raw); err == nil {
			return parsed.UTC().Format(time.RFC3339), nil
		}
	}
	for _, layout := range []string{"2006-01-02T15:04", "2006-01-02 15:04:05"} {
		if parsed, err := time.ParseInLocation(layout, raw, time.Local); err == nil {
			return parsed.UTC().Format(time.RFC3339), nil
		}
	}
	return "", fmt.Errorf("%w: invalid scheduled_at timestamp", ErrInvalidInput)
}

func PublishDueScheduled(ctx context.Context, db *sql.DB, now time.Time) (int, error) {
	nowStr := now.UTC().Format(time.RFC3339)
	nowNano := now.UTC().Format(time.RFC3339Nano)
	res, err := db.ExecContext(ctx, `
		UPDATE posts
		SET status = 'published',
		    published_at = CASE WHEN COALESCE(published_at, '') = '' THEN ? ELSE published_at END,
		    scheduled_at = '',
		    updated_at = ?
		WHERE status = 'draft'
		  AND deleted_at IS NULL
		  AND scheduled_at != ''
		  AND scheduled_at <= ?
	`, nowNano, nowNano, nowStr)
	if err != nil {
		return 0, fmt.Errorf("publish due scheduled posts: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("check scheduled publish count: %w", err)
	}
	return int(affected), nil
}

func ListRevisions(ctx context.Context, db *sql.DB, postID int64, limit int) ([]Revision, error) {
	if postID <= 0 {
		return nil, nil
	}
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := db.QueryContext(ctx, `
		SELECT post_revisions.id, post_revisions.post_id, COALESCE(users.username, ''),
		       post_revisions.title, post_revisions.slug, post_revisions.content,
		       post_revisions.status, post_revisions.created_at
		FROM post_revisions
		LEFT JOIN users ON users.id = post_revisions.author_id
		WHERE post_revisions.post_id = ?
		ORDER BY post_revisions.id DESC
		LIMIT ?
	`, postID, limit)
	if err != nil {
		return nil, fmt.Errorf("list revisions: %w", err)
	}
	defer rows.Close()
	var items []Revision
	for rows.Next() {
		var r Revision
		if err := rows.Scan(&r.ID, &r.PostID, &r.Author, &r.Title, &r.Slug, &r.Content, &r.Status, &r.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan revision: %w", err)
		}
		items = append(items, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read revisions: %w", err)
	}
	return items, nil
}

func RestoreRevision(ctx context.Context, db *sql.DB, postID, revisionID, authorID int64) (Item, error) {
	if postID <= 0 || revisionID <= 0 {
		return Item{}, ErrNotFound
	}
	var rev Revision
	err := db.QueryRowContext(ctx, `
		SELECT id, post_id, title, slug, content, status, created_at
		FROM post_revisions
		WHERE id = ? AND post_id = ?
	`, revisionID, postID).Scan(&rev.ID, &rev.PostID, &rev.Title, &rev.Slug, &rev.Content, &rev.Status, &rev.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Item{}, ErrNotFound
	}
	if err != nil {
		return Item{}, fmt.Errorf("load revision: %w", err)
	}
	var contentType string
	if err := db.QueryRowContext(ctx, `SELECT content_type FROM posts WHERE id = ? AND deleted_at IS NULL`, postID).Scan(&contentType); errors.Is(err, sql.ErrNoRows) {
		return Item{}, ErrNotFound
	} else if err != nil {
		return Item{}, fmt.Errorf("load post type for revision restore: %w", err)
	}
	current, err := Get(ctx, db, postID, contentType)
	if err != nil {
		return Item{}, err
	}
	catIDs := make([]int64, 0, len(current.Categories))
	for _, c := range current.Categories {
		catIDs = append(catIDs, c.ID)
	}
	return Save(ctx, db, Input{
		ID:             current.ID,
		Type:           current.Type,
		Title:          rev.Title,
		Slug:           current.Slug,
		Content:        rev.Content,
		Status:         current.Status,
		FeaturedImage:  current.FeaturedImage,
		SEOTitle:       current.SEOTitle,
		SEODescription: current.SEODescription,
		CanonicalURL:   current.CanonicalURL,
		OGTitle:        current.OGTitle,
		OGDescription:  current.OGDescription,
		OGImage:        current.OGImage,
		TwitterCard:    current.TwitterCard,
		SchemaType:     current.SchemaType,
		Noindex:        current.Noindex,
		LayoutID:       current.LayoutID,
		ScheduledAt:    current.ScheduledAt,
		CategoryIDs:    catIDs,
		Tags:           strings.Join(current.Tags, ", "),
	}, authorID)
}

func ListFiltered(ctx context.Context, db *sql.DB, f AdminFilter) ([]Item, int64, error) {
	if !validType(f.ContentType) {
		return nil, 0, fmt.Errorf("%w: unsupported content type", ErrInvalidInput)
	}
	if f.Page < 1 {
		f.Page = 1
	}
	if f.PerPage < 1 || f.PerPage > 100 {
		f.PerPage = 20
	}
	where := []string{"content_type = ?"}
	args := []any{f.ContentType}
	if f.Trashed {
		where = append(where, "deleted_at IS NOT NULL")
	} else {
		where = append(where, "deleted_at IS NULL")
	}
	if f.AuthorID > 0 {
		where = append(where, "author_id = ?")
		args = append(args, f.AuthorID)
	}
	if f.Status == StatusDraft || f.Status == StatusPublished {
		where = append(where, "status = ?")
		args = append(args, f.Status)
	}
	q := strings.TrimSpace(f.Query)
	if q != "" {
		like := "%" + strings.ToLower(q) + "%"
		where = append(where, "(LOWER(title) LIKE ? OR LOWER(slug) LIKE ?)")
		args = append(args, like, like)
	}
	clause := strings.Join(where, " AND ")

	var total int64
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM posts WHERE "+clause, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count filtered content: %w", err)
	}

	listArgs := append(append([]any(nil), args...), f.PerPage, (f.Page-1)*f.PerPage)
	rows, err := db.QueryContext(ctx, `
		SELECT id, content_type, title, slug, content, status, featured_image,
		       seo_title, seo_description, canonical_url, og_title, og_description,
		       og_image, twitter_card, schema_type, noindex, COALESCE(layout_id, 0),
		       created_at, updated_at, COALESCE(published_at, ''), COALESCE(scheduled_at, '')
		FROM posts
		WHERE `+clause+`
		ORDER BY updated_at DESC, id DESC
		LIMIT ? OFFSET ?
	`, listArgs...)
	if err != nil {
		return nil, 0, fmt.Errorf("list filtered content: %w", err)
	}
	defer rows.Close()
	var items []Item
	for rows.Next() {
		var item Item
		var noindexInt int
		if err := rows.Scan(
			&item.ID, &item.Type, &item.Title, &item.Slug, &item.Content, &item.Status,
			&item.FeaturedImage, &item.SEOTitle, &item.SEODescription, &item.CanonicalURL,
			&item.OGTitle, &item.OGDescription, &item.OGImage, &item.TwitterCard,
			&item.SchemaType, &noindexInt, &item.LayoutID,
			&item.CreatedAt, &item.UpdatedAt, &item.PublishedAt, &item.ScheduledAt,
		); err != nil {
			return nil, 0, fmt.Errorf("scan filtered content: %w", err)
		}
		item.Noindex = noindexInt == 1
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("read filtered content: %w", err)
	}
	return items, total, nil
}

func PublishedPagesPage(ctx context.Context, db *sql.DB, page, limit int) ([]Item, int64, error) {
	if page < 1 || limit < 1 || limit > 100 {
		return nil, 0, fmt.Errorf("%w: invalid pagination parameters", ErrInvalidInput)
	}
	var total int64
	if err := db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM posts
		WHERE content_type = 'page' AND status = 'published' AND deleted_at IS NULL
	`).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count public pages: %w", err)
	}
	rows, err := db.QueryContext(ctx, `
		SELECT posts.id, posts.content_type, posts.title, posts.slug, posts.content, posts.status,
		       posts.featured_image, posts.seo_title, posts.seo_description, posts.canonical_url,
		       posts.og_title, posts.og_description, posts.og_image, posts.twitter_card,
		       posts.schema_type, posts.noindex, COALESCE(posts.layout_id, 0),
		       posts.created_at, posts.updated_at,
		       COALESCE(posts.published_at, ''), COALESCE(users.username, '')
		FROM posts
		LEFT JOIN users ON users.id = posts.author_id
		WHERE posts.content_type = 'page' AND posts.status = 'published' AND posts.deleted_at IS NULL
		ORDER BY posts.updated_at DESC, posts.id DESC
		LIMIT ? OFFSET ?
	`, limit, (page-1)*limit)
	if err != nil {
		return nil, 0, fmt.Errorf("list public pages: %w", err)
	}
	items, err := scanPublicItemsWithMeta(ctx, db, rows)
	if err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

func BySlugAndTypeAnyStatus(ctx context.Context, db *sql.DB, slug, contentType string) (Item, error) {
	slug = strings.TrimSpace(strings.ToLower(slug))
	if slug == "" || !validType(contentType) {
		return Item{}, ErrNotFound
	}
	var item Item
	var noindexInt int
	err := db.QueryRowContext(ctx, `
		SELECT posts.id, posts.content_type, posts.title, posts.slug, posts.content, posts.status,
		       posts.featured_image, posts.seo_title, posts.seo_description, posts.canonical_url,
		       posts.og_title, posts.og_description, posts.og_image, posts.twitter_card,
		       posts.schema_type, posts.noindex, COALESCE(posts.layout_id, 0),
		       posts.created_at, posts.updated_at,
		       COALESCE(posts.published_at, ''), COALESCE(users.username, '')
		FROM posts
		LEFT JOIN users ON users.id = posts.author_id
		WHERE posts.slug = ? AND posts.content_type = ? AND posts.deleted_at IS NULL
	`, slug, contentType).Scan(
		&item.ID, &item.Type, &item.Title, &item.Slug, &item.Content, &item.Status,
		&item.FeaturedImage, &item.SEOTitle, &item.SEODescription, &item.CanonicalURL,
		&item.OGTitle, &item.OGDescription, &item.OGImage, &item.TwitterCard,
		&item.SchemaType, &noindexInt, &item.LayoutID,
		&item.CreatedAt, &item.UpdatedAt, &item.PublishedAt, &item.Author,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Item{}, ErrNotFound
	}
	if err != nil {
		return Item{}, fmt.Errorf("load preview content: %w", err)
	}
	item.Noindex = noindexInt == 1
	item.Categories, err = itemCategories(ctx, db, item.ID)
	if err != nil {
		return Item{}, err
	}
	item.Tags, err = itemTags(ctx, db, item.ID)
	if err != nil {
		return Item{}, err
	}
	return item, nil
}
