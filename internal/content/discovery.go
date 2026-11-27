package content

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strings"
	"unicode"
)

type Tag struct {
	ID   int64
	Name string
	Slug string
}

type ArchiveSitemapEntry struct {
	Slug    string
	LastMod string
}

func CategoryBySlug(ctx context.Context, db *sql.DB, slug string) (Category, error) {
	slug = strings.TrimSpace(strings.ToLower(slug))
	if slug == "" {
		return Category{}, ErrNotFound
	}
	var cat Category
	var parentID sql.NullInt64
	err := db.QueryRowContext(ctx, `
		SELECT id, name, slug, parent_id
		FROM categories
		WHERE slug = ?
	`, slug).Scan(&cat.ID, &cat.Name, &cat.Slug, &parentID)
	if errors.Is(err, sql.ErrNoRows) {
		return Category{}, ErrNotFound
	}
	if err != nil {
		return Category{}, fmt.Errorf("load category by slug: %w", err)
	}
	if parentID.Valid {
		cat.ParentID = parentID.Int64
	}
	return cat, nil
}

func TagBySlug(ctx context.Context, db *sql.DB, slug string) (Tag, error) {
	slug = strings.TrimSpace(strings.ToLower(slug))
	if slug == "" {
		return Tag{}, ErrNotFound
	}
	var tag Tag
	err := db.QueryRowContext(ctx, `
		SELECT id, name, slug
		FROM tags
		WHERE slug = ?
	`, slug).Scan(&tag.ID, &tag.Name, &tag.Slug)
	if errors.Is(err, sql.ErrNoRows) {
		return Tag{}, ErrNotFound
	}
	if err != nil {
		return Tag{}, fmt.Errorf("load tag by slug: %w", err)
	}
	return tag, nil
}

