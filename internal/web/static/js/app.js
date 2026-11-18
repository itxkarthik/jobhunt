// Entry point: token handling, hash routing and the theme switch.
import { takeToken, token, setToken, whenUnauthorized } from "./api.js";
import { h, replace } from "./dom.js";

// Views register here; each is { mount(main) -> optional cleanup }.
const views = {
  search: () => import("./search.js"),
  tracker: () => import("./tracker.js"),
  sources: () => import("./sources.js"),
};

const main = document.getElementById("main");
let cleanup = null;
let generation = 0;

function routeName() {
  const m = /^#\/(\w+)/.exec(location.hash);
  return m && views[m[1]] ? m[1] : "search";
}

async function render() {
  const gen = ++generation;
  if (cleanup) { cleanup(); cleanup = null; }
  const name = routeName();
  for (const a of document.querySelectorAll("nav a[data-route]")) {
    if (a.dataset.route === name) a.setAttribute("aria-current", "page");
    else a.removeAttribute("aria-current");
  }
  if (!token()) {
    showLogin();
    return;
  }
  document.title = `jobhunt · ${name}`;
  replace(main, h("p", { class: "muted pad" }, "Loading…"));
  try {
    const mod = await views[name]();
    if (gen !== generation) return; // the route changed while loading
    main.replaceChildren();
    cleanup = (await mod.mount(main)) || null;
  } catch (err) {
    if (gen !== generation) return;
    replace(main, h("p", { class: "notice error" }, `Could not load this view: ${err.message}`));
  }
}

function showLogin(reason = "") {
  const input = h("input", { type: "password", id: "token", autocomplete: "off", required: true, minlength: 16 });
  const form = h("form", { class: "login card" },
    h("h1", {}, "Connect to jobhunt"),
    reason && h("p", { class: "notice error" }, reason),
    h("p", {}, "Open the web UI link printed by ", h("code", {}, "jobhunt serve"),
      " (it carries the token), or paste the API token here."),
    h("label", { for: "token" }, "API token"),
    input,
    h("button", { type: "submit" }, "Connect"));
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

// Theme: follows the system unless the user picks light or dark.
const themes = ["auto", "light", "dark"];
function applyTheme(t) {
  if (t === "auto") delete document.documentElement.dataset.theme;
  else document.documentElement.dataset.theme = t;
  document.getElementById("theme").textContent = `Theme: ${t}`;
}
function initTheme() {
  let t = "auto";
  try { t = localStorage.getItem("jobhunt.theme") || "auto"; } catch { /* ignore */ }
  applyTheme(themes.includes(t) ? t : "auto");
  document.getElementById("theme").addEventListener("click", () => {
    const cur = document.documentElement.dataset.theme || "auto";
    const next = themes[(themes.indexOf(cur) + 1) % themes.length];
    try { localStorage.setItem("jobhunt.theme", next); } catch { /* ignore */ }
    applyTheme(next);
  });
}

takeToken();
initTheme();
window.addEventListener("hashchange", render);
render();
