package media

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	DefaultMaxUploadSize = int64(10 << 20)
	maxFilenameLength    = 180
)

var (
	ErrInvalidFile = errors.New("invalid or unsupported file")
	ErrTooLarge    = errors.New("file exceeds the upload size limit")
	ErrNotFound    = errors.New("media not found")
)

type Item struct {
	ID         int64
	Filename   string
	Filepath   string
	FileType   string
	FileSize   int64
	AltText    string
	UploadedAt string
}

type Store struct {
	db      *sql.DB
	root    string
	maxSize int64
}

func NewStore(db *sql.DB, root string, maxSize int64) (*Store, error) {
	if db == nil {
		return nil, fmt.Errorf("media database must not be nil")
	}
	if strings.TrimSpace(root) == "" {
		return nil, fmt.Errorf("upload directory must not be empty")
	}
	if maxSize < 1 || maxSize > 100<<20 {
		return nil, fmt.Errorf("upload size limit must be between 1 byte and 100 MiB")
	}
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve upload directory: %w", err)
	}
	absoluteRoot = filepath.Clean(absoluteRoot)
	if err := os.MkdirAll(absoluteRoot, 0750); err != nil {
		return nil, fmt.Errorf("create upload directory: %w", err)
	}
	info, err := os.Stat(absoluteRoot)
	if err != nil {
		return nil, fmt.Errorf("inspect upload directory: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("upload path is not a directory")
	}
	return &Store{db: db, root: absoluteRoot, maxSize: maxSize}, nil
}

func (s *Store) MaxSize() int64 {
	return s.maxSize
}

