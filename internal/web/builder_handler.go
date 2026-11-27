package web

import (
	"encoding/json"
	"errors"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/Luw17/go-content-go/internal/builder"
	"github.com/Luw17/go-content-go/internal/content"
	"github.com/Luw17/go-content-go/internal/navigation"
	"github.com/Luw17/go-content-go/internal/siteconfig"
)

func (h *handler) builderList(w http.ResponseWriter, r *http.Request) {
	user, ok := h.requireAdmin(w, r)
	if !ok {
		return
	}
	layouts, err := h.builder.ListLayouts(r.Context())
	if err != nil {
		log.Printf("list page layouts: %v", err)
		http.Error(w, "Não foi possível carregar os layouts.", http.StatusInternalServerError)
		return
	}
	submissions, err := h.builder.ListFormSubmissions(r.Context(), 50)
	if err != nil {
		log.Printf("list form submissions: %v", err)
		http.Error(w, "Não foi possível carregar os envios de formulários.", http.StatusInternalServerError)
		return
	}
	token, err := issueCSRFToken(w, r)
	if err != nil {
		http.Error(w, "Não foi possível preparar o construtor visual.", http.StatusInternalServerError)
		return
	}
	page := adminPage{
		Mode:            "builder-list",
		Title:           "Construtor Visual",
		Username:        user.Username,
		Role:            user.Role,
		CSRFToken:       token,
		Layouts:         layouts,
		BuilderPresets:  builder.Presets(),
		FormSubmissions: submissions,
	}
	switch r.URL.Query().Get("notice") {
	case "saved":
		page.Notice = "Layout salvo com sucesso."
	case "homepage":
		page.Notice = "Homepage visual atualizada."
	case "homepage_cleared":
		page.Notice = "Homepage visual desativada. O tema padrão voltará a exibir o layout clássico."
	case "duplicated":
		page.Notice = "Layout duplicado com sucesso."
	case "deleted":
		page.Notice = "Layout excluído."
	case "submission_deleted":
		page.Notice = "Envio removido."
	}
	h.renderAdmin(w, page)
}

func (h *handler) builderNew(w http.ResponseWriter, r *http.Request) {
	user, ok := h.requireAdmin(w, r)
	if !ok {
		return
	}
	layout := builder.Layout{
		Name:       "",
		BlocksJSON: "[]",
	}
	if presetID := strings.TrimSpace(r.URL.Query().Get("preset")); presetID != "" {
		if preset, found := builder.PresetByID(presetID); found {
			layout.Name = preset.Name
			layout.Description = preset.Description
			layout.Blocks = preset.Blocks
			if encoded, err := json.Marshal(preset.Blocks); err == nil {
				layout.BlocksJSON = string(encoded)
			}
			if presetID == "homepage-editorial" {
				layout.IsHomepage = true
			}
		}
	}
	h.renderBuilderEditor(w, r, user.Username, user.Role, layout, "", "")
}

func (h *handler) builderEdit(w http.ResponseWriter, r *http.Request) {
	user, ok := h.requireAdmin(w, r)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		http.NotFound(w, r)
		return
	}
	layout, err := h.builder.GetLayout(r.Context(), id)
	if errors.Is(err, builder.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		log.Printf("load layout %d: %v", id, err)
		http.Error(w, "Não foi possível carregar o layout.", http.StatusInternalServerError)
		return
	}
	notice := ""
	if r.URL.Query().Get("notice") == "saved" {
		notice = "Layout salvo com sucesso."
	}
	h.renderBuilderEditor(w, r, user.Username, user.Role, layout, notice, "")
}

func (h *handler) renderBuilderEditor(w http.ResponseWriter, r *http.Request, username, role string, layout builder.Layout, notice, errMsg string) {
	token, err := issueCSRFToken(w, r)
	if err != nil {
		http.Error(w, "Não foi possível preparar o editor visual.", http.StatusInternalServerError)
		return
	}
	categories, err := content.Categories(r.Context(), h.db)
	if err != nil {
		log.Printf("load categories for builder: %v", err)
		http.Error(w, "Não foi possível carregar as categorias.", http.StatusInternalServerError)
		return
	}
	mediaItems, err := h.media.List(r.Context())
	if err != nil {
		log.Printf("load media for builder: %v", err)
		http.Error(w, "Não foi possível carregar as mídias.", http.StatusInternalServerError)
		return
	}
	if strings.TrimSpace(layout.BlocksJSON) == "" {
		layout.BlocksJSON = "[]"
	}
	previewHTML, err := builder.RenderBlocks(r.Context(), layout.Blocks, builder.RenderContext{
		DB:       h.db,
		PagePath: "/",
		Preview:  true,
		FilterMarkdown: func(md string) string {
			return h.filteredMarkdown(r, md)
		},
	})
	if err != nil {
		log.Printf("render builder editor preview: %v", err)
	}
	title := "Novo Layout Visual"
	if layout.ID > 0 {
		title = "Editar Layout: " + layout.Name
	}
	status := http.StatusOK
	if errMsg != "" {
		status = http.StatusUnprocessableEntity
	}
	h.renderAdminStatus(w, adminPage{
		Mode:           "builder-editor",
		Title:          title,
		Username:       username,
		Role:           role,
		CSRFToken:      token,
		Layout:         layout,
		Categories:     categories,
		MediaItems:     mediaItems,
		BuilderPreview: previewHTML,
		Notice:         notice,
		Error:          errMsg,
	}, status)
}

