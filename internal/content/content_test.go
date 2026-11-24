package content

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Luw17/go-content-go/internal/database"
)

func TestContentLifecycleAndTaxonomies(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := database.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO users (username, password_hash, role, created_at, updated_at)
		VALUES ('author', 'hash', 'admin', 'now', 'now')
	`); err != nil {
		t.Fatal(err)
	}

	post, err := Save(ctx, db, Input{
		Type:        TypePost,
		Title:       "Café e notícias!",
		Content:     "## Olá\n\n**Conteúdo**",
		Status:      StatusPublished,
		CategoryIDs: nil,
		NewCategory: "Tecnologia",
		Tags:        "Go, SQLite, Go",
	}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if post.Slug != "cafe-e-noticias" || post.Status != StatusPublished {
		t.Fatalf("created post = %+v, want generated slug and published status", post)
	}
	if len(post.Categories) != 1 || post.Categories[0].Name != "Tecnologia" {
		t.Fatalf("created categories = %+v, want new category assigned", post.Categories)
	}
	if len(post.Tags) != 2 {
		t.Fatalf("created tags = %v, want duplicate tags removed", post.Tags)
	}
	sanitized, err := Save(ctx, db, Input{
		Type: TypePost, Title: "Sanitized input",
		Content: `<script>alert(1)</script><img src="x" onerror="alert(1)"><strong>safe</strong>`,
		Status:  StatusDraft,
	}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(sanitized.Content, "<script") || strings.Contains(sanitized.Content, "onerror") ||
		!strings.Contains(sanitized.Content, "<strong>safe</strong>") {
		t.Fatalf("saved editor content was not safely sanitized: %s", sanitized.Content)
	}
	rendered, err := RenderMarkdown(sanitized.Content)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(rendered, "<script") || strings.Contains(rendered, "onerror") {
		t.Fatalf("sanitized editor content rendered unsafely: %s", rendered)
	}

	categories, err := Categories(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	parentID := categories[0].ID
	child, err := Save(ctx, db, Input{
		Type:                TypePost,
		Title:               "Outro post",
		Status:              StatusDraft,
		NewCategory:         "Go",
		NewCategoryParentID: parentID,
		CategoryIDs:         []int64{parentID},
	}, 1)
	if err != nil {
		t.Fatal(err)
	}
	categories, err = Categories(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	var childCategory Category
	for _, category := range categories {
		if category.Name == "Go" {
			childCategory = category
		}
	}
	if childCategory.ParentID != parentID || childCategory.Depth != 1 {
		t.Fatalf("hierarchical category = %+v; categories: %+v", childCategory, categories)
	}

	duplicateTitle, err := Save(ctx, db, Input{
		Type: TypePost, Title: "Café e notícias!", Status: StatusDraft,
	}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if duplicateTitle.Slug != "cafe-e-noticias-2" {
		t.Errorf("duplicate title slug = %q, want cafe-e-noticias-2", duplicateTitle.Slug)
	}

	if _, err := Save(ctx, db, Input{
		Type: TypePage, Title: "Sobre", Slug: post.Slug, Status: StatusPublished,
	}, 1); !errors.Is(err, ErrSlugTaken) {
		t.Errorf("duplicate custom slug error = %v, want ErrSlugTaken", err)
	}

	published, err := Published(ctx, db, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(published) != 1 || published[0].ID != post.ID {
		t.Fatalf("published posts = %+v, want only first published post", published)
	}
	if _, err := PublishedBySlug(ctx, db, post.Slug); err != nil {
		t.Fatalf("published content lookup: %v", err)
	}
	if _, err := PublishedBySlug(ctx, db, child.Slug); !errors.Is(err, ErrNotFound) {
		t.Errorf("draft was publicly visible: %v", err)
	}

	updated, err := Save(ctx, db, Input{
		ID: post.ID, Type: TypePost, Title: "Post atualizado", Slug: "meu-post",
		Content: "Atualizado", Status: StatusDraft,
	}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Slug != "meu-post" || updated.Status != StatusDraft || len(updated.Categories) != 0 {
		t.Errorf("updated post = %+v, want edited data and replaced categories", updated)
	}
	if err := Trash(ctx, db, post.ID, TypePost); err != nil {
		t.Fatal(err)
	}
	if _, err := Get(ctx, db, post.ID, TypePost); !errors.Is(err, ErrNotFound) {
		t.Errorf("trashed content remained editable: %v", err)
	}
	trashed, err := List(ctx, db, TypePost, true)
	if err != nil || len(trashed) != 1 || trashed[0].ID != post.ID {
		t.Fatalf("trashed list = %+v, %v", trashed, err)
	}
	if err := Restore(ctx, db, post.ID, TypePost); err != nil {
		t.Fatal(err)
	}
	if err := Trash(ctx, db, post.ID, TypePost); err != nil {
		t.Fatal(err)
	}
	if err := Delete(ctx, db, post.ID, TypePost); err != nil {
		t.Fatal(err)
	}
	if err := Delete(ctx, db, post.ID, TypePost); !errors.Is(err, ErrNotFound) {
		t.Errorf("second permanent delete = %v, want ErrNotFound", err)
	}
}

func TestSlugifyAndMarkdownSanitization(t *testing.T) {
	if got := Slugify("  Olá, MUNDO!  "); got != "ola-mundo" {
		t.Errorf("Slugify() = %q, want ola-mundo", got)
	}
	rendered, err := RenderMarkdown("# título\n\n<script>alert(1)</script><img src=x onerror=alert(1)>")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rendered, "<h1>título</h1>") {
		t.Errorf("rendered markdown did not preserve heading: %s", rendered)
	}
	if strings.Contains(rendered, "<script") || strings.Contains(rendered, "onerror") {
		t.Errorf("rendered markdown contains unsafe HTML: %s", rendered)
	}
	image, err := RenderMarkdown("![Site logo](</uploads/site-logo.png>)")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(image, `<img src="/uploads/site-logo.png" alt="Site logo"`) {
		t.Errorf("rendered markdown did not preserve a safe inline image: %s", image)
	}
}

func TestSaveRejectsInvalidInputs(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := database.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO media (filename, filepath, file_type, file_size, uploaded_at)
		VALUES ('document.pdf', '/uploads/document.pdf', 'application/pdf', 100, 'now')
	`); err != nil {
		t.Fatal(err)
	}
	invalid := []Input{
		{Type: TypePost, Title: "", Status: StatusDraft},
		{Type: TypePage, Title: "Valid", Status: "scheduled"},
		{Type: TypePost, Title: "Valid", Status: StatusDraft, FeaturedImage: "javascript:alert(1)"},
		{Type: TypePost, Title: "Missing media", Status: StatusDraft, FeaturedImage: "/uploads/missing.png"},
		{Type: TypePost, Title: "Document as image", Status: StatusDraft, FeaturedImage: "/uploads/document.pdf"},
		{Type: "unknown", Title: "Valid", Status: StatusDraft},
	}
	for _, input := range invalid {
		if _, err := Save(ctx, db, input, 1); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("Save(%+v) error = %v, want ErrInvalidInput", input, err)
		}
	}
}

