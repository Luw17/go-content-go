package content

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/microcosm-cc/bluemonday"
	"github.com/yuin/goldmark"
	"golang.org/x/text/unicode/norm"
)

const (
	TypePost        = "post"
	TypePage        = "page"
	StatusDraft     = "draft"
	StatusPublished = "published"

	maxTitleSize   = 200
	maxSlugSize    = 100
	maxContentSize = 1 << 20
	maxImageSize   = 2048
)

var (
	ErrNotFound     = errors.New("content not found")
	ErrInvalidInput = errors.New("invalid content input")
	ErrSlugTaken    = errors.New("slug is already in use")
	slugPattern     = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
	markdown        = goldmark.New()
	htmlPolicy      = bluemonday.UGCPolicy()
)

type Item struct {
	ID            int64
	Type          string
	Title         string
	Slug          string
	Content       string
	Status        string
	FeaturedImage string
	CreatedAt     string
	UpdatedAt     string
	PublishedAt   string
	Author        string
	Categories    []Category
	Tags          []string
}

type Category struct {
	ID       int64
	Name     string
	Slug     string
	ParentID int64
	Depth    int
}

type Input struct {
	ID                  int64
	Type                string
	Title               string
	Slug                string
	Content             string
	Status              string
	FeaturedImage       string
	CategoryIDs         []int64
	NewCategory         string
	NewCategoryParentID int64
	Tags                string
}

func List(ctx context.Context, db *sql.DB, contentType string, trashed bool) ([]Item, error) {
	return list(ctx, db, contentType, trashed, 0)
}

func ListByAuthor(ctx context.Context, db *sql.DB, contentType string, trashed bool, authorID int64) ([]Item, error) {
	if authorID <= 0 {
		return nil, ErrInvalidInput
	}
	return list(ctx, db, contentType, trashed, authorID)
}