func (h *handler) saveBuilderLayout(w http.ResponseWriter, r *http.Request) {
	user, ok := h.requireAdmin(w, r)
	if !ok {
		return
	}
	if err := parseContentForm(w, r); err != nil {
		http.Error(w, "Formulário inválido.", http.StatusBadRequest)
		return
	}
	if !validCSRFToken(r) {
		http.Error(w, "A sessão do formulário expirou.", http.StatusForbidden)
		return
	}
	id, err := optionalID(r.FormValue("id"))
	if err != nil {
		http.Error(w, "Identificador de layout inválido.", http.StatusBadRequest)
		return
	}
	blocksRaw := strings.TrimSpace(r.FormValue("blocks_json"))
	if blocksRaw == "" {
		blocksRaw = "[]"
	}
	var blocks []builder.Block
	if err := json.Unmarshal([]byte(blocksRaw), &blocks); err != nil {
		h.renderBuilderEditor(w, r, user.Username, user.Role, builder.Layout{
			ID:          id,
			Name:        r.FormValue("name"),
			Slug:        r.FormValue("slug"),
			Description: r.FormValue("description"),
			BlocksJSON:  blocksRaw,
			IsHomepage:  r.FormValue("is_homepage") == "1",
		}, "", "A estrutura de blocos enviada é inválida.")
		return
	}

	saved, err := h.builder.SaveLayout(r.Context(), builder.LayoutInput{
		ID:          id,
		Name:        r.FormValue("name"),
		Slug:        r.FormValue("slug"),
		Description: r.FormValue("description"),
		Blocks:      blocks,
		IsHomepage:  r.FormValue("is_homepage") == "1",
	})
	if errors.Is(err, builder.ErrInvalidInput) || errors.Is(err, builder.ErrSlugTaken) {
		msg := "Revise os dados do layout: informe um nome válido e verifique URLs dos blocos."
		if errors.Is(err, builder.ErrSlugTaken) {
			msg = "Este slug já está em uso por outro layout."
		}
		h.renderBuilderEditor(w, r, user.Username, user.Role, builder.Layout{
			ID:          id,
			Name:        r.FormValue("name"),
			Slug:        r.FormValue("slug"),
			Description: r.FormValue("description"),
			Blocks:      blocks,
			BlocksJSON:  blocksRaw,
			IsHomepage:  r.FormValue("is_homepage") == "1",
		}, "", msg)
		return
	}
	if errors.Is(err, builder.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		log.Printf("save layout: %v", err)
		http.Error(w, "Não foi possível salvar o layout.", http.StatusInternalServerError)
		return
	}
	h.cache.Clear()
	http.Redirect(w, r, "/admin/builder/"+strconv.FormatInt(saved.ID, 10)+"/edit?notice=saved", http.StatusSeeOther)
}

