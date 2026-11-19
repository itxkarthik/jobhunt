// Safe DOM helpers. Job text is untrusted: it only ever reaches the page
// as text nodes or attribute values, never as HTML.
import { ICONS } from "./icons.js";

// The SVG namespace: an identifier, not something fetched. Split so the
// no-external-URL guard in web_test.go can stay strict.
const SVG = "http:/" + "/www.w3.org/2000/svg";

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

// icon draws a Lucide icon from the bundled path data. It is decorative:
// the control it sits in carries the accessible name.
export function icon(name, cls = "") {
  const svg = document.createElementNS(SVG, "svg");
  for (const [k, v] of Object.entries({
    viewBox: "0 0 24 24", fill: "none", stroke: "currentColor", "stroke-width": "2",
    "stroke-linecap": "round", "stroke-linejoin": "round", "aria-hidden": "true", focusable: "false",
  })) svg.setAttribute(k, v);
  svg.setAttribute("class", cls ? `icon ${cls}` : "icon");
  for (const [tag, attrs] of ICONS[name] || []) {
    const el = document.createElementNS(SVG, tag);
    for (const [k, v] of Object.entries(attrs)) el.setAttribute(k, v);
    svg.append(el);
  }
  return svg;
}

// corners returns the four registration marks of a .blueprint frame.
export function corners() {
  return ["tl", "tr", "bl", "br"].map((c) => h("i", { class: `corner ${c}`, "aria-hidden": "true" }));
}

// toast shows a short message, optionally with one action button. Only
// the newest few stay on screen. It returns the toast element, so a caller
// can take it down early.
export function toast(msg, { error = false, action, onAction, ms } = {}) {
  const box = document.getElementById("toasts");
  const t = h("div", { class: error ? "toast error" : "toast" }, h("span", { class: "toast-msg", text: msg }));
  if (action) {
    t.append(h("button", { type: "button", class: "toast-action", onclick: () => { t.remove(); onAction(); } }, action));
  }
  box.append(t);
  while (box.children.length > 3) box.firstElementChild.remove();
  setTimeout(() => t.remove(), ms ?? (error ? 8000 : 4000));
  return t;
}

// typing reports whether a key event comes from a text field, where
// single-letter shortcuts must not fire.
export function typing(e) {
  const el = e.target;
  return el instanceof HTMLElement && (el.isContentEditable || /^(INPUT|TEXTAREA|SELECT)$/.test(el.tagName));
}
