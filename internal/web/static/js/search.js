// Search view: filter sidebar, live results from /api/events, and a
// detail pane with actions. Keyboard shortcuts match the TUI.
import { api, stream, meta, enc, APIError } from "./api.js";
import { h, replace, extLink, openURL, toast, typing } from "./dom.js";
import { since, dateTime, location, salary, visa, statusLabel } from "./format.js";

const SORTS = ["score", "date", "company"];
const PAGE = 200; // rows rendered at a time
// Values as the API returns them (app.FormatAge prints whole days as "1d").
const AGES = [["", "Any time"], ["1d", "24 hours"], ["3d", "3 days"], ["7d", "7 days"], ["30d", "30 days"]];
const LAST = "jobhunt.search"; // last filters, a per-browser convenience

export async function mount(main) {
  const m = await meta();
  const st = {
    jobs: [],      // results in display order
    statuses: {},  // tracker status by job key
    sel: -1,       // selected index into jobs
    shown: PAGE,   // rows rendered
    sort: "score",
    ctrl: null,    // AbortController of the running search
    reports: [],
    expected: 0,   // sources that should report
    run: null,     // the final run event
  };

  // ---- Filters -------------------------------------------------------

  const chipGroup = (name, legend, values, labels = {}) =>
    h("fieldset", {},
      h("legend", {}, legend),
      h("div", { class: "chips" }, values.map((v) =>
        h("label", { class: "chip" },
          h("input", { type: "checkbox", name, value: v }),
          h("span", {}, labels[v] || v)))));

  const presetSel = h("select", { id: "preset", "aria-label": "Preset" }, h("option", { value: "" }, "Load a preset…"));
  const kw = h("input", { type: "text", id: "kw", name: "keywords", placeholder: "go, rust, kubernetes", autocomplete: "off" });
  const ex = h("input", { type: "text", id: "ex", name: "exclude", placeholder: "senior, unpaid", autocomplete: "off" });
  const age = h("select", { id: "age", name: "posted_within" }, AGES.map(([v, t]) => h("option", { value: v }, t)));
  const minSalary = h("input", { type: "number", id: "min-salary", min: 0, step: "any", inputmode: "numeric", placeholder: "0", class: "grow" });
  const currency = h("input", {
    type: "text", id: "currency", maxlength: 3, placeholder: "INR", "aria-label": "Currency",
    pattern: "[A-Za-z]{3}", autocomplete: "off", class: "currency",
  });
  const check = (name, text, title) =>
    h("label", { class: "check", title }, h("input", { type: "checkbox", name }), text);
  const searchBtn = h("button", { type: "submit" }, "Search");
  const refreshBtn = h("button", { type: "button", class: "ghost", title: "Search again, ignoring the cache (r)" }, "Refresh");
  const presetName = h("input", {
    type: "text", placeholder: "preset name", "aria-label": "Preset name",
    pattern: "[A-Za-z0-9][A-Za-z0-9._\\-]{0,63}", maxlength: 64, autocomplete: "off",
  });

  const narrow = window.matchMedia("(max-width: 720px)");
  const toggle = h("button", { type: "button", class: "ghost filters-toggle", "aria-expanded": "true" }, "Filters");
  const form = h("form", { class: "filters", "aria-label": "Filters" },
    toggle,
    presetSel,
    chipGroup("countries", "Country", m.options.countries, { "remote-global": "Remote (global)" }),
    chipGroup("types", "Job type", m.options.types),
    chipGroup("modes", "Work mode", m.options.modes),
    chipGroup("areas", "Area", m.options.areas),
    chipGroup("experience", "Experience", m.options.experience),
    h("label", { for: "kw" }, "Keywords ", h("kbd", {}, "/")), kw,
    h("label", { for: "ex" }, "Exclude"), ex,
    h("label", { for: "age" }, "Posted within"), age,
    h("label", { for: "min-salary", title: "Jobs without salary info, or in another currency, are dropped unless Include unknown fields is on" }, "Min salary (yearly)"),
    h("div", { class: "row" }, minSalary, currency),
    h("fieldset", {},
      h("legend", {}, "Options"),
      check("new_only", "New since last search only"),
      check("include_unknown", "Include unknown fields", "Keep jobs whose country, type or area couldn't be worked out"),
      check("strict_country", "Strict country", "Don't count worldwide-remote jobs as matching a country"),
      check("visa", "Visa sponsorship only"),
      check("show_hidden", "Show hidden jobs")),
    h("div", { class: "actions" }, searchBtn, refreshBtn),
    h("label", {}, "Save these filters"),
    h("div", { class: "preset-save" }, presetName, h("button", { type: "button", class: "ghost", onclick: savePreset }, "Save")));

  function readInput() {
    const fd = new FormData(form);
    const list = (k) => fd.getAll(k);
    const words = (s) => String(s || "").split(",").map((w) => w.trim()).filter(Boolean);
    const input = {
      countries: list("countries"), types: list("types"), modes: list("modes"),
      areas: list("areas"), experience: list("experience"),
      keywords: words(kw.value), exclude: words(ex.value),
      posted_within: age.value,
      min_salary: Number(minSalary.value) || 0,
      currency: currency.value.trim().toUpperCase(),
    };
    for (const k of ["new_only", "include_unknown", "strict_country", "visa", "show_hidden"]) {
      input[k] = form.elements[k].checked;
    }
    return input;
  }

  function writeInput(input) {
    for (const el of form.querySelectorAll(".chips input")) {
      el.checked = (input[el.name] || []).includes(el.value);
    }
    kw.value = (input.keywords || []).join(", ");
    ex.value = (input.exclude || []).join(", ");
    const want = input.posted_within || "";
    if (want && ![...age.options].some((o) => o.value === want)) {
      age.append(h("option", { value: want }, want)); // e.g. a preset saved from the CLI as 48h
    }
    age.value = want;
    minSalary.value = input.min_salary ? String(input.min_salary) : "";
    currency.value = input.currency || "";
    for (const k of ["new_only", "include_unknown", "strict_country", "visa", "show_hidden"]) {
      form.elements[k].checked = Boolean(input[k]);
    }
  }

  // Query parameters for /api/events and /api/links, named like the JSON.
  function query(input, extra = {}) {
    const q = new URLSearchParams();
    for (const [k, v] of Object.entries({ ...input, ...extra })) {
      if (Array.isArray(v)) { if (v.length) q.set(k, v.join(",")); }
      else if (typeof v === "boolean") { if (v) q.set(k, "true"); }
      else if (v) q.set(k, String(v));
    }
    return q.toString();
  }

  // ---- Results -------------------------------------------------------

  const summary = h("span", { class: "grow muted" }, "Pick filters and search.");
  const sortSel = h("select", { "aria-label": "Sort by", title: "Sort (s)" },
    SORTS.map((s) => h("option", { value: s }, `Sort: ${s}`)));
  const progressBar = h("progress", { max: 1, value: 0 });
  const progressText = h("div", { class: "muted" });
  const progressSources = h("div", { class: "sources" });
  const progress = h("div", { class: "progress", hidden: true }, progressText, progressBar, progressSources);
  const list = h("ul", { class: "joblist", role: "listbox", "aria-label": "Jobs", tabindex: 0 });
  const links = h("div", { class: "links", hidden: true });
  const results = h("section", { class: "results", "aria-label": "Results" },
    h("div", { class: "results-head" }, summary, sortSel), progress, list, links);

  const detail = h("article", { class: "detail idle", "aria-live": "polite" });

  main.append(h("div", { class: "search" }, form, results, detail));

  // ---- Rendering -------------------------------------------------------

  function sortJobs() {
    const key = st.jobs[st.sel]?.key;
    const time = (j) => Date.parse(j.posted_at || j.first_seen_at || 0) || 0;
    const by = {
      score: (a, b) => (b.score || 0) - (a.score || 0),
      date: (a, b) => time(b) - time(a) || (b.score || 0) - (a.score || 0),
      company: (a, b) => (a.company || "").localeCompare(b.company || "", undefined, { sensitivity: "base" }) || (b.score || 0) - (a.score || 0),
    }[st.sort];
    st.jobs.sort(by);
    st.sel = key ? st.jobs.findIndex((j) => j.key === key) : -1;
  }

  function row(j, i) {
    const status = st.statuses[j.key];
    return h("li", {
      class: "job", role: "option", id: `job-${i}`, "aria-selected": String(i === st.sel),
      dataset: { i: String(i) },
    },
      h("span", { class: "score", title: "Score" }, String(Math.round(j.score || 0))),
      h("span", { class: "title" }, j.new && h("span", { class: "star", title: "New since your last search" }, "★"), j.title || "(untitled)"),
      h("span", { class: "age", title: j.posted_at ? `Posted ${dateTime(j.posted_at)}` : "Posting date unknown" }, since(j.posted_at)),
      h("span", { class: "sub" }, `${j.company || "unknown company"} · ${location(j)}`),
      h("span", { class: "tags" },
        status && h("span", { class: "badge accent" }, statusLabel[status] || status),
        h("span", { class: "badge", title: (j.sources || [j.source]).join(", ") }, (j.sources || [j.source]).join(", "))));
  }

  function renderList() {
    const rows = st.jobs.slice(0, st.shown).map(row);
    if (st.jobs.length > st.shown) {
      rows.push(h("li", { class: "empty" },
        h("button", { type: "button", class: "ghost", onclick: () => { st.shown += PAGE; renderList(); } },
          `Show ${Math.min(PAGE, st.jobs.length - st.shown)} more of ${st.jobs.length - st.shown}`)));
    }
    if (!st.jobs.length && !st.ctrl) {
      rows.push(h("li", { class: "empty" }, st.run ? "No jobs match these filters." : "No results yet."));
    }
    replace(list, rows);
    if (st.sel >= 0) list.setAttribute("aria-activedescendant", `job-${st.sel}`);
    else list.removeAttribute("aria-activedescendant");
  }

  function renderSummary() {
    if (st.ctrl) {
      summary.textContent = `Searching… ${st.jobs.length} jobs so far`;
    } else if (st.run) {
      const r = st.run;
      summary.textContent = `${r.matched} jobs (of ${r.fetched.toLocaleString()} fetched)` +
        (r.source_errors ? ` · ${r.source_errors} source error${r.source_errors > 1 ? "s" : ""}` : "");
    }
  }

  function renderProgress() {
    const done = st.reports.length;
    const total = Math.max(st.expected, done, 1);
    progressBar.max = total;
    progressBar.value = done;
    progressText.textContent = st.ctrl ? `Fetching sources: ${done} of ${total} done` : `${done} sources reported`;
    replace(progressSources, st.reports.map((r) => {
      const cls = { ok: "ok", cached: "ok", skipped: "warn", error: "bad" }[r.status] || "";
      return h("span", { class: `badge ${cls}`, title: r.error || `${r.status}, ${r.duration_ms} ms` },
        `${r.name} ${r.status === "error" || r.status === "skipped" ? r.status : r.count}`);
    }));
  }

  function renderLinks(ls) {
    links.hidden = !ls.length;
    replace(links, h("span", { class: "muted" }, "Also search: "), ls.map((l) => extLink(l.url, l.site)));
  }

  function renderDetail() {
    const j = st.jobs[st.sel];
    detail.classList.toggle("idle", !j);
    if (!j) {
      replace(detail,
        h("h2", {}, "No job selected"),
        h("p", { class: "muted" }, "Pick a job to see its details."),
        h("h3", {}, "Keyboard"),
        h("dl", { class: "meta" },
          ...[["j / k", "next / previous job"], ["o", "open the posting"], ["S", "save to tracker"],
            ["a", "mark applied"], ["h", "hide"], ["s", "change sort"], ["r", "refresh"], ["/", "edit keywords"]]
            .flatMap(([k, d]) => [h("dt", {}, h("kbd", {}, k)), h("dd", {}, d)])));
      return;
    }
    const status = st.statuses[j.key];
    const field = (k, v) => v ? [h("dt", {}, k), h("dd", {}, v)] : [];
    replace(detail,
      h("div", { class: "drawer-head" },
        h("h2", {}, j.new && h("span", { class: "star" }, "★ "), j.title || "(untitled)"),
        h("button", { type: "button", class: "ghost small close", onclick: () => select(-1), "aria-label": "Close details" }, "Close")),
      h("div", { class: "company" }, j.company || "unknown company"),
      h("div", { class: "actions" },
        h("button", { type: "button", onclick: () => openURL(j.url), disabled: !j.url }, "Open ", h("kbd", {}, "o")),
        h("button", { type: "button", class: "ghost", onclick: () => track("saved") }, "Save ", h("kbd", {}, "S")),
        h("button", { type: "button", class: "ghost", onclick: () => track("applied") }, "Applied ", h("kbd", {}, "a")),
        h("button", { type: "button", class: "ghost", onclick: hide }, "Hide ", h("kbd", {}, "h")),
        status && h("span", { class: "badge accent" }, `Tracked: ${statusLabel[status] || status}`)),
      h("dl", { class: "meta" },
        ...field("Score", String(Math.round(j.score || 0))),
        ...field("Location", location(j)),
        ...field("Countries", (j.countries || []).join(", ")),
        ...field("Work mode", j.work_mode),
        ...field("Type", j.job_type),
        ...field("Experience", j.experience),
        ...field("Areas", (j.areas || []).join(", ")),
        ...field("Salary", salary(j)),
        ...field("Visa", visa(j)),
        ...field("Posted", j.posted_at && `${dateTime(j.posted_at)} (${since(j.posted_at)} ago)`),
        ...field("First seen", j.first_seen_at && dateTime(j.first_seen_at)),
        ...field("Sources", (j.sources || [j.source]).join(", ")),
        ...field("Tags", (j.tags || []).join(", ")),
        ...field("Link", j.url && extLink(j.url, j.url)),
        ...field("Key", h("code", {}, j.key))),
      h("h3", {}, "Description"),
      h("div", { class: "description" }, j.description || "No description."));
  }

  function select(i) {
    st.sel = i >= 0 && i < st.jobs.length ? i : -1;
    if (st.sel >= st.shown) { st.shown = st.sel + 1; }
    renderList();
    renderDetail();
    if (st.sel >= 0) list.querySelector(`#job-${st.sel}`)?.scrollIntoView({ block: "nearest" });
  }

  // Rendering is batched while job events arrive.
  let frame = 0;
  function schedule() {
    if (frame) return;
    frame = requestAnimationFrame(() => {
      frame = 0;
      renderSummary();
      renderProgress();
      renderList();
    });
  }

  // ---- Actions ---------------------------------------------------------

  async function run({ refresh = false } = {}) {
    if (!form.reportValidity()) return;
    st.ctrl?.abort();
    const ctrl = new AbortController();
    const input = readInput();
    try { localStorage.setItem(LAST, JSON.stringify(input)); } catch { /* ignore */ }
    Object.assign(st, { ctrl, jobs: [], sel: -1, shown: PAGE, reports: [], run: null });
    progress.hidden = false;
    searchBtn.textContent = "Stop";
    renderDetail();
    schedule();
    const q = query(input, { refresh });

    // Side requests; a failure here shouldn't stop the search.
    api("GET", "/api/links?" + q, undefined, ctrl.signal).then(renderLinks, () => renderLinks([]));
    api("GET", "/api/tracker", undefined, ctrl.signal).then((rows) => {
      st.statuses = Object.fromEntries(rows.map((r) => [r.key, r.status]));
      schedule();
    }, () => {});
    api("GET", "/api/sources", undefined, ctrl.signal).then((rows) => {
      st.expected = rows.filter((r) => r.enabled).length;
      schedule();
    }, () => {});

    try {
      await stream("/api/events?" + q, (name, ev) => {
        if (name === "source") st.reports.push(ev.source);
        else if (name === "job") st.jobs.push(ev.job);
        else if (name === "run") st.run = ev.run;
        else if (name === "error") toast(`Search failed: ${ev.error}`, { error: true });
        schedule();
      }, ctrl.signal);
    } catch (err) {
      if (ctrl.signal.aborted) return;
      toast(err instanceof APIError ? err.message : `Search failed: ${err.message}`, { error: true });
    } finally {
      if (st.ctrl === ctrl) {
        st.ctrl = null;
        searchBtn.textContent = "Search";
        sortJobs();
        if (st.jobs.length && window.matchMedia("(min-width: 1101px)").matches) st.sel = 0;
        renderSummary();
        renderProgress();
        renderList();
        renderDetail();
      }
    }
  }

  function stop() {
    st.ctrl?.abort();
    st.ctrl = null;
    searchBtn.textContent = "Search";
    progressText.textContent = "Stopped.";
    sortJobs();
    renderList();
  }

  async function track(status) {
    const j = st.jobs[st.sel];
    if (!j) return;
    try {
      await api("PUT", `/api/tracker/${enc(j.key)}`, { status });
      st.statuses[j.key] = status;
      toast(`${statusLabel[status]}: ${j.title}`);
      renderList();
      renderDetail();
    } catch (err) {
      toast(`Couldn't update the tracker: ${err.message}`, { error: true });
    }
  }

  const hiding = new Set(); // keys with a hide request in flight

  async function hide() {
    const j = st.jobs[st.sel];
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
    // Find the row again: the list may have been re-sorted meanwhile.
    const i = st.jobs.findIndex((x) => x.key === j.key);
    if (i < 0) return;
    const wasSelected = i === st.sel;
    st.jobs.splice(i, 1);
    if (wasSelected) select(Math.min(i, st.jobs.length - 1));
    else { if (st.sel > i) st.sel--; renderList(); }
    toast(`Hidden: ${j.title}`, {
      action: "Undo",
      onAction: async () => {
        try {
          await api("DELETE", `/api/jobs/${enc(j.key)}/hide`);
          if (st.jobs.some((x) => x.key === j.key)) return;
          st.jobs.splice(Math.min(i, st.jobs.length), 0, j);
          select(Math.min(i, st.jobs.length - 1));
        } catch (err) {
          toast(`Couldn't unhide the job: ${err.message}`, { error: true });
        }
      },
    });
  }

  async function loadPresets(selected = "") {
    try {
      const ps = await api("GET", "/api/presets");
      replace(presetSel, h("option", { value: "" }, ps.length ? "Load a preset…" : "No presets yet"),
        ps.map((p) => h("option", { value: p.name }, p.name)));
      presetSel.value = selected;
      presetSel.onchange = () => {
        const p = ps.find((x) => x.name === presetSel.value);
        if (p) { writeInput(p.filters); presetName.value = p.name; run(); }
      };
    } catch (err) {
      toast(`Couldn't load presets: ${err.message}`, { error: true });
    }
  }

  async function savePreset() {
    const name = presetName.value.trim();
    if (!name || !presetName.reportValidity()) {
      presetName.focus();
      toast("Preset names use letters, digits, '.', '_' and '-'.", { error: true });
      return;
    }
    const { new_only, show_hidden, ...filters } = readInput(); // search options, not filters
    try {
      await api("PUT", `/api/presets/${enc(name)}`, filters);
      toast(`Saved preset ${name}`);
      loadPresets(name);
    } catch (err) {
      toast(`Couldn't save the preset: ${err.message}`, { error: true });
    }
  }

  // ---- Events ------------------------------------------------------------

  // On phones the filters fold away so results are on screen.
  function setFolded(folded) {
    form.classList.toggle("folded", folded);
    toggle.setAttribute("aria-expanded", String(!folded));
    toggle.textContent = folded ? "Show filters" : "Hide filters";
  }
  toggle.addEventListener("click", () => setFolded(!form.classList.contains("folded")));
  setFolded(narrow.matches);

  form.addEventListener("submit", (e) => {
    e.preventDefault();
    if (st.ctrl) { stop(); return; }
    if (narrow.matches) setFolded(true);
    run();
  });
  refreshBtn.addEventListener("click", () => run({ refresh: true }));
  sortSel.addEventListener("change", () => { st.sort = sortSel.value; sortJobs(); renderList(); });
  list.addEventListener("click", (e) => {
    const li = e.target.closest("li.job");
    if (li) select(Number(li.dataset.i));
  });
  list.addEventListener("dblclick", (e) => {
    if (e.target.closest("li.job")) openURL(st.jobs[st.sel]?.url);
  });

  function onKey(e) {
    if (e.ctrlKey || e.metaKey || e.altKey) return;
    if (e.key === "Escape" && !typing(e)) { select(-1); return; }
    if (typing(e) || document.querySelector(".drawer-backdrop")) return;
    const actions = {
      j: () => select(Math.min(st.sel + 1, st.jobs.length - 1)),
      k: () => select(Math.max(st.sel - 1, 0)),
      o: () => openURL(st.jobs[st.sel]?.url),
      S: () => track("saved"),
      a: () => track("applied"),
      h: hide,
      s: () => {
        st.sort = SORTS[(SORTS.indexOf(st.sort) + 1) % SORTS.length];
        sortSel.value = st.sort;
        sortJobs();
        renderList();
        toast(`Sorted by ${st.sort}`);
      },
      r: () => run({ refresh: true }),
      "/": () => { setFolded(false); kw.focus(); },
    };
    const fn = actions[e.key];
    if (fn) { e.preventDefault(); fn(); }
  }
  document.addEventListener("keydown", onKey);

  // ---- Start -------------------------------------------------------------

  let last = null;
  try { last = JSON.parse(localStorage.getItem(LAST) || "null"); } catch { /* ignore */ }
  writeInput(last || m.defaults);
  renderDetail();
  renderList();
  loadPresets();
  run();

  return () => {
    document.removeEventListener("keydown", onKey);
    st.ctrl?.abort();
    if (frame) cancelAnimationFrame(frame);
  };
}