func (h *handler) previewBuilderBlocks(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	if err := parseContentForm(w, r); err != nil {
		http.Error(w, "Formulário inválido.", http.StatusBadRequest)
		return
	}
	if !validCSRFToken(r) {
		http.Error(w, "A sessão do formulário expirou.", http.StatusForbidden)
		return
	}
	blocksRaw := strings.TrimSpace(r.FormValue("blocks_json"))
	if blocksRaw == "" {
		blocksRaw = "[]"
	}
	var rawBlocks []builder.Block
	if err := json.Unmarshal([]byte(blocksRaw), &rawBlocks); err != nil {
		http.Error(w, "JSON de blocos inválido.", http.StatusBadRequest)
		return
	}
	sanitized, err := builder.SanitizeBlocks(rawBlocks)
	if err != nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`<p class="error">Revise as URLs ou campos dos blocos para gerar a prévia.</p>`))
		return
	}
	rendered, err := builder.RenderBlocks(r.Context(), sanitized, builder.RenderContext{
		DB:       h.db,
		PagePath: "/",
		Preview:  true,
		FilterMarkdown: func(md string) string {
			return h.filteredMarkdown(r, md)
		},
	})
	if err != nil {
		log.Printf("preview builder blocks: %v", err)
		http.Error(w, "Não foi possível renderizar a prévia.", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	if rendered == "" {
		_, _ = w.Write([]byte(`<p class="empty-state">Adicione blocos ao canvas para visualizar a prévia aqui.</p>`))
		return
	}
	_, _ = w.Write([]byte(rendered))
}

func (h *handler) setHomepageLayout(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	if err := parseForm(w, r); err != nil {
		http.Error(w, "Formulário inválido.", http.StatusBadRequest)
		return
	}
	if !validCSRFToken(r) {
		http.Error(w, "A sessão do formulário expirou.", http.StatusForbidden)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		http.NotFound(w, r)
		return
	}
	if err := h.builder.SetHomepageLayout(r.Context(), id); errors.Is(err, builder.ErrNotFound) {
		http.NotFound(w, r)
		return
	} else if err != nil {
		log.Printf("set homepage layout %d: %v", id, err)
		http.Error(w, "Não foi possível ativar o layout na homepage.", http.StatusInternalServerError)
		return
	}
	h.cache.Clear()
	http.Redirect(w, r, "/admin/builder?notice=homepage", http.StatusSeeOther)
}

func (h *handler) clearHomepageLayout(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	if err := parseForm(w, r); err != nil {
		http.Error(w, "Formulário inválido.", http.StatusBadRequest)
		return
	}
	if !validCSRFToken(r) {
		http.Error(w, "A sessão do formulário expirou.", http.StatusForbidden)
		return
	}
	if err := h.builder.SetHomepageLayout(r.Context(), 0); err != nil {
		log.Printf("clear homepage layout: %v", err)
		http.Error(w, "Não foi possível restaurar a homepage padrão.", http.StatusInternalServerError)
		return
	}
	h.cache.Clear()
	http.Redirect(w, r, "/admin/builder?notice=homepage_cleared", http.StatusSeeOther)
}

func (h *handler) duplicateBuilderLayout(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	if err := parseForm(w, r); err != nil {
		http.Error(w, "Formulário inválido.", http.StatusBadRequest)
		return
	}
	if !validCSRFToken(r) {
		http.Error(w, "A sessão do formulário expirou.", http.StatusForbidden)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		http.NotFound(w, r)
		return
	}
	if _, err := h.builder.DuplicateLayout(r.Context(), id); errors.Is(err, builder.ErrNotFound) {
		http.NotFound(w, r)
		return
	} else if err != nil {
		log.Printf("duplicate layout %d: %v", id, err)
		http.Error(w, "Não foi possível duplicar o layout.", http.StatusInternalServerError)
		return
	}
	h.cache.Clear()
	http.Redirect(w, r, "/admin/builder?notice=duplicated", http.StatusSeeOther)
}

func (h *handler) deleteBuilderLayout(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	if err := parseForm(w, r); err != nil {
		http.Error(w, "Formulário inválido.", http.StatusBadRequest)
		return
	}
	if !validCSRFToken(r) {
		http.Error(w, "A sessão do formulário expirou.", http.StatusForbidden)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		http.NotFound(w, r)
		return
	}
	if err := h.builder.DeleteLayout(r.Context(), id); errors.Is(err, builder.ErrNotFound) {
		http.NotFound(w, r)
		return
	} else if err != nil {
		log.Printf("delete layout %d: %v", id, err)
		http.Error(w, "Não foi possível excluir o layout.", http.StatusInternalServerError)
		return
	}
	h.cache.Clear()
	http.Redirect(w, r, "/admin/builder?notice=deleted", http.StatusSeeOther)
}

func (h *handler) deleteFormSubmission(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	if err := parseForm(w, r); err != nil {
		http.Error(w, "Formulário inválido.", http.StatusBadRequest)
		return
	}
	if !validCSRFToken(r) {
		http.Error(w, "A sessão do formulário expirou.", http.StatusForbidden)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		http.NotFound(w, r)
		return
	}
	if err := h.builder.DeleteFormSubmission(r.Context(), id); errors.Is(err, builder.ErrNotFound) {
		http.NotFound(w, r)
		return
	} else if err != nil {
		log.Printf("delete form submission %d: %v", id, err)
		http.Error(w, "Não foi possível excluir o envio.", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/admin/builder?notice=submission_deleted", http.StatusSeeOther)
}

// ── Global Headers & Footers (Site Sections) ────────────────────────────────

func (h *handler) sectionsPage(w http.ResponseWriter, r *http.Request) {
	user, ok := h.requireAdmin(w, r)
	if !ok {
		return
	}
	sections, err := h.builder.ListSections(r.Context())
	if err != nil {
		log.Printf("list site sections: %v", err)
		http.Error(w, "Não foi possível carregar cabeçalhos e rodapés.", http.StatusInternalServerError)
		return
	}
	settings, err := siteconfig.Load(r.Context(), h.db)
	if err != nil {
		log.Printf("load site settings for sections: %v", err)
		http.Error(w, "Não foi possível carregar configurações do site.", http.StatusInternalServerError)
		return
	}
	selected := builder.SiteSection{
		SectionType: builder.SectionHeader,
		TargetScope: builder.ScopeAll,
		Active:      true,
		Config:      builder.DefaultHeaderConfig(),
	}
	if r.URL.Query().Get("type") == builder.SectionFooter {
		selected.SectionType = builder.SectionFooter
		selected.Config = builder.DefaultFooterConfig()
	}
	if editRaw := r.URL.Query().Get("edit"); editRaw != "" {
		if editID, err := strconv.ParseInt(editRaw, 10, 64); err == nil && editID > 0 {
			if loaded, err := h.builder.GetSection(r.Context(), editID); err == nil {
				selected = loaded
			}
		}
	}
	if encoded, err := json.Marshal(selected.Config.Columns); err == nil {
		if selected.Config.Columns == nil {
			encoded = []byte("[]")
		}
		selected.ConfigJSON = string(encoded)
	}
	token, err := issueCSRFToken(w, r)
	if err != nil {
		http.Error(w, "Não foi possível preparar a página de cabeçalhos e rodapés.", http.StatusInternalServerError)
		return
	}
	page := adminPage{
		Mode:            "sections",
		Title:           "Cabeçalhos e Rodapés",
		Username:        user.Username,
		Role:            user.Role,
		CSRFToken:       token,
		SiteSettings:    settings,
		SiteSections:    sections,
		SelectedSection: selected,
	}
	switch r.URL.Query().Get("notice") {
	case "saved":
		page.Notice = "Seção global salva com sucesso."
	case "deleted":
		page.Notice = "Seção global removida."
	}
	if r.URL.Query().Get("error") == "invalid" {
		page.Error = "Revise os dados da seção: informe um nome válido, slug (se aplicável) e URLs válidas."
	}
	h.renderAdmin(w, page)
}

func (h *handler) saveSection(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	if err := parseContentForm(w, r); err != nil {
		http.Error(w, "Formulário inválido.", http.StatusBadRequest)
		return
	}
	if !validCSRFToken(r) {
		http.Error(w, "A sessão do formulário expirou.", http.StatusForbidden)
		return
	}
	id, err := optionalID(r.FormValue("id"))
	if err != nil {
		http.Redirect(w, r, "/admin/appearance/sections?error=invalid", http.StatusSeeOther)
		return
	}
	var columns []builder.FooterColumn
	if rawCols := strings.TrimSpace(r.FormValue("columns_json")); rawCols != "" && rawCols != "[]" {
		if err := json.Unmarshal([]byte(rawCols), &columns); err != nil {
			http.Redirect(w, r, "/admin/appearance/sections?error=invalid", http.StatusSeeOther)
			return
		}
	}
	input := builder.SectionInput{
		ID:          id,
		Name:        r.FormValue("name"),
		SectionType: r.FormValue("section_type"),
		TargetScope: r.FormValue("target_scope"),
		TargetSlug:  r.FormValue("target_slug"),
		Active:      r.FormValue("active") == "1",
		Config: builder.SectionConfig{
			Layout:           r.FormValue("layout"),
			Tone:             r.FormValue("tone"),
			ShowLogo:         r.FormValue("show_logo") == "1",
			ShowSiteTitle:    r.FormValue("show_site_title") == "1",
			ShowTagline:      r.FormValue("show_tagline") == "1",
			ShowMenu:         r.FormValue("show_menu") == "1",
			MenuLocation:     r.FormValue("menu_location"),
			Sticky:           r.FormValue("sticky") == "1",
			AnnouncementText: r.FormValue("announcement_text"),
			AnnouncementURL:  r.FormValue("announcement_url"),
			CTALabel:         r.FormValue("cta_label"),
			CTAURL:           r.FormValue("cta_url"),
			CustomText:       r.FormValue("custom_text"),
			CopyrightText:    r.FormValue("copyright_text"),
			Columns:          columns,
		},
	}
	saved, err := h.builder.SaveSection(r.Context(), input)
	if errors.Is(err, builder.ErrInvalidInput) {
		http.Redirect(w, r, "/admin/appearance/sections?error=invalid", http.StatusSeeOther)
		return
	}
	if errors.Is(err, builder.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		log.Printf("save site section: %v", err)
		http.Error(w, "Não foi possível salvar a seção global.", http.StatusInternalServerError)
		return
	}
	h.cache.Clear()
	http.Redirect(w, r, "/admin/appearance/sections?edit="+strconv.FormatInt(saved.ID, 10)+"&notice=saved", http.StatusSeeOther)
}

func (h *handler) deleteSection(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	if err := parseForm(w, r); err != nil {
		http.Error(w, "Formulário inválido.", http.StatusBadRequest)
		return
	}
	if !validCSRFToken(r) {
		http.Error(w, "A sessão do formulário expirou.", http.StatusForbidden)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		http.NotFound(w, r)
		return
	}
	if err := h.builder.DeleteSection(r.Context(), id); errors.Is(err, builder.ErrNotFound) {
		http.NotFound(w, r)
		return
	} else if err != nil {
		log.Printf("delete site section %d: %v", id, err)
		http.Error(w, "Não foi possível excluir a seção global.", http.StatusInternalServerError)
		return
	}
	h.cache.Clear()
	http.Redirect(w, r, "/admin/appearance/sections?notice=deleted", http.StatusSeeOther)
}

// ── Public Form Submissions ─────────────────────────────────────────────────

func (h *handler) submitPublicForm(w http.ResponseWriter, r *http.Request) {
	if err := parseForm(w, r); err != nil {
		http.Error(w, "Formulário inválido.", http.StatusBadRequest)
		return
	}
	formID := strings.TrimSpace(r.FormValue("form_id"))
	pagePath := strings.TrimSpace(r.FormValue("page_path"))
	if pagePath == "" || !strings.HasPrefix(pagePath, "/") || strings.HasPrefix(pagePath, "//") {
		pagePath = "/"
	}
	if parsed, err := url.Parse(pagePath); err != nil || parsed.Host != "" || parsed.Scheme != "" {
		pagePath = "/"
	} else {
		pagePath = parsed.Path
		if pagePath == "" {
			pagePath = "/"
		}
	}

	// Honeypot field: silently pretend success to discourage bots.
	if strings.TrimSpace(r.FormValue("website")) != "" {
		http.Redirect(w, r, pagePath+"?form_submitted="+url.QueryEscape(formID)+"#"+url.PathEscape(formID), http.StatusSeeOther)
		return
	}

	submission := builder.FormSubmission{
		FormID:      formID,
		PagePath:    pagePath,
		SenderName:  r.FormValue("name"),
		SenderEmail: r.FormValue("email"),
		Message:     r.FormValue("message"),
	}
	_, err := h.builder.SaveFormSubmission(r.Context(), submission)
	if errors.Is(err, builder.ErrInvalidInput) {
		http.Redirect(w, r, pagePath+"?form_error="+url.QueryEscape(formID)+"#"+url.PathEscape(formID), http.StatusSeeOther)
		return
	}
	if err != nil {
		log.Printf("save public form submission: %v", err)
		http.Error(w, "Não foi possível processar o formulário.", http.StatusInternalServerError)
		return
	}
	go h.dispatchFormNotifications(submission)
	http.Redirect(w, r, pagePath+"?form_submitted="+url.QueryEscape(formID)+"#"+url.PathEscape(formID), http.StatusSeeOther)
}

// ── Theme Header / Footer Resolution Helper ─────────────────────────────────

func (h *handler) renderVisualSection(r *http.Request, sectionType, scope, slug string, settings siteconfig.Settings) (template.HTML, error) {
	sec, found, err := h.builder.ResolveSection(r.Context(), sectionType, scope, slug)
	if err != nil || !found {
		return "", err
	}
	var menuHTML template.HTML
	if sec.Config.ShowMenu {
		location := sec.Config.MenuLocation
		if location == "" {
			if sectionType == builder.SectionFooter {
				location = navigation.LocationFooter
			} else {
				location = navigation.LocationPrimary
			}
		}
		menuHTML, err = h.menus.RenderHTML(r.Context(), location)
		if err != nil {
			return "", err
		}
	}
	if sectionType == builder.SectionHeader {
		return builder.RenderHeaderHTML(sec, settings, menuHTML)
	}
	return builder.RenderFooterHTML(sec, settings, menuHTML)
}
