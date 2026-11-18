// Sources view: the same facts as `jobhunt sources`, plus the live state
// of the current run and its fetch log. Key values never reach the
// browser; the API only says which config field a source needs and
// whether an enabled source has it. Turning a source on or off stays in
// config.yaml, so the switches here only show the state.
import { api } from "./api.js";
import { h, icon, corners, replace, toast } from "./dom.js";
import { ago, clock, count, dateTime } from "./format.js";
import * as shell from "./shell.js";
import * as runlog from "./runlog.js";
import { fetchNow, currentInput, query, hiddenSites, LINKS_OFF } from "./search.js";

export async function mount(view, { live = () => true } = {}) {
  let list = [];
  let sites = [];

  const runBtn = h("button", { type: "button", class: "btn btn-primary blueprint", title: "Search again with your last filters, ignoring the cache" },
    corners(), icon("play"), "Run all sources");
  const stats = h("div", { class: "plate stats" });
  const tbody = h("tbody");
  const mlist = h("div", { class: "src-list" });
  const logTitle = h("span", { class: "label" });
  const logList = h("ol", { "aria-live": "polite" });
  const extChips = h("div", { class: "chips" });
  const loadError = h("p", { class: "notice error", hidden: true, role: "alert" });
  view.append(h("div", { class: "sources-view" }, h("div", { class: "sources-inner" },
    h("div", { class: "sources-title" },
      h("div", {},
        h("h2", {}, "Sources"),
        h("p", {}, "Job boards and ATS feeds fetched on every run. Skipped sources need attention; turn sources on or off in config.yaml.")),
      runBtn),
    loadError,
    stats,
    h("div", { class: "sources-grid" },
      h("div", {},
        h("table", { class: "table" },
          h("thead", {}, h("tr", {},
            ["Source", "Status", "Jobs", "Fetch time", "Last run", "On"].map((c, i) =>
              h("th", { scope: "col", class: i === 2 || i === 5 ? "num" : null }, c)))),
          tbody),
        mlist),
      h("div", { class: "side" },
        h("div", { class: "fetchlog blueprint" }, corners(), logTitle, logList),
        h("div", { class: "ext" },
          h("h5", {}, "External searches"),
          h("p", {}, "Shown as “Also search” links under results, with your filters filled in. jobhunt never fetches these sites."),
          extChips))))));

  // state merges the saved last run with the live run, if one is going.
  function state(s) {
    const live = runlog.last.reports.get(s.name);
    if (!s.enabled) {
      return { label: "disabled", cls: "", reason: s.credential ? `Needs ${s.credential}` : "Off in config.yaml" };
    }
    if (!s.ready) return { label: "needs key", cls: "error", reason: s.not_ready || "Not ready" };
    if (live) {
      return {
        label: live.status, cls: live.status, reason: live.error || "",
        count: live.count, ms: live.duration_ms, at: runlog.last.finishedAt || Date.now(), now: true,
      };
    }
    if (runlog.last.running) return { label: "running…", cls: "running", reason: "", now: true };
    if (!s.last) return { label: "never run", cls: "", reason: "" };
    return {
      label: s.last.status, cls: s.last.status, reason: s.last.error || "",
      count: s.last.count, ms: s.last.duration_ms, at: Date.parse(s.last.run_at),
    };
  }

  const seconds = (ms) => (ms ? `${(ms / 1000).toFixed(1)}s` : "—");
  const jobs = (st) => (st.count ? count(st.count) : st.count === 0 ? "0" : "—");

  function sw(s) {
    return h("span", {
      class: s.enabled ? "switch on" : "switch", role: "img",
      "aria-label": s.enabled ? "On" : "Off", title: `Set sources.${s.name}.enabled in config.yaml`,
    });
  }

  function render() {
    const rows = list.map((s) => ({ s, st: state(s) }));
    const slowest = Math.max(1, ...rows.map((r) => r.st.ms || 0));
    replace(tbody, rows.map(({ s, st }) => {
      const fill = h("i");
      fill.style.width = `${Math.round(((st.ms || 0) / slowest) * 100)}%`;
      return h("tr", { class: s.enabled ? null : "off" },
        h("td", {}, h("div", { class: "src-name" }, h("b", {}, s.name), h("span", {}, s.description || ""))),
        h("td", {}, h("div", { class: "st" },
          h("span", { class: `st-label ${st.cls}` }, st.label),
          st.reason && h("span", { class: "st-reason" }, st.reason))),
        h("td", { class: "num td-jobs" }, jobs(st)),
        h("td", {}, h("div", { class: "time" }, h("span", { class: "bar" }, fill), h("span", {}, seconds(st.ms)))),
        h("td", { class: "td-mono", title: st.at ? dateTime(st.at) : null }, st.now && !st.at ? "now" : st.at ? ago(st.at) : "—"),
        h("td", { class: "num" }, sw(s)));
    }));
    replace(mlist, rows.map(({ s, st }) => h("div", { class: s.enabled ? "src-row" : "src-row off" },
      h("div", { class: "st" },
        h("span", { class: "top" }, h("b", {}, s.name), h("span", {}, s.description || "")),
        h("span", { class: `st-label ${st.cls}` }, `${st.label} · ${jobs(st)} jobs · ${seconds(st.ms)}`),
        st.reason && h("span", { class: "st-reason" }, st.reason)),
      sw(s))));

    const on = list.filter((s) => s.enabled);
    const reporting = on.filter((s) => ["ok", "cached"].includes(state(s).label)).length;
    const lastAt = Math.max(0, ...list.map((s) => Date.parse(s.last?.run_at) || 0), runlog.last.finishedAt);
    const run = runlog.last.run;
    const fetched = run ? run.fetched : on.reduce((n, s) => n + (s.last?.count || 0), 0);
    replace(stats, [
      ["Fetched", count(fetched)],
      ["After filters", run ? count(run.matched) : "—"],
      ["Reporting", `${reporting} / ${on.length}`],
      ["Last run", runlog.last.running ? "running…" : lastAt ? ago(lastAt) : "never"],
    ].map(([k, v]) => h("div", { class: "stat" }, h("span", { class: "label" }, k), h("span", { class: "v" }, v))));

    // The log of this tab's last run, or each source's last run otherwise.
    let lines = runlog.last.lines;
    logTitle.textContent = "fetch log · last run";
    if (!lines.length) {
      logTitle.textContent = "fetch log · each source's last run";
      lines = list.filter((s) => s.last).sort((a, b) => Date.parse(a.last.run_at) - Date.parse(b.last.run_at))
        .map((s) => ({
          t: clock(Date.parse(s.last.run_at)),
          msg: s.last.status === "error" ? `${s.name} ${s.last.error || "failed"}`
            : s.last.status === "skipped" ? `${s.name} skipped: ${s.last.error || "not ready"}`
              : `${s.name} ${count(s.last.count)} jobs`,
          kind: s.last.status === "error" ? "warn" : s.last.status === "skipped" ? "dim" : "",
        }));
    }
    replace(logList, lines.length
      ? lines.map((l) => h("li", { class: l.kind || null }, h("span", { class: "t" }, l.t), h("span", { class: "m" }, l.msg)))
      : h("li", {}, h("span", { class: "t" }, "--:--:--"), h("span", { class: "m" }, "no runs yet")));
    if (runlog.last.running) logList.lastElementChild?.scrollIntoView({ block: "nearest" });

    runBtn.disabled = runlog.last.running;
    if (!live()) return;
    shell.setBusy(runlog.last.running);
    shell.setCount("sources", `${reporting}/${on.length}`);
    shell.setContext(runlog.last.running ? "fetching…" : `${on.length} of ${list.length} enabled`);
  }

  function renderSites() {
    const off = hiddenSites();
    replace(extChips, sites.map((name) => h("button", {
      type: "button", class: "chip soft", "aria-pressed": String(!off.has(name)),
      onclick: () => {
        const now = hiddenSites();
        if (now.has(name)) now.delete(name); else now.add(name);
        try { localStorage.setItem(LINKS_OFF, JSON.stringify([...now])); } catch { /* ignore */ }
        renderSites();
      },
    }, name)));
  }

  async function load() {
    try {
      list = await api("GET", "/api/sources");
      loadError.hidden = true;
      render();
    } catch (err) {
      loadError.textContent = `Couldn't load sources: ${err.message}`;
      loadError.hidden = false;
    }
  }

  async function runAll() {
    if (runlog.last.running) return;
    const done = fetchNow({ refresh: true });
    render();
    await done;
    await load();
  }
  runBtn.addEventListener("click", runAll);

  const unsubscribe = runlog.subscribe(() => {
    render();
    if (!runlog.last.running) load();
  });
  shell.setFetch(runAll, "Run all");
  shell.setStatus("SOURCES", [["r", "run all"], ["1 2 3", "tabs"], ["t", "theme"]]);

  await load();
  try {
    const ls = await api("GET", "/api/links?" + query(await currentInput()));
    sites = ls.map((l) => l.site);
    renderSites();
  } catch (err) {
    toast(`Couldn't load the external searches: ${err.message}`, { error: true });
  }

  return () => unsubscribe();
}
