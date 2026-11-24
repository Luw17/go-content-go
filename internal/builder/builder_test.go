package builder

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Luw17/go-content-go/internal/auth"
	"github.com/Luw17/go-content-go/internal/content"
	"github.com/Luw17/go-content-go/internal/database"
	"github.com/Luw17/go-content-go/internal/siteconfig"
)

func openTestStore(t *testing.T) (*Store, func()) {
	t.Helper()
	ctx := context.Background()
	db, err := database.Open(ctx, filepath.Join(t.TempDir(), "builder.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(ctx, db); err != nil {
		db.Close()
		t.Fatal(err)
	}
	store, err := NewStore(db)
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	return store, func() { _ = db.Close() }
}

func TestLayoutCRUDHomepageAndDuplication(t *testing.T) {
	store, cleanup := openTestStore(t)
	defer cleanup()
	ctx := context.Background()

	preset, ok := PresetByID("homepage-editorial")
	if !ok {
		t.Fatal("expected homepage-editorial preset")
	}

	created, err := store.SaveLayout(ctx, LayoutInput{
		Name:        "Minha Homepage",
		Description: "Layout modular da página inicial",
		Blocks:      preset.Blocks,
		IsHomepage:  true,
	})
	if err != nil {
		t.Fatalf("SaveLayout() error = %v", err)
	}
	if created.ID <= 0 || created.Slug != "minha-homepage" || !created.IsHomepage {
		t.Fatalf("unexpected created layout: %+v", created)
	}
	if len(created.Blocks) != 3 {
		t.Fatalf("blocks len = %d, want 3", len(created.Blocks))
	}

	active, found, err := store.ActiveHomepageLayout(ctx)
	if err != nil || !found || active.ID != created.ID {
		t.Fatalf("ActiveHomepageLayout() = (%+v, %v, %v), want id=%d", active, found, err, created.ID)
	}

	copied, err := store.DuplicateLayout(ctx, created.ID)
	if err != nil {
		t.Fatalf("DuplicateLayout() error = %v", err)
	}
	if copied.ID == created.ID || copied.IsHomepage {
		t.Fatalf("copied layout should have new ID and not be homepage: %+v", copied)
	}

	if err := store.SetHomepageLayout(ctx, copied.ID); err != nil {
		t.Fatalf("SetHomepageLayout() error = %v", err)
	}
	active, found, err = store.ActiveHomepageLayout(ctx)
	if err != nil || !found || active.ID != copied.ID {
		t.Fatalf("ActiveHomepageLayout() after switch = (%+v, %v, %v), want id=%d", active, found, err, copied.ID)
	}

	// Clearing homepage layout
	if err := store.SetHomepageLayout(ctx, 0); err != nil {
		t.Fatalf("clear homepage layout error = %v", err)
	}
	if _, found, err := store.ActiveHomepageLayout(ctx); err != nil || found {
		t.Fatalf("expected no active homepage layout, got found=%v err=%v", found, err)
	}

	if err := store.DeleteLayout(ctx, copied.ID); err != nil {
		t.Fatalf("DeleteLayout() error = %v", err)
	}
	if _, err := store.GetLayout(ctx, copied.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetLayout() after delete error = %v, want ErrNotFound", err)
	}
}

func TestSanitizeBlocksRejectsUnsafeURLsAndTypes(t *testing.T) {
	if _, err := SanitizeBlocks([]Block{{Type: "unknown"}}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput for unknown block type, got %v", err)
	}
	if _, err := SanitizeBlocks([]Block{{
		Type:            BlockHero,
		Heading:         "Hero",
		PrimaryCTALabel: "Click",
		PrimaryCTAURL:   "javascript:alert(1)",
	}}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput for javascript: CTA URL, got %v", err)
	}
	if _, err := SanitizeBlocks([]Block{{
		Type:     BlockHero,
		Heading:  "Hero",
		ImageURL: "javascript:alert(1)",
	}}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput for javascript: image URL, got %v", err)
	}
}

func TestRenderBlocksAllComponentsAndPosts(t *testing.T) {
	store, cleanup := openTestStore(t)
	defer cleanup()
	ctx := context.Background()

	admin, err := auth.CreateInitialAdmin(ctx, store.db, "admin", "strong-password-123")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := content.Save(ctx, store.db, content.Input{
		Type:        content.TypePost,
		Title:       "Post de Tecnologia",
		Content:     "Conteúdo sobre **Go** e CMS.",
		Status:      content.StatusPublished,
		NewCategory: "Tecnologia",
	}, admin.ID); err != nil {
		t.Fatal(err)
	}

	blocks, err := SanitizeBlocks([]Block{
		{
			ID:                "hero-main",
			Type:              BlockHero,
			Eyebrow:           "Novidade",
			Heading:           "Bem-vindo <script>alert(1)</script>",
			Subheading:        "Subtítulo seguro",
			PrimaryCTALabel:   "Começar",
			PrimaryCTAURL:     "/sobre",
			SecondaryCTALabel: "Posts",
			SecondaryCTAURL:   "#lista",
			Align:             "center",
			Tone:              "accent",
		},
		{
			ID:           "lista",
			Type:         BlockPosts,
			Heading:      "Artigos de Tecnologia",
			Count:        3,
			Columns:      2,
			CategorySlug: "tecnologia",
			ShowExcerpt:  true,
		},
		{
			ID:      "destaques",
			Type:    BlockFeatures,
			Heading: "Diferenciais",
			Columns: 3,
			Items: []FeatureItem{
				{Icon: "✦", Title: "Rápido", Description: "Sem peso extra", LinkLabel: "Ver", LinkURL: "/sobre"},
			},
		},
		{
			ID:      "texto",
			Type:    BlockRichText,
			Heading: "Editorial",
			Body:    "Parágrafo em **negrito** <script>evil()</script>",
		},
		{
			ID:              "chamada",
			Type:            BlockCTA,
			Heading:         "Vamos começar",
			Body:            "Fale conosco hoje.",
			PrimaryCTALabel: "Contato",
			PrimaryCTAURL:   "#contato",
			Tone:            "dark",
		},
		{
			ID:             "form-contato",
			Type:           BlockForm,
			FormID:         "contato",
			FormMode:       "contact",
			Heading:        "Fale conosco",
			SubmitLabel:    "Enviar",
			SuccessMessage: "Mensagem recebida!",
		},
	})
	if err != nil {
		t.Fatalf("SanitizeBlocks() error = %v", err)
	}

	html, err := RenderBlocks(ctx, blocks, RenderContext{
		DB:              store.db,
		PagePath:        "/",
		SubmittedFormID: "contato",
	})
	if err != nil {
		t.Fatalf("RenderBlocks() error = %v", err)
	}
	rendered := string(html)
	if strings.Contains(rendered, "<script>") {
		t.Fatalf("rendered HTML contains unescaped script tag: %s", rendered)
	}
	for _, want := range []string{
		"builder-hero",
		"Post de Tecnologia",
		"Diferenciais",
		"<strong>negrito</strong>",
		"Vamos começar",
		"Mensagem recebida!",
		`action="/forms/submit"`,
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("rendered blocks missing %q", want)
		}
	}
}

