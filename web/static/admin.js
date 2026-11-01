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
