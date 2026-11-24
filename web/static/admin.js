"use strict";

document.addEventListener("click", async (event) => {
  const button = event.target.closest("[data-copy-url]");
  if (!button) {
    return;
  }

  try {
    await navigator.clipboard.writeText(new URL(button.dataset.copyUrl, window.location.origin).href);
    const originalText = button.textContent;
    button.textContent = "Copiado!";
    window.setTimeout(() => {
      button.textContent = originalText;
    }, 1500);
  } catch {
    button.textContent = "Não foi possível copiar";
  }
});

document.body.addEventListener("htmx:beforeSwap", (event) => {
  const target = event.detail.target;
  if (target?.id === "media-content" && event.detail.xhr.status >= 400) {
    event.detail.shouldSwap = true;
    event.detail.isError = false;
  }
});

document.addEventListener("click", (event) => {
  const target = event.target instanceof Element ? event.target : null;
  const openButton = target?.closest("[data-picker-open]");
  if (openButton) {
    const dialog = document.getElementById("media-picker-dialog");
    if (dialog && !dialog.open) {
      dialog.showModal();
    }
    return;
  }

  if (target?.closest("[data-picker-close]")) {
    document.getElementById("media-picker-dialog")?.close();
    return;
  }

  const selected = target?.closest("[data-media-select]");
  if (!selected) {
    return;
  }

  const path = selected.dataset.path;
  const alt = selected.dataset.alt || "";
  const mode = selected.dataset.mode;
  if (!path || (mode !== "featured" && mode !== "inline" && mode !== "og")) {
    return;
  }

  if (mode === "og") {
    const input = document.getElementById("og_image");
    if (input) {
      input.value = path;
    }
  } else if (mode === "featured") {
    const input = document.getElementById("featured_image");
    if (!input) {
      return;
    }
    input.value = path;
    const preview = document.getElementById("featured-image-preview");
    if (preview) {
      preview.replaceChildren();
      const image = document.createElement("img");
      image.className = "featured-preview";
      image.src = path;
      image.alt = alt || "Prévia da imagem destacada";
      preview.append(image);
    }
  } else {
    const textarea = document.getElementById("content");
    if (!textarea) {
      return;
    }
    const safeAlt = alt.replace(/([\\[\]])/g, "\\$1").replace(/[\r\n]+/g, " ");
    const markdown = `![${safeAlt}](<${path}>)`;
    const start = textarea.selectionStart;
    const end = textarea.selectionEnd;
    textarea.setRangeText(markdown, start, end, "end");
    textarea.focus();
    textarea.dispatchEvent(new Event("input", { bubbles: true }));
  }

  document.getElementById("media-picker-dialog")?.close();
}, true);

document.body.addEventListener("htmx:responseError", (event) => {
  if (event.detail.target?.id === "media-picker-content") {
    event.detail.target.textContent = "Não foi possível carregar as imagens. Feche e tente novamente.";
  }
});

function updateMenuItemFields(form) {
  const type = form.querySelector("#menu-item-type")?.value;
  for (const section of form.querySelectorAll("[data-menu-item-fields]")) {
    const active = section.dataset.menuItemFields === type;
    section.hidden = !active;
    for (const field of section.querySelectorAll("input, select")) {
      field.disabled = !active;
      field.required = active && field.dataset.required === "true";
    }
  }
}

document.addEventListener("change", (event) => {
  if (event.target.matches("#menu-item-type")) {
    updateMenuItemFields(event.target.form);
  }

  if (event.target.matches("[data-menu-select]")) {
    const url = new URL(window.location.href);
    url.searchParams.set("menu", event.target.value);
    window.location.assign(url);
  }
});

function initializeMenuItemFields() {
  const form = document.querySelector(".menu-item-form");
  if (form) {
    for (const field of form.querySelectorAll("[required]")) {
      field.dataset.required = "true";
    }
    updateMenuItemFields(form);
  }
}

function updateSERPPreview() {
  const titleInput = document.getElementById("title");
  const seoTitleInput = document.getElementById("seo_title");
  const slugInput = document.getElementById("slug");
  const descInput = document.getElementById("seo_description");

  const serpTitle = document.getElementById("serp-title");
  const serpUrl = document.getElementById("serp-url");
  const serpDesc = document.getElementById("serp-desc");

  if (!serpTitle || !serpUrl || !serpDesc) {
    return;
  }

  const effectiveTitle = (seoTitleInput?.value || "").trim() || (titleInput?.value || "").trim() || "Título do conteúdo";
  serpTitle.textContent = effectiveTitle;

  const effectiveSlug = (slugInput?.value || "").trim();
  serpUrl.textContent = "/" + effectiveSlug;

  const effectiveDesc = (descInput?.value || "").trim() || "Prévia da descrição deste conteúdo nos resultados de busca do Google…";
  serpDesc.textContent = effectiveDesc;

  const titleCounter = document.getElementById("seo-title-counter");
  if (titleCounter) {
    const titleLen = ((seoTitleInput?.value || "").trim() || (titleInput?.value || "").trim()).length;
    titleCounter.textContent = `(${titleLen} / ~60 caracteres)`;
  }
  const descCounter = document.getElementById("seo-desc-counter");
  if (descCounter) {
    const descLen = (descInput?.value || "").trim().length;
    descCounter.textContent = `(${descLen} / ~160 caracteres)`;
  }
}

