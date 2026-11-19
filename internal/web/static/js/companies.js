// Companies view: the companies list (`jobhunt companies`) with its
// filters, a details drawer, board adding and outreach drafts. Company
// records come from importers, agents and the open web, so every field is
// untrusted text: it reaches the page only through h() and extLink().
// jobhunt never sends email; a draft is text to copy or a mailto: link
// that opens the user's own mail client.
import { api, enc, token } from "./api.js";
import { h, icon, corners, extLink, replace, toast, typing } from "./dom.js";
import { ago, count, date, place } from "./format.js";
import * as shell from "./shell.js";

const STAGES = ["pre-seed", "seed", "series-a", "series-b", "series-c", "growth", "public", "bootstrapped"];
const ATS = ["greenhouse", "lever", "ashby", "workable", "recruitee"];
const WINDOWS = [["", "Any time"], ["90d", "Last 3 months"], ["180d", "Last 6 months"], ["270d", "Last 9 months"], ["365d", "Last year"]];
const FILTERS = "jobhunt.v1.companies";

const stageLabel = (s) => (!s || s === "unknown" ? "—" : s.replace("series-", "Series ").replace(/^./, (c) => c.toUpperCase()));

function money(r) {
  if (!r?.amount) return "";
  const v = r.amount;
  const n = v >= 1e9 ? `${(v / 1e9).toFixed(1)}B` : v >= 1e6 ? `${(v / 1e6).toFixed(1)}M` : v >= 1e3 ? `${Math.round(v / 1e3)}K` : String(v);
  return `${n} ${r.currency || ""}`.trim();
}

function where(c) {
  const p = [c.city, c.country && place(c.country)].filter(Boolean).join(", ");
  return c.remote ? (p ? `${p} · remote` : "remote") : p || "—";
}

// mailtoFrom rebuilds a mailto: link for the edited draft: the first line
// "Subject: …" is the subject, the rest the body. The recipient comes from
// the server's link.
function mailtoFrom(href, text) {
  const to = href.split("?")[0];
  let subject = "";
  let body = text;
  const m = /^Subject: ?(.*)\n\n?/.exec(text);
  if (m) { subject = m[1]; body = text.slice(m[0].length); }
  return `${to}?subject=${encodeURIComponent(subject)}&body=${encodeURIComponent(body)}`;
}

