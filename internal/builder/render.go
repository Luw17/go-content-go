package builder

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	stdhtml "html"
	"html/template"
	"regexp"
	"strings"
	"time"

	"github.com/Luw17/go-content-go/internal/content"
)

type RenderContext struct {
	DB              *sql.DB
	PagePath        string
	SubmittedFormID string
	ErrorFormID     string
	Preview         bool
	FilterMarkdown  func(string) string
}

type renderedPost struct {
	Title                string
	Slug                 string
	FeaturedImage        string
	Summary              string
	Author               string
	PublishedAt          string
	FormattedPublishedAt string
}

type renderedBlock struct {
	Block
	Posts         []renderedPost
	BodyHTML      template.HTML
	PagePath      string
	FormSubmitted bool
	FormError     bool
	Preview       bool
}

var htmlStripPattern = regexp.MustCompile(`<[^>]*>`)

var blockTemplates = template.Must(template.New("blocks").Parse(`
{{define "builder-blocks"}}
<div class="builder-stack">
  {{range $block := .}}
    {{if eq .Type "hero"}}
      <section id="{{.ID}}" class="builder-block builder-hero builder-align-{{.Align}} builder-tone-{{.Tone}}{{if .ImageURL}} builder-hero-with-media{{end}}">
        <div class="builder-hero-copy">
          {{if .Eyebrow}}<p class="builder-eyebrow">{{.Eyebrow}}</p>{{end}}
          <h1 class="builder-hero-heading">{{.Heading}}</h1>
          {{if .Subheading}}<p class="builder-subheading">{{.Subheading}}</p>{{end}}
          {{if or (and .PrimaryCTALabel .PrimaryCTAURL) (and .SecondaryCTALabel .SecondaryCTAURL)}}
            <div class="builder-actions">
              {{if and .PrimaryCTALabel .PrimaryCTAURL}}
                <a class="builder-btn builder-btn-primary" href="{{.PrimaryCTAURL}}">{{.PrimaryCTALabel}}</a>
              {{end}}
              {{if and .SecondaryCTALabel .SecondaryCTAURL}}
                <a class="builder-btn builder-btn-secondary" href="{{.SecondaryCTAURL}}">{{.SecondaryCTALabel}}</a>
              {{end}}
            </div>
          {{end}}
        </div>
        {{if .ImageURL}}
          <figure class="builder-hero-media">
            <img src="{{.ImageURL}}" alt="{{.ImageAlt}}" fetchpriority="high">
          </figure>
        {{end}}
      </section>
    {{else if eq .Type "posts"}}
      <section id="{{.ID}}" class="builder-block builder-posts builder-tone-{{.Tone}}">
        <div class="builder-section-header">
          <div>
            {{if .Eyebrow}}<p class="builder-eyebrow">{{.Eyebrow}}</p>{{end}}
            <h2>{{.Heading}}</h2>
            {{if .Subheading}}<p class="builder-subheading">{{.Subheading}}</p>{{end}}
          </div>
          {{if and .PrimaryCTALabel .PrimaryCTAURL}}
            <a class="builder-link-more" href="{{.PrimaryCTAURL}}">{{.PrimaryCTALabel}} →</a>
          {{end}}
        </div>
        {{if .Posts}}
          <div class="builder-posts-grid builder-cols-{{.Columns}}">
            {{range $post := .Posts}}
              <article class="builder-post-card">
                {{if and $block.ShowImage $post.FeaturedImage}}
                  <a class="builder-post-thumb" href="/{{$post.Slug}}" tabindex="-1" aria-hidden="true">
                    <img src="{{$post.FeaturedImage}}" alt="" loading="lazy">
                  </a>
                {{end}}
                <div class="builder-post-body">
                  <div class="builder-post-meta">
                    {{if $post.FormattedPublishedAt}}<time datetime="{{$post.PublishedAt}}">{{$post.FormattedPublishedAt}}</time>{{end}}
                    {{if and $post.FormattedPublishedAt $post.Author}}<span aria-hidden="true">·</span>{{end}}
                    {{if $post.Author}}<span>Por {{$post.Author}}</span>{{end}}
                  </div>
                  <h3><a href="/{{$post.Slug}}">{{$post.Title}}</a></h3>
                  {{if and $block.ShowExcerpt $post.Summary}}<p class="builder-post-excerpt">{{$post.Summary}}</p>{{end}}
                  <a class="builder-read-more" href="/{{$post.Slug}}">Continuar lendo <span aria-hidden="true">→</span></a>
                </div>
              </article>
            {{end}}
          </div>
        {{else}}
          <div class="builder-empty">
            <p>Nenhuma publicação encontrada para esta lista ainda.</p>
          </div>
        {{end}}
      </section>
    {{else if eq .Type "features"}}
      <section id="{{.ID}}" class="builder-block builder-features builder-align-{{.Align}} builder-tone-{{.Tone}}">
        <div class="builder-section-header">
          <div>
            {{if .Eyebrow}}<p class="builder-eyebrow">{{.Eyebrow}}</p>{{end}}
            {{if .Heading}}<h2>{{.Heading}}</h2>{{end}}
            {{if .Subheading}}<p class="builder-subheading">{{.Subheading}}</p>{{end}}
          </div>
        </div>
        {{if .Items}}
          <div class="builder-features-grid builder-cols-{{.Columns}}">
            {{range .Items}}
              <article class="builder-feature-card">
                {{if .Icon}}<span class="builder-feature-icon" aria-hidden="true">{{.Icon}}</span>{{end}}
                {{if .Title}}<h3>{{.Title}}</h3>{{end}}
                {{if .Description}}<p>{{.Description}}</p>{{end}}
                {{if and .LinkLabel .LinkURL}}
                  <a class="builder-read-more" href="{{.LinkURL}}">{{.LinkLabel}} <span aria-hidden="true">→</span></a>
                {{end}}
              </article>
            {{end}}
          </div>
        {{end}}
      </section>
    {{else if eq .Type "rich_text"}}
      <section id="{{.ID}}" class="builder-block builder-richtext builder-align-{{.Align}} builder-tone-{{.Tone}}">
        {{if .Eyebrow}}<p class="builder-eyebrow">{{.Eyebrow}}</p>{{end}}
        {{if .Heading}}<h2>{{.Heading}}</h2>{{end}}
        <div class="builder-richtext-body article-content">{{.BodyHTML}}</div>
      </section>
    {{else if eq .Type "cta"}}
      <section id="{{.ID}}" class="builder-block builder-cta builder-align-{{.Align}} builder-tone-{{.Tone}}">
        <div class="builder-cta-copy">
          {{if .Eyebrow}}<p class="builder-eyebrow">{{.Eyebrow}}</p>{{end}}
          <h2>{{.Heading}}</h2>
          {{if .Body}}<p class="builder-subheading">{{.Body}}</p>{{end}}
        </div>
        {{if or (and .PrimaryCTALabel .PrimaryCTAURL) (and .SecondaryCTALabel .SecondaryCTAURL)}}
          <div class="builder-actions">
            {{if and .PrimaryCTALabel .PrimaryCTAURL}}
              <a class="builder-btn builder-btn-primary" href="{{.PrimaryCTAURL}}">{{.PrimaryCTALabel}}</a>
            {{end}}
            {{if and .SecondaryCTALabel .SecondaryCTAURL}}
              <a class="builder-btn builder-btn-secondary" href="{{.SecondaryCTAURL}}">{{.SecondaryCTALabel}}</a>
            {{end}}
          </div>
        {{end}}
      </section>
    {{else if eq .Type "form"}}
      <section id="{{.FormID}}" class="builder-block builder-form-section builder-align-{{.Align}} builder-tone-{{.Tone}}">
        <div class="builder-form-intro">
          {{if .Eyebrow}}<p class="builder-eyebrow">{{.Eyebrow}}</p>{{end}}
          {{if .Heading}}<h2>{{.Heading}}</h2>{{end}}
          {{if .Subheading}}<p class="builder-subheading">{{.Subheading}}</p>{{end}}
        </div>
        {{if .FormSubmitted}}
          <p class="builder-form-notice" role="status">{{.SuccessMessage}}</p>
        {{else if .FormError}}
          <p class="builder-form-error" role="alert">Revise os campos obrigatórios e informe um e-mail válido.</p>
        {{end}}
        {{if .Preview}}
        <div class="builder-form builder-form-{{.FormMode}}" aria-label="Prévia do formulário">
          {{if eq .FormMode "newsletter"}}
            <div class="builder-form-inline">
              <label class="sr-only" for="preview-email-{{.FormID}}">Seu e-mail</label>
              <input id="preview-email-{{.FormID}}" type="email" placeholder="voce@exemplo.com" disabled>
              <button class="builder-btn builder-btn-primary" type="button" disabled>{{.SubmitLabel}}</button>
            </div>
          {{else}}
            <div class="builder-form-grid">
              <div>
                <label for="preview-name-{{.FormID}}">Nome</label>
                <input id="preview-name-{{.FormID}}" type="text" placeholder="Seu nome" disabled>
              </div>
              <div>
                <label for="preview-email-{{.FormID}}">E-mail</label>
                <input id="preview-email-{{.FormID}}" type="email" placeholder="voce@exemplo.com" disabled>
              </div>
            </div>
            <div>
              <label for="preview-message-{{.FormID}}">Mensagem</label>
              <textarea id="preview-message-{{.FormID}}" rows="4" placeholder="Como podemos ajudar?" disabled></textarea>
            </div>
            <button class="builder-btn builder-btn-primary" type="button" disabled>{{.SubmitLabel}}</button>
          {{end}}
          {{if .PrivacyNote}}<p class="builder-form-note">{{.PrivacyNote}}</p>{{end}}
        </div>
        {{else}}
        <form class="builder-form builder-form-{{.FormMode}}" method="post" action="/forms/submit">
          <input type="hidden" name="form_id" value="{{.FormID}}">
          <input type="hidden" name="page_path" value="{{.PagePath}}">
          <div class="builder-honeypot" aria-hidden="true">
            <label for="hp-{{.FormID}}">Website</label>
            <input id="hp-{{.FormID}}" name="website" type="text" tabindex="-1" autocomplete="off">
          </div>
          {{if eq .FormMode "newsletter"}}
            <div class="builder-form-inline">
              <label class="sr-only" for="email-{{.FormID}}">Seu e-mail</label>
              <input id="email-{{.FormID}}" name="email" type="email" maxlength="254" placeholder="voce@exemplo.com" required>
              <button class="builder-btn builder-btn-primary" type="submit">{{.SubmitLabel}}</button>
            </div>
          {{else}}
            <div class="builder-form-grid">
              <div>
                <label for="name-{{.FormID}}">Nome</label>
                <input id="name-{{.FormID}}" name="name" type="text" maxlength="120" placeholder="Seu nome" required>
              </div>
              <div>
                <label for="email-{{.FormID}}">E-mail</label>
                <input id="email-{{.FormID}}" name="email" type="email" maxlength="254" placeholder="voce@exemplo.com" required>
              </div>
            </div>
            <div>
              <label for="message-{{.FormID}}">Mensagem</label>
              <textarea id="message-{{.FormID}}" name="message" rows="4" maxlength="5000" placeholder="Como podemos ajudar?" required></textarea>
            </div>
            <button class="builder-btn builder-btn-primary" type="submit">{{.SubmitLabel}}</button>
          {{end}}
          {{if .PrivacyNote}}<p class="builder-form-note">{{.PrivacyNote}}</p>{{end}}
        </form>
        {{end}}
      </section>
    {{end}}
  {{end}}
</div>
{{end}}
`))

