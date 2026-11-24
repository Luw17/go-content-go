package web

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Luw17/go-content-go/internal/auth"
	"github.com/Luw17/go-content-go/internal/database"
)

// TestLoadPublicCache10kPostsAnd1kConcurrentUsers verifies SSR and API stability
// under 10,000 published posts and 1,000 concurrent readers with the in-memory cache active.
func TestLoadPublicCache10kPostsAnd1kConcurrentUsers(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping 10k-post load test in -short mode")
	}
	ctx := context.Background()
	db, err := database.Open(ctx, filepath.Join(t.TempDir(), "load-10k.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := database.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	admin, err := auth.CreateInitialAdmin(ctx, db, "admin", "strong-password-123")
	if err != nil {
		t.Fatal(err)
	}

	const totalPosts = 10000
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO posts (author_id, title, slug, content, status, created_at, updated_at, published_at, content_type)
		VALUES (?, ?, ?, ?, 'published', ?, ?, ?, 'post')
	`)
	if err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for i := 1; i <= totalPosts; i++ {
		if _, err := stmt.ExecContext(ctx,
			admin.ID,
			fmt.Sprintf("Artigo de Carga #%d", i),
			fmt.Sprintf("artigo-carga-%d", i),
			fmt.Sprintf("Conteúdo de teste de carga número %d.", i),
			now, now, now,
		); err != nil {
			_ = stmt.Close()
			_ = tx.Rollback()
			t.Fatalf("seed post %d: %v", i, err)
		}
	}
	_ = stmt.Close()
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	handler, err := NewHandler(db, Options{
		UploadDir:    t.TempDir(),
		ThemeDir:     t.TempDir(),
		PluginDir:    t.TempDir(),
		BackupDir:    t.TempDir(),
		APIRateLimit: 10000,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Warm hot routes once to populate in-memory cache before concurrent burst.
	hotPaths := []string{"/", "/artigo-carga-10000", "/artigo-carga-5000", "/artigo-carga-1", "/api/v1/posts?page=1&limit=20"}
	for _, p := range hotPaths {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, p, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("warmup %s status = %d", p, rec.Code)
		}
	}

	const concurrentUsers = 1000
	var failures atomic.Int64
	start := time.Now()
	var wg sync.WaitGroup
	wg.Add(concurrentUsers)
	for i := 0; i < concurrentUsers; i++ {
		target := hotPaths[i%len(hotPaths)]
		clientIP := fmt.Sprintf("10.0.%d.%d:40000", i/250, (i%250)+1)
		go func(path, remoteAddr string) {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodGet, path, nil)
			req.RemoteAddr = remoteAddr
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				failures.Add(1)
			}
		}(target, clientIP)
	}
	wg.Wait()
	elapsed := time.Since(start)

	if failures.Load() != 0 {
		t.Fatalf("load test had %d failed responses out of %d concurrent users", failures.Load(), concurrentUsers)
	}
	t.Logf("10,000 posts seeded; %d concurrent cached requests completed in %s (0 failures)", concurrentUsers, elapsed)
}