func list(ctx context.Context, db *sql.DB, contentType string, trashed bool, authorID int64) ([]Item, error) {
	if !validType(contentType) {
		return nil, fmt.Errorf("%w: unsupported content type", ErrInvalidInput)
	}
	query := `
		SELECT id, content_type, title, slug, content, status, featured_image,
		       created_at, updated_at, COALESCE(published_at, '')
		FROM posts
		WHERE content_type = ? AND `
	if trashed {
		query += "deleted_at IS NOT NULL "
	} else {
		query += "deleted_at IS NULL "
	}
	if authorID > 0 {
		query += "AND author_id = ? "
	}
	query += "ORDER BY updated_at DESC, id DESC"
	var rows *sql.Rows
	var err error
	if authorID > 0 {
		rows, err = db.QueryContext(ctx, query, contentType, authorID)
	} else {
		rows, err = db.QueryContext(ctx, query, contentType)
	}
	if err != nil {
		return nil, fmt.Errorf("list content: %w", err)
	}
	defer rows.Close()

	var items []Item
	for rows.Next() {
		var item Item
		if err := rows.Scan(
			&item.ID, &item.Type, &item.Title, &item.Slug, &item.Content, &item.Status,
			&item.FeaturedImage, &item.CreatedAt, &item.UpdatedAt, &item.PublishedAt,
		); err != nil {
			return nil, fmt.Errorf("scan content: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read content list: %w", err)
	}
	return items, nil
}

func AuthorID(ctx context.Context, db *sql.DB, id int64, contentType string) (int64, error) {
	if id <= 0 || !validType(contentType) {
		return 0, ErrNotFound
	}
	var authorID sql.NullInt64
	err := db.QueryRowContext(ctx, `
		SELECT author_id FROM posts
		WHERE id = ? AND content_type = ?
	`, id, contentType).Scan(&authorID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("load content owner: %w", err)
	}
	if !authorID.Valid {
		return 0, ErrNotFound
	}
	return authorID.Int64, nil
}

func Get(ctx context.Context, db *sql.DB, id int64, contentType string) (Item, error) {
	if id <= 0 || !validType(contentType) {
		return Item{}, ErrNotFound
	}
	var item Item
	err := db.QueryRowContext(ctx, `
		SELECT id, content_type, title, slug, content, status, featured_image,
		       created_at, updated_at, COALESCE(published_at, '')
		FROM posts
		WHERE id = ? AND content_type = ? AND deleted_at IS NULL
	`, id, contentType).Scan(
		&item.ID, &item.Type, &item.Title, &item.Slug, &item.Content, &item.Status,
		&item.FeaturedImage, &item.CreatedAt, &item.UpdatedAt, &item.PublishedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Item{}, ErrNotFound
	}
	if err != nil {
		return Item{}, fmt.Errorf("load content: %w", err)
	}
	item.Categories, err = itemCategories(ctx, db, id)
	if err != nil {
		return Item{}, err
	}
	item.Tags, err = itemTags(ctx, db, id)
	if err != nil {
		return Item{}, err
	}
	return item, nil
}

func Save(ctx context.Context, db *sql.DB, input Input, authorID int64) (Item, error) {
	input.Title = strings.TrimSpace(input.Title)
	input.Content = strings.TrimSpace(input.Content)
	input.FeaturedImage = strings.TrimSpace(input.FeaturedImage)
	input.NewCategory = strings.TrimSpace(input.NewCategory)
	input.Tags = strings.TrimSpace(input.Tags)
	input.Type = strings.TrimSpace(input.Type)
	input.Status = strings.TrimSpace(input.Status)

	if authorID <= 0 || input.ID < 0 || !validType(input.Type) || input.Title == "" || utf8.RuneCountInString(input.Title) > maxTitleSize ||
		len(input.Content) > maxContentSize || len(input.FeaturedImage) > maxImageSize ||
		(input.Status != StatusDraft && input.Status != StatusPublished) {
		return Item{}, ErrInvalidInput
	}
	if !validFeaturedImage(input.FeaturedImage) {
		return Item{}, fmt.Errorf("%w: image URL must be a relative /uploads path or an HTTP(S) URL", ErrInvalidInput)
	}
	slug := Slugify(input.Slug)
	if input.Slug == "" {
		slug = Slugify(input.Title)
	}
	if slug == "" || len(slug) > maxSlugSize || !slugPattern.MatchString(slug) {
		return Item{}, fmt.Errorf("%w: slug must contain lowercase letters, numbers, and hyphens", ErrInvalidInput)
	}

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return Item{}, fmt.Errorf("begin content save: %w", err)
	}
	defer tx.Rollback()

	if input.ID > 0 {
		var exists bool
		if err := tx.QueryRowContext(ctx, `
			SELECT EXISTS(SELECT 1 FROM posts WHERE id = ? AND content_type = ? AND deleted_at IS NULL)
		`, input.ID, input.Type).Scan(&exists); err != nil {
			return Item{}, fmt.Errorf("check content before update: %w", err)
		}
		if !exists {
			return Item{}, ErrNotFound
		}
	}
	slug, err = availableSlug(ctx, tx, slug, input.ID, input.Slug == "")
	if err != nil {
		return Item{}, err
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	var publishedAt any
	if input.Status == StatusPublished {
		publishedAt = now
		if input.ID > 0 {
			var existing string
			if err := tx.QueryRowContext(ctx, "SELECT COALESCE(published_at, '') FROM posts WHERE id = ?", input.ID).Scan(&existing); err != nil {
				return Item{}, fmt.Errorf("load publication date: %w", err)
			}
			if existing != "" {
				publishedAt = existing
			}
		}
	}

	var id int64
	if input.ID == 0 {
		result, err := tx.ExecContext(ctx, `
			INSERT INTO posts (author_id, title, slug, content, status, created_at, updated_at, published_at,
			                   content_type, featured_image)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, authorID, input.Title, slug, input.Content, input.Status, now, now, publishedAt, input.Type, input.FeaturedImage)
		if err != nil {
			return Item{}, translateContentError(err)
		}
		id, err = result.LastInsertId()
		if err != nil {
			return Item{}, fmt.Errorf("get inserted content ID: %w", err)
		}
	} else {
		id = input.ID
		if _, err := tx.ExecContext(ctx, `
			UPDATE posts
			SET title = ?, slug = ?, content = ?, status = ?, updated_at = ?, published_at = ?, featured_image = ?
			WHERE id = ? AND content_type = ?
		`, input.Title, slug, input.Content, input.Status, now, publishedAt, input.FeaturedImage, id, input.Type); err != nil {
			return Item{}, translateContentError(err)
		}
	}

	if input.Type == TypePost {
		if err := saveCategories(ctx, tx, id, input); err != nil {
			return Item{}, err
		}
		if err := saveTags(ctx, tx, id, input.Tags); err != nil {
			return Item{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return Item{}, fmt.Errorf("commit content save: %w", err)
	}
	return Get(ctx, db, id, input.Type)
}

func Categories(ctx context.Context, db *sql.DB) ([]Category, error) {
	rows, err := db.QueryContext(ctx, "SELECT id, name, slug, COALESCE(parent_id, 0) FROM categories ORDER BY name")
	if err != nil {
		return nil, fmt.Errorf("list categories: %w", err)
	}
	defer rows.Close()

	var categories []Category
	byID := make(map[int64]Category)
	for rows.Next() {
		var category Category
		if err := rows.Scan(&category.ID, &category.Name, &category.Slug, &category.ParentID); err != nil {
			return nil, fmt.Errorf("scan category: %w", err)
		}
		byID[category.ID] = category
		categories = append(categories, category)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read categories: %w", err)
	}
	for index := range categories {
		categories[index].Depth = categoryDepth(categories[index], byID)
	}
	sort.SliceStable(categories, func(i, j int) bool {
		if categories[i].ParentID == categories[j].ParentID {
			return strings.ToLower(categories[i].Name) < strings.ToLower(categories[j].Name)
		}
		return categoryPath(categories[i], byID) < categoryPath(categories[j], byID)
	})
	return categories, nil
}

func PublishedBySlug(ctx context.Context, db *sql.DB, slug string) (Item, error) {
	if len(slug) > maxSlugSize || !slugPattern.MatchString(slug) {
		return Item{}, ErrNotFound
	}
	var item Item
	err := db.QueryRowContext(ctx, `
		SELECT posts.id, posts.content_type, posts.title, posts.slug, posts.content, posts.status,
		       posts.featured_image, posts.created_at, posts.updated_at,
		       COALESCE(posts.published_at, ''), COALESCE(users.username, '')
		FROM posts
		LEFT JOIN users ON users.id = posts.author_id
		WHERE slug = ? AND status = 'published' AND deleted_at IS NULL
	`, slug).Scan(
		&item.ID, &item.Type, &item.Title, &item.Slug, &item.Content, &item.Status,
		&item.FeaturedImage, &item.CreatedAt, &item.UpdatedAt, &item.PublishedAt, &item.Author,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Item{}, ErrNotFound
	}
	if err != nil {
		return Item{}, fmt.Errorf("load public content: %w", err)
	}
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

func Published(ctx context.Context, db *sql.DB, limit int) ([]Item, error) {
	if limit < 1 || limit > 100 {
		return nil, fmt.Errorf("%w: public list limit must be between 1 and 100", ErrInvalidInput)
	}
	rows, err := db.QueryContext(ctx, `
		SELECT posts.id, posts.content_type, posts.title, posts.slug, posts.content, posts.status,
		       posts.featured_image, posts.created_at, posts.updated_at,
		       COALESCE(posts.published_at, ''), COALESCE(users.username, '')
		FROM posts
		LEFT JOIN users ON users.id = posts.author_id
		WHERE posts.content_type = 'post' AND posts.status = 'published' AND posts.deleted_at IS NULL
		ORDER BY posts.published_at DESC, posts.id DESC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("list public content: %w", err)
	}
	defer rows.Close()

	var items []Item
	for rows.Next() {
		var item Item
		if err := rows.Scan(
			&item.ID, &item.Type, &item.Title, &item.Slug, &item.Content, &item.Status,
			&item.FeaturedImage, &item.CreatedAt, &item.UpdatedAt, &item.PublishedAt, &item.Author,
		); err != nil {
			return nil, fmt.Errorf("scan public content: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read public content: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close public content rows: %w", err)
	}
	for index := range items {
		items[index].Categories, err = itemCategories(ctx, db, items[index].ID)
		if err != nil {
			return nil, err
		}
		items[index].Tags, err = itemTags(ctx, db, items[index].ID)
		if err != nil {
			return nil, err
		}
	}
	return items, nil
}

func PublishedPage(ctx context.Context, db *sql.DB, page, limit int) ([]Item, int64, error) {
	if page < 1 || limit < 1 || limit > 100 || int64(page-1) > int64(^uint64(0)>>1)/int64(limit) {
		return nil, 0, fmt.Errorf("%w: page must be positive and limit must be between 1 and 100", ErrInvalidInput)
	}
	var total int64
	if err := db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM posts
		WHERE content_type = 'post' AND status = 'published' AND deleted_at IS NULL
	`).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count public posts: %w", err)
	}

	rows, err := db.QueryContext(ctx, `
		SELECT posts.id, posts.content_type, posts.title, posts.slug, posts.content, posts.status,
		       posts.featured_image, posts.created_at, posts.updated_at,
		       COALESCE(posts.published_at, ''), COALESCE(users.username, '')
		FROM posts
		LEFT JOIN users ON users.id = posts.author_id
		WHERE posts.content_type = 'post' AND posts.status = 'published' AND posts.deleted_at IS NULL
		ORDER BY posts.published_at DESC, posts.id DESC
		LIMIT ? OFFSET ?
	`, limit, (page-1)*limit)
	if err != nil {
		return nil, 0, fmt.Errorf("list public posts page: %w", err)
	}
	defer rows.Close()

	var items []Item
	for rows.Next() {
		var item Item
		if err := rows.Scan(
			&item.ID, &item.Type, &item.Title, &item.Slug, &item.Content, &item.Status,
			&item.FeaturedImage, &item.CreatedAt, &item.UpdatedAt, &item.PublishedAt, &item.Author,
		); err != nil {
			return nil, 0, fmt.Errorf("scan public post: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("read public posts page: %w", err)
	}
	return items, total, nil
}

func PublishedBySlugAndType(ctx context.Context, db *sql.DB, slug, contentType string) (Item, error) {
	if !validType(contentType) {
		return Item{}, ErrNotFound
	}
	item, err := PublishedBySlug(ctx, db, slug)
	if err != nil {
		return Item{}, err
	}
	if item.Type != contentType {
		return Item{}, ErrNotFound
	}
	return item, nil
}

func UpdateStatus(ctx context.Context, db *sql.DB, id int64, contentType, status string) error {
	if id <= 0 || !validType(contentType) || (status != StatusDraft && status != StatusPublished) {
		return ErrInvalidInput
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	result, err := db.ExecContext(ctx, `
		UPDATE posts
		SET status = ?, updated_at = ?,
		    published_at = CASE WHEN ? = 'published' THEN COALESCE(published_at, ?) ELSE NULL END
		WHERE id = ? AND content_type = ? AND deleted_at IS NULL
	`, status, now, status, now, id, contentType)
	if err != nil {
		return fmt.Errorf("update content status: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("check content status update: %w", err)
	}
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}

func Trash(ctx context.Context, db *sql.DB, id int64, contentType string) error {
	if id <= 0 || !validType(contentType) {
		return ErrInvalidInput
	}
	result, err := db.ExecContext(ctx, `
		UPDATE posts SET deleted_at = ?, updated_at = ?, status = 'draft', published_at = NULL
		WHERE id = ? AND content_type = ? AND deleted_at IS NULL
	`, time.Now().UTC().Format(time.RFC3339Nano), time.Now().UTC().Format(time.RFC3339Nano), id, contentType)
	if err != nil {
		return fmt.Errorf("move content to trash: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("check trashed content: %w", err)
	}
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}

func Restore(ctx context.Context, db *sql.DB, id int64, contentType string) error {
	if id <= 0 || !validType(contentType) {
		return ErrInvalidInput
	}
	result, err := db.ExecContext(ctx, `
		UPDATE posts SET deleted_at = NULL, updated_at = ?
		WHERE id = ? AND content_type = ? AND deleted_at IS NOT NULL
	`, time.Now().UTC().Format(time.RFC3339Nano), id, contentType)
	if err != nil {
		return fmt.Errorf("restore content: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("check restored content: %w", err)
	}
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}

func Delete(ctx context.Context, db *sql.DB, id int64, contentType string) error {
	if id <= 0 || !validType(contentType) {
		return ErrInvalidInput
	}
	result, err := db.ExecContext(ctx, "DELETE FROM posts WHERE id = ? AND content_type = ? AND deleted_at IS NOT NULL", id, contentType)
	if err != nil {
		return fmt.Errorf("delete content permanently: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("check permanently deleted content: %w", err)
	}
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}

func RenderMarkdown(source string) (string, error) {
	var rendered strings.Builder
	if err := markdown.Convert([]byte(source), &rendered); err != nil {
		return "", fmt.Errorf("render markdown: %w", err)
	}
	return htmlPolicy.Sanitize(rendered.String()), nil
}

func Slugify(value string) string {
	var result strings.Builder
	value = strings.ToLower(norm.NFD.String(strings.TrimSpace(value)))
	separator := false
	for _, char := range value {
		if unicode.Is(unicode.Mn, char) {
			continue
		}
		if (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') {
			if separator && result.Len() > 0 {
				result.WriteByte('-')
			}
			result.WriteRune(char)
			separator = false
		} else if char == '-' || unicode.IsSpace(char) || unicode.IsPunct(char) {
			separator = true
		}
	}
	return strings.Trim(result.String(), "-")
}

func availableSlug(ctx context.Context, tx *sql.Tx, base string, currentID int64, generated bool) (string, error) {
	for suffix := 1; suffix <= 100; suffix++ {
		slug := base
		if suffix > 1 {
			trailer := fmt.Sprintf("-%d", suffix)
			slug = strings.TrimRight(base, "-")
			if len(slug)+len(trailer) > maxSlugSize {
				slug = strings.TrimRight(slug[:maxSlugSize-len(trailer)], "-")
			}
			slug += trailer
		}
		var exists bool
		if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM posts WHERE slug = ? AND id != ?)", slug, currentID).Scan(&exists); err != nil {
			return "", fmt.Errorf("check content slug: %w", err)
		}
		if !exists {
			return slug, nil
		}
		if !generated {
			return "", ErrSlugTaken
		}
	}
	return "", fmt.Errorf("%w: could not generate a unique slug", ErrSlugTaken)
}

func saveCategories(ctx context.Context, tx *sql.Tx, postID int64, input Input) error {
	if _, err := tx.ExecContext(ctx, "DELETE FROM post_categories WHERE post_id = ?", postID); err != nil {
		return fmt.Errorf("replace post categories: %w", err)
	}
	categoryIDs := make(map[int64]bool)
	for _, id := range input.CategoryIDs {
		if id <= 0 || categoryIDs[id] {
			return fmt.Errorf("%w: invalid category selection", ErrInvalidInput)
		}
		var exists bool
		if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM categories WHERE id = ?)", id).Scan(&exists); err != nil {
			return fmt.Errorf("validate category: %w", err)
		}
		if !exists {
			return fmt.Errorf("%w: category does not exist", ErrInvalidInput)
		}
		categoryIDs[id] = true
	}
	if input.NewCategory != "" {
		if utf8.RuneCountInString(input.NewCategory) > 80 || input.NewCategoryParentID < 0 {
			return fmt.Errorf("%w: invalid new category", ErrInvalidInput)
		}
		if input.NewCategoryParentID > 0 {
			var exists bool
			if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM categories WHERE id = ?)", input.NewCategoryParentID).Scan(&exists); err != nil {
				return fmt.Errorf("validate parent category: %w", err)
			}
			if !exists {
				return fmt.Errorf("%w: parent category does not exist", ErrInvalidInput)
			}
		}
		categorySlug := Slugify(input.NewCategory)
		if categorySlug == "" {
			return fmt.Errorf("%w: invalid category name", ErrInvalidInput)
		}
		now := time.Now().UTC().Format(time.RFC3339Nano)
		var categoryID int64
		err := tx.QueryRowContext(ctx, "SELECT id FROM categories WHERE slug = ?", categorySlug).Scan(&categoryID)
		if errors.Is(err, sql.ErrNoRows) {
			result, err := tx.ExecContext(ctx, `
				INSERT INTO categories (name, slug, parent_id, created_at, updated_at) VALUES (?, ?, ?, ?, ?)
			`, input.NewCategory, categorySlug, nullableID(input.NewCategoryParentID), now, now)
			if err != nil {
				return fmt.Errorf("create category: %w", err)
			}
			categoryID, err = result.LastInsertId()
			if err != nil {
				return fmt.Errorf("get category ID: %w", err)
			}
		} else if err != nil {
			return fmt.Errorf("load existing category: %w", err)
		}
		categoryIDs[categoryID] = true
	}
	for id := range categoryIDs {
		if _, err := tx.ExecContext(ctx, "INSERT INTO post_categories (post_id, category_id) VALUES (?, ?)", postID, id); err != nil {
			return fmt.Errorf("assign category: %w", err)
		}
	}
	return nil
}

func saveTags(ctx context.Context, tx *sql.Tx, postID int64, raw string) error {
	if _, err := tx.ExecContext(ctx, "DELETE FROM post_tags WHERE post_id = ?", postID); err != nil {
		return fmt.Errorf("replace post tags: %w", err)
	}
	tags := strings.Split(raw, ",")
	if len(tags) > 50 {
		return fmt.Errorf("%w: at most 50 tags may be assigned", ErrInvalidInput)
	}
	seen := make(map[string]bool)
	for _, name := range tags {
		name = strings.TrimSpace(name)
		if name == "" || seen[strings.ToLower(name)] {
			continue
		}
		if utf8.RuneCountInString(name) > 50 {
			return fmt.Errorf("%w: tag names must be at most 50 characters", ErrInvalidInput)
		}
		slug := Slugify(name)
		if slug == "" {
			return fmt.Errorf("%w: invalid tag name", ErrInvalidInput)
		}
		now := time.Now().UTC().Format(time.RFC3339Nano)
		var tagID int64
		err := tx.QueryRowContext(ctx, "SELECT id FROM tags WHERE lower(name) = lower(?)", name).Scan(&tagID)
		if errors.Is(err, sql.ErrNoRows) {
			slug, err = availableTagSlug(ctx, tx, slug)
			if err != nil {
				return err
			}
			result, err := tx.ExecContext(ctx, `
				INSERT INTO tags (name, slug, created_at) VALUES (?, ?, ?)
			`, name, slug, now)
			if err != nil {
				return fmt.Errorf("create tag: %w", err)
			}
			tagID, err = result.LastInsertId()
			if err != nil {
				return fmt.Errorf("get tag ID: %w", err)
			}
		} else if err != nil {
			return fmt.Errorf("load existing tag: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO post_tags (post_id, tag_id) VALUES (?, ?) ON CONFLICT DO NOTHING
		`, postID, tagID); err != nil {
			return fmt.Errorf("assign tag: %w", err)
		}
		seen[strings.ToLower(name)] = true
	}
	return nil
}

func availableTagSlug(ctx context.Context, tx *sql.Tx, base string) (string, error) {
	for suffix := 1; suffix <= 100; suffix++ {
		slug := base
		if suffix > 1 {
			slug = fmt.Sprintf("%s-%d", base, suffix)
		}
		var exists bool
		if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM tags WHERE slug = ?)", slug).Scan(&exists); err != nil {
			return "", fmt.Errorf("check tag slug: %w", err)
		}
		if !exists {
			return slug, nil
		}
	}
	return "", fmt.Errorf("%w: could not generate a unique tag slug", ErrSlugTaken)
}

