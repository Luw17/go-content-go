package themes

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const DefaultName = "default"

var (
	ErrNotFound = errors.New("theme not found")
	namePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

	//go:embed default
	defaultFiles embed.FS
)

type Theme struct {
	ID      string
	Name    string
	Author  string
	Version string
	Valid   bool
}

type Manager struct {
	root string
}

func New(root string) (*Manager, error) {
	if strings.TrimSpace(root) == "" {
		return nil, fmt.Errorf("themes directory must not be empty")
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve themes directory: %w", err)
	}
	return &Manager{root: filepath.Clean(absolute)}, nil
}

func ValidName(name string) bool {
	return namePattern.MatchString(name)
}

func (m *Manager) List() ([]Theme, error) {
	entries, err := os.ReadDir(m.root)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read themes directory: %w", err)
	}
	themes := make([]Theme, 0, len(entries)+1)
	hasDefault := false
	for _, entry := range entries {
		if !entry.IsDir() || !ValidName(entry.Name()) {
			continue
		}
		theme := Theme{ID: entry.Name(), Name: entry.Name()}
		if err := m.readMetadata(&theme); err != nil {
			return nil, err
		}
		theme.Valid = m.Validate(theme.ID) == nil
		themes = append(themes, theme)
		if theme.ID == DefaultName {
			hasDefault = true
		}
	}
	if !hasDefault {
		themes = append(themes, Theme{ID: DefaultName, Name: DefaultName, Valid: true})
	}
	return themes, nil
}

func (m *Manager) readMetadata(theme *Theme) error {
	path := filepath.Join(m.root, theme.ID, "theme.json")
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect metadata for theme %q: %w", theme.ID, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("metadata for theme %q must be a regular file", theme.ID)
	}
	if info.Size() > 16<<10 {
		return fmt.Errorf("metadata for theme %q exceeds 16 KiB", theme.ID)
	}
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open metadata for theme %q: %w", theme.ID, err)
	}
	defer file.Close()

	var metadata struct {
		Name    string `json:"name"`
		Author  string `json:"author"`
		Version string `json:"version"`
	}
	decoder := json.NewDecoder(file)
	if err := decoder.Decode(&metadata); err != nil {
		return fmt.Errorf("parse metadata for theme %q: %w", theme.ID, err)
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("parse metadata for theme %q: multiple JSON values", theme.ID)
		}
		return fmt.Errorf("parse metadata for theme %q: %w", theme.ID, err)
	}
	if metadata.Name != "" {
		theme.Name = metadata.Name
	}
	theme.Author = metadata.Author
	theme.Version = metadata.Version
	return nil
}

func (m *Manager) Active(ctx context.Context, db *sql.DB) (string, error) {
	var active string
	err := db.QueryRowContext(ctx, `SELECT value FROM options WHERE key = 'active_theme'`).Scan(&active)
	if errors.Is(err, sql.ErrNoRows) {
		return DefaultName, nil
	}
	if err != nil {
		return "", fmt.Errorf("read active theme option: %w", err)
	}
	if !ValidName(active) {
		log.Printf("configured active theme %q is invalid; using %q", active, DefaultName)
		return DefaultName, nil
	}
	return active, nil
}

func (m *Manager) Resolve(name string) (string, *template.Template, error) {
	if ValidName(name) {
		templates, err := m.load(name)
		if err == nil {
			return name, templates, nil
		}
		if name != DefaultName {
			log.Printf("load active theme %q: %v; using embedded default theme", name, err)
		} else {
			log.Printf("load default theme: %v; using embedded default theme", err)
		}
	}
	files, err := defaultFS()
	if err != nil {
		return "", nil, fmt.Errorf("load embedded default theme: %w", err)
	}
	templates, err := parseTheme(files)
	if err != nil {
		return "", nil, fmt.Errorf("parse embedded default theme: %w", err)
	}
	return DefaultName, templates, nil
}

