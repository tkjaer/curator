// Lightbox: progressive enhancement over links marked data-lightbox. The active
// image is reflected in the URL hash (#photo-<name>) so it is shareable and
// deep-linkable. Without JavaScript the links simply open the larger image.
(function () {
  const dialog = document.getElementById("lightbox");
  if (!dialog || typeof dialog.showModal !== "function") return;

  const img = dialog.querySelector(".lb-img");
  const imageButton = dialog.querySelector(".lb-image-button");
  const caption = dialog.querySelector(".lb-caption");
  const exif = dialog.querySelector(".lb-exif");
  if (!img || !imageButton) return;
  const links = [];
  const indexByID = new Map();
  function registerLinks(root) {
    root.querySelectorAll("a[data-lightbox]").forEach((link) => {
      if (link.dataset.lightboxBound) return;
      link.dataset.lightboxBound = "true";
      const i = links.length;
      links.push(link);
      if (link.dataset.id) indexByID.set(link.dataset.id, i);
      link.addEventListener("click", (e) => {
        e.preventDefault();
        openAt(i);
      });
    });
  }
  registerLinks(document);
  if (links.length === 0) return;

  let index = -1;
  let zoomed = false;

  function resetZoom() {
    zoomed = false;
    dialog.classList.remove("is-zoomed");
    imageButton.setAttribute("aria-label", "View image at actual size");
    dialog.scrollLeft = 0;
    dialog.scrollTop = 0;
  }

  function toggleZoom(e) {
    if (e.pointerType && e.pointerType !== "mouse" && e.pointerType !== "pen") return;
    if (!window.matchMedia("(pointer: fine)").matches) return;
    if (zoomed) {
      resetZoom();
      return;
    }

    const rect = img.getBoundingClientRect();
    const x = e.clientX ? (e.clientX - rect.left) / rect.width : 0.5;
    const y = e.clientY ? (e.clientY - rect.top) / rect.height : 0.5;
    zoomed = true;
    dialog.classList.add("is-zoomed");
    imageButton.setAttribute("aria-label", "Fit image to window");

    const center = () => requestAnimationFrame(() => {
      dialog.scrollLeft = Math.max(0, imageButton.offsetLeft + x * img.offsetWidth - dialog.clientWidth / 2);
      dialog.scrollTop = Math.max(0, imageButton.offsetTop + y * img.offsetHeight - dialog.clientHeight / 2);
    });
    const zoomSrc = links[index].dataset.zoomSrc;
    if (zoomSrc && img.getAttribute("src") !== zoomSrc) {
      img.addEventListener("load", center, { once: true });
      img.src = zoomSrc;
    } else {
      center();
    }
  }

  function setHash(hash) {
    if (location.hash === hash) return;
    try {
      history.replaceState(null, "", hash);
    } catch (e) {
      location.hash = hash;
    }
  }

  function clearHash() {
    if (!location.hash) return;
    try {
      history.replaceState(null, "", location.pathname + location.search);
    } catch (e) { /* file:// or restricted context */ }
    // Fall back (or double-check) by clearing the fragment directly.
    if (location.hash) location.hash = "";
  }

  function render(i) {
    resetZoom();
    index = (i + links.length) % links.length;
    const link = links[index];
    img.src = link.getAttribute("href");
    img.alt = link.dataset.title || link.dataset.caption || link.dataset.description || "";
    caption.textContent = [...new Set([link.dataset.title, link.dataset.description, link.dataset.caption].filter(Boolean))].join(" · ");
    if (exif) exif.textContent = link.dataset.exif || "";
  }

  function openAt(i) {
    render(i);
    if (!dialog.open) dialog.showModal();
    const id = links[index].dataset.id;
    if (id) setHash("#photo-" + id);
  }

  // Clear the hash whenever the lightbox closes (backdrop, Esc, or button).
  // Registered first so a later missing button can't stop it from running.
  dialog.addEventListener("close", clearHash);
  dialog.addEventListener("close", resetZoom);
  dialog.addEventListener("cancel", clearHash);

  document.addEventListener("curator:content-added", (e) => registerLinks(e.detail || document));

  bindClick(".lb-next", () => openAt(index + 1));
  bindClick(".lb-prev", () => openAt(index - 1));
  bindClick(".lb-close", () => dialog.close());
  imageButton.addEventListener("click", toggleZoom);

  // Close when the dark backdrop (the dialog itself, not the image or buttons)
  // is clicked.
  dialog.addEventListener("click", (e) => {
    if (e.target === dialog && !zoomed) dialog.close();
  });

  function bindClick(selector, fn) {
    const el = dialog.querySelector(selector);
    if (el) el.addEventListener("click", fn);
  }

  document.addEventListener("keydown", (e) => {
    if (!dialog.open) return;
    if (e.key === "ArrowRight") openAt(index + 1);
    else if (e.key === "ArrowLeft") openAt(index - 1);
  });

  let startX = null;
  dialog.addEventListener("touchstart", (e) => {
    startX = e.touches.length === 1 ? e.touches[0].clientX : null;
  }, { passive: true });
  dialog.addEventListener("touchend", (e) => {
    if (startX === null) return;
    const dx = e.changedTouches[0].clientX - startX;
    if (Math.abs(dx) > 40) openAt(index + (dx < 0 ? 1 : -1));
    startX = null;
  });

  // Open the matching image from the URL hash (a shared link or manual edit).
  function openFromHash() {
    const match = location.hash.match(/^#photo-(.+)$/);
    if (match && indexByID.has(match[1])) {
      openAt(indexByID.get(match[1]));
    } else if (!match && dialog.open) {
      dialog.close();
    }
  }
  window.addEventListener("hashchange", openFromHash);
  openFromHash();
})();

// Facet pages are static and fully navigable without JavaScript. Enhance their
// Load more link by appending the next page's justified rows in place.
(function () {
  const next = document.querySelector("a[data-load-more]");
  const target = document.querySelector("[data-facet-page] .grid");
  if (!next || !target) return;

  next.addEventListener("click", async (e) => {
    e.preventDefault();
    if (next.dataset.loading) return;
    next.dataset.loading = "true";
    next.textContent = "Loading...";
    try {
      const response = await fetch(next.href);
      if (!response.ok) throw new Error("page load failed");
      const page = new DOMParser().parseFromString(await response.text(), "text/html");
      const source = page.querySelector("[data-facet-page] .grid");
      if (!source) throw new Error("page content missing");
      const added = document.createDocumentFragment();
      Array.from(source.children).forEach((row) => added.appendChild(document.importNode(row, true)));
      target.appendChild(added);
      document.dispatchEvent(new CustomEvent("curator:content-added", { detail: target }));
      const following = page.querySelector("a[data-load-more]");
      const status = document.querySelector(".facet-page-status");
      const followingStatus = page.querySelector(".facet-page-status");
      if (status && followingStatus) status.textContent = followingStatus.textContent;
      if (following) {
        next.href = following.href;
        next.textContent = "Load more";
        delete next.dataset.loading;
      } else {
        next.remove();
      }
    } catch (error) {
      next.textContent = "Try again";
      delete next.dataset.loading;
    }
  });
})();