func RenderBlocks(ctx context.Context, blocks []Block, rc RenderContext) (template.HTML, error) {
	if len(blocks) == 0 {
		return "", nil
	}
	pagePath := rc.PagePath
	if pagePath == "" {
		pagePath = "/"
	}
	prepared := make([]renderedBlock, 0, len(blocks))
	for _, block := range blocks {
		rb := renderedBlock{
			Block:    block,
			PagePath: pagePath,
			Preview:  rc.Preview,
		}
		switch block.Type {
		case BlockPosts:
			if rc.DB != nil {
				limit := block.Count
				if limit < 1 || limit > 12 {
					limit = 6
				}
				var items []content.Item
				var err error
				if block.CategorySlug != "" {
					items, err = content.PublishedByCategory(ctx, rc.DB, block.CategorySlug, limit)
				} else {
					items, err = content.Published(ctx, rc.DB, limit)
				}
				if err != nil {
					return "", fmt.Errorf("load posts for builder block %q: %w", block.ID, err)
				}
				for _, item := range items {
					md := item.Content
					if rc.FilterMarkdown != nil {
						md = rc.FilterMarkdown(md)
					}
					rendered, err := content.RenderMarkdown(md)
					if err != nil {
						return "", fmt.Errorf("render post summary for builder block: %w", err)
					}
					rb.Posts = append(rb.Posts, toRenderedPost(item, rendered))
				}
			}
		case BlockRichText:
			md := block.Body
			if rc.FilterMarkdown != nil {
				md = rc.FilterMarkdown(md)
			}
			rendered, err := content.RenderMarkdown(md)
			if err != nil {
				return "", fmt.Errorf("render rich text block %q: %w", block.ID, err)
			}
			rb.BodyHTML = template.HTML(rendered)
		case BlockForm:
			rb.FormSubmitted = rc.SubmittedFormID != "" && rc.SubmittedFormID == block.FormID
			rb.FormError = rc.ErrorFormID != "" && rc.ErrorFormID == block.FormID
		}
		prepared = append(prepared, rb)
	}
	var out bytes.Buffer
	if err := blockTemplates.ExecuteTemplate(&out, "builder-blocks", prepared); err != nil {
		return "", fmt.Errorf("render builder blocks: %w", err)
	}
	return template.HTML(out.String()), nil
}

func toRenderedPost(item content.Item, renderedHTML string) renderedPost {
	plain := stdhtml.UnescapeString(htmlStripPattern.ReplaceAllString(renderedHTML, " "))
	plain = strings.Join(strings.Fields(plain), " ")
	runes := []rune(plain)
	if len(runes) > 180 {
		plain = strings.TrimSpace(string(runes[:180])) + "…"
	}
	formatted := item.PublishedAt
	if parsed, err := time.Parse(time.RFC3339Nano, item.PublishedAt); err == nil {
		formatted = parsed.Format("02/01/2006")
	}
	return renderedPost{
		Title:                item.Title,
		Slug:                 item.Slug,
		FeaturedImage:        item.FeaturedImage,
		Summary:              plain,
		Author:               item.Author,
		PublishedAt:          item.PublishedAt,
		FormattedPublishedAt: formatted,
	}
}