func TestContentSEOFields(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := database.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO users (username, password_hash, role, created_at, updated_at)
		VALUES ('author', 'hash', 'admin', 'now', 'now')
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO media (filename, filepath, file_type, file_size, uploaded_at)
		VALUES ('cover.png', '/uploads/cover.png', 'image/png', 100, 'now')
	`); err != nil {
		t.Fatal(err)
	}

	input := Input{
		Type:           TypePost,
		Title:          "Meu Artigo Especial",
		Content:        "Texto do artigo",
		Status:         StatusPublished,
		SEOTitle:       "Título Otimizado para o Google",
		SEODescription: "Meta descrição super detalhada e atrativa.",
		CanonicalURL:   "https://externo.com/artigo-original",
		OGTitle:        "Título para Redes Sociais",
		OGDescription:  "Descrição para o Facebook e Twitter.",
		OGImage:        "/uploads/cover.png",
		TwitterCard:    "summary_large_image",
		SchemaType:     "Article",
		Noindex:        true,
	}
	saved, err := Save(ctx, db, input, 1)
	if err != nil {
		t.Fatalf("Save() failed: %v", err)
	}
	if saved.SEOTitle != input.SEOTitle ||
		saved.SEODescription != input.SEODescription ||
		saved.CanonicalURL != input.CanonicalURL ||
		saved.OGTitle != input.OGTitle ||
		saved.OGDescription != input.OGDescription ||
		saved.OGImage != input.OGImage ||
		saved.TwitterCard != input.TwitterCard ||
		saved.SchemaType != input.SchemaType ||
		!saved.Noindex {
		t.Fatalf("saved item SEO fields = %+v, want %+v", saved, input)
	}

	loaded, err := Get(ctx, db, saved.ID, TypePost)
	if err != nil {
		t.Fatalf("Get() failed: %v", err)
	}
	if loaded.SEOTitle != input.SEOTitle || !loaded.Noindex || loaded.OGImage != input.OGImage {
		t.Fatalf("loaded item SEO fields = %+v, want %+v", loaded, input)
	}

	pub, err := PublishedBySlug(ctx, db, saved.Slug)
	if err != nil {
		t.Fatalf("PublishedBySlug() failed: %v", err)
	}
	if pub.SEOTitle != input.SEOTitle || !pub.Noindex || pub.SchemaType != "Article" {
		t.Fatalf("published item SEO fields = %+v, want %+v", pub, input)
	}
}



func TestSaveCategoryMultipleParentsAndDeletion(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := database.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}

	parentA, err := SaveCategory(ctx, db, CategoryInput{Name: "Tecnologia"})
	if err != nil {
		t.Fatalf("SaveCategory(parentA) error = %v", err)
	}
	if parentA.Slug != "tecnologia" {
		t.Fatalf("parentA.Slug = %q, want tecnologia", parentA.Slug)
	}

	parentB, err := SaveCategory(ctx, db, CategoryInput{Name: "Inovação"})
	if err != nil {
		t.Fatalf("SaveCategory(parentB) error = %v", err)
	}
	if parentB.Slug != "inovacao" {
		t.Fatalf("parentB.Slug = %q, want inovacao", parentB.Slug)
	}

	child, err := SaveCategory(ctx, db, CategoryInput{
		Name:      "Inteligência Artificial",
		ParentIDs: []int64{parentA.ID, parentB.ID},
	})
	if err != nil {
		t.Fatalf("SaveCategory(child) error = %v", err)
	}
	if child.Slug != "inteligencia-artificial" || len(child.ParentIDs) != 2 || child.ParentID != parentA.ID {
		t.Fatalf("unexpected child category: %+v", child)
	}

	all, err := CategoriesWithParents(ctx, db)
	if err != nil {
		t.Fatalf("CategoriesWithParents() error = %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("CategoriesWithParents() len = %d, want 3", len(all))
	}
	var foundChild bool
	for _, c := range all {
		if c.ID == child.ID {
			foundChild = true
			if c.Depth != 1 || len(c.ParentIDs) != 2 {
				t.Errorf("child depth=%d parents=%v, want depth=1 and 2 parents", c.Depth, c.ParentIDs)
			}
		}
	}
	if !foundChild {
		t.Fatal("child category not returned by CategoriesWithParents")
	}

	if err := DeleteCategory(ctx, db, child.ID); err != nil {
		t.Fatalf("DeleteCategory() error = %v", err)
	}
}