func TagsForItem(ctx context.Context, db *sql.DB, postID int64) ([]Tag, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT tags.id, tags.name, tags.slug
		FROM tags
		INNER JOIN post_tags ON post_tags.tag_id = tags.id
		WHERE post_tags.post_id = ?
		ORDER BY tags.name COLLATE NOCASE, tags.id
	`, postID)
	if err != nil {
		return nil, fmt.Errorf("list item tags with slugs: %w", err)
	}
	defer rows.Close()
	var tags []Tag
	for rows.Next() {
		var tag Tag
		if err := rows.Scan(&tag.ID, &tag.Name, &tag.Slug); err != nil {
			return nil, fmt.Errorf("scan item tag: %w", err)
		}
		tags = append(tags, tag)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read item tags: %w", err)
	}
	return tags, nil
}

func PublishedByCategoryPage(ctx context.Context, db *sql.DB, categorySlug string, page, limit int) ([]Item, int64, error) {
	if page < 1 || limit < 1 || limit > 100 {
		return nil, 0, fmt.Errorf("%w: invalid pagination parameters", ErrInvalidInput)
	}
	categorySlug = strings.TrimSpace(strings.ToLower(categorySlug))
	if categorySlug == "" {
		return nil, 0, ErrNotFound
	}
	var total int64
	if err := db.QueryRowContext(ctx, `
		SELECT COUNT(DISTINCT posts.id)
		FROM posts
		INNER JOIN post_categories ON post_categories.post_id = posts.id
		INNER JOIN categories ON categories.id = post_categories.category_id
		WHERE categories.slug = ? AND posts.content_type = 'post'
		      AND posts.status = 'published' AND posts.deleted_at IS NULL
	`, categorySlug).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count category posts: %w", err)
	}
	rows, err := db.QueryContext(ctx, `
		SELECT DISTINCT posts.id, posts.content_type, posts.title, posts.slug, posts.content, posts.status,
		       posts.featured_image, posts.seo_title, posts.seo_description, posts.canonical_url,
		       posts.og_title, posts.og_description, posts.og_image, posts.twitter_card,
		       posts.schema_type, posts.noindex, COALESCE(posts.layout_id, 0),
		       posts.created_at, posts.updated_at,
		       COALESCE(posts.published_at, ''), COALESCE(users.username, '')
		FROM posts
		INNER JOIN post_categories ON post_categories.post_id = posts.id
		INNER JOIN categories ON categories.id = post_categories.category_id
		LEFT JOIN users ON users.id = posts.author_id
		WHERE categories.slug = ? AND posts.content_type = 'post'
		      AND posts.status = 'published' AND posts.deleted_at IS NULL
		ORDER BY posts.published_at DESC, posts.id DESC
		LIMIT ? OFFSET ?
	`, categorySlug, limit, (page-1)*limit)
	if err != nil {
		return nil, 0, fmt.Errorf("list category posts page: %w", err)
	}
	items, err := scanPublicItemsWithMeta(ctx, db, rows)
	if err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

func PublishedByTagPage(ctx context.Context, db *sql.DB, tagSlug string, page, limit int) ([]Item, int64, error) {
	if page < 1 || limit < 1 || limit > 100 {
		return nil, 0, fmt.Errorf("%w: invalid pagination parameters", ErrInvalidInput)
	}
	tagSlug = strings.TrimSpace(strings.ToLower(tagSlug))
	if tagSlug == "" {
		return nil, 0, ErrNotFound
	}
	var total int64
	if err := db.QueryRowContext(ctx, `
		SELECT COUNT(DISTINCT posts.id)
		FROM posts
		INNER JOIN post_tags ON post_tags.post_id = posts.id
		INNER JOIN tags ON tags.id = post_tags.tag_id
		WHERE tags.slug = ? AND posts.content_type = 'post'
		      AND posts.status = 'published' AND posts.deleted_at IS NULL
	`, tagSlug).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count tag posts: %w", err)
	}
	rows, err := db.QueryContext(ctx, `
		SELECT DISTINCT posts.id, posts.content_type, posts.title, posts.slug, posts.content, posts.status,
		       posts.featured_image, posts.seo_title, posts.seo_description, posts.canonical_url,
		       posts.og_title, posts.og_description, posts.og_image, posts.twitter_card,
		       posts.schema_type, posts.noindex, COALESCE(posts.layout_id, 0),
		       posts.created_at, posts.updated_at,
		       COALESCE(posts.published_at, ''), COALESCE(users.username, '')
		FROM posts
		INNER JOIN post_tags ON post_tags.post_id = posts.id
		INNER JOIN tags ON tags.id = post_tags.tag_id
		LEFT JOIN users ON users.id = posts.author_id
		WHERE tags.slug = ? AND posts.content_type = 'post'
		      AND posts.status = 'published' AND posts.deleted_at IS NULL
		ORDER BY posts.published_at DESC, posts.id DESC
		LIMIT ? OFFSET ?
	`, tagSlug, limit, (page-1)*limit)
	if err != nil {
		return nil, 0, fmt.Errorf("list tag posts page: %w", err)
	}
	items, err := scanPublicItemsWithMeta(ctx, db, rows)
	if err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

func IndexableCategories(ctx context.Context, db *sql.DB) ([]ArchiveSitemapEntry, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT categories.slug, MAX(COALESCE(posts.updated_at, posts.published_at, ''))
		FROM categories
		INNER JOIN post_categories ON post_categories.category_id = categories.id
		INNER JOIN posts ON posts.id = post_categories.post_id
		WHERE posts.content_type = 'post' AND posts.status = 'published'
		      AND posts.deleted_at IS NULL AND posts.noindex = 0
		GROUP BY categories.id, categories.slug
		ORDER BY categories.slug
	`)
	if err != nil {
		return nil, fmt.Errorf("list indexable categories: %w", err)
	}
	defer rows.Close()
	var entries []ArchiveSitemapEntry
	for rows.Next() {
		var entry ArchiveSitemapEntry
		if err := rows.Scan(&entry.Slug, &entry.LastMod); err != nil {
			return nil, fmt.Errorf("scan indexable category: %w", err)
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read indexable categories: %w", err)
	}
	return entries, nil
}

