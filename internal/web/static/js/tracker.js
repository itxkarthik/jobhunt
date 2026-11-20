// Tracker view: the application pipeline. A funnel shows how far jobs
// got; cards move between columns by drag and drop, the "Next →" button,
// or ← and → on a focused card. On phones, stage tabs pick one column.
// Changes go through PUT /api/tracker/{key}, so `jobhunt tracker` sees
// them too.
import { api, meta, enc } from "./api.js";
import { h, icon, replace, extLink, safeURL, toast, typing } from "./dom.js";
import { dateTime, shortDate, location, statusLabel, count } from "./format.js";
import * as shell from "./shell.js";

// The stage after each one, for the "Next →" button. Offer and Rejected
// are where a job ends.
const NEXT = { saved: "applied", applied: "oa", oa: "interview", interview: "offer" };
let stage = null; // the phone's current stage tab; kept across visits

// Moves are optimistic. One card's requests go out one at a time, in
// order, so the server ends on the last choice; only the reply to the
// latest move updates the card, and a failure reverts it to the last
// status the server confirmed. Removing a card cancels its queued moves
// (see untrack), so none can re-create the row after the DELETE.
//
// The queue outlives a visit to the tab: requests keep going after the
// user leaves, and a later visit must still be able to cancel them.
// Replies go to whichever board is mounted then, through hooks.
const moves = new Map(); // key -> { chain, seq, confirmed, cancelled }
let hooks = null;        // { row(key), render(), refreshDrawer(key) } of the mounted board

// apply copies an API reply into a row. Fields the API omits when empty
// must be cleared, not left at their old values.
function apply(r, reply) {
  Object.assign(r, { notes: "", applied_at: null }, reply);
}