document.addEventListener("input", (event) => {
  if (event.target.matches("#title, #seo_title, #slug, #seo_description")) {
    updateSERPPreview();
  }
});

document.addEventListener("DOMContentLoaded", () => {
  initializeMenuItemFields();
  updateSERPPreview();
  initializeCategoryEditor();
  initializeVisualBuilder();
  initializeSectionEditor();
});
document.body.addEventListener("htmx:afterSettle", () => {
  initializeMenuItemFields();
  updateSERPPreview();
  initializeCategoryEditor();
  initializeVisualBuilder();
  initializeSectionEditor();
});

function initializeCategoryEditor() {
  const root = document.querySelector("[data-category-editor]");
  if (!root || root.dataset.categoryReady === "true") {
    return;
  }
  root.dataset.categoryReady = "true";

  const newBtn = document.getElementById("new-cat-btn");
  const cancelBtn = document.getElementById("cancel-cat-btn");
  const formPanel = document.getElementById("new-cat-form");
  const tree = document.getElementById("cat-tree");

  newBtn?.addEventListener("click", () => {
    if (formPanel) {
      formPanel.hidden = false;
      formPanel.querySelector("#cat-name")?.focus();
    }
  });
  cancelBtn?.addEventListener("click", () => {
    if (formPanel) {
      formPanel.hidden = true;
    }
  });

  if (!tree) {
    return;
  }
  let dragging = null;
  tree.addEventListener("dragstart", (event) => {
    dragging = event.target.closest(".cat-item");
    if (dragging) {
      dragging.classList.add("dragging");
    }
  });
  tree.addEventListener("dragend", () => {
    if (dragging) {
      dragging.classList.remove("dragging");
    }
    dragging = null;
    const order = [...tree.querySelectorAll(".cat-item")].map((el) => el.dataset.id).filter(Boolean);
    const csrf = tree.dataset.csrf || "";
    if (order.length > 0 && csrf) {
      const params = new URLSearchParams();
      params.set("csrf_token", csrf);
      params.set("order", order.join(","));
      fetch("/admin/categories/reorder", {
        method: "POST",
        headers: { "Content-Type": "application/x-www-form-urlencoded" },
        body: params.toString(),
      });
    }
  });
  tree.addEventListener("dragover", (event) => {
    event.preventDefault();
    const target = event.target.closest(".cat-item");
    if (target && dragging && target !== dragging) {
      const rect = target.getBoundingClientRect();
      if (event.clientY < rect.top + rect.height / 2) {
        tree.insertBefore(dragging, target);
      } else {
        tree.insertBefore(dragging, target.nextSibling);
      }
    }
  });
}

const BUILDER_BLOCK_LABELS = {
  hero: "Hero Section",
  posts: "Lista de Posts",
  features: "Grade de Destaques",
  form: "Formulário",
  rich_text: "Texto / Markdown",
  cta: "Banner CTA",
};

