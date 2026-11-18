package content

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
)

// CategoryInput is used by SaveCategory / the admin API.
type CategoryInput struct {
	ID        int64   // 0 = create new
	Name      string
	ParentIDs []int64 // many-to-many parents (replaces legacy single ParentID)
}

// SaveCategory creates or updates a category and its parent relationships.
func SaveCategory(ctx context.Context, db *sql.DB, input CategoryInput) (Category, error) {
	input.Name = strings.TrimSpace(input.Name)
	if input.Name == "" {
		return Category{}, fmt.Errorf("%w: category name is required", ErrInvalidInput)
	}
	if len(input.Name) > 100 {
		return Category{}, fmt.Errorf("%w: category name too long", ErrInvalidInput)
	}

	slug := slugify(input.Name)

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return Category{}, fmt.Errorf("begin category save: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var id int64
	if input.ID <= 0 {
		// Create
		res, err := tx.ExecContext(ctx,
			"INSERT INTO categories (name, slug) VALUES (?, ?)", input.Name, slug)
		if err != nil {
			return Category{}, fmt.Errorf("insert category: %w", err)
		}
		id, err = res.LastInsertId()
		if err != nil {
			return Category{}, fmt.Errorf("category last insert id: %w", err)
		}
	} else {
		id = input.ID
		if _, err := tx.ExecContext(ctx,
			"UPDATE categories SET name = ?, slug = ? WHERE id = ?",
			input.Name, slug, id); err != nil {
			return Category{}, fmt.Errorf("update category: %w", err)
		}
	}

	// Replace parents (delete + re-insert)
	if _, err := tx.ExecContext(ctx, "DELETE FROM category_parents WHERE category_id = ?", id); err != nil {
		return Category{}, fmt.Errorf("clear category parents: %w", err)
	}
	for _, pid := range input.ParentIDs {
		if pid <= 0 || pid == id {
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
	byID := make(map[int64]*Category)
	for rows.Next() {
		var c Category
		if err := rows.Scan(&c.ID, &c.Name, &c.Slug, &c.ParentID); err != nil {
			return nil, fmt.Errorf("scan category: %w", err)
		}
		byID[c.ID] = &c
		cats = append(cats, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read categories: %w", err)
	}

	// Load many-to-many parents
	prows, err := db.QueryContext(ctx, "SELECT category_id, parent_id FROM category_parents")
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

	for i := range cats {
		cats[i].ParentIDs = parentMap[cats[i].ID]
		cats[i].Depth = categoryDepth(cats[i], byIDMap(byID))
	}

	sort.SliceStable(cats, func(i, j int) bool {
		ai := categoryPath(cats[i], byIDMap(byID))
		aj := categoryPath(cats[j], byIDMap(byID))
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

	rows, err := db.QueryContext(ctx, "SELECT parent_id FROM category_parents WHERE category_id = ?", id)
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

func byIDMap(m map[int64]*Category) map[int64]Category {
	out := make(map[int64]Category, len(m))
	for k, v := range m {
		out[k] = *v
	}
	return out
}

func slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ', r == '-', r == '_':
			b.WriteByte('-')
		}
	}
	return strings.Trim(b.String(), "-")
}
