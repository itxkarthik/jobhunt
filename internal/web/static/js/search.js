// Search view: a filter rail, live results from /api/events and a detail
// pane, in three resizable columns. On phones the rail is a bottom sheet
// and a job opens full screen.
import { api, meta, enc } from "./api.js";
import { h, icon, corners, replace, extLink, openURL, safeURL, toast, typing } from "./dom.js";
import { since, dateTime, location, place, salary, visa, statusLabel, count } from "./format.js";
import * as shell from "./shell.js";
import * as runlog from "./runlog.js";

const PAGE = 200; // cards rendered at a time
const AREAS_SHOWN = 8;
// Values as the API returns them (app.FormatAge prints whole days as "1d").
const AGES = [["1d", "24h"], ["3d", "3d"], ["7d", "7d"], ["", "Any"]];
const OPTIONS = [
  ["new_only", "New only", "Only jobs no earlier search has seen"],
  ["include_unknown", "Include unknown", "Keep jobs whose country, type or area couldn't be worked out"],
  ["strict_country", "Strict country", "Don't count worldwide-remote jobs as matching a country"],
  ["visa", "Visa sponsors", "Only jobs that say they sponsor visas"],
  ["show_hidden", "Show hidden", "Include jobs you hid"],
];
const FACTORS = {
  match: ["Match", "Your keywords and areas"],
  recency: ["Recency", "Newer postings score higher; points halve every 7 days"],
  level: ["Level", "Internship, new-grad or entry-level"],
  fit: ["Location", "In your country, or remote from it"],
  info: ["Detail", "Salary or visa sponsorship stated"],
};
const NAMES = { "remote-global": "Remote (global)" };
const TRACK_ORDER = ["saved", "applied", "oa", "interview", "offer", "rejected"];
const LAST = "jobhunt.v2.search";   // last filters, a per-browser convenience
const WIDTHS = "jobhunt.v2.widths"; // column widths
export const LINKS_OFF = "jobhunt.v2.links"; // sites hidden from "Also search"

const phone = window.matchMedia("(max-width: 759px)");
const narrow = window.matchMedia("(max-width: 1119px)");

// ---- State ------------------------------------------------------------------
// Kept at module level, so results survive a trip to another tab, and a
// search that is still running keeps filling them.

const S = {
  input: null,     // filters, named like the API's JSON
  where: "",       // location text, matched in the browser
  sort: "score",
  jobs: [],        // results in arrival order
  tracked: {},     // key -> { status, notes }
  links: [],       // "also search" sites
  selKey: null,
  shown: PAGE,
  running: false,
  run: null,       // the final run event
  reports: 0,
  expected: 0,
  moreAreas: false,
  started: false,
};
let runSeq = 0;
let ui = null;     // render hooks of the mounted view, if any

const clone = (v) => JSON.parse(JSON.stringify(v));
const words = (s) => String(s || "").split(",").map((w) => w.trim()).filter(Boolean);
const nameOf = (v) => NAMES[v] || v;
const ageName = (v) => AGES.find(([a]) => a === v)?.[1] || v;

function loadLast() {
  try { return JSON.parse(localStorage.getItem(LAST) || "null"); } catch { return null; }
}
function saveLast() {
  try { localStorage.setItem(LAST, JSON.stringify({ input: S.input, sort: S.sort })); } catch { /* ignore */ }
}
export function hiddenSites() {
  try { return new Set(JSON.parse(localStorage.getItem(LINKS_OFF) || "[]")); } catch { return new Set(); }
}

// The filters the server fills from config when they are empty. A group
// the user cleared is sent as "any", so the server doesn't put the
// default back.
const DEFAULTED = ["countries", "modes", "types", "areas"];
function explicit(input) {
  const out = { ...input };
  for (const k of DEFAULTED) if (!out[k]?.length) out[k] = ["any"];
  return out;
}

// fromAPI turns the server's ["any"] (in presets and meta defaults) back
// into an empty group: no chips on.
function fromAPI(input) {
  const out = { ...input };
  for (const k of DEFAULTED) if (out[k]?.length === 1 && out[k][0] === "any") out[k] = [];
  return out;
}

// query builds /api/events and /api/links parameters, named like the JSON.
export function query(input, extra = {}) {
  const q = new URLSearchParams();
  for (const [k, v] of Object.entries({ ...explicit(input), ...extra })) {
    if (Array.isArray(v)) { if (v.length) q.set(k, v.join(",")); }
    else if (typeof v === "boolean") { if (v) q.set(k, "true"); }
    else if (v) q.set(k, String(v));
  }
  return q.toString();
}

export async function currentInput() {
  if (!S.input) {
    const m = await meta();
    const last = loadLast();
    S.input = fromAPI({ ...clone(m.defaults), new_only: false, show_hidden: false, ...(last?.input || {}) });
    S.sort = last?.sort === "date" ? "date" : "score";
  }
  return S.input;
}

function time(j) {
  return Date.parse(j.posted_at || j.first_seen_at || 0) || 0;
}

// visible is the results after the "where" text, in display order.
function visible() {
  const w = S.where.trim().toLowerCase();
  const out = w
    ? S.jobs.filter((j) => `${location(j)} ${j.city || ""} ${(j.countries || []).join(" ")}`.toLowerCase().includes(w))
    : S.jobs.slice();
  if (S.sort === "date") out.sort((a, b) => time(b) - time(a) || (b.score || 0) - (a.score || 0));
  else out.sort((a, b) => (b.score || 0) - (a.score || 0));
  return out;
}

