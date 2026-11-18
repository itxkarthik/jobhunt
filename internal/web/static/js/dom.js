// Safe DOM helpers. Job text is untrusted: it only ever reaches the page
// as text nodes or attribute values, never as HTML.

// h creates an element. props may hold class, text, dataset, on<event>
// handlers and plain attributes; children are nodes or strings (which
// become text nodes).
export function h(tag, props = {}, ...children) {
  const el = document.createElement(tag);
  for (const [k, v] of Object.entries(props)) {
    if (v == null || v === false) continue;
    if (k === "class") el.className = v;
    else if (k === "text") el.textContent = v;
    else if (k === "dataset") Object.assign(el.dataset, v);
    else if (k.startsWith("on")) el.addEventListener(k.slice(2), v);
    else if (k === "href") el.setAttribute("href", internalHref(v));
    else if (k === "src" || k === "style" || k === "srcdoc") throw new Error(`h: ${k} is not allowed`);
    else el.setAttribute(k, v === true ? "" : String(v));
  }
  append(el, children);
  return el;
}

function append(el, children) {
  for (const c of children.flat(Infinity)) {
    if (c == null || c === false) continue;
    el.append(c instanceof Node ? c : String(c));
  }
}

// internalHref allows only in-app hash routes; external links go
// through extLink.
function internalHref(v) {
  if (typeof v === "string" && v.startsWith("#")) return v;
  throw new Error("h: use extLink for external URLs");
}

// safeURL returns u if it is an absolute http(s) URL, otherwise null.
export function safeURL(u) {
  try {
    const url = new URL(u);
    return url.protocol === "http:" || url.protocol === "https:" ? url.href : null;
  } catch {
    return null;
  }
}

// extLink links to an untrusted URL in a new tab, or renders plain text
// when the URL isn't http(s).
export function extLink(url, ...children) {
  const href = safeURL(url);
  if (!href) return h("span", {}, ...children);
  const a = h("a", { target: "_blank", rel: "noopener noreferrer" }, ...children);
  a.href = href;
  return a;
}

// openURL opens an untrusted URL in a new tab without giving it a handle
// on this page.
export function openURL(url) {
  const href = safeURL(url);
  if (href) window.open(href, "_blank", "noopener,noreferrer");
}

// replace swaps el's children.
export function replace(el, ...children) {
  el.replaceChildren();
  append(el, children);
}

// toast shows a short message, optionally with one action button.
export function toast(msg, { error = false, action, onAction } = {}) {
  const box = document.getElementById("toasts");
  const t = h("div", { class: error ? "toast error" : "toast" }, h("span", { text: msg }));
  if (action) {
    t.append(h("button", { type: "button", class: "ghost", onclick: () => { t.remove(); onAction(); } }, action));
  }
  box.append(t);
  setTimeout(() => t.remove(), error ? 8000 : 5000);
}

// typing reports whether a key event comes from a text field, where
// single-letter shortcuts must not fire.
export function typing(e) {
  const el = e.target;
  return el instanceof HTMLElement && (el.isContentEditable || /^(INPUT|TEXTAREA|SELECT)$/.test(el.tagName));
}
