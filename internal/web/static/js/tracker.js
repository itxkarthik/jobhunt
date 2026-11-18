// Tracker view: a kanban board of tracked jobs. Cards move between
// statuses by drag and drop, by the "Move to" menu, or with ← and → when
// a card has focus. Changes go through PUT /api/tracker/{key}, so the CLI
// (`jobhunt tracker`) sees them too.
import { api, meta, enc } from "./api.js";
import { h, replace, extLink, toast, typing } from "./dom.js";
import { since, date, dateTime, location, statusLabel } from "./format.js";

export async function mount(main) {
  const m = await meta();
  const statuses = m.options.statuses;
  let rows = [];
  let drawer = null; // the open details drawer, if any

  const filter = h("input", { type: "search", placeholder: "Filter cards", "aria-label": "Filter cards", class: "grow" });
  const count = h("span", { class: "muted" });
  const board = h("div", { class: "board" });
  main.append(
    h("div", { class: "board-head" },
      h("h1", {}, "Tracker"), count,
      h("span", { class: "muted grow" }, "Drag cards between columns, or focus one and press ← →."),
      filter,
      h("button", { type: "button", class: "ghost", onclick: load }, "Refresh")),
    board);

  const columns = Object.fromEntries(statuses.map((s) => {
    const list = h("ol", { "aria-label": statusLabel[s] || s });
    const n = h("span", { class: "muted" });
    const col = h("section", { class: "column", dataset: { status: s } },
      h("h2", {}, h("span", {}, statusLabel[s] || s), n), list);
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
    return [s, { list, n }];
  }));

  const title = (r) => r.job?.title || `Job ${r.key}`;
  const company = (r) => (r.job ? `${r.job.company || "unknown company"} · ${location(r.job)}` : "No longer in the job cache");

  function card(r) {
    const sel = h("select", { "aria-label": `Move "${title(r)}" to`, class: "grow" },
      statuses.map((s) => h("option", { value: s }, statusLabel[s] || s)));
    sel.value = r.status;
    sel.addEventListener("change", () => move(r.key, sel.value, { focus: true }));
    const when = r.status !== "saved" && r.applied_at
      ? `applied ${date(r.applied_at)}`
      : `updated ${since(r.updated_at)} ago`;
    const li = h("li", {
      class: "tcard card", draggable: "true", tabindex: 0, dataset: { key: r.key },
      "aria-label": `${title(r)}, ${statusLabel[r.status]}. Press Enter for details, left or right arrow to move.`,
    },
      h("div", { class: "title" }, title(r)),
      h("div", { class: "sub clamp", title: company(r) }, company(r)),
      h("div", { class: "sub" }, when),
      r.notes && h("div", { class: "note" }, r.notes),
      h("div", { class: "controls" }, sel,
        h("button", { type: "button", class: "ghost small", onclick: () => openDrawer(r.key) }, "Details")));
    li.addEventListener("dragstart", (e) => {
      e.dataTransfer.setData("application/x-jobhunt-key", r.key);
      e.dataTransfer.effectAllowed = "move";
      li.classList.add("dragging");
    });
    li.addEventListener("dragend", () => li.classList.remove("dragging"));
    li.addEventListener("keydown", (e) => {
      if (e.target !== li) return; // keys in the select or button act normally
      const i = statuses.indexOf(r.status);
      if (e.key === "ArrowRight" && i < statuses.length - 1) { e.preventDefault(); move(r.key, statuses[i + 1], { focus: true }); }
      else if (e.key === "ArrowLeft" && i > 0) { e.preventDefault(); move(r.key, statuses[i - 1], { focus: true }); }
      else if (e.key === "Enter") { e.preventDefault(); openDrawer(r.key); }
    });
    return li;
  }

  function render() {
    const q = filter.value.trim().toLowerCase();
    const shown = q
      ? rows.filter((r) => `${title(r)} ${company(r)} ${r.notes || ""}`.toLowerCase().includes(q))
      : rows;
    count.textContent = q ? `${shown.length} of ${rows.length} jobs` : `${rows.length} jobs`;
    for (const s of statuses) {
      const inCol = shown.filter((r) => r.status === s);
      columns[s].n.textContent = String(inCol.length);
      replace(columns[s].list, inCol.length ? inCol.map(card) : h("li", { class: "empty" }, "Nothing here"));
    }
  }

  function focusCard(key) {
    board.querySelector(`li.tcard[data-key="${CSS.escape(key)}"]`)?.focus();
  }

  async function load() {
    try {
      rows = await api("GET", "/api/tracker");
      render();
    } catch (err) {
      replace(board, h("p", { class: "notice error" }, `Couldn't load the tracker: ${err.message}`));
    }
  }

  async function move(key, status, { focus = false } = {}) {
    const r = rows.find((x) => x.key === key);
    if (!r || r.status === status) return;
    const before = r.status;
    r.status = status; // optimistic; reverted on failure
    render();
    if (focus) focusCard(key);
    try {
      Object.assign(r, await api("PUT", `/api/tracker/${enc(key)}`, { status }));
      if (drawer?.key === key) drawer.refresh();
    } catch (err) {
      r.status = before;
      toast(`Couldn't move the card: ${err.message}`, { error: true });
    }
    render();
    if (focus) focusCard(key);
  }

  // ---- Details drawer ----------------------------------------------------

  function closeDrawer() {
    if (!drawer) return;
    const { el, key } = drawer;
    drawer = null;
    el.remove();
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
    document.addEventListener("keydown", onDrawerKey);
    drawer = { el, key, refresh: () => fill(body, key) };
    fill(body, key).then(() => body.querySelector("textarea")?.focus());
  }

  async function fill(body, key) {
    const r = rows.find((x) => x.key === key);
    if (!r) { closeDrawer(); return; }
    const sel = h("select", { id: "drawer-status" }, statuses.map((s) => h("option", { value: s }, statusLabel[s] || s)));
    sel.value = r.status;
    sel.addEventListener("change", () => move(key, sel.value));
    const notes = h("textarea", { id: "drawer-notes", maxlength: 20000, rows: 6, placeholder: "Contacts, interview dates, links…" });
    notes.value = r.notes || "";
    const saveNotes = h("button", { type: "button" }, "Save notes");
    saveNotes.addEventListener("click", async () => {
      saveNotes.disabled = true;
      try {
        Object.assign(r, await api("PUT", `/api/tracker/${enc(key)}/notes`, { notes: notes.value }));
        if (r.notes == null) r.notes = ""; // omitted when empty
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
    const removeBox = h("div", { class: "confirm" });
    const askRemove = () => replace(removeBox,
      h("span", {}, "Stop tracking this job? Its history is kept."),
      h("button", { type: "button", class: "danger", onclick: remove }, "Remove"),
      h("button", { type: "button", class: "ghost", onclick: resetRemove }, "Cancel"));
    const resetRemove = () => replace(removeBox,
      h("button", { type: "button", class: "danger", onclick: askRemove }, "Remove from tracker"));
    async function remove() {
      try {
        await api("DELETE", `/api/tracker/${enc(key)}`);
        rows = rows.filter((x) => x.key !== key);
        closeDrawer();
        render();
        toast(`Removed: ${title(r)}`);
      } catch (err) {
        toast(`Couldn't remove it: ${err.message}`, { error: true });
      }
    }
    resetRemove();

    const field = (k, v) => (v ? [h("dt", {}, k), h("dd", {}, v)] : []);
    replace(body,
      h("div", { class: "drawer-head" },
        h("h2", { id: "drawer-title" }, title(r)),
        h("button", { type: "button", class: "ghost small", onclick: closeDrawer, "aria-label": "Close" }, "Close")),
      h("div", { class: "muted" }, company(r)),
      h("dl", { class: "meta" },
        ...field("Posting", r.job?.url && extLink(r.job.url, "Open the posting")),
        ...field("Tracked since", dateTime(r.created_at)),
        ...field("Applied", r.applied_at && dateTime(r.applied_at)),
        ...field("Updated", dateTime(r.updated_at)),
        ...field("Key", h("code", {}, r.key))),
      h("label", { for: "drawer-status" }, "Status"), sel,
      h("label", { for: "drawer-notes" }, "Notes"), notes,
      h("div", { class: "row pad-y" }, saveNotes, h("span", { class: "muted" }, "Ctrl+Enter")),
      h("h3", {}, "History"), history,
      h("h3", {}, "Remove"), removeBox);

    try {
      const evs = await api("GET", `/api/tracker/${enc(key)}/events`);
      replace(history, evs.length ? evs.map((e) => h("li", {},
        h("div", { class: "when" }, dateTime(e.at)),
        h("div", { class: "what" }, h("strong", {}, statusLabel[e.status] || e.status), e.note ? ` · ${e.note}` : "")))
        : h("li", { class: "muted" }, "No history yet."));
    } catch (err) {
      replace(history, h("li", { class: "muted" }, `Couldn't load the history: ${err.message}`));
    }
  }

  filter.addEventListener("input", render);
  function onKey(e) {
    if (e.key === "/" && !typing(e) && !drawer && !e.ctrlKey && !e.metaKey) { e.preventDefault(); filter.focus(); }
  }
  document.addEventListener("keydown", onKey);
  await load();

  return () => {
    closeDrawer();
    document.removeEventListener("keydown", onKey);
  };
}
