package navigation

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"html/template"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	LocationPrimary = "primary"
	LocationFooter  = "footer"
	TypePage        = "page"
	TypeCustom      = "custom"
)

var (
	ErrInvalidInput = errors.New("invalid menu input")
	ErrNotFound     = errors.New("menu or menu item not found")
)

type Menu struct {
	ID       int64
	Name     string
	Location string
}

type Item struct {
	ID       int64
	MenuID   int64
	Label    string
	URL      string
	Type     string
	ObjectID int64
	Order    int
}

type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) (*Store, error) {
	if db == nil {
		return nil, errors.New("menu database must not be nil")
	}
	return &Store{db: db}, nil
}

func (s *Store) List(ctx context.Context) ([]Menu, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, location FROM menus ORDER BY location, id`)
	if err != nil {
		return nil, fmt.Errorf("list menus: %w", err)
	}
	defer rows.Close()
	var menus []Menu
	for rows.Next() {
		var menu Menu
		if err := rows.Scan(&menu.ID, &menu.Name, &menu.Location); err != nil {
			return nil, fmt.Errorf("scan menu: %w", err)
		}
		menus = append(menus, menu)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read menus: %w", err)
	}
	return menus, nil
}

func (s *Store) Items(ctx context.Context, menuID int64) ([]Item, error) {
	if menuID <= 0 {
		return nil, ErrNotFound
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, menu_id, label, url, type, COALESCE(object_id, 0), item_order
		FROM menu_items WHERE menu_id = ? ORDER BY item_order, id
	`, menuID)
	if err != nil {
		return nil, fmt.Errorf("list menu items: %w", err)
	}
	defer rows.Close()
	var items []Item
	for rows.Next() {
		var item Item
		if err := rows.Scan(&item.ID, &item.MenuID, &item.Label, &item.URL, &item.Type, &item.ObjectID, &item.Order); err != nil {
			return nil, fmt.Errorf("scan menu item: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read menu items: %w", err)
	}
	return items, nil
}

func (s *Store) Create(ctx context.Context, name, location string) (Menu, error) {
	name = strings.TrimSpace(name)
	if utf8.RuneCountInString(name) < 1 || utf8.RuneCountInString(name) > 100 ||
		(location != LocationPrimary && location != LocationFooter) {
		return Menu{}, ErrInvalidInput
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	result, err := s.db.ExecContext(ctx, `
		INSERT INTO menus (name, location, created_at, updated_at) VALUES (?, ?, ?, ?)
	`, name, location, now, now)
	if err != nil {
		return Menu{}, fmt.Errorf("create menu: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return Menu{}, fmt.Errorf("get created menu ID: %w", err)
	}
	return Menu{ID: id, Name: name, Location: location}, nil
}

func (s *Store) AddPage(ctx context.Context, menuID, pageID int64, label string) error {
	label = strings.TrimSpace(label)
	if menuID <= 0 || pageID <= 0 || utf8.RuneCountInString(label) < 1 || utf8.RuneCountInString(label) > 100 {
		return ErrInvalidInput
	}
	var slug string
	if err := s.db.QueryRowContext(ctx, `
		SELECT slug FROM posts
		WHERE id = ? AND content_type = 'page' AND status = 'published' AND deleted_at IS NULL
	`, pageID).Scan(&slug); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("validate menu page: %w", err)
	}
	return s.add(ctx, Item{
		MenuID: menuID, Label: label, URL: "/" + slug, Type: TypePage, ObjectID: pageID,
	})
}

func (s *Store) AddLink(ctx context.Context, menuID int64, label, rawURL string) error {
	label = strings.TrimSpace(label)
	rawURL = strings.TrimSpace(rawURL)
	if menuID <= 0 || utf8.RuneCountInString(label) < 1 || utf8.RuneCountInString(label) > 100 || !validURL(rawURL) {
		return ErrInvalidInput
	}
	return s.add(ctx, Item{MenuID: menuID, Label: label, URL: rawURL, Type: TypeCustom})
}

func (s *Store) add(ctx context.Context, item Item) error {
	var exists bool
	if err := s.db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM menus WHERE id = ?)", item.MenuID).Scan(&exists); err != nil {
		return fmt.Errorf("validate menu: %w", err)
	}
	if !exists {
		return ErrNotFound
	}
	var nextOrder int
	if err := s.db.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(item_order), -1) + 1 FROM menu_items WHERE menu_id = ?
	`, item.MenuID).Scan(&nextOrder); err != nil {
		return fmt.Errorf("find next menu item position: %w", err)
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO menu_items (menu_id, label, url, type, object_id, item_order, created_at)
		VALUES (?, ?, ?, ?, NULLIF(?, 0), ?, ?)
	`, item.MenuID, item.Label, item.URL, item.Type, item.ObjectID, nextOrder, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("add menu item: %w", err)
	}
	return nil
}