func (s *Store) Save(ctx context.Context, originalFilename, altText string, source io.Reader) (Item, error) {
	safeName, extension, err := sanitizeFilename(originalFilename)
	if err != nil {
		return Item{}, err
	}
	if len(altText) > 500 {
		return Item{}, fmt.Errorf("%w: alt text must be at most 500 bytes", ErrInvalidFile)
	}
	if source == nil {
		return Item{}, ErrInvalidFile
	}

	now := time.Now().UTC()
	relativeDir := filepath.Join(now.Format("2006"), now.Format("01"))
	directory := filepath.Join(s.root, relativeDir)
	if err := os.MkdirAll(directory, 0750); err != nil {
		return Item{}, fmt.Errorf("create upload directory: %w", err)
	}

	randomName := make([]byte, 16)
	if _, err := rand.Read(randomName); err != nil {
		return Item{}, fmt.Errorf("generate unique media name: %w", err)
	}
	storedName := hex.EncodeToString(randomName) + extension
	destination := filepath.Join(directory, storedName)
	temp, err := os.CreateTemp(directory, ".upload-*")
	if err != nil {
		return Item{}, fmt.Errorf("create temporary upload: %w", err)
	}
	tempPath := temp.Name()
	committedFile := false
	tempOpen := true
	defer func() {
		if tempOpen {
			if err := temp.Close(); err != nil {
				log.Printf("close temporary upload: %v", err)
			}
		}
		if !committedFile {
			if err := os.Remove(tempPath); err != nil && !errors.Is(err, os.ErrNotExist) {
				log.Printf("remove temporary upload: %v", err)
			}
		}
	}()

	if err := temp.Chmod(0640); err != nil {
		return Item{}, fmt.Errorf("set upload permissions: %w", err)
	}
	size, err := io.Copy(temp, io.LimitReader(source, s.maxSize+1))
	if err != nil {
		return Item{}, fmt.Errorf("write uploaded file: %w", err)
	}
	if size > s.maxSize {
		return Item{}, ErrTooLarge
	}
	if size == 0 {
		return Item{}, ErrInvalidFile
	}
	if err := temp.Sync(); err != nil {
		return Item{}, fmt.Errorf("flush uploaded file: %w", err)
	}
	if err := temp.Close(); err != nil {
		return Item{}, fmt.Errorf("close uploaded file: %w", err)
	}
	tempOpen = false

	fileType, err := validateFile(tempPath, extension)
	if err != nil {
		return Item{}, err
	}
	if err := os.Rename(tempPath, destination); err != nil {
		return Item{}, fmt.Errorf("store uploaded file: %w", err)
	}
	committedFile = true

	publicPath := "/uploads/" + filepath.ToSlash(filepath.Join(relativeDir, storedName))
	item := Item{
		Filename:   safeName,
		Filepath:   publicPath,
		FileType:   fileType,
		FileSize:   size,
		AltText:    strings.TrimSpace(altText),
		UploadedAt: now.Format(time.RFC3339Nano),
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		if removeErr := os.Remove(destination); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			return Item{}, fmt.Errorf("begin media index transaction: %v; cleanup failed: %w", err, removeErr)
		}
		return Item{}, fmt.Errorf("begin media index transaction: %w", err)
	}
	result, err := tx.ExecContext(ctx, `
		INSERT INTO media (filename, filepath, file_type, file_size, alt_text, uploaded_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, item.Filename, item.Filepath, item.FileType, item.FileSize, item.AltText, item.UploadedAt)
	if err != nil {
		_ = tx.Rollback()
		if removeErr := os.Remove(destination); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			return Item{}, fmt.Errorf("index uploaded media: %v; cleanup failed: %w", err, removeErr)
		}
		return Item{}, fmt.Errorf("index uploaded media: %w", err)
	}
	item.ID, err = result.LastInsertId()
	if err != nil {
		_ = tx.Rollback()
		if removeErr := os.Remove(destination); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			return Item{}, fmt.Errorf("get media ID: %v; cleanup failed: %w", err, removeErr)
		}
		return Item{}, fmt.Errorf("get media ID: %w", err)
	}
	if err := tx.Commit(); err != nil {
		if removeErr := os.Remove(destination); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			return Item{}, fmt.Errorf("commit media index: %v; cleanup failed: %w", err, removeErr)
		}
		return Item{}, fmt.Errorf("commit media index: %w", err)
	}
	return item, nil
}

func (s *Store) List(ctx context.Context) ([]Item, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, filename, filepath, file_type, file_size, alt_text, uploaded_at
		FROM media ORDER BY uploaded_at DESC, id DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("list media: %w", err)
	}
	defer rows.Close()
	items := make([]Item, 0)
	for rows.Next() {
		var item Item
		if err := rows.Scan(
			&item.ID, &item.Filename, &item.Filepath, &item.FileType,
			&item.FileSize, &item.AltText, &item.UploadedAt,
		); err != nil {
			return nil, fmt.Errorf("scan media: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read media: %w", err)
	}
	return items, nil
}

func (s *Store) FindByPath(ctx context.Context, publicPath string) (Item, error) {
	var item Item
	err := s.db.QueryRowContext(ctx, `
		SELECT id, filename, filepath, file_type, file_size, alt_text, uploaded_at
		FROM media WHERE filepath = ?
	`, publicPath).Scan(
		&item.ID, &item.Filename, &item.Filepath, &item.FileType,
		&item.FileSize, &item.AltText, &item.UploadedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Item{}, ErrNotFound
	}
	if err != nil {
		return Item{}, fmt.Errorf("lookup media path: %w", err)
	}
	return item, nil
}

func (s *Store) Delete(ctx context.Context, id int64) error {
	if id <= 0 {
		return ErrNotFound
	}
	var publicPath string
	if err := s.db.QueryRowContext(ctx, "SELECT filepath FROM media WHERE id = ?", id).Scan(&publicPath); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("load media before deletion: %w", err)
	}
	absolutePath, err := s.resolvePath(publicPath)
	if err != nil {
		return err
	}
	quarantinePath := absolutePath + ".deleting"
	if err := os.Rename(absolutePath, quarantinePath); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("stage media file for deletion: %w", err)
		}
	}

	result, err := s.db.ExecContext(ctx, "DELETE FROM media WHERE id = ?", id)
	if err != nil {
		if renameErr := os.Rename(quarantinePath, absolutePath); renameErr != nil && !errors.Is(renameErr, os.ErrNotExist) {
			return fmt.Errorf("delete media record: %v; restore file failed: %w", err, renameErr)
		}
		return fmt.Errorf("delete media record: %w", err)
	}
	deleted, err := result.RowsAffected()
	if err != nil {
		if renameErr := os.Rename(quarantinePath, absolutePath); renameErr != nil && !errors.Is(renameErr, os.ErrNotExist) {
			return fmt.Errorf("check deleted media record: %v; restore file failed: %w", err, renameErr)
		}
		return fmt.Errorf("check deleted media record: %w", err)
	}
	if deleted == 0 {
		if renameErr := os.Rename(quarantinePath, absolutePath); renameErr != nil && !errors.Is(renameErr, os.ErrNotExist) {
			return fmt.Errorf("media record disappeared; restore file failed: %w", renameErr)
		}
		return ErrNotFound
	}
	if err := os.Remove(quarantinePath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("media record deleted but file cleanup failed: %w", err)
	}
	return nil
}

func (s *Store) ResolvePublicPath(publicPath string) (string, error) {
	return s.resolvePath(publicPath)
}

func (s *Store) resolvePath(publicPath string) (string, error) {
	const prefix = "/uploads/"
	if !strings.HasPrefix(publicPath, prefix) || strings.Contains(publicPath, "\\") {
		return "", ErrNotFound
	}
	relative := filepath.FromSlash(strings.TrimPrefix(publicPath, prefix))
	decoded, err := url.PathUnescape(filepath.ToSlash(relative))
	if err != nil {
		return "", ErrNotFound
	}
	relative = filepath.FromSlash(decoded)
	if relative == "" || filepath.IsAbs(relative) || filepath.Clean(relative) != relative || relative == "." {
		return "", ErrNotFound
	}
	absolute := filepath.Join(s.root, relative)
	rel, err := filepath.Rel(s.root, absolute)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", ErrNotFound
	}
	return absolute, nil
}

func sanitizeFilename(filename string) (string, string, error) {
	if filename == "" || len(filename) > 1024 || strings.ContainsAny(filename, `/\`) ||
		strings.Contains(filename, "..") || filepath.Base(filename) != filename {
		return "", "", ErrInvalidFile
	}
	extension := strings.ToLower(filepath.Ext(filename))
	switch extension {
	case ".jpg", ".jpeg", ".png", ".webp", ".gif", ".svg", ".pdf":
	default:
		return "", "", ErrInvalidFile
	}
	name := strings.TrimSuffix(filename, filepath.Ext(filename))
	var clean strings.Builder
	for _, char := range name {
		switch {
		case char >= 'a' && char <= 'z', char >= 'A' && char <= 'Z',
			char >= '0' && char <= '9', char == '-', char == '_':
			clean.WriteRune(char)
		case char == ' ':
			clean.WriteByte('-')
		}
	}
	safeName := strings.Trim(clean.String(), "-_")
	if safeName == "" {
		safeName = "file"
	}
	if len(safeName)+len(extension) > maxFilenameLength {
		safeName = safeName[:maxFilenameLength-len(extension)]
	}
	return safeName + extension, extension, nil
}

func validateFile(path, extension string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("inspect uploaded file: %w", err)
	}
	defer file.Close()

	buffer := make([]byte, 512)
	n, err := io.ReadFull(file, buffer)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return "", fmt.Errorf("inspect uploaded file: %w", err)
	}
	buffer = buffer[:n]

	if extension == ".svg" {
		if err := validateSVG(path); err != nil {
			return "", err
		}
		return "image/svg+xml", nil
	}
	detected := http.DetectContentType(buffer)
	expected := map[string]string{
		".jpg":  "image/jpeg",
		".jpeg": "image/jpeg",
		".png":  "image/png",
		".webp": "image/webp",
		".gif":  "image/gif",
		".pdf":  "application/pdf",
	}[extension]
	if detected != expected {
		return "", ErrInvalidFile
	}
	return mime.TypeByExtension(extension), nil
}

func validateSVG(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("inspect SVG upload: %w", err)
	}
	defer file.Close()
	decoder := xml.NewDecoder(file)
	decoder.Strict = true
	rootSeen := false
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return ErrInvalidFile
		}
		switch element := token.(type) {
		case xml.Directive:
			return ErrInvalidFile
		case xml.StartElement:
			name := strings.ToLower(element.Name.Local)
			if !rootSeen {
				if name != "svg" {
					return ErrInvalidFile
				}
				rootSeen = true
			}
			switch name {
			case "script", "foreignobject", "iframe", "object", "embed", "audio", "video":
				return ErrInvalidFile
			}
			for _, attribute := range element.Attr {
				attrName := strings.ToLower(attribute.Name.Local)
				value := strings.TrimSpace(strings.ToLower(attribute.Value))
				if strings.HasPrefix(attrName, "on") || attrName == "style" {
					return ErrInvalidFile
				}
				if (attrName == "href" || attrName == "src") &&
					value != "" && !strings.HasPrefix(value, "#") && !strings.HasPrefix(value, "data:image/") {
					return ErrInvalidFile
				}
				if strings.Contains(value, "javascript:") || strings.Contains(value, "expression(") {
					return ErrInvalidFile
				}
			}
		}
	}
	if !rootSeen {
		return ErrInvalidFile
	}
	return nil
}
