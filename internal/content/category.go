package content

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// CategoryInput is used by SaveCategory / the admin API.
type CategoryInput struct {
	ID        int64   // 0 = create new
	Name      string
	ParentIDs []int64 // many-to-many parents
}

// SaveCategory creates or updates a category and its parent relationships.
func SaveCategory(ctx context.Context, db *sql.DB, input CategoryInput) (Category, error) {
	input.Name = strings.TrimSpace(input.Name)
	if input.Name == "" {
		return Category{}, fmt.Errorf("%w: category name is required", ErrInvalidInput)
	}
	if utf8.RuneCountInString(input.Name) > 100 {
		return Category{}, fmt.Errorf("%w: category name too long", ErrInvalidInput)
	}

	slug := Slugify(input.Name)
	if slug == "" || len(slug) > maxSlugSize {
		return Category{}, fmt.Errorf("%w: invalid category name", ErrInvalidInput)
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return Category{}, fmt.Errorf("begin category save: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Deduplicate and validate parent IDs before inserting.
	validParents := make([]int64, 0, len(input.ParentIDs))
	seenParents := make(map[int64]bool, len(input.ParentIDs))
	for _, pid := range input.ParentIDs {
		if pid <= 0 || pid == input.ID || seenParents[pid] {
			continue
		}
		var exists bool
		if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM categories WHERE id = ?)", pid).Scan(&exists); err != nil {
			return Category{}, fmt.Errorf("validate category parent: %w", err)
		}
		if !exists {
			return Category{}, fmt.Errorf("%w: parent category does not exist", ErrInvalidInput)
		}
		seenParents[pid] = true
		validParents = append(validParents, pid)
	}

	var primaryParentID int64
	if len(validParents) > 0 {
		primaryParentID = validParents[0]
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)

	var id int64
	if input.ID <= 0 {
		// Ensure unique slug on creation.
		slug, err = availableCategorySlug(ctx, tx, slug, 0)
		if err != nil {
			return Category{}, err
		}
		res, err := tx.ExecContext(ctx,
			"INSERT INTO categories (name, slug, parent_id, created_at, updated_at) VALUES (?, ?, ?, ?, ?)",
			input.Name, slug, nullableID(primaryParentID), now, now)
		if err != nil {
			return Category{}, fmt.Errorf("insert category: %w", err)
		}
		id, err = res.LastInsertId()
		if err != nil {
			return Category{}, fmt.Errorf("category last insert id: %w", err)
		}
	} else {
		id = input.ID
		slug, err = availableCategorySlug(ctx, tx, slug, id)
		if err != nil {
			return Category{}, err
		}
		res, err := tx.ExecContext(ctx,
			"UPDATE categories SET name = ?, slug = ?, parent_id = ?, updated_at = ? WHERE id = ?",
			input.Name, slug, nullableID(primaryParentID), now, id)
		if err != nil {
			return Category{}, fmt.Errorf("update category: %w", err)
		}
		affected, err := res.RowsAffected()
		if err != nil {
			return Category{}, fmt.Errorf("check updated category: %w", err)
		}
		if affected == 0 {
			return Category{}, ErrNotFound
		}
	}

	// Replace parents (delete + re-insert)
	if _, err := tx.ExecContext(ctx, "DELETE FROM category_parents WHERE category_id = ?", id); err != nil {
		return Category{}, fmt.Errorf("clear category parents: %w", err)
	}
	for _, pid := range validParents {
		if pid == id {
			continue
		}
		if _, err := tx.ExecContext(ctx,
			"INSERT OR IGNORE INTO category_parents (category_id, parent_id) VALUES (?, ?)", id, pid); err != nil {
			return Category{}, fmt.Errorf("insert category parent: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return Category{}, fmt.Errorf("commit category save: %w", err)
	}
	return categoryByID(ctx, db, id)
}

func availableCategorySlug(ctx context.Context, tx *sql.Tx, base string, currentID int64) (string, error) {
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
		if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM categories WHERE slug = ? AND id != ?)", slug, currentID).Scan(&exists); err != nil {
			return "", fmt.Errorf("check category slug: %w", err)
		}
		if !exists {
			return slug, nil
		}
	}
	return "", ErrSlugTaken
}

// DeleteCategory removes a category and all its parent links.
func DeleteCategory(ctx context.Context, db *sql.DB, id int64) error {
	if id <= 0 {
		return fmt.Errorf("%w: invalid category id", ErrInvalidInput)
	}
	res, err := db.ExecContext(ctx, "DELETE FROM categories WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("delete category: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// CategoriesWithParents returns all categories, each with their ParentIDs slice
// and their legacy single ParentID (first parent, for backward compat).
func CategoriesWithParents(ctx context.Context, db *sql.DB) ([]Category, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT id, name, slug, COALESCE(parent_id, 0)
		FROM categories ORDER BY name
	`)
	if err != nil {
		return nil, fmt.Errorf("list categories: %w", err)
	}
	defer rows.Close()

	var cats []Category
	for rows.Next() {
		var c Category
		if err := rows.Scan(&c.ID, &c.Name, &c.Slug, &c.ParentID); err != nil {
			return nil, fmt.Errorf("scan category: %w", err)
		}
		cats = append(cats, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read categories: %w", err)
	}

	// Load many-to-many parents
	prows, err := db.QueryContext(ctx, "SELECT category_id, parent_id FROM category_parents ORDER BY parent_id")
	if err != nil {
		return nil, fmt.Errorf("list category parents: %w", err)
	}
	defer prows.Close()
	parentMap := make(map[int64][]int64)
	for prows.Next() {
		var cid, pid int64
		if err := prows.Scan(&cid, &pid); err != nil {
			return nil, fmt.Errorf("scan category parent: %w", err)
		}
		parentMap[cid] = append(parentMap[cid], pid)
	}
	if err := prows.Err(); err != nil {
		return nil, fmt.Errorf("read category parents: %w", err)
	}

	byID := make(map[int64]Category, len(cats))
	for i := range cats {
		cats[i].ParentIDs = parentMap[cats[i].ID]
		if cats[i].ParentID == 0 && len(cats[i].ParentIDs) > 0 {
			cats[i].ParentID = cats[i].ParentIDs[0]
		}
		byID[cats[i].ID] = cats[i]
	}

	for i := range cats {
		cats[i].Depth = categoryDepth(cats[i], byID)
	}

	sort.SliceStable(cats, func(i, j int) bool {
		ai := categoryPath(cats[i], byID)
		aj := categoryPath(cats[j], byID)
		return ai < aj
	})
	return cats, nil
}

func categoryByID(ctx context.Context, db *sql.DB, id int64) (Category, error) {
	var c Category
	if err := db.QueryRowContext(ctx,
		"SELECT id, name, slug, COALESCE(parent_id, 0) FROM categories WHERE id = ?", id,
	).Scan(&c.ID, &c.Name, &c.Slug, &c.ParentID); err != nil {
		if err == sql.ErrNoRows {
			return Category{}, ErrNotFound
		}
		return Category{}, fmt.Errorf("load category: %w", err)
	}

	rows, err := db.QueryContext(ctx, "SELECT parent_id FROM category_parents WHERE category_id = ? ORDER BY parent_id", id)
	if err != nil {
		return Category{}, fmt.Errorf("load category parents: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var pid int64
		if err := rows.Scan(&pid); err != nil {
			return Category{}, fmt.Errorf("scan category parent: %w", err)
		}
		c.ParentIDs = append(c.ParentIDs, pid)
	}
	return c, rows.Err()
}