function defaultBlock(type, index) {
  const id = `${type.replace("_", "-")}-${index}`;
  switch (type) {
    case "hero":
      return {
        id,
        type: "hero",
        eyebrow: "Destaque",
        heading: "Título principal da sua página",
        subheading: "Uma apresentação clara e objetiva para guiar quem visita o site.",
        primary_cta_label: "Saiba mais",
        primary_cta_url: "#conteudo",
        secondary_cta_label: "",
        secondary_cta_url: "",
        image_url: "",
        image_alt: "",
        align: "left",
        tone: "default",
      };
    case "posts":
      return {
        id,
        type: "posts",
        eyebrow: "Publicações",
        heading: "Artigos recentes",
        subheading: "Confira os conteúdos publicados recentemente.",
        count: 6,
        columns: 2,
        category_slug: "",
        show_excerpt: true,
        show_image: true,
        primary_cta_label: "",
        primary_cta_url: "",
        tone: "default",
      };
    case "features":
      return {
        id,
        type: "features",
        eyebrow: "Diferenciais",
        heading: "Por que escolher este projeto",
        subheading: "",
        columns: 3,
        align: "left",
        tone: "default",
        items: [
          { icon: "✦", title: "Primeiro destaque", description: "Descreva este benefício em poucas palavras.", link_label: "", link_url: "" },
          { icon: "⚡", title: "Segundo destaque", description: "Explique outro ponto forte de forma direta.", link_label: "", link_url: "" },
          { icon: "◎", title: "Terceiro destaque", description: "Complete a grade com mais um diferencial.", link_label: "", link_url: "" },
        ],
      };
    case "form":
      return {
        id,
        type: "form",
        form_id: `contato-${index}`,
        form_mode: "contact",
        eyebrow: "Contato",
        heading: "Envie uma mensagem",
        subheading: "Preencha os campos abaixo e retornaremos em breve.",
        submit_label: "Enviar mensagem",
        success_message: "Mensagem recebida com sucesso!",
        privacy_note: "",
        align: "left",
        tone: "surface",
      };
    case "rich_text":
      return {
        id,
        type: "rich_text",
        eyebrow: "",
        heading: "Seção editorial",
        body: "Escreva seu conteúdo em **Markdown** aqui.",
        align: "left",
        tone: "default",
      };
    case "cta":
      return {
        id,
        type: "cta",
        eyebrow: "Chamada",
        heading: "Pronto para dar o próximo passo?",
        body: "Use este bloco para destacar uma ação importante.",
        primary_cta_label: "Começar agora",
        primary_cta_url: "/",
        secondary_cta_label: "",
        secondary_cta_url: "",
        align: "left",
        tone: "dark",
      };
    default:
      return { id, type: "rich_text", heading: "Bloco", body: "" };
  }
}