func IndexableTags(ctx context.Context, db *sql.DB) ([]ArchiveSitemapEntry, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT tags.slug, MAX(COALESCE(posts.updated_at, posts.published_at, ''))
		FROM tags
		INNER JOIN post_tags ON post_tags.tag_id = tags.id
		INNER JOIN posts ON posts.id = post_tags.post_id
		WHERE posts.content_type = 'post' AND posts.status = 'published'
		      AND posts.deleted_at IS NULL AND posts.noindex = 0
		GROUP BY tags.id, tags.slug
		ORDER BY tags.slug
	`)
	if err != nil {
		return nil, fmt.Errorf("list indexable tags: %w", err)
	}
	defer rows.Close()
	var entries []ArchiveSitemapEntry
	for rows.Next() {
		var entry ArchiveSitemapEntry
		if err := rows.Scan(&entry.Slug, &entry.LastMod); err != nil {
			return nil, fmt.Errorf("scan indexable tag: %w", err)
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read indexable tags: %w", err)
	}
	return entries, nil
}

func SearchPublishedPage(ctx context.Context, db *sql.DB, rawQuery string, page, limit int) ([]Item, int64, error) {
	if page < 1 || limit < 1 || limit > 100 {
		return nil, 0, fmt.Errorf("%w: invalid pagination parameters", ErrInvalidInput)
	}
	ftsQuery := buildFTSQuery(rawQuery)
	if ftsQuery == "" {
		return nil, 0, nil
	}
	var total int64
	if err := db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM posts_fts
		INNER JOIN posts ON posts.id = posts_fts.rowid
		WHERE posts_fts MATCH ? AND posts.status = 'published' AND posts.deleted_at IS NULL
	`, ftsQuery).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count search results: %w", err)
	}
	rows, err := db.QueryContext(ctx, `
		SELECT posts.id, posts.content_type, posts.title, posts.slug, posts.content, posts.status,
		       posts.featured_image, posts.seo_title, posts.seo_description, posts.canonical_url,
		       posts.og_title, posts.og_description, posts.og_image, posts.twitter_card,
		       posts.schema_type, posts.noindex, COALESCE(posts.layout_id, 0),
		       posts.created_at, posts.updated_at,
		       COALESCE(posts.published_at, ''), COALESCE(users.username, '')
		FROM posts_fts
		INNER JOIN posts ON posts.id = posts_fts.rowid
		LEFT JOIN users ON users.id = posts.author_id
		WHERE posts_fts MATCH ? AND posts.status = 'published' AND posts.deleted_at IS NULL
		ORDER BY bm25(posts_fts), posts.published_at DESC, posts.id DESC
		LIMIT ? OFFSET ?
	`, ftsQuery, limit, (page-1)*limit)
	if err != nil {
		return nil, 0, fmt.Errorf("search published content: %w", err)
	}
	items, err := scanPublicItemsWithMeta(ctx, db, rows)
	if err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

func RelatedPosts(ctx context.Context, db *sql.DB, postID int64, limit int) ([]Item, error) {
	if postID <= 0 || limit < 1 {
		return nil, nil
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
		WHERE posts.id != ? AND posts.content_type = 'post'
		      AND posts.status = 'published' AND posts.deleted_at IS NULL
		      AND (
		          EXISTS (
		              SELECT 1 FROM post_categories pc1
		              INNER JOIN post_categories pc2 ON pc1.category_id = pc2.category_id
		              WHERE pc1.post_id = posts.id AND pc2.post_id = ?
		          )
		          OR EXISTS (
		              SELECT 1 FROM post_tags pt1
		              INNER JOIN post_tags pt2 ON pt1.tag_id = pt2.tag_id
		              WHERE pt1.post_id = posts.id AND pt2.post_id = ?
		          )
		      )
		ORDER BY (
		    (SELECT COUNT(*) FROM post_categories pc1
		     INNER JOIN post_categories pc2 ON pc1.category_id = pc2.category_id
		     WHERE pc1.post_id = posts.id AND pc2.post_id = ?) +
		    (SELECT COUNT(*) FROM post_tags pt1
		     INNER JOIN post_tags pt2 ON pt1.tag_id = pt2.tag_id
		     WHERE pt1.post_id = posts.id AND pt2.post_id = ?)
		) DESC, posts.published_at DESC, posts.id DESC
		LIMIT ?
	`, postID, postID, postID, postID, postID, limit)
	if err != nil {
		return nil, fmt.Errorf("load related posts: %w", err)
	}
	return scanPublicItemsWithMeta(ctx, db, rows)
}

func ReadingTimeMinutes(plainText string) int {
	words := len(strings.Fields(plainText))
	if words == 0 {
		return 1
	}
	minutes := int(math.Ceil(float64(words) / 200.0))
	if minutes < 1 {
		return 1
	}
	return minutes
}

func buildFTSQuery(raw string) string {
	var tokens []string
	var current strings.Builder
	flush := func() {
		if current.Len() > 0 {
			tokens = append(tokens, `"`+current.String()+`"*`)
			current.Reset()
		}
	}
	for _, r := range raw {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			current.WriteRune(unicode.ToLower(r))
		} else {
			flush()
		}
	}
	flush()
	if len(tokens) > 12 {
		tokens = tokens[:12]
	}
	return strings.Join(tokens, " ")
}

func scanPublicItemsWithMeta(ctx context.Context, db *sql.DB, rows *sql.Rows) ([]Item, error) {
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
			&item.CreatedAt, &item.UpdatedAt, &item.PublishedAt, &item.Author,
		); err != nil {
			return nil, fmt.Errorf("scan public content item: %w", err)
		}
		item.Noindex = noindexInt == 1
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read public content items: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close public content rows: %w", err)
	}
	for i := range items {
		var err error
		items[i].Categories, err = itemCategories(ctx, db, items[i].ID)
		if err != nil {
			return nil, err
		}
		items[i].Tags, err = itemTags(ctx, db, items[i].ID)
		if err != nil {
			return nil, err
		}
	}
	return items, nil
}