// ---- Data -------------------------------------------------------------------

async function loadTracked() {
  try {
    const rows = await api("GET", "/api/tracker");
    S.tracked = Object.fromEntries(rows.map((r) => [r.key, { status: r.status, notes: Boolean(r.notes) }]));
    ui?.renderList();
    ui?.renderDetail();
  } catch { /* statuses are decoration here */ }
}

// fetchNow runs the search with the current filters. refresh ignores the
// cache (each source's minimum TTL still applies).
export async function fetchNow({ refresh = false } = {}) {
  const input = await currentInput();
  saveLast();
  const seq = ++runSeq;
  Object.assign(S, { jobs: [], run: null, running: true, shown: PAGE, reports: 0, started: true });
  ui?.setBusy(true);
  ui?.renderAll();
  const q = query(input, { refresh });
  api("GET", "/api/links?" + q).then((ls) => { S.links = ls; ui?.renderLinks(); }, () => {});
  loadTracked();
  try {
    S.expected = (await api("GET", "/api/sources")).filter((s) => s.enabled).length;
  } catch { S.expected = 0; }
  if (seq !== runSeq) return;
  try {
    await runlog.start(q, {
      expected: S.expected,
      onEvent(name, ev) {
        if (seq !== runSeq) return;
        if (name === "source") S.reports++;
        else if (name === "job") S.jobs.push(ev.job);
        else if (name === "run") S.run = ev.run;
        else if (name === "error") toast(`Search failed: ${ev.error}`, { error: true });
        ui?.schedule();
      },
    });
  } catch (err) {
    if (seq === runSeq && err.name !== "AbortError") toast(`Search failed: ${err.message}`, { error: true });
  } finally {
    if (seq === runSeq) {
      S.running = false;
      ui?.setBusy(false);
      if (!S.jobs.some((j) => j.key === S.selKey)) S.selKey = phone.matches ? null : visible()[0]?.key ?? null;
      ui?.renderAll();
    }
  }
}

function stopSearch() {
  runSeq++;
  runlog.stop();
  S.running = false;
  ui?.setBusy(false);
  ui?.renderAll();
}

// ---- Tracking and hiding ------------------------------------------------------

function trackerChanged() {
  window.dispatchEvent(new Event("jobhunt:tracker"));
}

// mark is s and a: put the job in stage, or take it out again when it is
// already there. It never moves a job back down the pipeline.
async function mark(j, stage) {
  if (!j) return;
  const cur = S.tracked[j.key]?.status;
  if (cur === stage) { untrack(j); return; }
  if (cur && TRACK_ORDER.indexOf(cur) > TRACK_ORDER.indexOf(stage)) {
    toast(`Already at ${statusLabel[cur] || cur}. Change it on the Tracker tab.`);
    return;
  }
  try {
    const r = await api("PUT", `/api/tracker/${enc(j.key)}`, { status: stage });
    S.tracked[j.key] = { status: r.status, notes: Boolean(r.notes) };
    toast(`${statusLabel[stage]}: ${j.title || "job"}`);
    trackerChanged();
  } catch (err) {
    toast(`Couldn't update the tracker: ${err.message}`, { error: true });
  }
  ui?.renderList();
  ui?.renderDetail();
}

async function untrack(j) {
  const prev = S.tracked[j.key];
  if (prev.notes) {
    toast("This job has notes. Remove it on the Tracker tab, so they aren't lost by accident.");
    return;
  }
  try {
    await api("DELETE", `/api/tracker/${enc(j.key)}`);
    delete S.tracked[j.key];
    trackerChanged();
    toast(`Removed from tracker: ${j.title || "job"}`, {
      action: "Undo",
      onAction: async () => {
        try {
          const r = await api("PUT", `/api/tracker/${enc(j.key)}`, { status: prev.status });
          S.tracked[j.key] = { status: r.status, notes: Boolean(r.notes) };
          trackerChanged();
          ui?.renderList();
          ui?.renderDetail();
        } catch (err) {
          toast(`Couldn't put it back: ${err.message}`, { error: true });
        }
      },
    });
  } catch (err) {
    toast(`Couldn't update the tracker: ${err.message}`, { error: true });
  }
  ui?.renderList();
  ui?.renderDetail();
}

const hiding = new Set(); // keys with a hide request in flight

async function hide(j) {
  if (!j || hiding.has(j.key)) return;
  hiding.add(j.key);
  try {
    await api("POST", `/api/jobs/${enc(j.key)}/hide`);
  } catch (err) {
    toast(`Couldn't hide the job: ${err.message}`, { error: true });
    return;
  } finally {
    hiding.delete(j.key);
  }
  // Look again: results may have changed while the request was out.
  const i = S.jobs.findIndex((x) => x.key === j.key);
  if (i < 0) return;
  if (S.selKey === j.key) {
    const v = visible();
    const at = v.findIndex((x) => x.key === j.key);
    S.selKey = (v[at + 1] || v[at - 1])?.key ?? null;
  }
  S.jobs.splice(i, 1);
  ui?.renderAll();
  const shownRun = runSeq; // the results the job was hidden from
  const t = j.title || "job";
  toast(`Hid “${t.length > 36 ? `${t.slice(0, 36)}…` : t}”`, {
    action: "Undo",
    onAction: async () => {
      try {
        await api("DELETE", `/api/jobs/${enc(j.key)}/hide`);
        // Put it back only into the results it came from; another search
        // may not match it, so run that one again instead.
        if (runSeq !== shownRun) { fetchNow(); return; }
        if (!S.jobs.some((x) => x.key === j.key)) S.jobs.push(j);
        S.selKey = j.key;
        ui?.renderAll();
        ui?.reveal();
      } catch (err) {
        toast(`Couldn't unhide the job: ${err.message}`, { error: true });
      }
    },
  });
}

