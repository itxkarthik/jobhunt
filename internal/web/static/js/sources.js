// Sources view: the same table as `jobhunt sources`. Key values never
// reach the browser; the API only says which config field a source needs
// and whether an enabled source has it.
import { api } from "./api.js";
import { h, replace } from "./dom.js";
import { since, dateTime } from "./format.js";

const statusClass = { ok: "ok", cached: "ok", skipped: "warn", error: "bad" };

export async function mount(main) {
  const summary = h("span", { class: "muted grow" });
  const body = h("tbody");
  main.append(
    h("div", { class: "board-head" },
      h("h1", {}, "Sources"), summary,
      h("button", { type: "button", class: "ghost", onclick: load }, "Refresh")),
    h("div", { class: "table-wrap" },
      h("table", { class: "sources" },
        h("thead", {}, h("tr", {},
          ["Source", "Enabled", "Last run", "Status", "Jobs", "Last OK", "Errors (last 10)", "Note"]
            .map((c, i) => h("th", { scope: "col", class: i === 4 || i === 6 ? "num" : null }, c)))),
        body)));

  function row(s) {
    const ran = Boolean(s.last);
    // Same rules as cmd/jobhunt/sources.go.
    let enabled = s.enabled ? h("span", { class: "badge ok" }, "yes") : h("span", { class: "badge" }, "no");
    let note = s.description || "";
    if (ran && s.last.error && s.enabled) note = s.last.error;
    if (s.enabled && !s.ready) {
      enabled = h("span", { class: "badge bad" }, "no key");
      note = s.not_ready || "not ready";
    } else if (s.enabled && s.credential) {
      enabled = h("span", { class: "badge ok", title: `${s.credential} is configured` }, "yes, key set");
    } else if (!s.enabled && s.credential) {
      note = `${note} — needs ${s.credential}`;
    }
    const ago = (t) => h("span", { title: dateTime(t) }, `${since(t)} ago`);
    return h("tr", { class: s.enabled ? null : "off" },
      h("th", { scope: "row" }, s.name),
      h("td", {}, enabled),
      h("td", {}, ran ? ago(s.last.run_at) : "never"),
      h("td", {}, ran ? h("span", { class: `badge ${statusClass[s.last.status] || ""}` }, s.last.status) : "-"),
      h("td", { class: "num" }, ran ? s.last.count.toLocaleString() : "-"),
      h("td", {}, s.last_success ? ago(s.last_success) : "-"),
      h("td", { class: "num" }, ran ? String(s.recent_errors) : "-"),
      h("td", { class: "note" }, note));
  }

  async function load() {
    try {
      const list = await api("GET", "/api/sources");
      const on = list.filter((s) => s.enabled);
      const failing = on.filter((s) => s.last?.status === "error").length;
      summary.textContent = `${on.length} of ${list.length} enabled · ${on.filter((s) => s.ready).length} ready` +
        (failing ? ` · ${failing} failed last run` : "");
      replace(body, list.map(row));
    } catch (err) {
      replace(body, h("tr", {}, h("td", { colspan: 8, class: "notice error" }, `Couldn't load sources: ${err.message}`)));
    }
  }

  await load();
}
