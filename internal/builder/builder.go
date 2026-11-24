package builder

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Luw17/go-content-go/internal/content"
)

const (
	BlockHero     = "hero"
	BlockPosts    = "posts"
	BlockForm     = "form"
	BlockFeatures = "features"
	BlockRichText = "rich_text"
	BlockCTA      = "cta"

	maxBlocksPerLayout = 30
	maxFeatureItems    = 12
)

var (
	ErrNotFound     = errors.New("layout or section not found")
	ErrInvalidInput = errors.New("invalid builder input")
	ErrSlugTaken    = errors.New("layout slug is already in use")
	idPattern       = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
	emailPattern    = regexp.MustCompile(`^[^\s@]+@[^\s@]+\.[^\s@]+$`)
)

type FeatureItem struct {
	Icon        string `json:"icon,omitempty"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	LinkLabel   string `json:"link_label,omitempty"`
	LinkURL     string `json:"link_url,omitempty"`
}

type Block struct {
	ID                string        `json:"id"`
	Type              string        `json:"type"`
	Eyebrow           string        `json:"eyebrow,omitempty"`
	Heading           string        `json:"heading,omitempty"`
	Subheading        string        `json:"subheading,omitempty"`
	Body              string        `json:"body,omitempty"`
	PrimaryCTALabel   string        `json:"primary_cta_label,omitempty"`
	PrimaryCTAURL     string        `json:"primary_cta_url,omitempty"`
	SecondaryCTALabel string        `json:"secondary_cta_label,omitempty"`
	SecondaryCTAURL   string        `json:"secondary_cta_url,omitempty"`
	ImageURL          string        `json:"image_url,omitempty"`
	ImageAlt          string        `json:"image_alt,omitempty"`
	Align             string        `json:"align,omitempty"`
	Tone              string        `json:"tone,omitempty"`
	Count             int           `json:"count,omitempty"`
	Columns           int           `json:"columns,omitempty"`
	CategorySlug      string        `json:"category_slug,omitempty"`
	ShowExcerpt       bool          `json:"show_excerpt,omitempty"`
	ShowImage         bool          `json:"show_image,omitempty"`
	FormID            string        `json:"form_id,omitempty"`
	FormMode          string        `json:"form_mode,omitempty"`
	SubmitLabel       string        `json:"submit_label,omitempty"`
	SuccessMessage    string        `json:"success_message,omitempty"`
	PrivacyNote       string        `json:"privacy_note,omitempty"`
	Items             []FeatureItem `json:"items,omitempty"`
}

type Layout struct {
	ID          int64
	Name        string
	Slug        string
	Description string
	Blocks      []Block
	BlocksJSON  string
	IsHomepage  bool
	CreatedAt   string
	UpdatedAt   string
}

type LayoutInput struct {
	ID          int64
	Name        string
	Slug        string
	Description string
	Blocks      []Block
	IsHomepage  bool
}

type StarterPreset struct {
	ID          string
	Name        string
	Description string
	Blocks      []Block
}

type FormSubmission struct {
	ID          int64
	FormID      string
	PagePath    string
	SenderName  string
	SenderEmail string
	Message     string
	CreatedAt   string
}

type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) (*Store, error) {
	if db == nil {
		return nil, errors.New("builder database must not be nil")
	}
	return &Store{db: db}, nil
}

// Presets returns built-in modular layout templates that users can clone in one click.
func Presets() []StarterPreset {
	return []StarterPreset{
		{
			ID:          "homepage-editorial",
			Name:        "Homepage Editorial",
			Description: "Hero de boas-vindas, grade de publicações recentes e formulário de newsletter.",
			Blocks: []Block{
				{
					ID:                "hero-1",
					Type:              BlockHero,
					Eyebrow:           "Bem-vindo",
					Heading:           "Histórias, ideias e guias feitos para ler com calma.",
					Subheading:        "Explore nossos artigos mais recentes e descubra conteúdos selecionados pela equipe editorial.",
					PrimaryCTALabel:   "Explorar artigos",
					PrimaryCTAURL:     "#publicacoes",
					SecondaryCTALabel: "Assinar novidades",
					SecondaryCTAURL:   "#newsletter",
					Align:             "left",
					Tone:              "default",
				},
				{
					ID:          "posts-1",
					Type:        BlockPosts,
					Eyebrow:     "Do arquivo",
					Heading:     "Publicações recentes",
					Subheading:  "Os textos mais novos publicados no site.",
					Count:       6,
					Columns:     2,
					ShowExcerpt: true,
					ShowImage:   true,
				},
				{
					ID:             "form-1",
					Type:           BlockForm,
					FormID:         "newsletter",
					FormMode:       "newsletter",
					Eyebrow:        "Newsletter",
					Heading:        "Receba novos conteúdos no seu e-mail",
					Subheading:     "Sem spam. Apenas um resumo ocasional com nossos melhores textos.",
					SubmitLabel:    "Inscrever-se",
					SuccessMessage: "Inscrição recebida! Obrigado por acompanhar o site.",
					PrivacyNote:    "Seus dados são usados apenas para envio das novidades.",
					Tone:           "surface",
				},
			},
		},
		{
			ID:          "landing-produto",
			Name:        "Landing Page de Produto",
			Description: "Hero centralizado, grade de diferenciais, banner CTA e formulário de contato.",
			Blocks: []Block{
				{
					ID:                "hero-1",
					Type:              BlockHero,
					Eyebrow:           "Lançamento",
					Heading:           "Construa experiências rápidas sem depender de código.",
					Subheading:        "Combine seções modulares, publique em segundos e personalize cada detalhe visualmente.",
					PrimaryCTALabel:   "Falar com a equipe",
					PrimaryCTAURL:     "#contato",
					SecondaryCTALabel: "Ver recursos",
					SecondaryCTAURL:   "#recursos",
					Align:             "center",
					Tone:              "accent",
				},
				{
					ID:         "features-1",
					Type:       BlockFeatures,
					Eyebrow:    "Recursos",
					Heading:    "Tudo o que você precisa no mesmo lugar",
					Subheading: "Componentes prontos, responsivos e otimizados para velocidade.",
					Columns:    3,
					Items: []FeatureItem{
						{Icon: "✦", Title: "Blocos modulares", Description: "Monte páginas inteiras combinando Hero, listas de posts, destaques e formulários."},
						{Icon: "⚡", Title: "Performance nativa", Description: "HTML limpo renderizado no servidor com cache integrado e zero scripts pesados."},
						{Icon: "◎", Title: "Cabeçalhos por seção", Description: "Defina cabeçalhos e rodapés específicos para a homepage, blog ou páginas especiais."},
					},
				},
				{
					ID:              "cta-1",
					Type:            BlockCTA,
					Eyebrow:         "Próximo passo",
					Heading:         "Pronto para publicar sua nova página?",
					Body:            "Personalize cada bloco diretamente no Construtor Visual e ative quando estiver satisfeito.",
					PrimaryCTALabel: "Entrar em contato",
					PrimaryCTAURL:   "#contato",
					Tone:            "dark",
				},
				{
					ID:             "form-1",
					Type:           BlockForm,
					FormID:         "contato",
					FormMode:       "contact",
					Eyebrow:        "Contato",
					Heading:        "Envie uma mensagem",
					Subheading:     "Responderemos o mais rápido possível.",
					SubmitLabel:    "Enviar mensagem",
					SuccessMessage: "Mensagem enviada com sucesso! Entraremos em contato em breve.",
					Tone:           "default",
				},
			},
		},
		{
			ID:          "institucional",
			Name:        "Página Institucional",
			Description: "Cabeçalho Hero, seção de texto editorial em Markdown e formulário de contato.",
			Blocks: []Block{
				{
					ID:         "hero-1",
					Type:       BlockHero,
					Eyebrow:    "Sobre nós",
					Heading:    "Quem somos e o que construímos",
					Subheading: "Uma visão clara da nossa missão, valores e projetos em andamento.",
					Align:      "left",
					Tone:       "default",
				},
				{
					ID:      "text-1",
					Type:    BlockRichText,
					Heading: "Nossa história",
					Body:    "Escreva aqui a apresentação completa da sua marca ou projeto usando **Markdown**.\n\n- Clareza editorial\n- Foco em velocidade e acessibilidade\n- Autonomia para sua equipe",
					Align:   "left",
				},
				{
					ID:             "form-1",
					Type:           BlockForm,
					FormID:         "contato-institucional",
					FormMode:       "contact",
					Heading:        "Vamos conversar",
					Subheading:     "Tem alguma dúvida ou proposta? Preencha o formulário abaixo.",
					SubmitLabel:    "Enviar",
					SuccessMessage: "Obrigado pela mensagem!",
					Tone:           "surface",
				},
			},
		},
	}
}

func PresetByID(id string) (StarterPreset, bool) {
	for _, preset := range Presets() {
		if preset.ID == id {
			return preset, true
		}
	}
	return StarterPreset{}, false
}

// Revision returns a lightweight cache-busting revision string for layouts and sections.
func (s *Store) Revision(ctx context.Context) (string, error) {
	var layoutRev, sectionRev string
	var homeID int64
	if err := s.db.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(updated_at), ''), COALESCE(MAX(CASE WHEN is_homepage = 1 THEN id ELSE 0 END), 0)
		FROM page_layouts
	`).Scan(&layoutRev, &homeID); err != nil {
		return "", fmt.Errorf("read layout revision: %w", err)
	}
	if err := s.db.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(updated_at), '') FROM site_sections
	`).Scan(&sectionRev); err != nil {
		return "", fmt.Errorf("read section revision: %w", err)
	}
	var layoutCount, sectionCount int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM page_layouts`).Scan(&layoutCount); err != nil {
		return "", fmt.Errorf("count layouts: %w", err)
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM site_sections`).Scan(&sectionCount); err != nil {
		return "", fmt.Errorf("count sections: %w", err)
	}
	return fmt.Sprintf("l=%d:%d:%s|s=%d:%s", layoutCount, homeID, layoutRev, sectionCount, sectionRev), nil
}

func (s *Store) ListLayouts(ctx context.Context) ([]Layout, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, slug, description, blocks_json, is_homepage, created_at, updated_at
		FROM page_layouts
		ORDER BY is_homepage DESC, updated_at DESC, id DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("list page layouts: %w", err)
	}
	defer rows.Close()

	var layouts []Layout
	for rows.Next() {
		layout, err := scanLayout(rows)
		if err != nil {
			return nil, err
		}
		layouts = append(layouts, layout)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read page layouts: %w", err)
	}
	return layouts, nil
}

func (s *Store) GetLayout(ctx context.Context, id int64) (Layout, error) {
	if id <= 0 {
		return Layout{}, ErrNotFound
	}
	row := s.db.QueryRowContext(ctx, `
		SELECT id, name, slug, description, blocks_json, is_homepage, created_at, updated_at
		FROM page_layouts WHERE id = ?
	`, id)
	return scanLayout(row)
}

func (s *Store) ActiveHomepageLayout(ctx context.Context) (Layout, bool, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, name, slug, description, blocks_json, is_homepage, created_at, updated_at
		FROM page_layouts WHERE is_homepage = 1
		ORDER BY updated_at DESC, id DESC LIMIT 1
	`)
	layout, err := scanLayout(row)
	if errors.Is(err, ErrNotFound) {
		return Layout{}, false, nil
	}
	if err != nil {
		return Layout{}, false, err
	}
	return layout, true, nil
}