func (s *Store) Move(ctx context.Context, itemID int64, direction string) error {
	if itemID <= 0 || (direction != "up" && direction != "down") {
		return ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin menu item reorder: %w", err)
	}
	defer tx.Rollback()
	var menuID int64
	var currentOrder int
	if err := tx.QueryRowContext(ctx, "SELECT menu_id, item_order FROM menu_items WHERE id = ?", itemID).Scan(&menuID, &currentOrder); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("load menu item for reorder: %w", err)
	}
	operator, comparison := "<", "DESC"
	if direction == "down" {
		operator, comparison = ">", "ASC"
	}
	query := fmt.Sprintf(`
		SELECT id, item_order FROM menu_items
		WHERE menu_id = ? AND item_order %s ?
		ORDER BY item_order %s, id %s LIMIT 1
	`, operator, comparison, comparison)
	var otherID int64
	var otherOrder int
	err = tx.QueryRowContext(ctx, query, menuID, currentOrder).Scan(&otherID, &otherOrder)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("find neighboring menu item: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "UPDATE menu_items SET item_order = ? WHERE id = ?", otherOrder, itemID); err != nil {
		return fmt.Errorf("move menu item: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "UPDATE menu_items SET item_order = ? WHERE id = ?", currentOrder, otherID); err != nil {
		return fmt.Errorf("reorder neighboring menu item: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit menu item reorder: %w", err)
	}
	return nil
}

func (s *Store) DeleteItem(ctx context.Context, itemID int64) error {
	if itemID <= 0 {
		return ErrNotFound
	}
	result, err := s.db.ExecContext(ctx, "DELETE FROM menu_items WHERE id = ?", itemID)
	if err != nil {
		return fmt.Errorf("delete menu item: %w", err)
	}
	deleted, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("check deleted menu item: %w", err)
	}
	if deleted == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) DeleteMenu(ctx context.Context, menuID int64) error {
	if menuID <= 0 {
		return ErrNotFound
	}
	result, err := s.db.ExecContext(ctx, "DELETE FROM menus WHERE id = ?", menuID)
	if err != nil {
		return fmt.Errorf("delete menu: %w", err)
	}
	deleted, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("check deleted menu: %w", err)
	}
	if deleted == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) RenderHTML(ctx context.Context, location string) (template.HTML, error) {
	if location != LocationPrimary && location != LocationFooter {
		return "", ErrInvalidInput
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT item.label, CASE WHEN item.type = 'page' THEN '/' || page.slug ELSE item.url END
		FROM menus menu
		JOIN menu_items item ON item.menu_id = menu.id
		LEFT JOIN posts page ON item.type = 'page' AND page.id = item.object_id
		WHERE menu.location = ?
		  AND (item.type = 'custom' OR (
			page.id IS NOT NULL AND page.content_type = 'page' AND page.status = 'published'
			AND page.deleted_at IS NULL
		  ))
		ORDER BY item.item_order, item.id
	`, location)
	if err != nil {
		return "", fmt.Errorf("load public menu: %w", err)
	}
	defer rows.Close()
	var links strings.Builder
	for rows.Next() {
		var label, target string
		if err := rows.Scan(&label, &target); err != nil {
			return "", fmt.Errorf("scan public menu item: %w", err)
		}
		if !validURL(target) {
			continue
		}
		fmt.Fprintf(&links, `<a href="%s">%s</a>`, template.HTMLEscapeString(target), template.HTMLEscapeString(label))
	}
	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("read public menu: %w", err)
	}
	if links.Len() == 0 {
		return "", nil
	}
	return template.HTML(`<nav aria-label="Navegação ` + template.HTMLEscapeString(location) + `">` + links.String() + `</nav>`), nil
}

func validURL(raw string) bool {
	if raw == "" || strings.ContainsAny(raw, "\\\r\n\t") {
		return false
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.User != nil {
		return false
	}
	if parsed.IsAbs() {
		return (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Host != ""
	}
	return strings.HasPrefix(raw, "/") && !strings.HasPrefix(raw, "//")
}