func (m *Manager) Validate(name string) error {
	if !ValidName(name) {
		return ErrNotFound
	}
	if _, err := m.load(name); err == nil {
		return nil
	} else if name != DefaultName {
		return err
	}
	files, err := defaultFS()
	if err != nil {
		return err
	}
	_, err = parseTheme(files)
	return err
}

func (m *Manager) load(name string) (*template.Template, error) {
	if !ValidName(name) {
		return nil, ErrNotFound
	}
	directory := filepath.Join(m.root, name)
	resolved, err := filepath.EvalSymlinks(directory)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("resolve theme directory: %w", err)
	}
	resolved, err = filepath.Abs(resolved)
	if err != nil {
		return nil, fmt.Errorf("resolve theme directory: %w", err)
	}
	relative, err := filepath.Rel(m.root, resolved)
	if err != nil || relative != name {
		return nil, ErrNotFound
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return nil, fmt.Errorf("inspect theme directory: %w", err)
	}
	if !info.IsDir() {
		return nil, ErrNotFound
	}
	for _, filename := range []string{"index.html", "single.html", "page.html", "404.html", "style.css"} {
		fileInfo, err := os.Lstat(filepath.Join(resolved, filename))
		if err != nil {
			return nil, fmt.Errorf("inspect theme template %q: %w", filename, err)
		}
		if !fileInfo.Mode().IsRegular() {
			return nil, fmt.Errorf("theme template %q must be a regular file", filename)
		}
	}
	return parseTheme(os.DirFS(resolved))
}

func parseTheme(files fs.FS) (*template.Template, error) {
	required := []string{"index.html", "single.html", "page.html", "404.html", "style.css"}
	for _, name := range required {
		file, err := files.Open(name)
		if err != nil {
			return nil, fmt.Errorf("open %s: %w", name, err)
		}
		if err := file.Close(); err != nil {
			return nil, fmt.Errorf("close %s: %w", name, err)
		}
	}
	templates, err := template.New("index.html").ParseFS(files, required[:4]...)
	if err != nil {
		return nil, fmt.Errorf("parse theme templates: %w", err)
	}
	return templates, nil
}

func (m *Manager) OpenAsset(themeName, assetPath string) (fs.File, error) {
	if !ValidName(themeName) || assetPath == "" || strings.Contains(assetPath, "\\") ||
		filepath.IsAbs(assetPath) || filepath.Clean(assetPath) != assetPath || assetPath == "." ||
		strings.HasPrefix(assetPath, ".."+string(filepath.Separator)) {
		return nil, ErrNotFound
	}
	if themeName == DefaultName {
		if _, err := m.load(DefaultName); err != nil {
			files, fsErr := defaultFS()
			if fsErr != nil {
				return nil, fsErr
			}
			return files.Open(filepath.ToSlash(assetPath))
		}
	}
	directory := filepath.Join(m.root, themeName)
	resolvedDirectory, err := filepath.EvalSymlinks(directory)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("resolve theme asset directory: %w", err)
	}
	relativeDirectory, err := filepath.Rel(m.root, resolvedDirectory)
	if err != nil || relativeDirectory != themeName {
		return nil, ErrNotFound
	}
	resolvedFile, err := filepath.EvalSymlinks(filepath.Join(resolvedDirectory, assetPath))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("resolve theme asset: %w", err)
	}
	relative, err := filepath.Rel(resolvedDirectory, resolvedFile)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return nil, ErrNotFound
	}
	info, err := os.Stat(resolvedFile)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("inspect theme asset: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, ErrNotFound
	}
	file, err := os.Open(resolvedFile)
	if err != nil {
		return nil, fmt.Errorf("open theme asset: %w", err)
	}
	return file, nil
}

func defaultFS() (fs.FS, error) {
	files, err := fs.Sub(defaultFiles, DefaultName)
	if err != nil {
		return nil, fmt.Errorf("open embedded default theme: %w", err)
	}
	return files, nil
}