// mailtoHref accepts only the mailto: links the server builds.
function mailtoHref(u) {
  return typeof u === "string" && /^mailto:[^\s"<>]+$/.test(u) ? u : null;
}

export async function mount(view, { live = () => true } = {}) {
  let list = [];
  let drawer = null;
  let ctrl = null;
  const f = loadFilters();

  const q = h("input", { type: "search", class: "input", id: "co-q", placeholder: "Name, tag or note", value: f.q, "aria-label": "Search companies" });
  const stage = h("select", { class: "input", id: "co-stage", "aria-label": "Stage" },
    h("option", { value: "" }, "Any stage"), STAGES.map((s) => h("option", { value: s }, stageLabel(s))));
  stage.value = f.stage;
  const funded = h("select", { class: "input", id: "co-funded", "aria-label": "Funded within" },
    WINDOWS.map(([v, l]) => h("option", { value: v }, l)));
  funded.value = f.funded;
  const contact = toggle("Has contact", f.contact);
  const jobs = toggle("Open jobs", f.jobs);

  const tbody = h("tbody");
  const mlist = h("div", { class: "co-list" });
  const summary = h("p", { class: "muted" });
  const loadError = h("p", { class: "notice error", hidden: true, role: "alert" });
  const empty = h("div", { class: "empty-box", hidden: true },
    h("p", {}, "No companies match."),
    h("p", { class: "muted" }, "Import some with ", h("code", {}, "jobhunt companies sync"),
      ", let an agent add them through ", h("code", {}, "jobhunt mcp"), ", or add a job board below."));

  const addName = h("input", { class: "input", id: "add-company", required: true, maxlength: 120, placeholder: "Company" });
  const addATS = h("select", { class: "input", id: "add-ats", "aria-label": "ATS" }, ATS.map((a) => h("option", { value: a }, a)));
  const addSlug = h("input", { class: "input", id: "add-slug", required: true, maxlength: 100, placeholder: "board slug", pattern: "[A-Za-z0-9._-]+" });
  const addBtn = h("button", { type: "submit", class: "btn btn-secondary" }, "Add board");
  const addForm = h("form", { class: "co-add", "aria-label": "Add a job board" },
    h("span", { class: "label" }, "Add a job board"), addName, addATS, addSlug, addBtn);

  view.append(h("div", { class: "sources-view companies" }, h("div", { class: "sources-inner" },
    h("div", { class: "sources-title" },
      h("div", {},
        h("h2", {}, "Companies"),
        h("p", {}, "Startups and employers you're watching: funding with sources, published contacts and job boards. Drafts are yours to send.")),
    ),
    h("div", { class: "co-filters" }, q, stage, funded, contact, jobs),
    summary,
    loadError,
    h("table", { class: "table co-table" },
      h("thead", {}, h("tr", {},
        ["Company", "Stage", "Last round", "People", "Where", "Jobs", "Contacts"].map((c, i) =>
          h("th", { scope: "col", class: i >= 5 ? "num" : null }, c)))),
      tbody),
    mlist,
    empty,
    addForm)));

  function toggle(label, on) {
    return h("button", { type: "button", class: "chip", "aria-pressed": String(Boolean(on)) }, label);
  }
  const pressed = (b) => b.getAttribute("aria-pressed") === "true";

  function loadFilters() {
    const d = { q: "", stage: "", funded: "", contact: false, jobs: false };
    try { return { ...d, ...JSON.parse(localStorage.getItem(FILTERS) || "{}") }; } catch { return d; }
  }
  function saveFilters() {
    try {
      localStorage.setItem(FILTERS, JSON.stringify({ q: q.value, stage: stage.value, funded: funded.value, contact: pressed(contact), jobs: pressed(jobs) }));
    } catch { /* ignore */ }
  }

  function params() {
    const p = new URLSearchParams();
    if (q.value.trim()) p.set("q", q.value.trim());
    if (stage.value) p.set("stages", stage.value);
    if (funded.value) p.set("funded_within", funded.value);
    if (pressed(contact)) p.set("has_contact", "true");
    if (pressed(jobs)) p.set("hiring", "any"); // companies with open jobs at any level
    return p.toString();
  }

  async function load() {
    ctrl?.abort();
    const c = new AbortController();
    ctrl = c;
    try {
      const ps = params();
      list = await api("GET", "/api/companies" + (ps ? `?${ps}` : ""), undefined, c.signal);
      if (pending.size) list = list.filter((x) => !pending.has(x.key));
      loadError.hidden = true;
      render();
    } catch (err) {
      if (err.name === "AbortError") return;
      loadError.textContent = `Couldn't load companies: ${err.message}`;
      loadError.hidden = false;
    }
  }

  function render() {
    const last = (c) => c.rounds?.[0];
    replace(tbody, list.map((c) => h("tr", { class: "co-row", tabindex: 0, dataset: { key: c.key }, onclick: () => openDrawer(c.key) },
      h("td", {}, h("div", { class: "src-name" }, h("b", {}, c.name), h("span", {}, (c.tags || []).slice(0, 4).join(" · ")))),
      h("td", {}, stageLabel(c.stage)),
      h("td", { class: "td-mono" }, last(c) ? [money(last(c)), last(c).date && ` · ${date(last(c).date)}`] : "—"),
      h("td", { class: "td-mono" }, c.headcount && c.headcount !== "unknown" ? c.headcount : "—"),
      h("td", {}, where(c)),
      h("td", { class: "num td-mono" }, c.open_jobs ? count(c.open_jobs) : "—"),
      h("td", { class: "num td-mono" }, (c.contacts || []).length || "—"))));
    replace(mlist, list.map((c) => h("button", { type: "button", class: "co-card", onclick: () => openDrawer(c.key) },
      h("span", { class: "top" }, h("b", {}, c.name), h("span", { class: "td-mono" }, c.open_jobs ? `${c.open_jobs} ${c.open_jobs === 1 ? "job" : "jobs"}` : "")),
      h("span", { class: "muted" }, [stageLabel(c.stage), last(c) && money(last(c)), where(c)].filter((x) => x && x !== "—").join(" · ")))));
    empty.hidden = list.length > 0;
    summary.textContent = `${count(list.length)} ${list.length === 1 ? "company" : "companies"}${list.length >= 500 ? " (first 500; narrow the filters)" : ""}`;
    if (!live()) return;
    shell.setCount("companies", String(list.length));
    shell.setContext(`${list.length} shown`);
  }

  tbody.addEventListener("keydown", (e) => {
    const row = e.target.closest?.("tr.co-row");
    if (row && (e.key === "Enter" || e.key === " ")) { e.preventDefault(); openDrawer(row.dataset.key); }
  });

  let debounce = 0;
  q.addEventListener("input", () => { clearTimeout(debounce); debounce = setTimeout(() => { saveFilters(); load(); }, 250); });
  for (const s of [stage, funded]) s.addEventListener("change", () => { saveFilters(); load(); });
  for (const b of [contact, jobs]) {
    b.addEventListener("click", () => { b.setAttribute("aria-pressed", String(!pressed(b))); saveFilters(); load(); });
  }

  addForm.addEventListener("submit", async (e) => {
    e.preventDefault();
    addBtn.disabled = true;
    try {
      const c = await api("POST", "/api/boards", { company: addName.value.trim(), ats: addATS.value, slug: addSlug.value.trim() });
      toast(`Added ${addATS.value}:${addSlug.value.trim()} to ${c.name}`);
      addSlug.value = "";
      await load();
    } catch (err) {
      toast(err.message, { error: true });
    } finally {
      addBtn.disabled = false;
    }
  });

  // ---- Details drawer ------------------------------------------------------

  function setBackgroundInert(on) {
    const app = document.querySelector(".app");
    if (app) app.inert = on;
  }

  function closeDrawer() {
    if (!drawer) return;
    const { el, key } = drawer;
    drawer = null;
    el.remove();
    setBackgroundInert(false);
    document.removeEventListener("keydown", onDrawerKey);
    tbody.querySelector(`tr[data-key="${CSS.escape(key)}"]`)?.focus();
  }

  function onDrawerKey(e) {
    if (e.key === "Escape") { e.preventDefault(); closeDrawer(); }
  }

  function openDrawer(key) {
    closeDrawer();
    const body = h("div", { class: "drawer co-drawer", role: "dialog", "aria-modal": "true", "aria-labelledby": "drawer-title", tabindex: -1 },
      h("p", { class: "loading" }, "Loading…"));
    const el = h("div", { class: "drawer-backdrop" }, body);
    el.addEventListener("click", (e) => { if (e.target === el) closeDrawer(); });
    document.body.append(el);
    setBackgroundInert(true);
    document.addEventListener("keydown", onDrawerKey);
    drawer = { el, key };
    body.focus();
    fill(body, key);
  }

  async function fill(body, key) {
    let c;
    try {
      c = await api("GET", `/api/companies/${enc(key)}`);
    } catch (err) {
      replace(body, h("p", { class: "notice error" }, `Couldn't load this company: ${err.message}`));
      return;
    }
    if (drawer?.key !== key) return;
    const notes = h("textarea", { id: "co-notes", class: "input", rows: 5, maxlength: 20000, "aria-label": "Notes" });
    notes.value = c.notes || "";
    const draftBox = h("div", { class: "co-draft" });

    replace(body,
      h("div", { class: "drawer-head" },
        h("h2", { id: "drawer-title" }, c.name),
        h("button", { type: "button", class: "btn btn-ghost btn-icon", "aria-label": "Close", onclick: closeDrawer }, icon("x"))),
      h("div", { class: "row" },
        c.website && extLink(c.website, icon("arrow-up-right"), " ", hostOf(c.website)),
        (c.tags || []).map((t) => h("span", { class: "tag tag-outline" }, t))),
      h("div", { class: "plate" },
        cell("Stage", stageLabel(c.stage)),
        cell("People", c.headcount && c.headcount !== "unknown" ? c.headcount : "—",
          c.headcount_source && c.headcount_source !== "estimate" ? extLink(c.headcount_source, "source") : c.headcount_source),
        cell("Where", where(c)),
        cell("Open jobs", c.open_jobs ? count(c.open_jobs) : "—"),
        cell("Found by", (c.sources || []).join(", ") || "—"),
        cell("Updated", c.updated_at ? ago(c.updated_at) : "—")),
      section("Funding", (c.rounds || []).length
        ? h("ul", { class: "timeline" }, c.rounds.map((r) => h("li", {},
          h("span", { class: "when" }, r.date ? date(r.date) : "date unknown"),
          h("span", { class: "what" }, [stageLabel(r.stage), money(r)].filter((x) => x && x !== "—").join(" · ") || "round", " ",
            extLink(r.source_url, "source")))))
        : h("p", { class: "muted" }, "No rounds recorded.")),
      section("Contacts", (c.contacts || []).length
        ? h("ul", { class: "timeline" }, c.contacts.map((ct) => h("li", {},
          h("span", { class: "when" }, ct.role || "contact"),
          h("span", { class: "what" }, [ct.name, ct.email].filter(Boolean).join(" · "), " ",
            ct.url && extLink(ct.url, "profile"), " ", extLink(ct.source_url, "where published")))))
        : h("p", { class: "muted" }, "No published contacts. jobhunt only stores contacts a person published themselves.")),
      section("Job boards", (c.boards || []).length
        ? h("div", { class: "row" }, c.boards.map((b) => h("span", { class: "tag tag-neutral mono" }, `${b.ats}:${b.slug}`)))
        : h("p", { class: "muted" }, "No boards. Add one below the list to include its jobs in searches.")),
      (c.jobs || []).length > 0 && section("Open jobs", h("ul", { class: "timeline" }, c.jobs.slice(0, 20).map((j) => h("li", {},
        h("span", { class: "when" }, j.posted_at ? date(j.posted_at) : ""),
        h("span", { class: "what" }, extLink(j.url, j.title), " ",
          h("button", { type: "button", class: "btn btn-ghost", onclick: () => showDraft(draftBox, c.key, j.key) }, icon("mail"), "Draft")))))),
      section("Notes", notes,
        h("div", { class: "row" },
          h("button", { type: "button", class: "btn btn-secondary", onclick: () => saveNotes(c, notes.value) }, "Save notes"),
          h("button", { type: "button", class: "btn btn-secondary", onclick: () => markChecked(c) }, icon("check"), "Mark checked"))),
      section("Outreach", h("p", { class: "muted" }, "A starting draft for you to edit and send yourself. jobhunt never sends email."),
        h("button", { type: "button", class: "btn btn-primary", onclick: () => showDraft(draftBox, c.key) }, icon("mail"), "Draft an email"),
        draftBox),
      h("div", { class: "detail-foot" },
        h("span", {}, `key ${c.key}`),
        c.checked_at && h("span", {}, `checked ${ago(c.checked_at)}`),
        h("button", { type: "button", class: "btn btn-ghost danger", onclick: () => remove(c) }, icon("trash-2"), "Delete")));
  }

  function cell(label, value, extra) {
    return h("div", { class: "plate-cell" }, h("span", { class: "label" }, label), h("span", { class: "value" }, value), extra && h("span", { class: "muted" }, extra));
  }

  function section(title, ...children) {
    return h("section", { class: "co-section" }, h("h5", {}, title), ...children);
  }

  function hostOf(u) {
    try { return new URL(u).hostname.replace(/^www\./, ""); } catch { return u; }
  }

  async function showDraft(box, key, job) {
    try {
      const d = await api("GET", `/api/companies/${enc(key)}/draft${job ? `?job=${enc(job)}` : ""}`);
      const text = h("textarea", { class: "input", rows: 12, "aria-label": "Draft email" });
      text.value = `Subject: ${d.subject}\n\n${d.body}`;
      const href = mailtoHref(d.mailto);
      const mail = href && h("a", { class: "btn btn-secondary" }, icon("mail"), "Open in mail app");
      if (mail) {
        mail.href = href;
        // Open what's in the box now, edits included.
        mail.addEventListener("click", () => { mail.href = mailtoFrom(href, text.value); });
      }
      replace(box,
        h("p", { class: "muted" }, d.to
          ? ["To ", h("b", {}, d.to_name ? `${d.to_name} <${d.to}>` : d.to), " · ", extLink(d.source_url, "where they published it")]
          : "No published email for this company: find the right person yourself, or apply through a job board."),
        text,
        h("div", { class: "row" },
          h("button", {
            type: "button", class: "btn btn-secondary", onclick: async () => {
              try { await navigator.clipboard.writeText(text.value); toast("Draft copied"); } catch { text.select(); toast("Select and copy the draft", { error: true }); }
            },
          }, icon("copy"), "Copy"),
          mail));
      text.focus();
    } catch (err) {
      toast(`Couldn't draft: ${err.message}`, { error: true });
    }
  }

  async function update(c, body, msg) {
    try {
      await api("PUT", `/api/companies/${enc(c.key)}`, body);
      toast(msg);
      await load();
      if (drawer?.key === c.key) fill(drawer.el.firstElementChild, c.key);
    } catch (err) {
      toast(err.message, { error: true });
    }
  }
  // An empty notes field means "unchanged" to the API, so clearing says so.
  const saveNotes = (c, notes) => update(c, notes.trim() ? { notes } : { remove: { notes: true } }, notes.trim() ? "Notes saved" : "Notes cleared");
  const markChecked = (c) => update(c, { checked: true }, "Marked checked");

  // Deleting waits while the Undo toast is up: the row disappears at once,
  // and the DELETE goes out when the toast expires, the view closes or the
  // page unloads. Undo just cancels it, so nothing has to be recreated
  // (re-adding boards would need each to pass a live check again). Each
  // delete has its own timer and toast; sending one early takes its Undo
  // button down with it.
  const UNDO_MS = 10000;
  const pending = new Map(); // key -> { key, name, timer, note }

  function remove(c) {
    if (pending.has(c.key)) return;
    closeDrawer();
    const p = { key: c.key, name: c.name };
    pending.set(c.key, p);
    p.timer = setTimeout(() => flushDelete(c.key), UNDO_MS);
    list = list.filter((x) => x.key !== c.key);
    render();
    p.note = toast(`Deleted ${c.name}`, {
      action: "Undo", ms: UNDO_MS, onAction: () => {
        if (pending.get(c.key) !== p) return;
        clearTimeout(p.timer);
        pending.delete(c.key);
        load();
      },
    });
  }

  function flushAll(keepalive = false) {
    for (const key of [...pending.keys()]) flushDelete(key, keepalive);
  }

  // flushDelete sends one pending DELETE now. keepalive lets it finish
  // while the page unloads.
  async function flushDelete(key, keepalive = false) {
    const p = pending.get(key);
    if (!p) return;
    clearTimeout(p.timer);
    pending.delete(key);
    p.note?.remove(); // its Undo can't work any more
    try {
      if (keepalive) {
        fetch(`/api/companies/${enc(p.key)}`, { method: "DELETE", keepalive: true, headers: { Authorization: `Bearer ${token()}` } });
        return;
      }
      await api("DELETE", `/api/companies/${enc(p.key)}`);
    } catch (err) {
      toast(`Couldn't delete ${p.name}: ${err.message}`, { error: true });
      if (live()) load();
    }
  }
  const onPageHide = () => flushAll(true);
  window.addEventListener("pagehide", onPageHide);

  function onKey(e) {
    if (drawer || typing(e) || e.ctrlKey || e.metaKey || e.altKey) return;
    if (e.key === "/") { e.preventDefault(); q.focus(); }
  }
  document.addEventListener("keydown", onKey);

  shell.setFetch(load, "Reload");
  shell.setStatus("COMPANIES", [["/", "search"], ["↵", "details"], ["1–4", "tabs"], ["t", "theme"]]);
  await load();

  return () => {
    flushAll();
    window.removeEventListener("pagehide", onPageHide);
    clearTimeout(debounce);
    ctrl?.abort();
    document.removeEventListener("keydown", onKey);
    closeDrawer();
  };
}