function initializeVisualBuilder() {
  const form = document.querySelector("[data-visual-builder]");
  if (!form || form.dataset.builderReady === "true") {
    return;
  }
  form.dataset.builderReady = "true";

  const jsonField = document.getElementById("builder-blocks-json");
  const canvas = document.getElementById("builder-canvas");
  const inspector = document.getElementById("builder-inspector");
  const categoriesSource = document.getElementById("builder-categories-source");
  const mediaSource = document.getElementById("builder-media-source");
  if (!jsonField || !canvas || !inspector) {
    return;
  }

  let blocks = [];
  try {
    const parsed = JSON.parse(jsonField.value || "[]");
    if (Array.isArray(parsed)) {
      blocks = parsed;
    }
  } catch {
    blocks = [];
  }
  let selectedIndex = blocks.length > 0 ? 0 : -1;
  let draggedIndex = -1;

  function syncJSON() {
    jsonField.value = JSON.stringify(blocks);
  }

  function blockSummary(block) {
    switch (block.type) {
      case "hero":
        return block.heading || "Hero sem título";
      case "posts":
        return `${block.heading || "Lista de posts"} (${block.count || 6} posts, ${block.columns || 2} col.)`;
      case "features":
        return `${block.heading || "Destaques"} (${(block.items || []).length} cards)`;
      case "form":
        return `${block.heading || "Formulário"} (${block.form_mode === "newsletter" ? "Newsletter" : "Contato"})`;
      case "rich_text":
        return block.heading || (block.body || "").slice(0, 60) || "Bloco de texto";
      case "cta":
        return block.heading || "Banner CTA";
      default:
        return block.type;
    }
  }

  function renderCanvas() {
    syncJSON();
    canvas.replaceChildren();
    if (blocks.length === 0) {
      const empty = document.createElement("p");
      empty.className = "empty-state";
      empty.textContent = "Seu layout está vazio. Clique em um componente à esquerda para começar.";
      canvas.append(empty);
      return;
    }

    blocks.forEach((block, index) => {
      const card = document.createElement("article");
      card.className = "builder-canvas-card" + (index === selectedIndex ? " is-selected" : "");
      card.draggable = true;
      card.dataset.index = String(index);

      card.addEventListener("dragstart", () => {
        draggedIndex = index;
        card.classList.add("is-dragging");
      });
      card.addEventListener("dragend", () => {
        draggedIndex = -1;
        card.classList.remove("is-dragging");
      });
      card.addEventListener("dragover", (event) => {
        event.preventDefault();
      });
      card.addEventListener("drop", (event) => {
        event.preventDefault();
        if (draggedIndex < 0 || draggedIndex === index) {
          return;
        }
        const [moved] = blocks.splice(draggedIndex, 1);
        blocks.splice(index, 0, moved);
        selectedIndex = index;
        renderCanvas();
        renderInspector();
      });

      const info = document.createElement("div");
      info.className = "builder-canvas-info";
      info.addEventListener("click", () => {
        selectedIndex = index;
        renderCanvas();
        renderInspector();
      });

      const badge = document.createElement("span");
      badge.className = "status-badge";
      badge.textContent = BUILDER_BLOCK_LABELS[block.type] || block.type;

      const title = document.createElement("strong");
      title.textContent = blockSummary(block);

      const meta = document.createElement("span");
      meta.className = "muted";
      meta.textContent = `ID: #${block.form_id || block.id || `block-${index + 1}`} · Tom: ${block.tone || "default"}`;

      info.append(badge, title, meta);

      const controls = document.createElement("div");
      controls.className = "builder-canvas-controls";

      const upBtn = makeIconButton("↑", "Mover para cima", index === 0, () => {
        const [item] = blocks.splice(index, 1);
        blocks.splice(index - 1, 0, item);
        selectedIndex = index - 1;
        renderCanvas();
        renderInspector();
      });
      const downBtn = makeIconButton("↓", "Mover para baixo", index === blocks.length - 1, () => {
        const [item] = blocks.splice(index, 1);
        blocks.splice(index + 1, 0, item);
        selectedIndex = index + 1;
        renderCanvas();
        renderInspector();
      });
      const dupBtn = makeIconButton("Duplicar", "Duplicar bloco", false, () => {
        const clone = JSON.parse(JSON.stringify(block));
        clone.id = `${block.type.replace("_", "-")}-${blocks.length + 1}`;
        if (clone.form_id) {
          clone.form_id = `${clone.form_id}-${blocks.length + 1}`;
        }
        blocks.splice(index + 1, 0, clone);
        selectedIndex = index + 1;
        renderCanvas();
        renderInspector();
      });
      const delBtn = makeIconButton("Remover", "Remover bloco", false, () => {
        blocks.splice(index, 1);
        if (selectedIndex >= blocks.length) {
          selectedIndex = blocks.length - 1;
        }
        renderCanvas();
        renderInspector();
      }, "button button-danger");

      controls.append(upBtn, downBtn, dupBtn, delBtn);
      card.append(info, controls);
      canvas.append(card);
    });
  }

  function makeIconButton(label, ariaLabel, disabled, onClick, className = "button") {
    const btn = document.createElement("button");
    btn.type = "button";
    btn.className = className;
    btn.textContent = label;
    btn.setAttribute("aria-label", ariaLabel);
    btn.disabled = disabled;
    btn.addEventListener("click", (event) => {
      event.stopPropagation();
      onClick();
    });
    return btn;
  }

  function addField(container, labelText, value, onChange, options = {}) {
    const wrap = document.createElement("div");
    wrap.className = "inspector-field";
    const label = document.createElement("label");
    label.textContent = labelText;
    let input;
    if (options.type === "textarea") {
      input = document.createElement("textarea");
      input.rows = options.rows || 4;
      input.value = value || "";
    } else if (options.type === "select") {
      input = document.createElement("select");
      for (const opt of options.choices || []) {
        const optionEl = document.createElement("option");
        optionEl.value = String(opt.value);
        optionEl.textContent = opt.label;
        if (String(value ?? "") === String(opt.value)) {
          optionEl.selected = true;
        }
        input.append(optionEl);
      }
    } else if (options.type === "checkbox") {
      label.className = "checkbox-label";
      input = document.createElement("input");
      input.type = "checkbox";
      input.checked = Boolean(value);
      label.prepend(input);
      input.addEventListener("change", () => {
        onChange(input.checked);
        renderCanvas();
      });
      wrap.append(label);
      container.append(wrap);
      return;
    } else {
      input = document.createElement("input");
      input.type = options.type || "text";
      input.value = value ?? "";
      if (options.placeholder) {
        input.placeholder = options.placeholder;
      }
      if (options.min !== undefined) {
        input.min = String(options.min);
      }
      if (options.max !== undefined) {
        input.max = String(options.max);
      }
    }
    const eventName = options.type === "select" ? "change" : "input";
    input.addEventListener(eventName, () => {
      onChange(options.type === "number" ? parseInt(input.value, 10) || 1 : input.value);
      renderCanvas();
    });
    wrap.append(label, input);
    container.append(wrap);
  }

  function renderInspector() {
    inspector.replaceChildren();
    if (selectedIndex < 0 || selectedIndex >= blocks.length) {
      const hint = document.createElement("p");
      hint.className = "muted";
      hint.textContent = "Selecione um bloco no canvas para editar suas propriedades.";
      inspector.append(hint);
      return;
    }

    const block = blocks[selectedIndex];
    const heading = document.createElement("p");
    heading.className = "eyebrow";
    heading.textContent = BUILDER_BLOCK_LABELS[block.type] || block.type;
    inspector.append(heading);

    const toneChoices = [
      { value: "default", label: "Padrão" },
      { value: "surface", label: "Superfície destacada" },
      { value: "accent", label: "Cor de destaque suave" },
      { value: "dark", label: "Escuro" },
    ];
    const alignChoices = [
      { value: "left", label: "Alinhado à esquerda" },
      { value: "center", label: "Centralizado" },
    ];

    addField(inspector, "ID da âncora (ex.: destaque)", block.id, (v) => { block.id = v; });

    if (block.type === "hero") {
      addField(inspector, "Etiqueta (Eyebrow)", block.eyebrow, (v) => { block.eyebrow = v; });
      addField(inspector, "Título principal", block.heading, (v) => { block.heading = v; });
      addField(inspector, "Subtítulo", block.subheading, (v) => { block.subheading = v; }, { type: "textarea", rows: 3 });
      addField(inspector, "Botão principal — Texto", block.primary_cta_label, (v) => { block.primary_cta_label = v; });
      addField(inspector, "Botão principal — Link", block.primary_cta_url, (v) => { block.primary_cta_url = v; }, { placeholder: "/sobre ou #secao" });
      addField(inspector, "Botão secundário — Texto", block.secondary_cta_label, (v) => { block.secondary_cta_label = v; });
      addField(inspector, "Botão secundário — Link", block.secondary_cta_url, (v) => { block.secondary_cta_url = v; }, { placeholder: "/contato ou https://…" });
      if (mediaSource) {
        const mediaChoices = [...mediaSource.options].map((o) => ({ value: o.value, label: o.textContent }));
        addField(inspector, "Imagem da biblioteca", block.image_url, (v) => { block.image_url = v; renderInspector(); }, { type: "select", choices: mediaChoices });
      }
      addField(inspector, "URL da imagem (opcional)", block.image_url, (v) => { block.image_url = v; }, { placeholder: "/uploads/… ou https://…" });
      addField(inspector, "Texto alternativo da imagem", block.image_alt, (v) => { block.image_alt = v; });
      addField(inspector, "Alinhamento", block.align || "left", (v) => { block.align = v; }, { type: "select", choices: alignChoices });
      addField(inspector, "Estilo visual", block.tone || "default", (v) => { block.tone = v; }, { type: "select", choices: toneChoices });
    } else if (block.type === "posts") {
      addField(inspector, "Etiqueta (Eyebrow)", block.eyebrow, (v) => { block.eyebrow = v; });
      addField(inspector, "Título da seção", block.heading, (v) => { block.heading = v; });
      addField(inspector, "Subtítulo", block.subheading, (v) => { block.subheading = v; });
      addField(inspector, "Quantidade de posts (1 a 12)", block.count || 6, (v) => { block.count = v; }, { type: "number", min: 1, max: 12 });
      addField(inspector, "Colunas", block.columns || 2, (v) => { block.columns = parseInt(v, 10) || 2; }, {
        type: "select",
        choices: [
          { value: 1, label: "1 coluna (Lista)" },
          { value: 2, label: "2 colunas" },
          { value: 3, label: "3 colunas" },
        ],
      });
      if (categoriesSource) {
        const catChoices = [...categoriesSource.options].map((o) => ({ value: o.value, label: o.textContent }));
        addField(inspector, "Filtrar por categoria", block.category_slug || "", (v) => { block.category_slug = v; }, { type: "select", choices: catChoices });
      }
      addField(inspector, "Exibir imagem destacada", block.show_image, (v) => { block.show_image = v; }, { type: "checkbox" });
      addField(inspector, "Exibir resumo do post", block.show_excerpt, (v) => { block.show_excerpt = v; }, { type: "checkbox" });
      addField(inspector, "Link 'Ver todos' — Texto", block.primary_cta_label, (v) => { block.primary_cta_label = v; });
      addField(inspector, "Link 'Ver todos' — URL", block.primary_cta_url, (v) => { block.primary_cta_url = v; });
      addField(inspector, "Estilo visual", block.tone || "default", (v) => { block.tone = v; }, { type: "select", choices: toneChoices });
    } else if (block.type === "form") {
      addField(inspector, "ID do formulário", block.form_id, (v) => { block.form_id = v; }, { placeholder: "contato ou newsletter" });
      addField(inspector, "Tipo de formulário", block.form_mode || "contact", (v) => { block.form_mode = v; }, {
        type: "select",
        choices: [
          { value: "contact", label: "Contato (Nome, E-mail e Mensagem)" },
          { value: "newsletter", label: "Newsletter (Somente E-mail)" },
        ],
      });
      addField(inspector, "Etiqueta (Eyebrow)", block.eyebrow, (v) => { block.eyebrow = v; });
      addField(inspector, "Título", block.heading, (v) => { block.heading = v; });
      addField(inspector, "Descrição", block.subheading, (v) => { block.subheading = v; }, { type: "textarea", rows: 2 });
      addField(inspector, "Texto do botão", block.submit_label, (v) => { block.submit_label = v; });
      addField(inspector, "Mensagem de sucesso", block.success_message, (v) => { block.success_message = v; });
      addField(inspector, "Nota de privacidade (opcional)", block.privacy_note, (v) => { block.privacy_note = v; });
      addField(inspector, "Alinhamento", block.align || "left", (v) => { block.align = v; }, { type: "select", choices: alignChoices });
      addField(inspector, "Estilo visual", block.tone || "surface", (v) => { block.tone = v; }, { type: "select", choices: toneChoices });
    } else if (block.type === "features") {
      addField(inspector, "Etiqueta (Eyebrow)", block.eyebrow, (v) => { block.eyebrow = v; });
      addField(inspector, "Título da seção", block.heading, (v) => { block.heading = v; });
      addField(inspector, "Subtítulo", block.subheading, (v) => { block.subheading = v; });
      addField(inspector, "Colunas", block.columns || 3, (v) => { block.columns = parseInt(v, 10) || 3; }, {
        type: "select",
        choices: [
          { value: 2, label: "2 colunas" },
          { value: 3, label: "3 colunas" },
        ],
      });
      addField(inspector, "Alinhamento", block.align || "left", (v) => { block.align = v; }, { type: "select", choices: alignChoices });
      addField(inspector, "Estilo visual", block.tone || "default", (v) => { block.tone = v; }, { type: "select", choices: toneChoices });

      const itemsHeader = document.createElement("div");
      itemsHeader.className = "builder-canvas-heading";
      const itemsTitle = document.createElement("h3");
      itemsTitle.textContent = "Cards de destaque";
      const addItemBtn = document.createElement("button");
      addItemBtn.type = "button";
      addItemBtn.className = "button";
      addItemBtn.textContent = "+ Card";
      addItemBtn.addEventListener("click", () => {
        block.items = block.items || [];
        block.items.push({ icon: "✦", title: "Novo destaque", description: "Descrição curta.", link_label: "", link_url: "" });
        renderCanvas();
        renderInspector();
      });
      itemsHeader.append(itemsTitle, addItemBtn);
      inspector.append(itemsHeader);

      (block.items || []).forEach((item, itemIdx) => {
        const card = document.createElement("div");
        card.className = "inspector-subcard";
        addField(card, `Card ${itemIdx + 1} — Ícone`, item.icon, (v) => { item.icon = v; });
        addField(card, "Título", item.title, (v) => { item.title = v; });
        addField(card, "Descrição", item.description, (v) => { item.description = v; }, { type: "textarea", rows: 2 });
        addField(card, "Texto do link (opcional)", item.link_label, (v) => { item.link_label = v; });
        addField(card, "URL do link", item.link_url, (v) => { item.link_url = v; });
        const remBtn = document.createElement("button");
        remBtn.type = "button";
        remBtn.className = "button button-danger";
        remBtn.textContent = "Remover card";
        remBtn.addEventListener("click", () => {
          block.items.splice(itemIdx, 1);
          renderCanvas();
          renderInspector();
        });
        card.append(remBtn);
        inspector.append(card);
      });
    } else if (block.type === "rich_text") {
      addField(inspector, "Etiqueta (Eyebrow)", block.eyebrow, (v) => { block.eyebrow = v; });
      addField(inspector, "Título", block.heading, (v) => { block.heading = v; });
      addField(inspector, "Conteúdo (Markdown)", block.body, (v) => { block.body = v; }, { type: "textarea", rows: 8 });
      addField(inspector, "Alinhamento", block.align || "left", (v) => { block.align = v; }, { type: "select", choices: alignChoices });
      addField(inspector, "Estilo visual", block.tone || "default", (v) => { block.tone = v; }, { type: "select", choices: toneChoices });
    } else if (block.type === "cta") {
      addField(inspector, "Etiqueta (Eyebrow)", block.eyebrow, (v) => { block.eyebrow = v; });
      addField(inspector, "Título", block.heading, (v) => { block.heading = v; });
      addField(inspector, "Texto de apoio", block.body, (v) => { block.body = v; }, { type: "textarea", rows: 3 });
      addField(inspector, "Botão principal — Texto", block.primary_cta_label, (v) => { block.primary_cta_label = v; });
      addField(inspector, "Botão principal — Link", block.primary_cta_url, (v) => { block.primary_cta_url = v; });
      addField(inspector, "Botão secundário — Texto", block.secondary_cta_label, (v) => { block.secondary_cta_label = v; });
      addField(inspector, "Botão secundário — Link", block.secondary_cta_url, (v) => { block.secondary_cta_url = v; });
      addField(inspector, "Alinhamento", block.align || "left", (v) => { block.align = v; }, { type: "select", choices: alignChoices });
      addField(inspector, "Estilo visual", block.tone || "dark", (v) => { block.tone = v; }, { type: "select", choices: toneChoices });
    }
  }

  for (const btn of form.querySelectorAll("[data-builder-add]")) {
    btn.addEventListener("click", () => {
      const type = btn.dataset.builderAdd;
      blocks.push(defaultBlock(type, blocks.length + 1));
      selectedIndex = blocks.length - 1;
      renderCanvas();
      renderInspector();
    });
  }

  form.addEventListener("submit", () => {
    syncJSON();
  });

  renderCanvas();
  renderInspector();
}

