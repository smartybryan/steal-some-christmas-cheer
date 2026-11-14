// Shared helpers for every page.
(function () {
  const base = ((window.DUCK_CONFIG && window.DUCK_CONFIG.apiBase) || "").replace(/\/$/, "");

  async function api(path, options = {}) {
    const res = await fetch(base + path, options);
    let body = null;
    try { body = await res.json(); } catch { /* not JSON */ }
    if (!res.ok) {
      const err = new Error((body && body.error) || `Request failed (${res.status})`);
      err.status = res.status;
      throw err;
    }
    return body;
  }

  function el(tag, attrs = {}, ...children) {
    const node = document.createElement(tag);
    for (const [k, v] of Object.entries(attrs)) {
      if (v == null || v === false) continue;
      if (k === "class") node.className = v;
      else if (k.startsWith("on")) node.addEventListener(k.slice(2), v);
      else node.setAttribute(k, v === true ? "" : v);
    }
    for (const child of children.flat()) {
      if (child == null || child === false) continue;
      node.append(child instanceof Node ? child : document.createTextNode(String(child)));
    }
    return node;
  }

  function formatDate(iso) {
    return new Date(iso).toLocaleDateString(undefined, { month: "short", day: "numeric", year: "numeric" });
  }

  function storage(kind) {
    return {
      get(key) { try { return window[kind].getItem(key); } catch { return null; } },
      set(key, value) { try { window[kind].setItem(key, value); } catch { /* ignore */ } },
      remove(key) { try { window[kind].removeItem(key); } catch { /* ignore */ } },
    };
  }

  function snow(count = 36) {
    if (window.matchMedia("(prefers-reduced-motion: reduce)").matches) return;
    const layer = el("div", { class: "snow", "aria-hidden": "true" });
    for (let i = 0; i < count; i++) {
      const flake = el("span", {}, Math.random() < 0.5 ? "❄" : "•");
      flake.style.left = Math.random() * 100 + "vw";
      flake.style.fontSize = 8 + Math.random() * 14 + "px";
      flake.style.animationDuration = 8 + Math.random() * 12 + "s";
      flake.style.animationDelay = -Math.random() * 20 + "s";
      flake.style.opacity = 0.4 + Math.random() * 0.6;
      layer.append(flake);
    }
    document.body.prepend(layer);
  }

  window.Duck = { api, el, formatDate, local: storage("localStorage"), apiBase: base, snow };
  document.addEventListener("DOMContentLoaded", () => snow());
})();
