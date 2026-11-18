// Entry point: token handling, hash routing, the tab bar, the theme and
// the keys that work on every tab (1 2 3, t, r).
import { api, takeToken, token, setToken, whenUnauthorized } from "./api.js";
import { h, icon, corners, replace, typing } from "./dom.js";
import * as shell from "./shell.js";
import * as runlog from "./runlog.js";

// Views register here; each is { mount(view, { live }) -> optional
// cleanup }. live() turns false once the user has moved on, so a mount
// still waiting on the network must not touch the shared shell.
const views = {
  search: { load: () => import("./search.js"), label: "Search", icon: "search", sub: "" },
  tracker: { load: () => import("./tracker.js"), label: "Tracker", icon: "kanban", sub: "pipeline" },
  sources: { load: () => import("./sources.js"), label: "Sources", icon: "database", sub: "sources" },
};

const main = document.getElementById("main");
let cleanup = null;
let generation = 0;

function routeName() {
  const m = /^#\/(\w+)/.exec(location.hash);
  return m && views[m[1]] ? m[1] : "search";
}

function go(name) {
  if (routeName() !== name || !location.hash.startsWith("#/")) location.hash = `#/${name}`;
}

// The tab links are filled here so the icons come from the same place.
function initTabs() {
  for (const a of document.querySelectorAll("nav.tabs a[data-route]")) {
    const v = views[a.dataset.route];
    replace(a,
      h("span", { class: "key", "aria-hidden": "true" }, a.dataset.key),
      icon(v.icon),
      h("span", { class: "text" }, v.label, h("span", { class: "count" })));
  }
}

async function render() {
  const gen = ++generation;
  if (cleanup) { cleanup(); cleanup = null; }
  document.body.classList.remove("m-detail");
  const name = routeName();
  for (const a of document.querySelectorAll("nav.tabs a[data-route]")) {
    if (a.dataset.route === name) a.setAttribute("aria-current", "page");
    else a.removeAttribute("aria-current");
  }
  replace(shell.slot());
  shell.setFetch(null);
  shell.setStatus("", []);
  if (!token()) {
    showLogin();
    return;
  }
  document.title = `jobhunt · ${views[name].label}`;
  shell.setSub(views[name].sub);
  refreshCounts();
  // Each mount gets its own container. If the route changes while it is
  // still loading, it fills a detached node, and its cleanup runs as soon
  // as it finishes instead of replacing the current view's.
  const view = h("div", { class: "view" }, h("p", { class: "loading" }, "Loading…"));
  main.replaceChildren(view);
  let done = null;
  try {
    const mod = await views[name].load();
    if (gen !== generation) return;
    view.replaceChildren();
    done = (await mod.mount(view, { live: () => gen === generation })) || null;
  } catch (err) {
    if (gen === generation) replace(view, h("p", { class: "notice error" }, `Could not load this view: ${err.message}`));
  }
  if (gen === generation) cleanup = done;
  else if (done) done();
}

// refreshCounts fills the Tracker and Sources tab counts; Search sets its
// own as results arrive.
let countsCtrl = null;
async function refreshCounts() {
  countsCtrl?.abort();
  const ctrl = new AbortController();
  countsCtrl = ctrl;
  try {
    const [rows, srcs] = await Promise.all([
      api("GET", "/api/tracker", undefined, ctrl.signal),
      api("GET", "/api/sources", undefined, ctrl.signal),
    ]);
    shell.setCount("tracker", String(rows.filter((r) => r.status !== "rejected").length));
    const on = srcs.filter((s) => s.enabled);
    const reporting = on.filter((s) => s.ready && s.last && (s.last.status === "ok" || s.last.status === "cached"));
    shell.setCount("sources", `${reporting.length}/${on.length}`);
    shell.synced(Math.max(0, ...srcs.map((s) => Date.parse(s.last?.run_at) || 0)));
  } catch { /* counts are decoration; the views report errors */ }
}
window.addEventListener("jobhunt:tracker", refreshCounts);
runlog.subscribe((last) => { if (!last.running && token()) refreshCounts(); });

function showLogin(reason = "") {
  shell.setSub("");
  const input = h("input", { type: "password", id: "token", class: "input", autocomplete: "off", required: true, minlength: 16 });
  const form = h("form", { class: "login blueprint" },
    corners(),
    h("h2", {}, "Connect to jobhunt"),
    reason && h("p", { class: "notice error" }, reason),
    h("p", { class: "muted" }, "Open the web UI link printed by ", h("code", {}, "jobhunt serve"),
      " (it carries the token), or paste the API token here."),
    h("div", { class: "field" }, h("label", { for: "token" }, "API token"), input),
    h("button", { type: "submit", class: "btn btn-primary" }, "Connect"));
  form.addEventListener("submit", (e) => {
    e.preventDefault();
    setToken(input.value);
    render();
  });
  replace(main, form);
  input.focus();
}

whenUnauthorized(() => {
  if (cleanup) { cleanup(); cleanup = null; }
  generation++;
  showLogin("The server didn't accept the token. jobhunt serve makes a new one each time it starts unless serve.token is set.");
});

// Theme: follows the system until the user picks one with the button or t.
const THEME = "jobhunt.v2.theme";
const systemDark = window.matchMedia("(prefers-color-scheme: dark)");
function isDark() {
  const t = document.documentElement.dataset.theme;
  return t ? t === "dark" : systemDark.matches;
}
function paintThemeButton() {
  const b = document.getElementById("theme");
  const label = isDark() ? "Light mode (t)" : "Dark mode (t)";
  b.title = label;
  b.setAttribute("aria-label", label);
  replace(b, icon(isDark() ? "sun" : "moon"));
}
function toggleTheme() {
  const next = isDark() ? "light" : "dark";
  document.documentElement.dataset.theme = next;
  try { localStorage.setItem(THEME, next); } catch { /* ignore */ }
  paintThemeButton();
}
function initTheme() {
  let t = "";
  try { t = localStorage.getItem(THEME) || ""; } catch { /* ignore */ }
  if (t === "light" || t === "dark") document.documentElement.dataset.theme = t;
  paintThemeButton();
  systemDark.addEventListener("change", paintThemeButton);
  document.getElementById("theme").addEventListener("click", toggleTheme);
}

function initFetch() {
  const b = document.getElementById("fetch");
  replace(b, icon("refresh-cw"), h("span", { class: "btn-label" }, "Fetch"));
  b.addEventListener("click", shell.runFetch);
}

// Keys for every tab. Views handle their own and skip these.
function onKey(e) {
  if (e.ctrlKey || e.metaKey || e.altKey || typing(e) || !token()) return;
  if (document.querySelector(".drawer-backdrop")) return;
  const tab = { 1: "search", 2: "tracker", 3: "sources" }[e.key];
  if (tab) { e.preventDefault(); go(tab); return; }
  if (e.key === "t") { e.preventDefault(); toggleTheme(); return; }
  if (e.key === "r") { e.preventDefault(); shell.runFetch(); }
}

takeToken();
initTabs();
initTheme();
initFetch();
document.addEventListener("keydown", onKey);
// A #token= link pasted into an open tab is a hash change, not a reload.
// In-page anchors such as the skip link aren't routes.
window.addEventListener("hashchange", () => {
  if (!/^#(\/|token=)/.test(location.hash) && location.hash !== "") return;
  takeToken();
  render();
});
render();
