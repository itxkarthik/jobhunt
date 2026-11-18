// The app shell around each view: the app bar's tab counts and slot, the
// Fetch button, "synced" time and the status bar. Views talk to it
// instead of reaching into index.html.
import { h, replace } from "./dom.js";
import { ago } from "./format.js";

const $ = (id) => document.getElementById(id);

let fetchHandler = null;
let syncedAt = 0;

// setFetch makes the app bar's Fetch button (and r) run fn; null hides it.
export function setFetch(fn, label = "Fetch") {
  fetchHandler = fn;
  const b = $("fetch");
  b.hidden = !fn;
  b.setAttribute("aria-label", label);
  b.querySelector(".btn-label").textContent = label;
}

export function runFetch() {
  if (fetchHandler) fetchHandler();
}

export function setBusy(busy) {
  $("fetch").classList.toggle("busy", busy);
}

// setStatus fills the status bar: a mode block, key hints and context.
export function setStatus(mode, hints, context = "") {
  $("mode").textContent = mode;
  replace($("hints"), hints.map(([k, v]) => h("span", {}, h("b", {}, k), ` ${v}`)));
  $("context").textContent = context;
}

export function setContext(text) {
  $("context").textContent = text;
}

// setCount sets the small number next to a tab.
export function setCount(route, text) {
  const el = document.querySelector(`nav.tabs a[data-route="${route}"] .count`);
  if (el) el.textContent = text ?? "";
}

// slot is the app bar area between the tabs and the buttons.
export function slot() {
  return $("appbar-slot");
}

export function setSub(text) {
  $("appbar-sub").textContent = text;
}

export function synced(at) {
  syncedAt = Math.max(syncedAt, at || 0);
  renderSynced();
}

function renderSynced() {
  const el = $("synced");
  el.hidden = !syncedAt;
  replace(el, h("i", { class: "dot", "aria-hidden": "true" }), `synced ${ago(syncedAt)}`);
}
setInterval(renderSynced, 30_000);
