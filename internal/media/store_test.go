package media

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Luw17/go-content-go/internal/database"
)

func TestSaveUniqueFilesAndDelete(t *testing.T) {
	ctx := context.Background()
	db := newMediaTestDB(t)
	root := t.TempDir()
	store, err := NewStore(db, root, 1024)
	if err != nil {
		t.Fatal(err)
	}
	png := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}
	first, err := store.Save(ctx, "same image.png", "Alt text", bytes.NewReader(png))
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.Save(ctx, "same image.png", "", bytes.NewReader(png))
	if err != nil {
		t.Fatal(err)
	}
	if first.Filepath == second.Filepath {
		t.Fatalf("duplicate filename was overwritten: %q", first.Filepath)
	}
	if !strings.HasPrefix(first.Filepath, "/uploads/") || !strings.HasSuffix(first.Filepath, ".png") {
		t.Errorf("stored filepath = %q", first.Filepath)
	}
	if first.Filename != "same-image.png" || first.FileType != "image/png" || first.FileSize != int64(len(png)) || first.AltText != "Alt text" {
		t.Errorf("stored media metadata = %+v", first)
	}
	diskPath, err := store.ResolvePublicPath(first.Filepath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(diskPath); err != nil {
		t.Fatalf("uploaded file not written: %v", err)
	}
	listed, err := store.List(ctx)
	if err != nil || len(listed) != 2 {
		t.Fatalf("List() = %d items, %v; want 2", len(listed), err)
	}
	if err := store.Delete(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(diskPath); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("deleted media file still exists: %v", err)
	}
	if _, err := store.FindByPath(ctx, first.Filepath); !errors.Is(err, ErrNotFound) {
		t.Errorf("deleted media still indexed: %v", err)
	}
}

func TestSaveRejectsUnsafeNamesTypesAndOversizeFiles(t *testing.T) {
	ctx := context.Background()
	db := newMediaTestDB(t)
	store, err := NewStore(db, t.TempDir(), 16)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name     string
		filename string
		content  []byte
		wantErr  error
	}{
		{name: "path traversal", filename: "../escape.png", content: []byte("png"), wantErr: ErrInvalidFile},
		{name: "unsupported extension", filename: "image.txt", content: []byte("text"), wantErr: ErrInvalidFile},
		{name: "mismatched mime", filename: "image.png", content: []byte("not an image"), wantErr: ErrInvalidFile},
		{name: "too large", filename: "image.png", content: bytes.Repeat([]byte{0}, 17), wantErr: ErrTooLarge},
		{name: "empty", filename: "image.png", content: nil, wantErr: ErrInvalidFile},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := store.Save(ctx, test.filename, "", bytes.NewReader(test.content))
			if !errors.Is(err, test.wantErr) {
				t.Errorf("Save() error = %v, want %v", err, test.wantErr)
			}
		})
	}
}

func TestSVGValidationAndDatabaseFailureCleanup(t *testing.T) {
	ctx := context.Background()
	db := newMediaTestDB(t)
	root := t.TempDir()
	store, err := NewStore(db, root, 4096)
	if err != nil {
		t.Fatal(err)
	}
	validSVG := `<svg xmlns="http://www.w3.org/2000/svg"><path d="M0 0"/></svg>`
	item, err := store.Save(ctx, "icon.svg", "", strings.NewReader(validSVG))
	if err != nil {
		t.Fatal(err)
	}
	if item.FileType != "image/svg+xml" {
		t.Errorf("SVG MIME = %q", item.FileType)
	}
	if _, err := store.Save(ctx, "unsafe.svg", "", strings.NewReader(`<svg><script>alert(1)</script></svg>`)); !errors.Is(err, ErrInvalidFile) {
		t.Errorf("unsafe SVG error = %v, want ErrInvalidFile", err)
	}

	if _, err := db.ExecContext(ctx, `
		CREATE TRIGGER reject_media BEFORE INSERT ON media BEGIN SELECT RAISE(FAIL, 'test rejection'); END
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Save(ctx, "rollback.png", "", bytes.NewReader([]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'})); err == nil {
		t.Fatal("Save() succeeded despite database rejection trigger")
	}
	files, err := filepath.Glob(filepath.Join(root, "*", "*", "*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 {
		t.Errorf("files remaining after database failure = %v, want only prior successful upload", files)
	}
	var count int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM media").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("media rows after failed insert = %d, want 1", count)
	}
}

func TestResolvePublicPathRejectsTraversal(t *testing.T) {
	store, err := NewStore(newMediaTestDB(t), t.TempDir(), 1024)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		"/etc/passwd",
		"/uploads/../secret",
		"/uploads/%2e%2e/secret",
		"/uploads/..\\secret",
	} {
		if _, err := store.ResolvePublicPath(path); !errors.Is(err, ErrNotFound) {
			t.Errorf("ResolvePublicPath(%q) error = %v, want ErrNotFound", path, err)
		}
	}
}

func newMediaTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := database.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(context.Background(), db); err != nil {
		db.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close media test database: %v", err)
		}
	})
	return db
}