function initializeSectionEditor() {
  const form = document.querySelector("[data-section-editor]");
  if (!form || form.dataset.sectionReady === "true") {
    return;
  }
  form.dataset.sectionReady = "true";

  const typeSelect = form.querySelector("#section-type");
  const scopeSelect = form.querySelector("#section-scope");
  const slugWrap = form.querySelector("[data-section-slug-wrap]");
  const layoutSelect = form.querySelector("#section-layout");
  const columnsField = document.getElementById("section-columns-json");
  const columnsEditor = document.getElementById("footer-columns-editor");
  const preview = document.getElementById("section-live-preview");

  let columns = [];
  try {
    const parsed = JSON.parse(columnsField?.value || "[]");
    if (Array.isArray(parsed)) {
      columns = parsed;
    }
  } catch {
    columns = [];
  }

  function syncColumns() {
    if (columnsField) {
      columnsField.value = JSON.stringify(columns);
    }
  }

  function syncTypeVisibility() {
    const isFooter = typeSelect?.value === "footer";
    for (const el of form.querySelectorAll("[data-header-only]")) {
      el.hidden = isFooter;
    }
    for (const el of form.querySelectorAll("[data-footer-only]")) {
      el.hidden = !isFooter;
    }
    if (layoutSelect) {
      const current = layoutSelect.value;
      const choices = isFooter
        ? [
            { value: "columns", label: "Colunas de links" },
            { value: "simple", label: "Simples horizontal" },
            { value: "centered", label: "Centralizado" },
          ]
        : [
            { value: "split", label: "Dividido (Marca + Menu + CTA)" },
            { value: "centered", label: "Centralizado" },
            { value: "minimal", label: "Minimalista" },
          ];
      layoutSelect.replaceChildren();
      for (const c of choices) {
        const opt = document.createElement("option");
        opt.value = c.value;
        opt.textContent = c.label;
        if (c.value === current) {
          opt.selected = true;
        }
        layoutSelect.append(opt);
      }
    }
    updatePreview();
  }

  function syncScopeVisibility() {
    if (slugWrap && scopeSelect) {
      slugWrap.hidden = scopeSelect.value !== "slug";
    }
  }

  function renderColumnsEditor() {
    syncColumns();
    if (!columnsEditor) {
      return;
    }
    columnsEditor.replaceChildren();
    if (columns.length === 0) {
      const hint = document.createElement("p");
      hint.className = "muted";
      hint.textContent = "Nenhuma coluna de links adicional configurada.";
      columnsEditor.append(hint);
      return;
    }
    columns.forEach((col, colIdx) => {
      const box = document.createElement("div");
      box.className = "inspector-subcard";

      const titleLabel = document.createElement("label");
      titleLabel.textContent = `Coluna ${colIdx + 1} — Título`;
      const titleInput = document.createElement("input");
      titleInput.type = "text";
      titleInput.value = col.title || "";
      titleInput.placeholder = "Ex.: Institucional";
      titleInput.addEventListener("input", () => {
        col.title = titleInput.value;
        syncColumns();
        updatePreview();
      });
      box.append(titleLabel, titleInput);

      col.links = col.links || [];
      col.links.forEach((link, linkIdx) => {
        const row = document.createElement("div");
        row.className = "footer-link-row";

        const lInput = document.createElement("input");
        lInput.type = "text";
        lInput.placeholder = "Texto do link";
        lInput.value = link.label || "";
        lInput.addEventListener("input", () => {
          link.label = lInput.value;
          syncColumns();
          updatePreview();
        });

        const uInput = document.createElement("input");
        uInput.type = "text";
        uInput.placeholder = "/pagina ou https://…";
        uInput.value = link.url || "";
        uInput.addEventListener("input", () => {
          link.url = uInput.value;
          syncColumns();
          updatePreview();
        });

        const delLink = document.createElement("button");
        delLink.type = "button";
        delLink.className = "button button-danger";
        delLink.textContent = "×";
        delLink.setAttribute("aria-label", "Remover link");
        delLink.addEventListener("click", () => {
          col.links.splice(linkIdx, 1);
          renderColumnsEditor();
          updatePreview();
        });

        row.append(lInput, uInput, delLink);
        box.append(row);
      });

      const actions = document.createElement("div");
      actions.className = "builder-card-actions";
      const addLinkBtn = document.createElement("button");
      addLinkBtn.type = "button";
      addLinkBtn.className = "button";
      addLinkBtn.textContent = "+ Link";
      addLinkBtn.addEventListener("click", () => {
        col.links.push({ label: "", url: "/" });
        renderColumnsEditor();
      });

      const delColBtn = document.createElement("button");
      delColBtn.type = "button";
      delColBtn.className = "button button-danger";
      delColBtn.textContent = "Remover coluna";
      delColBtn.addEventListener("click", () => {
        columns.splice(colIdx, 1);
        renderColumnsEditor();
        updatePreview();
      });

      actions.append(addLinkBtn, delColBtn);
      box.append(actions);
      columnsEditor.append(box);
    });
  }

  function updatePreview() {
    if (!preview) {
      return;
    }
    const isFooter = typeSelect?.value === "footer";
    const tone = form.querySelector("#section-tone")?.value || "default";
    const layout = layoutSelect?.value || (isFooter ? "columns" : "split");
    const siteTitle = preview.dataset.siteTitle || "Meu Site";
    const siteTagline = preview.dataset.siteTagline || "";
    const showTitle = form.querySelector("[name=show_site_title]")?.checked;
    const showMenu = form.querySelector("[name=show_menu]")?.checked;
    const showTagline = form.querySelector("[name=show_tagline]")?.checked;
    const announcement = (form.querySelector("#section-announcement-text")?.value || "").trim();
    const ctaLabel = (form.querySelector("#section-cta-label")?.value || "").trim();
    const customText = (form.querySelector("#section-custom-text")?.value || "").trim();
    const copyrightText = (form.querySelector("#section-copyright")?.value || "").trim();

    preview.replaceChildren();
    const box = document.createElement("div");
    box.className = `section-preview-box section-preview-tone-${tone} section-preview-layout-${layout}`;

    if (!isFooter && announcement) {
      const bar = document.createElement("div");
      bar.className = "section-preview-announcement";
      bar.textContent = announcement;
      box.append(bar);
    }

    const inner = document.createElement("div");
    inner.className = "section-preview-inner";

    const brand = document.createElement("strong");
    brand.textContent = showTitle ? siteTitle : "[Logo]";
    inner.append(brand);

    if (showMenu) {
      const nav = document.createElement("span");
      nav.className = "section-preview-nav";
      nav.textContent = "Início  |  Sobre  |  Blog";
      inner.append(nav);
    }

    if (ctaLabel) {
      const cta = document.createElement("span");
      cta.className = "section-preview-cta";
      cta.textContent = ctaLabel;
      inner.append(cta);
    }
    box.append(inner);

    if (isFooter) {
      const desc = customText || (showTagline ? siteTagline : "");
      if (desc) {
        const p = document.createElement("p");
        p.className = "section-preview-copy";
        p.textContent = desc;
        box.append(p);
      }
      if (columns.length > 0) {
        const colsRow = document.createElement("div");
        colsRow.className = "section-preview-cols";
        for (const col of columns) {
          const colEl = document.createElement("div");
          const h = document.createElement("strong");
          h.textContent = col.title || "Links";
          colEl.append(h);
          const count = document.createElement("span");
          count.textContent = `${(col.links || []).length} link(s)`;
          colEl.append(count);
          colsRow.append(colEl);
        }
        box.append(colsRow);
      }
      if (copyrightText) {
        const legal = document.createElement("p");
        legal.className = "section-preview-legal";
        legal.textContent = copyrightText;
        box.append(legal);
      }
    }

    preview.append(box);
  }

  form.querySelector("[data-add-footer-col]")?.addEventListener("click", () => {
    if (columns.length >= 4) {
      return;
    }
    columns.push({ title: "Nova coluna", links: [{ label: "Início", url: "/" }] });
    renderColumnsEditor();
    updatePreview();
  });

  typeSelect?.addEventListener("change", syncTypeVisibility);
  scopeSelect?.addEventListener("change", syncScopeVisibility);
  form.addEventListener("input", updatePreview);
  form.addEventListener("change", updatePreview);
  form.addEventListener("submit", syncColumns);

  syncScopeVisibility();
  renderColumnsEditor();
  updatePreview();
}