func itemCategories(ctx context.Context, db *sql.DB, postID int64) ([]Category, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT categories.id, categories.name, categories.slug, COALESCE(categories.parent_id, 0)
		FROM categories JOIN post_categories ON post_categories.category_id = categories.id
		WHERE post_categories.post_id = ? ORDER BY categories.name
	`, postID)
	if err != nil {
		return nil, fmt.Errorf("load content categories: %w", err)
	}
	defer rows.Close()
	var categories []Category
	for rows.Next() {
		var category Category
		if err := rows.Scan(&category.ID, &category.Name, &category.Slug, &category.ParentID); err != nil {
			return nil, fmt.Errorf("scan content category: %w", err)
		}
		categories = append(categories, category)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read content categories: %w", err)
	}
	return categories, nil
}

func itemTags(ctx context.Context, db *sql.DB, postID int64) ([]string, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT tags.name FROM tags JOIN post_tags ON post_tags.tag_id = tags.id
		WHERE post_tags.post_id = ? ORDER BY tags.name
	`, postID)
	if err != nil {
		return nil, fmt.Errorf("load content tags: %w", err)
	}
	defer rows.Close()
	var tags []string
	for rows.Next() {
		var tag string
		if err := rows.Scan(&tag); err != nil {
			return nil, fmt.Errorf("scan content tag: %w", err)
		}
		tags = append(tags, tag)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read content tags: %w", err)
	}
	return tags, nil
}