func (s *Store) SaveLayout(ctx context.Context, input LayoutInput) (Layout, error) {
	input.Name = strings.TrimSpace(input.Name)
	input.Description = strings.TrimSpace(input.Description)
	if input.ID < 0 || input.Name == "" || utf8.RuneCountInString(input.Name) > 120 ||
		utf8.RuneCountInString(input.Description) > 400 {
		return Layout{}, fmt.Errorf("%w: invalid layout name or description", ErrInvalidInput)
	}
	sanitizedBlocks, err := SanitizeBlocks(input.Blocks)
	if err != nil {
		return Layout{}, err
	}
	encoded, err := json.Marshal(sanitizedBlocks)
	if err != nil {
		return Layout{}, fmt.Errorf("encode layout blocks: %w", err)
	}
	slug := content.Slugify(input.Slug)
	generated := strings.TrimSpace(input.Slug) == ""
	if generated {
		slug = content.Slugify(input.Name)
	}
	if slug == "" || len(slug) > 100 {
		return Layout{}, fmt.Errorf("%w: invalid layout slug", ErrInvalidInput)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Layout{}, fmt.Errorf("begin layout save: %w", err)
	}
	defer tx.Rollback()

	if input.ID > 0 {
		var exists bool
		if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM page_layouts WHERE id = ?)", input.ID).Scan(&exists); err != nil {
			return Layout{}, fmt.Errorf("check existing layout: %w", err)
		}
		if !exists {
			return Layout{}, ErrNotFound
		}
	}

	slug, err = availableLayoutSlug(ctx, tx, slug, input.ID, generated)
	if err != nil {
		return Layout{}, err
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	isHomeInt := 0
	if input.IsHomepage {
		isHomeInt = 1
		if _, err := tx.ExecContext(ctx, "UPDATE page_layouts SET is_homepage = 0 WHERE is_homepage = 1"); err != nil {
			return Layout{}, fmt.Errorf("clear previous homepage layout: %w", err)
		}
	}

	var id int64
	if input.ID == 0 {
		res, err := tx.ExecContext(ctx, `
			INSERT INTO page_layouts (name, slug, description, blocks_json, is_homepage, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)
		`, input.Name, slug, input.Description, string(encoded), isHomeInt, now, now)
		if err != nil {
			return Layout{}, fmt.Errorf("insert layout: %w", err)
		}
		id, err = res.LastInsertId()
		if err != nil {
			return Layout{}, fmt.Errorf("get inserted layout ID: %w", err)
		}
	} else {
		id = input.ID
		if _, err := tx.ExecContext(ctx, `
			UPDATE page_layouts
			SET name = ?, slug = ?, description = ?, blocks_json = ?, is_homepage = ?, updated_at = ?
			WHERE id = ?
		`, input.Name, slug, input.Description, string(encoded), isHomeInt, now, id); err != nil {
			return Layout{}, fmt.Errorf("update layout: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return Layout{}, fmt.Errorf("commit layout save: %w", err)
	}
	return s.GetLayout(ctx, id)
}

func (s *Store) SetHomepageLayout(ctx context.Context, layoutID int64) error {
	if layoutID < 0 {
		return ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin homepage layout update: %w", err)
	}
	defer tx.Rollback()

	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := tx.ExecContext(ctx, "UPDATE page_layouts SET is_homepage = 0, updated_at = ? WHERE is_homepage = 1", now); err != nil {
		return fmt.Errorf("clear homepage layout: %w", err)
	}
	if layoutID > 0 {
		res, err := tx.ExecContext(ctx, "UPDATE page_layouts SET is_homepage = 1, updated_at = ? WHERE id = ?", now, layoutID)
		if err != nil {
			return fmt.Errorf("activate homepage layout: %w", err)
		}
		affected, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("check homepage layout activation: %w", err)
		}
		if affected == 0 {
			return ErrNotFound
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit homepage layout update: %w", err)
	}
	return nil
}

func (s *Store) DuplicateLayout(ctx context.Context, id int64) (Layout, error) {
	original, err := s.GetLayout(ctx, id)
	if err != nil {
		return Layout{}, err
	}
	name := original.Name + " (Cópia)"
	if utf8.RuneCountInString(name) > 120 {
		runes := []rune(original.Name)
		name = string(runes[:110]) + " (Cópia)"
	}
	return s.SaveLayout(ctx, LayoutInput{
		Name:        name,
		Description: original.Description,
		Blocks:      original.Blocks,
		IsHomepage:  false,
	})
}

func (s *Store) DeleteLayout(ctx context.Context, id int64) error {
	if id <= 0 {
		return ErrNotFound
	}
	res, err := s.db.ExecContext(ctx, "DELETE FROM page_layouts WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("delete layout: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("check layout deletion: %w", err)
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) SaveFormSubmission(ctx context.Context, sub FormSubmission) (FormSubmission, error) {
	sub.FormID = strings.TrimSpace(sub.FormID)
	sub.PagePath = strings.TrimSpace(sub.PagePath)
	sub.SenderName = strings.TrimSpace(sub.SenderName)
	sub.SenderEmail = strings.TrimSpace(sub.SenderEmail)
	sub.Message = strings.TrimSpace(sub.Message)

	if sub.FormID == "" || !idPattern.MatchString(sub.FormID) {
		return FormSubmission{}, fmt.Errorf("%w: invalid form identifier", ErrInvalidInput)
	}
	if sub.PagePath == "" || !strings.HasPrefix(sub.PagePath, "/") || len(sub.PagePath) > 500 {
		sub.PagePath = "/"
	}
	if sub.SenderEmail == "" || len(sub.SenderEmail) > 254 || !emailPattern.MatchString(sub.SenderEmail) {
		return FormSubmission{}, fmt.Errorf("%w: valid email is required", ErrInvalidInput)
	}
	if utf8.RuneCountInString(sub.SenderName) > 120 || utf8.RuneCountInString(sub.Message) > 5000 {
		return FormSubmission{}, fmt.Errorf("%w: submission fields exceed length limits", ErrInvalidInput)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO form_submissions (form_id, page_path, sender_name, sender_email, message, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, sub.FormID, sub.PagePath, sub.SenderName, sub.SenderEmail, sub.Message, now)
	if err != nil {
		return FormSubmission{}, fmt.Errorf("save form submission: %w", err)
	}
	sub.ID, _ = res.LastInsertId()
	sub.CreatedAt = now
	return sub, nil
}

func (s *Store) ListFormSubmissions(ctx context.Context, limit int) ([]FormSubmission, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, form_id, page_path, sender_name, sender_email, message, created_at
		FROM form_submissions
		ORDER BY created_at DESC, id DESC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("list form submissions: %w", err)
	}
	defer rows.Close()

	var subs []FormSubmission
	for rows.Next() {
		var sub FormSubmission
		if err := rows.Scan(&sub.ID, &sub.FormID, &sub.PagePath, &sub.SenderName, &sub.SenderEmail, &sub.Message, &sub.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan form submission: %w", err)
		}
		subs = append(subs, sub)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read form submissions: %w", err)
	}
	return subs, nil
}

func (s *Store) DeleteFormSubmission(ctx context.Context, id int64) error {
	if id <= 0 {
		return ErrNotFound
	}
	res, err := s.db.ExecContext(ctx, "DELETE FROM form_submissions WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("delete form submission: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("check deleted form submission: %w", err)
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

func SanitizeBlocks(raw []Block) ([]Block, error) {
	if len(raw) > maxBlocksPerLayout {
		return nil, fmt.Errorf("%w: too many blocks (max %d)", ErrInvalidInput, maxBlocksPerLayout)
	}
	cleaned := make([]Block, 0, len(raw))
	for index, block := range raw {
		b, err := sanitizeBlock(block, index+1)
		if err != nil {
			return nil, err
		}
		cleaned = append(cleaned, b)
	}
	return cleaned, nil
}

func sanitizeBlock(b Block, fallbackIndex int) (Block, error) {
	b.Type = strings.TrimSpace(b.Type)
	b.ID = strings.TrimSpace(b.ID)
	if b.ID == "" || !idPattern.MatchString(b.ID) {
		b.ID = fmt.Sprintf("block-%d", fallbackIndex)
	}
	b.Eyebrow = trimMax(b.Eyebrow, 80)
	b.Heading = trimMax(b.Heading, 200)
	b.Subheading = trimMax(b.Subheading, 500)
	b.Body = trimMax(b.Body, 20000)
	b.PrimaryCTALabel = trimMax(b.PrimaryCTALabel, 80)
	b.PrimaryCTAURL = strings.TrimSpace(b.PrimaryCTAURL)
	b.SecondaryCTALabel = trimMax(b.SecondaryCTALabel, 80)
	b.SecondaryCTAURL = strings.TrimSpace(b.SecondaryCTAURL)
	b.ImageURL = strings.TrimSpace(b.ImageURL)
	b.ImageAlt = trimMax(b.ImageAlt, 250)
	b.CategorySlug = strings.TrimSpace(b.CategorySlug)
	b.SubmitLabel = trimMax(b.SubmitLabel, 80)
	b.SuccessMessage = trimMax(b.SuccessMessage, 300)
	b.PrivacyNote = trimMax(b.PrivacyNote, 300)

	if b.PrimaryCTAURL != "" && !ValidLinkURL(b.PrimaryCTAURL) {
		return Block{}, fmt.Errorf("%w: invalid primary CTA URL", ErrInvalidInput)
	}
	if b.SecondaryCTAURL != "" && !ValidLinkURL(b.SecondaryCTAURL) {
		return Block{}, fmt.Errorf("%w: invalid secondary CTA URL", ErrInvalidInput)
	}
	if b.ImageURL != "" && !ValidImageURL(b.ImageURL) {
		return Block{}, fmt.Errorf("%w: invalid image URL", ErrInvalidInput)
	}

	switch b.Align {
	case "center":
	default:
		b.Align = "left"
	}

	switch b.Tone {
	case "accent", "dark", "surface":
	default:
		b.Tone = "default"
	}

	switch b.Type {
	case BlockHero:
		if b.Heading == "" {
			b.Heading = "Título principal"
		}
	case BlockPosts:
		if b.Count < 1 {
			b.Count = 6
		}
		if b.Count > 12 {
			b.Count = 12
		}
		if b.Columns < 1 || b.Columns > 3 {
			b.Columns = 2
		}
		if b.Heading == "" {
			b.Heading = "Publicações recentes"
		}
	case BlockForm:
		b.FormID = content.Slugify(b.FormID)
		if b.FormID == "" {
			b.FormID = fmt.Sprintf("form-%d", fallbackIndex)
		}
		if b.FormMode != "newsletter" {
			b.FormMode = "contact"
		}
		if b.SubmitLabel == "" {
			if b.FormMode == "newsletter" {
				b.SubmitLabel = "Inscrever-se"
			} else {
				b.SubmitLabel = "Enviar mensagem"
			}
		}
		if b.SuccessMessage == "" {
			b.SuccessMessage = "Recebemos seu envio com sucesso!"
		}
	case BlockFeatures:
		if b.Columns < 2 || b.Columns > 3 {
			b.Columns = 3
		}
		if len(b.Items) > maxFeatureItems {
			return Block{}, fmt.Errorf("%w: too many feature items", ErrInvalidInput)
		}
		items := make([]FeatureItem, 0, len(b.Items))
		for _, item := range b.Items {
			item.Icon = trimMax(item.Icon, 16)
			item.Title = trimMax(item.Title, 120)
			item.Description = trimMax(item.Description, 400)
			item.LinkLabel = trimMax(item.LinkLabel, 80)
			item.LinkURL = strings.TrimSpace(item.LinkURL)
			if item.LinkURL != "" && !ValidLinkURL(item.LinkURL) {
				return Block{}, fmt.Errorf("%w: invalid feature link URL", ErrInvalidInput)
			}
			if item.Title == "" && item.Description == "" {
				continue
			}
			items = append(items, item)
		}
		b.Items = items
	case BlockRichText:
		// Body is rendered through content.RenderMarkdown (goldmark + bluemonday).
	case BlockCTA:
		if b.Heading == "" {
			b.Heading = "Chamada para ação"
		}
	default:
		return Block{}, fmt.Errorf("%w: unsupported block type %q", ErrInvalidInput, b.Type)
	}
	return b, nil
}

func ValidLinkURL(raw string) bool {
	if raw == "" || len(raw) > 2048 || strings.ContainsAny(raw, "\r\n\t\\") {
		return false
	}
	if strings.HasPrefix(raw, "#") {
		return len(raw) > 1
	}
	if strings.HasPrefix(raw, "/") {
		return !strings.HasPrefix(raw, "//")
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return false
	}
	scheme := strings.ToLower(parsed.Scheme)
	return scheme == "http" || scheme == "https"
}

func ValidImageURL(raw string) bool {
	if raw == "" || len(raw) > 2048 || strings.ContainsAny(raw, "\r\n\t\\") {
		return false
	}
	if strings.HasPrefix(raw, "/uploads/") {
		return !strings.Contains(raw, "..")
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return false
	}
	scheme := strings.ToLower(parsed.Scheme)
	return scheme == "http" || scheme == "https"
}

func trimMax(value string, maxRunes int) string {
	value = strings.TrimSpace(value)
	runes := []rune(value)
	if len(runes) > maxRunes {
		return strings.TrimSpace(string(runes[:maxRunes]))
	}
	return value
}

func availableLayoutSlug(ctx context.Context, tx *sql.Tx, base string, currentID int64, generated bool) (string, error) {
	for suffix := 1; suffix <= 100; suffix++ {
		slug := base
		if suffix > 1 {
			trailer := fmt.Sprintf("-%d", suffix)
			slug = strings.TrimRight(base, "-")
			if len(slug)+len(trailer) > 100 {
				slug = strings.TrimRight(slug[:100-len(trailer)], "-")
			}
			slug += trailer
		}
		var exists bool
		if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM page_layouts WHERE slug = ? AND id != ?)", slug, currentID).Scan(&exists); err != nil {
			return "", fmt.Errorf("check layout slug: %w", err)
		}
		if !exists {
			return slug, nil
		}
		if !generated {
			return "", ErrSlugTaken
		}
	}
	return "", ErrSlugTaken
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanLayout(row rowScanner) (Layout, error) {
	var layout Layout
	var isHome int
	err := row.Scan(
		&layout.ID, &layout.Name, &layout.Slug, &layout.Description,
		&layout.BlocksJSON, &isHome, &layout.CreatedAt, &layout.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Layout{}, ErrNotFound
	}
	if err != nil {
		return Layout{}, fmt.Errorf("scan page layout: %w", err)
	}
	layout.IsHomepage = isHome == 1
	if strings.TrimSpace(layout.BlocksJSON) != "" {
		if err := json.Unmarshal([]byte(layout.BlocksJSON), &layout.Blocks); err != nil {
			layout.Blocks = nil
		}
	}
	return layout, nil
}