// ---- View ---------------------------------------------------------------------

export async function mount(view, { live = () => true } = {}) {
  const m = await meta();
  await currentInput();
  if (!live()) return null; // the user left while this was loading
  let mDetail = false; // phone: a job is open full screen
  let railOpen = false;
  let debounce = 0;
  let frame = 0;

  // -- Search box: keyword | where. In the app bar, or atop the list on phones.
  const kw = h("input", { type: "search", id: "kw", placeholder: "Job title, skill or company", autocomplete: "off", enterkeyhint: "search", "aria-label": "Keywords, comma-separated" });
  const where = h("input", { type: "search", id: "where", placeholder: "City, country or remote", autocomplete: "off", "aria-label": "Location contains" });
  const searchbox = h("div", { class: "searchbox", role: "search" },
    h("label", {}, icon("search"), kw, h("kbd", {}, "/")),
    h("label", {}, icon("map-pin"), where));

  // -- Filter rail
  const chip = (group, value, label, { soft = false, title } = {}) =>
    h("button", { type: "button", class: soft ? "chip soft" : "chip", "aria-pressed": "false", dataset: { group, value }, title }, label);
  const hint = () => h("span", { class: "hint" });
  const group = (name, label, chips, extra = null) => {
    const hintEl = hint();
    const box = h("section", { class: "group", "aria-label": label, dataset: { name } },
      h("div", { class: "group-head" }, h("span", { class: "name" }, label), hintEl),
      h("div", { class: "chips" }, chips, extra));
    return { box, hintEl };
  };
  const groups = {
    countries: group("countries", "Location", m.options.countries.map((v) => chip("countries", v, nameOf(v)))),
    modes: group("modes", "Work mode", m.options.modes.map((v) => chip("modes", v, v))),
    types: group("types", "Job type", m.options.types.map((v) => chip("types", v, v))),
    experience: group("experience", "Experience", m.options.experience.map((v) => chip("experience", v, v))),
  };
  const moreAreas = h("button", { type: "button", class: "more-link" });
  groups.areas = group("areas", "Area", m.options.areas.map((v, i) => {
    const c = chip("areas", v, v);
    if (i >= AREAS_SHOWN) c.classList.add("extra");
    return c;
  }), moreAreas);
  const ageGroup = group("posted_within", "Posted within", AGES.map(([v, l]) => chip("posted_within", v, l)));
  const optGroup = group("options", "Options", OPTIONS.map(([k, l, t]) => chip("options", k, l, { soft: true, title: t })));
  const minSalary = h("input", { type: "number", class: "input", id: "min-salary", min: 0, step: "any", inputmode: "numeric", placeholder: "Min yearly salary", "aria-label": "Minimum yearly salary" });
  const currency = h("input", { type: "text", class: "input", id: "currency", maxlength: 3, placeholder: "INR", pattern: "[A-Za-z]{3}", autocomplete: "off", "aria-label": "Currency" });
  const exclude = h("input", { type: "text", class: "input", id: "exclude", placeholder: "senior, unpaid", autocomplete: "off" });
  const presetSel = h("select", { class: "input", id: "preset" });
  const presetName = h("input", { type: "text", class: "input", placeholder: "preset name", "aria-label": "Preset name", pattern: "[A-Za-z0-9][A-Za-z0-9._\\-]{0,63}", maxlength: 64, autocomplete: "off" });
  const presetSave = h("div", { class: "preset-save", hidden: true },
    presetName,
    h("button", { type: "button", class: "btn btn-secondary", onclick: savePreset }, "Save"),
    h("button", { type: "button", class: "btn btn-ghost", "aria-label": "Cancel", onclick: () => { presetSave.hidden = true; presetSel.focus(); } }, icon("x")));
  const showBtn = h("button", { type: "button", class: "btn btn-primary blueprint", onclick: () => setRail(false) });
  const rail = h("aside", { class: "rail", "aria-label": "Filters", id: "filters" },
    h("div", { class: "rail-head" },
      h("h6", {}, "Filters"),
      h("span", { class: "row" },
        h("button", { type: "button", class: "btn btn-ghost", onclick: reset }, "Reset"),
        h("button", { type: "button", class: "btn btn-ghost close", "aria-label": "Close filters", onclick: () => setRail(false) }, icon("x")))),
    h("div", { class: "rail-body" },
      h("div", { class: "field" }, h("label", { for: "preset" }, "Preset"), presetSel, presetSave),
      groups.countries.box, groups.modes.box, groups.types.box, groups.experience.box, groups.areas.box,
      ageGroup.box, optGroup.box,
      h("div", { class: "field" },
        h("label", { for: "min-salary", title: "Jobs without salary info, or in another currency, are dropped unless Include unknown is on" }, "Minimum salary"),
        h("div", { class: "salary" }, minSalary, currency)),
      h("div", { class: "field" }, h("label", { for: "exclude" }, "Exclude words"), exclude)),
    h("div", { class: "sheet-foot" }, showBtn));

  // -- Results column
  const filtersBtn = h("button", { type: "button", class: "btn btn-secondary filters-btn", "aria-controls": "filters", "aria-expanded": "false", onclick: () => setRail(!railOpen) });
  const countEl = h("span", { class: "results-count", "aria-live": "polite" });
  const ofEl = h("span", { class: "results-of mono" });
  const sortSel = h("select", { class: "input", "aria-label": "Sort" },
    h("option", { value: "score" }), h("option", { value: "date" }));
  function labelSort() {
    const [best, newest] = phone.matches ? ["Best match", "Newest"] : ["Sort: best match", "Sort: newest"];
    sortSel.options[0].textContent = best;
    sortSel.options[1].textContent = newest;
  }
  labelSort();
  const chipsEl = h("div", { class: "active-chips" });
  const progressText = h("span", { class: "mono" });
  const progressBar = h("i");
  const progress = h("div", { class: "progress", hidden: true },
    h("span", { class: "row" }, progressText,
      h("button", { type: "button", class: "btn btn-ghost", onclick: stopSearch }, "Stop")),
    h("span", { class: "bar" }, progressBar));
  const boxHolder = h("div", { class: "box-holder" });
  const head = h("div", { class: "results-head" },
    boxHolder,
    h("div", { class: "results-row count-row" }, countEl, ofEl, sortSel),
    h("div", { class: "results-row chips-row" }, filtersBtn, chipsEl),
    progress);
  const list = h("ul", { class: "joblist", role: "listbox", "aria-label": "Jobs", tabindex: 0 });
  const also = h("div", { class: "also", hidden: true });
  const results = h("section", { class: "results", "aria-label": "Results" }, head, list, also);

  // -- Detail pane
  const detail = h("section", { class: "detail", "aria-label": "Job details" });

  // -- Column handles
  const handleRail = h("div", { class: "handle handle-rail", role: "separator", "aria-orientation": "vertical", "aria-label": "Resize filters", tabindex: 0 });
  const handleList = h("div", { class: "handle handle-list", role: "separator", "aria-orientation": "vertical", "aria-label": "Resize results", tabindex: 0 });
  const scrim = h("div", { class: "scrim", onclick: () => setRail(false) });
  const grid = h("div", { class: "search" }, rail, handleRail, results, handleList, detail, scrim);
  view.append(grid);

  function placeSearchbox() {
    if (phone.matches) boxHolder.append(searchbox);
    else shell.slot().append(searchbox);
  }
  placeSearchbox();

  // ---- Rendering --------------------------------------------------------------

  function syncRail() {
    const inp = S.input;
    for (const b of rail.querySelectorAll(".chip")) {
      const { group: g, value: v } = b.dataset;
      let on;
      if (g === "posted_within") on = (inp.posted_within || "") === v;
      else if (g === "options") on = Boolean(inp[v]);
      else on = (inp[g] || []).includes(v);
      b.setAttribute("aria-pressed", String(on));
      if (b.classList.contains("extra")) b.hidden = !S.moreAreas && !on;
    }
    // A preset saved from the CLI may use an age the chips don't offer.
    const age = inp.posted_within || "";
    const ages = ageGroup.box.querySelector(".chips");
    for (const b of ages.querySelectorAll(".chip.custom")) if (b.dataset.value !== age) b.remove();
    if (age && !ages.querySelector(`.chip[data-value="${CSS.escape(age)}"]`)) {
      const c = chip("posted_within", age, age);
      c.classList.add("custom");
      c.setAttribute("aria-pressed", "true");
      ages.prepend(c);
    }
    for (const [g, { hintEl }] of Object.entries(groups)) {
      const n = (inp[g] || []).length;
      hintEl.textContent = n ? `${n} on` : "any";
    }
    const n = OPTIONS.filter(([k]) => inp[k]).length;
    optGroup.hintEl.textContent = n ? `${n} on` : "off";
    const extra = m.options.areas.length - AREAS_SHOWN;
    moreAreas.textContent = S.moreAreas ? "fewer" : `+${extra} more`;
    moreAreas.hidden = extra <= 0;
    const set = (el, v) => { if (document.activeElement !== el) el.value = v; };
    set(kw, (inp.keywords || []).join(", "));
    set(where, S.where);
    set(exclude, (inp.exclude || []).join(", "));
    set(minSalary, inp.min_salary ? String(inp.min_salary) : "");
    set(currency, inp.currency || "");
    sortSel.value = S.sort;
  }

  function activeChips() {
    const inp = S.input;
    const out = [];
    for (const g of ["countries", "modes", "types", "experience", "areas"]) {
      for (const v of inp[g] || []) out.push([nameOf(v), () => toggleIn(g, v)]);
    }
    if (inp.posted_within) out.push([`last ${ageName(inp.posted_within)}`, () => { inp.posted_within = ""; changed(); }]);
    for (const [k, l] of OPTIONS) if (inp[k]) out.push([l.toLowerCase(), () => { inp[k] = false; changed(); }]);
    if (inp.min_salary) out.push([`≥ ${count(inp.min_salary)}${inp.currency ? ` ${inp.currency}` : ""}`, () => { inp.min_salary = 0; changed(); }]);
    for (const w of inp.exclude || []) out.push([`not “${w}”`, () => { inp.exclude = inp.exclude.filter((x) => x !== w); changed(); }]);
    if (S.where.trim()) out.push([`in “${S.where.trim()}”`, () => { S.where = ""; where.value = ""; renderAll(); }]);
    return out;
  }

  function renderHead() {
    const v = visible();
    const chips = activeChips();
    replace(chipsEl, chips.map(([label, remove]) =>
      h("button", { type: "button", class: "tag tag-accent", "aria-label": `Remove filter ${label}`, onclick: remove }, label, icon("x"))));
    replace(filtersBtn, icon("sliders-horizontal"), `Filters${chips.length ? ` (${chips.length})` : ""}`);
    filtersBtn.setAttribute("aria-expanded", String(railOpen));
    countEl.textContent = S.running && !S.jobs.length ? "Searching…" : `${count(v.length)} ${v.length === 1 ? "job" : "jobs"}`;
    ofEl.textContent = S.run ? `of ${count(S.run.fetched)} fetched` : S.running ? `${count(S.jobs.length)} so far` : "";
    showBtn.replaceChildren(...corners(), `Show ${count(v.length)} ${v.length === 1 ? "job" : "jobs"}`);
    progress.hidden = !S.running;
    const total = Math.max(S.expected, S.reports, 1);
    progressText.textContent = `fetching · ${S.reports} / ${total} sources`;
    progressBar.style.width = `${Math.round((S.reports / total) * 100)}%`;
    if (!live()) return;
    shell.setCount("search", S.running && !S.run ? "…" : count(v.length));
    shell.setContext(`${count(v.length)} shown${phone.matches ? "" : " · drag column edges to resize"}`);
  }

  function ticks(score) {
    const on = Math.round(score / 10);
    return h("span", { class: "ticks", "aria-hidden": "true" },
      Array.from({ length: 10 }, (_, i) => h("i", { class: i < on ? "on" : null })));
  }

  function card(j) {
    const t = S.tracked[j.key]?.status;
    const score = Math.round(j.score || 0);
    const sel = j.key === S.selKey;
    const age = since(j.posted_at);
    return h("li", {
      class: t && t !== "saved" ? "job past" : "job", role: "option", id: `job-${j.key}`,
      "aria-selected": String(sel), dataset: { key: j.key },
    },
      h("div", { class: "job-main" },
        h("div", { class: "job-meta" },
          h("span", { class: "company" }, j.company || "unknown company"),
          h("span", { "aria-hidden": "true" }, "·"),
          h("span", { title: j.posted_at ? `Posted ${dateTime(j.posted_at)}` : "Posting date unknown" }, age === "?" ? "date unknown" : `${age} ago`),
          j.new && h("span", { class: "badge new", title: "New since your last search" }, "new"),
          t && h("span", { class: "badge" }, statusLabel[t] || t)),
        h("div", { class: "job-title" }, j.title || "(untitled)"),
        h("div", { class: "job-loc", title: location(j) }, location(j), h("span", { class: "extra" }, ` · ${j.work_mode} · ${j.job_type}`)),
        h("div", { class: "job-tags" },
          h("span", { class: "tag tag-neutral" }, j.work_mode),
          h("span", { class: "tag tag-neutral" }, j.job_type),
          h("span", { class: "tag tag-neutral", title: (j.sources || [j.source]).join(", ") }, (j.sources || [j.source]).join(", ")))),
      h("div", { class: "job-score", title: `Score ${score} of 100` },
        h("span", { class: score >= 75 ? "score high" : "score" }, String(score)),
        ticks(score)));
  }

  function renderList() {
    const v = visible();
    const at = v.findIndex((j) => j.key === S.selKey);
    if (at >= S.shown) S.shown = at + 1;
    const rows = v.slice(0, S.shown).map(card);
    if (v.length > S.shown) {
      rows.push(h("li", { class: "list-more" },
        h("button", { type: "button", class: "btn btn-secondary", onclick: () => { S.shown += PAGE; renderList(); } },
          `Show ${count(Math.min(PAGE, v.length - S.shown))} more of ${count(v.length - S.shown)}`)));
    }
    if (!v.length && !S.running) {
      rows.push(h("li", { class: "list-note" },
        h("span", { class: "title" }, S.run ? "No jobs match these filters" : "No results yet"),
        S.run && h("button", { type: "button", class: "btn btn-secondary", onclick: reset }, "Reset filters")));
    }
    replace(list, rows);
    if (at >= 0) list.setAttribute("aria-activedescendant", `job-${S.selKey}`);
    else list.removeAttribute("aria-activedescendant");
  }

  function renderLinks() {
    const off = hiddenSites();
    const ls = S.links.filter((l) => !off.has(l.site) && safeURL(l.url));
    also.hidden = !ls.length;
    replace(also, h("span", { class: "label" }, "Also search"),
      ls.map((l) => extLink(l.url, `${l.site} ↗`)));
  }

  function selected() {
    return S.jobs.find((j) => j.key === S.selKey) || null;
  }

  function action(label, ico, key, run, on = false) {
    return h("button", { type: "button", class: on ? "btn btn-secondary on" : "btn btn-secondary", "aria-pressed": String(on), onclick: run },
      icon(ico), label, h("kbd", {}, key));
  }

  function applyLink(j, withKey) {
    const children = [...corners(), `Apply on ${j.source}`, icon("arrow-up-right")];
    if (withKey) children.push(h("kbd", {}, "o"));
    const a = extLink(j.url, ...children);
    a.className = "btn btn-primary blueprint";
    if (a.tagName !== "A") a.setAttribute("aria-disabled", "true");
    return a;
  }

  function renderDetail() {
    const j = selected();
    document.body.classList.toggle("m-detail", Boolean(j && mDetail && phone.matches));
    detail.classList.toggle("open", Boolean(j && mDetail));
    if (!j) {
      replace(detail, h("div", { class: "detail-empty" },
        h("h4", {}, S.running ? "Searching…" : "No job selected"),
        h("p", { class: "muted" }, "Pick a job to see why it scored what it did, and to save, apply or hide it."),
        h("dl", { class: "keys" },
          ...[["j k", "next / previous job"], ["o", "open the posting"], ["s", "save, again to unsave"], ["a", "mark applied"],
            ["h", "hide"], ["/", "search"], ["r", "fetch again"], ["1–4", "tabs"], ["t", "theme"]]
            .flatMap(([k, d]) => [h("dt", {}, h("kbd", {}, k)), h("dd", {}, d)]))));
      return;
    }
    const t = S.tracked[j.key]?.status;
    const score = Math.round(j.score || 0);
    const v = visible();
    const at = v.findIndex((x) => x.key === j.key);
    const past = t && t !== "saved";
    const acts = () => [
      action(t === "saved" ? "Saved" : "Save", "bookmark", "s", () => mark(j, "saved"), t === "saved"),
      action(past ? statusLabel[t] || t : "Mark applied", "check", "a", () => mark(j, "applied"), past),
      action("Hide", "eye-off", "h", () => hide(j)),
    ];
    const facts = [
      ["Work mode", j.work_mode], ["Type", j.job_type], ["Experience", j.experience],
      ["Posted", j.posted_at ? `${since(j.posted_at)} ago` : "unknown"],
      ["Countries", (j.countries || []).map(place).join(", ") || "unknown"],
      ["Source", (j.sources || [j.source]).join(", ")],
    ];
    if (salary(j)) facts.push(["Salary", salary(j)]);
    const parts = j.score_parts || [];
    const paras = String(j.description || "").split(/\n\s*\n/).map((p) => p.trim()).filter(Boolean);
    const tags = [...new Set([...(j.areas || []), ...(j.tags || [])])];
    replace(detail,
      h("div", { class: "detail-bar" },
        h("button", { type: "button", class: "back", onclick: closeDetail }, icon("chevron-left"), "Results"),
        h("span", { class: "pos" }, at >= 0 ? `${at + 1} / ${v.length}` : ""),
        h("button", { type: "button", class: "nav-btn", "aria-label": "Previous job", onclick: () => step(-1) }, icon("chevron-up")),
        h("button", { type: "button", class: "nav-btn", "aria-label": "Next job", onclick: () => step(1) }, icon("chevron-down"))),
      h("div", { class: "detail-scroll" },
        h("div", { class: "detail-head" },
          h("div", { class: "detail-kicker" }, `${j.company || "unknown company"} — ${location(j)}`),
          h("h2", {}, j.title || "(untitled)"),
          h("div", { class: "detail-sub" }, `${location(j)}${j.posted_at ? ` · posted ${since(j.posted_at)} ago` : ""}`),
          h("div", { class: "actions" }, applyLink(j, true), acts())),
        h("div", { class: "detail-body" },
          h("div", { class: "plate" }, facts.map(([k, val], i) =>
            h("div", { class: "plate-cell" },
              h("span", { class: "label" }, h("span", {}, k), h("span", {}, String(i + 1).padStart(2, "0"))),
              h("span", { class: "value" }, val)))),
          parts.length > 0 && h("div", { class: "why" },
            h("div", { class: "why-head" }, h("h4", {}, `Why it scored ${score}`), h("span", { class: "mono muted" }, "out of 100")),
            parts.map((p) => {
              const [label, about] = FACTORS[p.factor] || [p.factor, ""];
              const ratio = p.max ? p.points / p.max : 0;
              const fill = h("i", { class: ratio > 0.6 ? "good" : null });
              fill.style.width = `${Math.round(Math.min(1, ratio) * 100)}%`;
              return h("div", { class: "why-row", title: about },
                h("span", {}, label), h("span", { class: "bar" }, fill),
                h("span", { class: "val" }, `${Math.round(p.points)}/${Math.round(p.max)}`));
            })),
          h("article", { class: "about" },
            h("h4", {}, "About the role"),
            paras.length ? paras.map((p) => h("p", {}, p)) : h("p", { class: "muted" }, "No description.")),
          tags.length > 0 && h("div", { class: "tag-row" }, tags.map((x) => h("span", { class: "tag tag-outline" }, x))),
          h("div", { class: "detail-foot" },
            h("span", {}, `key ${j.key}`),
            j.first_seen_at && h("span", {}, `first seen ${dateTime(j.first_seen_at)}`),
            h("span", {}, `visa: ${visa(j) || "not stated"}`)))),
      h("div", { class: "detail-actions-m" },
        acts().map((b) => {
          // Icon-only on phones: the label becomes the accessible name.
          b.querySelector("kbd")?.remove();
          b.setAttribute("aria-label", b.textContent.trim());
          b.title = b.textContent.trim();
          for (const n of [...b.childNodes]) if (n.nodeType === Node.TEXT_NODE) n.remove();
          b.classList.add("btn-icon");
          return b;
        }),
        applyLink(j, false)));
  }

  function renderAll() {
    syncRail();
    renderHead();
    renderList();
    renderLinks();
    renderDetail();
  }

  function schedule() {
    if (frame) return;
    frame = requestAnimationFrame(() => {
      frame = 0;
      renderHead();
      renderList();
    });
  }

  function reveal() {
    const el = list.querySelector(`#job-${CSS.escape(S.selKey || "")}`);
    el?.scrollIntoView({ block: "nearest" });
  }

  // ---- Interaction ------------------------------------------------------------

  function select(key, { open = false } = {}) {
    S.selKey = key;
    if (open) mDetail = true;
    renderList();
    renderDetail();
    reveal();
    if (mDetail && phone.matches) detail.querySelector(".detail-scroll")?.scrollTo(0, 0);
  }

  function step(d) {
    const v = visible();
    if (!v.length) return;
    const at = v.findIndex((j) => j.key === S.selKey);
    const next = at < 0 ? 0 : Math.min(Math.max(at + d, 0), v.length - 1);
    select(v[next].key);
  }

  function closeDetail() {
    mDetail = false;
    renderDetail();
    reveal();
  }

  function setRail(open) {
    railOpen = open;
    grid.classList.toggle("rail-open", open);
    filtersBtn.setAttribute("aria-expanded", String(open));
    if (open) rail.querySelector(".chip")?.focus({ preventScroll: true });
  }

  // changed runs a new search shortly after the last filter change.
  function changed() {
    saveLast();
    syncRail();
    renderHead();
    clearTimeout(debounce);
    debounce = setTimeout(() => fetchNow(), 350);
  }

  function toggleIn(g, v) {
    const cur = S.input[g] || [];
    S.input[g] = cur.includes(v) ? cur.filter((x) => x !== v) : [...cur, v];
    changed();
  }

  function reset() {
    S.input = fromAPI({ ...clone(m.defaults), new_only: false, show_hidden: false });
    S.where = "";
    where.value = "";
    presetSel.value = "";
    changed();
  }

  rail.addEventListener("click", (e) => {
    const b = e.target.closest(".chip");
    if (!b) return;
    const { group: g, value: v } = b.dataset;
    if (g === "posted_within") S.input.posted_within = v;
    else if (g === "options") S.input[v] = !S.input[v];
    else { toggleIn(g, v); return; }
    changed();
  });
  moreAreas.addEventListener("click", () => { S.moreAreas = !S.moreAreas; syncRail(); });

  function commitKeywords() {
    const next = words(kw.value);
    if (next.join("\u0000") === (S.input.keywords || []).join("\u0000")) return;
    S.input.keywords = next;
    changed();
  }
  kw.addEventListener("change", commitKeywords);
  kw.addEventListener("keydown", (e) => { if (e.key === "Enter") { e.preventDefault(); commitKeywords(); } });
  where.addEventListener("input", () => { S.where = where.value; renderHead(); renderList(); });
  exclude.addEventListener("change", () => { S.input.exclude = words(exclude.value); changed(); });
  minSalary.addEventListener("change", () => { S.input.min_salary = Math.max(0, Number(minSalary.value) || 0); changed(); });
  currency.addEventListener("change", () => {
    if (!currency.reportValidity()) return;
    S.input.currency = currency.value.trim().toUpperCase();
    changed();
  });
  sortSel.addEventListener("change", () => { S.sort = sortSel.value; saveLast(); renderAll(); });

  list.addEventListener("click", (e) => {
    const li = e.target.closest("li.job");
    if (li) select(li.dataset.key, { open: phone.matches });
  });
  list.addEventListener("dblclick", (e) => {
    if (e.target.closest("li.job")) openURL(selected()?.url);
  });

  // -- Presets
  let presets = [];
  async function loadPresets(selectedName = "") {
    try {
      presets = await api("GET", "/api/presets");
    } catch (err) {
      toast(`Couldn't load presets: ${err.message}`, { error: true });
    }
    replace(presetSel,
      h("option", { value: "" }, presets.length ? "Choose a preset…" : "No presets yet"),
      presets.map((p) => h("option", { value: p.name }, p.name)),
      h("option", { value: "\u0000save" }, "Save current as preset…"));
    presetSel.value = selectedName;
  }
  presetSel.addEventListener("change", () => {
    if (presetSel.value === "\u0000save") {
      presetSel.value = "";
      presetSave.hidden = false;
      presetName.focus();
      return;
    }
    const p = presets.find((x) => x.name === presetSel.value);
    if (!p) return;
    // A preset is stored with defaults already applied, so a field it
    // leaves out means "any", not "the config default".
    const blank = {
      countries: [], modes: [], types: [], areas: [], experience: [], keywords: [], exclude: [], posted_within: "",
      visa: false, min_salary: 0, currency: "", strict_country: false, include_unknown: false,
    };
    S.input = fromAPI({ ...blank, ...clone(p.filters), new_only: S.input.new_only, show_hidden: S.input.show_hidden });
    presetName.value = p.name;
    changed();
  });
  presetName.addEventListener("keydown", (e) => {
    if (e.key === "Enter") { e.preventDefault(); savePreset(); }
    if (e.key === "Escape") { presetSave.hidden = true; presetSel.focus(); }
  });
  async function savePreset() {
    const name = presetName.value.trim();
    if (!name || !presetName.checkValidity()) {
      presetName.focus();
      toast("Preset names use letters, digits, '.', '_' and '-'.", { error: true });
      return;
    }
    const { new_only, show_hidden, ...filters } = S.input; // search options, not filters
    try {
      await api("PUT", `/api/presets/${enc(name)}`, explicit(filters));
      presetSave.hidden = true;
      toast(`Saved preset ${name}`);
      await loadPresets(name);
      presetSel.focus(); // the name field is gone; don't strand focus in it
    } catch (err) {
      toast(`Couldn't save the preset: ${err.message}`, { error: true });
    }
  }

  // -- Column resizing
  const widths = { rail: 248, list: 460 };
  try { Object.assign(widths, JSON.parse(localStorage.getItem(WIDTHS) || "{}")); } catch { /* ignore */ }
  const clamp = (v, lo, hi) => Math.round(Math.min(Math.max(v, lo), Math.max(lo, hi)));
  function applyWidths() {
    widths.rail = clamp(Number(widths.rail) || 248, 200, 380);
    const railW = narrow.matches ? 0 : widths.rail;
    widths.list = clamp(Number(widths.list) || 460, 320, window.innerWidth - railW - 380);
    const root = document.documentElement.style;
    root.setProperty("--rail", `${widths.rail}px`);
    root.setProperty("--list", `${widths.list}px`);
  }
  function saveWidths() {
    try { localStorage.setItem(WIDTHS, JSON.stringify({ rail: widths.rail, list: widths.list })); } catch { /* ignore */ }
  }
  function dragFrom(handle, which) {
    handle.addEventListener("pointerdown", (e) => {
      if (e.button !== 0) return;
      e.preventDefault();
      const x0 = e.clientX;
      const v0 = widths[which];
      const cover = h("div", { class: "drag-cover" });
      document.body.append(cover);
      handle.classList.add("active");
      const move = (ev) => { widths[which] = v0 + ev.clientX - x0; applyWidths(); };
      const end = () => {
        window.removeEventListener("pointermove", move);
        window.removeEventListener("pointerup", end);
        window.removeEventListener("pointercancel", end);
        cover.remove();
        handle.classList.remove("active");
        saveWidths();
      };
      window.addEventListener("pointermove", move);
      window.addEventListener("pointerup", end);
      window.addEventListener("pointercancel", end);
    });
    handle.addEventListener("keydown", (e) => {
      const d = { ArrowLeft: -16, ArrowRight: 16 }[e.key];
      if (!d) return;
      e.preventDefault();
      widths[which] += d;
      applyWidths();
      saveWidths();
    });
  }
  dragFrom(handleRail, "rail");
  dragFrom(handleList, "list");
  applyWidths();

  // -- Keys
  function onKey(e) {
    if (e.ctrlKey || e.metaKey || e.altKey) return;
    if (typing(e)) {
      if (e.key === "Escape") e.target.blur();
      return;
    }
    if (document.querySelector(".drawer-backdrop")) return;
    const j = selected();
    const keys = {
      "/": () => { if (railOpen && phone.matches) setRail(false); kw.focus(); },
      Escape: () => { if (mDetail) closeDetail(); else if (railOpen) setRail(false); },
      j: () => step(1), ArrowDown: () => step(1),
      k: () => step(-1), ArrowUp: () => step(-1),
      o: () => openURL(j?.url),
      s: () => mark(j, "saved"),
      a: () => mark(j, "applied"),
      h: () => hide(j),
    };
    const fn = keys[e.key];
    if (fn) { e.preventDefault(); fn(); }
  }
  document.addEventListener("keydown", onKey);

  const onPhone = () => { placeSearchbox(); labelSort(); if (!phone.matches) mDetail = false; renderAll(); };
  const onNarrow = () => { applyWidths(); if (!narrow.matches) setRail(false); };
  phone.addEventListener("change", onPhone);
  narrow.addEventListener("change", onNarrow);
  window.addEventListener("resize", applyWidths);

  shell.setFetch(() => fetchNow({ refresh: true }), "Fetch");
  shell.setBusy(S.running);
  shell.setStatus("BROWSE", [["j k", "move"], ["o", "open"], ["s", "save"], ["a", "applied"], ["h", "hide"], ["/", "search"], ["1–4", "tabs"], ["t", "theme"]]);

  const setBusy = (b) => { if (live()) shell.setBusy(b); };
  ui = { setBusy, schedule, renderAll, renderList, renderDetail, renderLinks, reveal };
  renderAll();
  loadPresets();
  if (!S.started) fetchNow();
  else loadTracked();
  if (S.links.length === 0 && S.started) {
    api("GET", "/api/links?" + query(S.input)).then((ls) => { S.links = ls; renderLinks(); }, () => {});
  }

  const mine = ui;
  return () => {
    if (ui === mine) ui = null; // a late cleanup mustn't unhook a newer view
    clearTimeout(debounce);
    if (frame) cancelAnimationFrame(frame);
    document.removeEventListener("keydown", onKey);
    phone.removeEventListener("change", onPhone);
    narrow.removeEventListener("change", onNarrow);
    window.removeEventListener("resize", applyWidths);
    document.body.classList.remove("m-detail");
    searchbox.remove();
  };
}