func categoryDepth(category Category, categories map[int64]Category) int {
	depth := 0
	parentID := category.ParentID
	for parentID > 0 && depth < 16 {
		parent, exists := categories[parentID]
		if !exists {
			break
		}
		depth++
		parentID = parent.ParentID
	}
	return depth
}

func categoryPath(category Category, categories map[int64]Category) string {
	var names []string
	current := category
	for range 16 {
		names = append([]string{strings.ToLower(current.Name)}, names...)
		if current.ParentID == 0 {
			break
		}
		parent, exists := categories[current.ParentID]
		if !exists {
			break
		}
		current = parent
	}
	return strings.Join(names, "\x00")
}

func nullableID(id int64) any {
	if id == 0 {
		return nil
	}
	return id
}

func validType(contentType string) bool {
	return contentType == TypePost || contentType == TypePage
}

func validFeaturedImage(value string) bool {
	if value == "" {
		return true
	}
	if strings.HasPrefix(value, "/uploads/") && !strings.Contains(value, "\\") {
		parsed, err := url.ParseRequestURI(value)
		if err != nil || parsed.Scheme != "" || parsed.Host != "" || path.Clean(parsed.Path) != parsed.Path {
			return false
		}
		decodedPath, err := url.PathUnescape(parsed.Path)
		return err == nil && path.Clean(decodedPath) == decodedPath && strings.HasPrefix(decodedPath, "/uploads/")
	}
	parsed, err := url.ParseRequestURI(value)
	return err == nil && (parsed.Scheme == "https" || parsed.Scheme == "http") && parsed.Host != ""
}

func translateContentError(err error) error {
	if strings.Contains(strings.ToLower(err.Error()), "unique constraint failed: posts.slug") {
		return ErrSlugTaken
	}
	return fmt.Errorf("write content: %w", err)
}
