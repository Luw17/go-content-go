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
  const openButton = event.target.closest("[data-picker-open]");
  if (openButton) {
    const dialog = document.getElementById("media-picker-dialog");
    if (dialog && !dialog.open) {
      dialog.showModal();
    }
    return;
  }

  if (event.target.closest("[data-picker-close]")) {
    document.getElementById("media-picker-dialog")?.close();
    return;
  }

  const selected = event.target.closest("[data-media-select]");
  if (!selected) {
    return;
  }

  const path = selected.dataset.path;
  const alt = selected.dataset.alt || "";
  const mode = selected.dataset.mode;
  if (!path || (mode !== "featured" && mode !== "inline")) {
    return;
  }

  if (mode === "featured") {
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
});

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

document.addEventListener("DOMContentLoaded", initializeMenuItemFields);
document.body.addEventListener("htmx:afterSettle", initializeMenuItemFields);