func TestGlobalSectionsScopeResolutionAndRendering(t *testing.T) {
	store, cleanup := openTestStore(t)
	defer cleanup()
	ctx := context.Background()

	globalHeader, err := store.SaveSection(ctx, SectionInput{
		Name:        "Header Global",
		SectionType: SectionHeader,
		TargetScope: ScopeAll,
		Active:      true,
		Config: SectionConfig{
			Layout:           "split",
			Tone:             "surface",
			ShowLogo:         true,
			ShowSiteTitle:    true,
			ShowMenu:         true,
			AnnouncementText: "Aviso global",
			CTALabel:         "Contato",
			CTAURL:           "/contato",
		},
	})
	if err != nil {
		t.Fatalf("SaveSection global header: %v", err)
	}

	homeHeader, err := store.SaveSection(ctx, SectionInput{
		Name:        "Header Homepage",
		SectionType: SectionHeader,
		TargetScope: ScopeHome,
		Active:      true,
		Config: SectionConfig{
			Layout:        "centered",
			Tone:          "dark",
			ShowSiteTitle: true,
			ShowMenu:      true,
		},
	})
	if err != nil {
		t.Fatalf("SaveSection home header: %v", err)
	}

	slugHeader, err := store.SaveSection(ctx, SectionInput{
		Name:        "Header Landing",
		SectionType: SectionHeader,
		TargetScope: ScopeSlug,
		TargetSlug:  "landing",
		Active:      true,
		Config: SectionConfig{
			Layout:        "minimal",
			Tone:          "accent",
			ShowSiteTitle: true,
		},
	})
	if err != nil {
		t.Fatalf("SaveSection slug header: %v", err)
	}

	// Scope precedence: slug > home/posts/pages > all
	got, found, err := store.ResolveSection(ctx, SectionHeader, ScopePages, "landing")
	if err != nil || !found || got.ID != slugHeader.ID {
		t.Fatalf("slug resolution = (%d, %v, %v), want %d", got.ID, found, err, slugHeader.ID)
	}
	got, found, err = store.ResolveSection(ctx, SectionHeader, ScopeHome, "")
	if err != nil || !found || got.ID != homeHeader.ID {
		t.Fatalf("home resolution = (%d, %v, %v), want %d", got.ID, found, err, homeHeader.ID)
	}
	got, found, err = store.ResolveSection(ctx, SectionHeader, ScopePosts, "um-artigo")
	if err != nil || !found || got.ID != globalHeader.ID {
		t.Fatalf("fallback global resolution = (%d, %v, %v), want %d", got.ID, found, err, globalHeader.ID)
	}

	renderedHeader, err := RenderHeaderHTML(globalHeader, siteconfig.Defaults(), `<nav><a href="/sobre">Sobre</a></nav>`)
	if err != nil {
		t.Fatalf("RenderHeaderHTML: %v", err)
	}
	if !strings.Contains(string(renderedHeader), "Aviso global") || !strings.Contains(string(renderedHeader), `href="/contato"`) {
		t.Errorf("unexpected rendered header: %s", renderedHeader)
	}

	footerSec, err := store.SaveSection(ctx, SectionInput{
		Name:        "Rodapé Institucional",
		SectionType: SectionFooter,
		TargetScope: ScopeAll,
		Active:      true,
		Config: SectionConfig{
			Layout:        "columns",
			Tone:          "dark",
			ShowSiteTitle: true,
			CustomText:    "Feito em Go.",
			CopyrightText: "© 2026 Go Content Go!",
			Columns: []FooterColumn{
				{Title: "Links", Links: []FooterLink{{Label: "Início", URL: "/"}}},
			},
		},
	})
	if err != nil {
		t.Fatalf("SaveSection footer: %v", err)
	}
	renderedFooter, err := RenderFooterHTML(footerSec, siteconfig.Defaults(), "")
	if err != nil {
		t.Fatalf("RenderFooterHTML: %v", err)
	}
	if !strings.Contains(string(renderedFooter), "Feito em Go.") || !strings.Contains(string(renderedFooter), "© 2026 Go Content Go!") {
		t.Errorf("unexpected rendered footer: %s", renderedFooter)
	}
}

func TestFormSubmissionsCRUD(t *testing.T) {
	store, cleanup := openTestStore(t)
	defer cleanup()
	ctx := context.Background()

	if _, err := store.SaveFormSubmission(ctx, FormSubmission{
		FormID:      "contato",
		SenderEmail: "invalido",
	}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected invalid email error, got %v", err)
	}

	saved, err := store.SaveFormSubmission(ctx, FormSubmission{
		FormID:      "contato",
		PagePath:    "/",
		SenderName:  "Maria",
		SenderEmail: "maria@example.com",
		Message:     "Olá!",
	})
	if err != nil {
		t.Fatalf("SaveFormSubmission() error = %v", err)
	}

	list, err := store.ListFormSubmissions(ctx, 10)
	if err != nil || len(list) != 1 || list[0].ID != saved.ID {
		t.Fatalf("ListFormSubmissions() = (%+v, %v)", list, err)
	}

	if err := store.DeleteFormSubmission(ctx, saved.ID); err != nil {
		t.Fatalf("DeleteFormSubmission() error = %v", err)
	}
}