export async function mount(view, { live = () => true } = {}) {
  const m = await meta();
  if (!live()) return null; // the user left while this was loading
  const statuses = m.options.statuses;
  let rows = [];
  let drawer = null; // the open details drawer, if any

  const filter = h("input", { type: "search", class: "input", placeholder: "Filter cards", "aria-label": "Filter cards" });
  const funnel = h("div", { class: "funnel" });
  const tabs = h("div", { class: "stage-tabs", role: "tablist", "aria-label": "Stages" });
  const board = h("div", { class: "board" });
  const loadError = h("p", { class: "notice error", hidden: true, role: "alert" });
  const root = h("div", { class: "tracker" },
    h("div", { class: "pipeline-head" },
      h("div", { class: "pipeline-title" },
        h("h2", {}, "Pipeline"),
        h("span", { class: "hint mono" }, "drag cards between columns · or press s / a on a job in Search"),
        filter),
      loadError,
      funnel),
    tabs,
    board);
  root.style.setProperty("--stages", String(statuses.length));
  view.append(root);

  const columns = Object.fromEntries(statuses.map((s) => {
    const list = h("ol", { "aria-label": statusLabel[s] || s });
    const n = h("span", { class: "n" });
    const col = h("section", { class: "column", dataset: { status: s } },
      h("div", { class: "column-head" }, h("span", { class: "name" }, statusLabel[s] || s), n), list);
    col.addEventListener("dragover", (e) => {
      if (!e.dataTransfer.types.includes("application/x-jobhunt-key")) return;
      e.preventDefault();
      e.dataTransfer.dropEffect = "move";
      col.classList.add("over");
    });
    col.addEventListener("dragleave", (e) => {
      if (!col.contains(e.relatedTarget)) col.classList.remove("over");
    });
    col.addEventListener("drop", (e) => {
      e.preventDefault();
      col.classList.remove("over");
      const key = e.dataTransfer.getData("application/x-jobhunt-key");
      if (key) move(key, s);
    });
    board.append(col);
    return [s, { col, list, n }];
  }));

  const title = (r) => r.job?.title || `Job ${r.key}`;
  const company = (r) => (r.job ? r.job.company || "unknown company" : "No longer in the job cache");
  const firstLine = (s) => String(s || "").split("\n").map((l) => l.trim()).find(Boolean) || "";

  function card(r) {
    const next = NEXT[r.status];
    const when = r.status !== "saved" && r.applied_at ? r.applied_at : r.updated_at;
    const note = firstLine(r.notes);
    const score = Math.round(r.job?.score || 0);
    const li = h("li", {
      class: "tcard", draggable: "true", tabindex: 0, dataset: { key: r.key },
      "aria-label": `${title(r)} at ${company(r)}, ${statusLabel[r.status]}. Enter for details, left or right arrow to move.`,
    },
      h("span", { class: "tcard-meta" },
        h("span", { class: "company", title: r.job ? `${company(r)} · ${location(r.job)}` : null }, company(r)),
        h("span", { class: "date", title: dateTime(when) }, shortDate(when))),
      h("span", { class: "tcard-title" }, title(r)),
      r.unlisted && h("span", {
        class: "tag tag-outline", title: "This posting hasn't shown up in your searches for 2 weeks; it has probably closed.",
      }, `not listed since ${shortDate(r.listed_at)}`),
      note && h("span", { class: "tcard-next", title: "First line of your notes" }, icon("calendar"), h("span", {}, note)),
      h("span", { class: "tcard-foot" },
        h("span", { class: "score-n" }, score ? `score ${score}` : ""),
        next && h("button", {
          type: "button", class: "advance", title: `Move to ${statusLabel[next]}`,
          onclick: (e) => { e.stopPropagation(); move(r.key, next); },
        }, h("span", { class: "short" }, `${statusLabel[next]} →`), h("span", { class: "long" }, `Move to ${statusLabel[next]} →`))));
    li.addEventListener("dragstart", (e) => {
      e.dataTransfer.setData("application/x-jobhunt-key", r.key);
      e.dataTransfer.effectAllowed = "move";
      li.classList.add("dragging");
    });
    li.addEventListener("dragend", () => li.classList.remove("dragging"));
    li.addEventListener("click", (e) => { if (!e.target.closest("button, a")) openDrawer(r.key); });
    li.addEventListener("keydown", (e) => {
      if (e.target !== li) return; // keys on the button act normally
      const i = statuses.indexOf(r.status);
      if (e.key === "ArrowRight" && i < statuses.length - 1) { e.preventDefault(); move(r.key, statuses[i + 1]); }
      else if (e.key === "ArrowLeft" && i > 0) { e.preventDefault(); move(r.key, statuses[i - 1]); }
      else if (e.key === "Enter") { e.preventDefault(); openDrawer(r.key); }
    });
    return li;
  }

  function renderFunnel() {
    const total = rows.length || 1;
    const order = statuses.filter((s) => s !== "rejected");
    replace(funnel, statuses.map((s, i) => {
      const n = rows.filter((r) => r.status === s).length;
      // "Reached": jobs at this stage or past it (rejected ones can't be placed).
      const at = order.indexOf(s);
      const reached = rows.filter((r) => at >= 0 && order.indexOf(r.status) >= at).length;
      const bar = h("i");
      bar.style.width = `${Math.round((n / total) * 100)}%`;
      return h("div", { class: "funnel-cell" },
        h("span", { class: "label" }, h("span", {}, statusLabel[s] || s), h("span", {}, String(i + 1).padStart(2, "0"))),
        h("span", { class: "n" }, String(n)),
        h("span", { class: "rate" }, s === "saved" || s === "rejected" || !rows.length ? "" : `${Math.round((reached / total) * 100)}% reached`),
        h("span", { class: "bar" }, bar));
    }));
  }

  function renderTabs() {
    if (!stage || !statuses.includes(stage)) {
      stage = statuses.find((s) => rows.some((r) => r.status === s)) || statuses[0];
    }
    replace(tabs, statuses.map((s) => h("button", {
      type: "button", role: "tab", "aria-selected": String(s === stage),
      onclick: () => { stage = s; render(); },
    }, statusLabel[s] || s, " ", h("span", { class: "n" }, String(rows.filter((r) => r.status === s).length)))));
  }

  // render redraws the board, keeping focus on the card that had it.
  function render() {
    const focused = document.activeElement?.closest?.("li.tcard")?.dataset.key;
    const q = filter.value.trim().toLowerCase();
    const shown = q
      ? rows.filter((r) => `${title(r)} ${company(r)} ${r.notes || ""}`.toLowerCase().includes(q))
      : rows;
    renderFunnel();
    renderTabs();
    for (const s of statuses) {
      const inCol = shown.filter((r) => r.status === s);
      const c = columns[s];
      c.col.classList.toggle("current", s === stage);
      c.n.textContent = String(inCol.length);
      replace(c.list, inCol.length ? inCol.map(card)
        : h("li", { class: "empty-box" }, q ? "No cards match." : "Drop a card here"));
    }
    if (live()) shell.setContext(q ? `${count(shown.length)} of ${count(rows.length)} tracked` : `${count(rows.length)} tracked`);
    if (live()) shell.setCount("tracker", String(rows.filter((r) => r.status !== "rejected").length));
    if (focused) focusCard(focused);
  }

  function focusCard(key) {
    board.querySelector(`li.tcard[data-key="${CSS.escape(key)}"]`)?.focus();
  }

  async function load() {
    try {
      rows = await api("GET", "/api/tracker");
      loadError.hidden = true;
      render();
    } catch (err) {
      loadError.textContent = `Couldn't load the tracker: ${err.message}`;
      loadError.hidden = false;
    }
  }

  const changed = () => window.dispatchEvent(new Event("jobhunt:tracker"));

  function move(key, status) {
    const r = rows.find((x) => x.key === key);
    if (!r || r.status === status) return;
    let mv = moves.get(key);
    if (!mv) {
      mv = { chain: Promise.resolve(), seq: 0, confirmed: r.status };
      moves.set(key, mv);
    }
    const seq = ++mv.seq;
    r.status = status;
    render();
    mv.chain = mv.chain.then(async () => {
      if (mv.cancelled) return;
      const latest = () => seq === mv.seq;
      try {
        const reply = await api("PUT", `/api/tracker/${enc(key)}`, { status });
        mv.confirmed = reply.status;
        if (latest()) {
          const row = hooks?.row(key);
          if (row) apply(row, reply);
          hooks?.refreshDrawer(key);
        }
      } catch (err) {
        if (latest()) {
          const row = hooks?.row(key);
          if (row) row.status = mv.confirmed;
          toast(`Couldn't move the card: ${err.message}`, { error: true });
        }
      }
      if (latest() && !mv.cancelled) {
        moves.delete(key);
        hooks?.render();
        changed();
      }
    });
  }

  // untrack stops tracking key. Its queued moves are dropped and any
  // request already sent finishes first, so the DELETE is the last word.
  async function untrack(key) {
    const mv = moves.get(key);
    if (mv) {
      mv.cancelled = true;
      await mv.chain;
      moves.delete(key);
      const r = rows.find((x) => x.key === key);
      if (r) r.status = mv.confirmed; // what the server has, should the DELETE fail
    }
    await api("DELETE", `/api/tracker/${enc(key)}`);
  }

  // ---- Details drawer --------------------------------------------------

  // While the drawer is open the page behind it is inert, so keyboard
  // focus can't reach controls it covers. Toasts stay usable.
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
    focusCard(key);
  }

  function onDrawerKey(e) {
    if (e.key === "Escape") { e.preventDefault(); closeDrawer(); }
  }

  function openDrawer(key) {
    closeDrawer();
    const body = h("div", { class: "drawer", role: "dialog", "aria-modal": "true", "aria-labelledby": "drawer-title", tabindex: -1 });
    const el = h("div", { class: "drawer-backdrop" }, body);
    el.addEventListener("click", (e) => { if (e.target === el) closeDrawer(); });
    document.body.append(el);
    setBackgroundInert(true);
    document.addEventListener("keydown", onDrawerKey);
    drawer = { el, key, refresh: () => fill(body, key) };
    fill(body, key).then(() => body.querySelector("textarea")?.focus());
  }

  async function fill(body, key) {
    const r = rows.find((x) => x.key === key);
    if (!r) { closeDrawer(); return; }
    const sel = h("select", { id: "drawer-status", class: "input" }, statuses.map((s) => h("option", { value: s }, statusLabel[s] || s)));
    sel.value = r.status;
    sel.addEventListener("change", () => move(key, sel.value));
    // A refresh (e.g. after a status change) keeps unsaved notes and focus.
    const draft = body.querySelector("#drawer-notes")?.value;
    const activeId = body.contains(document.activeElement) ? document.activeElement.id : "";
    const notes = h("textarea", { id: "drawer-notes", class: "input", maxlength: 20000, rows: 6, placeholder: "Next step on the first line, e.g. “Tech round Thu 21 Nov”. Contacts, links…" });
    notes.value = draft ?? (r.notes || "");
    const saveNotes = h("button", { type: "button", class: "btn btn-primary" }, "Save notes");
    saveNotes.addEventListener("click", async () => {
      saveNotes.disabled = true;
      try {
        // Take only the notes: a stage move may have overtaken this request,
        // and its status in the reply could be the old one.
        const reply = await api("PUT", `/api/tracker/${enc(key)}/notes`, { notes: notes.value });
        r.notes = reply.notes || "";
        r.updated_at = reply.updated_at;
        toast("Notes saved");
        render();
      } catch (err) {
        toast(`Couldn't save notes: ${err.message}`, { error: true });
      } finally {
        saveNotes.disabled = false;
      }
    });
    notes.addEventListener("keydown", (e) => {
      if (e.key === "Enter" && (e.ctrlKey || e.metaKey)) { e.preventDefault(); saveNotes.click(); }
    });

    const history = h("ol", { class: "timeline" }, h("li", { class: "muted" }, "Loading…"));
    const removeBox = h("div", { class: "row" });
    const askRemove = () => replace(removeBox,
      h("span", {}, "Stop tracking this job? Its notes go; its history is kept."),
      h("button", { type: "button", class: "btn btn-secondary danger", onclick: remove }, "Remove"),
      h("button", { type: "button", class: "btn btn-ghost", onclick: resetRemove }, "Cancel"));
    const resetRemove = () => replace(removeBox,
      h("button", { type: "button", class: "btn btn-secondary danger", onclick: askRemove }, icon("trash-2"), "Remove from tracker"));
    async function remove(e) {
      e.currentTarget.disabled = true;
      try {
        await untrack(key);
        rows = rows.filter((x) => x.key !== key);
        closeDrawer();
        render();
        changed();
        toast(`Removed: ${title(r)}`);
      } catch (err) {
        toast(`Couldn't remove it: ${err.message}`, { error: true });
        render();
        resetRemove();
      }
    }
    resetRemove();

    const cell = (k, v) => v && h("div", { class: "plate-cell" }, h("span", { class: "label" }, k), h("span", { class: "value" }, v));
    replace(body,
      h("div", { class: "drawer-head" },
        h("h2", { id: "drawer-title" }, title(r)),
        h("button", { type: "button", class: "btn btn-secondary btn-icon", onclick: closeDrawer, "aria-label": "Close" }, icon("x"))),
      h("div", { class: "detail-kicker" }, r.job ? `${company(r)} — ${location(r.job)}` : company(r)),
      safeURL(r.job?.url) && (() => {
        const a = extLink(r.job.url, "Open the posting ", icon("arrow-up-right"));
        a.className = "btn btn-secondary";
        return h("div", { class: "row" }, a);
      })(),
      h("div", { class: "plate" },
        cell("Tracked since", dateTime(r.created_at)),
        cell("Applied", r.applied_at && dateTime(r.applied_at)),
        cell("Updated", dateTime(r.updated_at)),
        cell("Last listed", r.listed_at && dateTime(r.listed_at)),
        cell("Key", r.key)),
      h("div", { class: "field" }, h("label", { for: "drawer-status" }, "Stage"), sel),
      h("div", { class: "field" }, h("label", { for: "drawer-notes" }, "Notes"), notes),
      h("div", { class: "row" }, saveNotes, h("span", { class: "muted mono" }, "Ctrl+Enter")),
      h("h4", {}, "History"), history,
      removeBox);
    if (activeId) body.querySelector(`#${CSS.escape(activeId)}`)?.focus();

    try {
      const evs = await api("GET", `/api/tracker/${enc(key)}/events`);
      replace(history, evs.length ? evs.map((e) => h("li", {},
        h("span", { class: "when" }, dateTime(e.at)),
        h("span", { class: "what" }, h("strong", {}, statusLabel[e.status] || e.status), e.note ? ` · ${e.note}` : "")))
        : h("li", { class: "muted" }, "No history yet."));
    } catch (err) {
      replace(history, h("li", { class: "muted" }, `Couldn't load the history: ${err.message}`));
    }
  }

  filter.addEventListener("input", render);
  function onKey(e) {
    if (e.key === "/" && !typing(e) && !drawer && !e.ctrlKey && !e.metaKey && !e.altKey) { e.preventDefault(); filter.focus(); }
  }
  document.addEventListener("keydown", onKey);

  const mine = {
    row: (key) => rows.find((x) => x.key === key),
    render,
    refreshDrawer: (key) => { if (drawer?.key === key) drawer.refresh(); },
  };
  hooks = mine;
  shell.setFetch(load, "Reload");
  shell.setStatus("TRACK", [["drag", "move card"], ["← →", "move focused card"], ["enter", "details"], ["/", "filter"], ["1–4", "tabs"]]);
  await load();

  return () => {
    if (hooks === mine) hooks = null; // a late cleanup mustn't unhook a newer board
    closeDrawer();
    document.removeEventListener("keydown", onKey);
  };
}
