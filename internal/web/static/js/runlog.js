// The latest search run, shared by the views: Search starts runs, Sources
// shows their per-source results and the fetch log. It lives as long as
// the tab.
import { stream } from "./api.js";
import { clock, count } from "./format.js";
import { synced } from "./shell.js";

const MAX_LINES = 200;

export const last = {
  running: false,
  startedAt: 0,
  finishedAt: 0,
  reports: new Map(), // source name -> source event payload
  run: null,          // the final run event payload
  lines: [],          // [{t, msg, kind}] kind: "", "warn" or "dim"
};

const listeners = new Set();

// subscribe calls fn(last) after every change until the returned
// function is called.
export function subscribe(fn) {
  listeners.add(fn);
  return () => listeners.delete(fn);
}

function emit() {
  for (const fn of listeners) fn(last);
}

function log(msg, kind = "") {
  last.lines.push({ t: clock(Date.now()), msg, kind });
  if (last.lines.length > MAX_LINES) last.lines.splice(0, last.lines.length - MAX_LINES);
}

// start streams /api/events?query, recording source and run events and
// passing every event to onEvent. It resolves when the stream ends and
// rejects like api.stream. One run at a time: a new one aborts the last.
let current = null;
export async function start(query, { onEvent = () => {}, expected = 0 } = {}) {
  current?.abort();
  const ctrl = new AbortController();
  current = ctrl;
  Object.assign(last, { running: true, startedAt: Date.now(), finishedAt: 0, run: null });
  last.reports = new Map();
  last.lines = [];
  log(expected ? `fetch started · ${expected} sources` : "fetch started");
  emit();
  let fresh = 0;
  try {
    await stream("/api/events?" + query, (name, ev) => {
      if (name === "source") {
        const s = ev.source;
        last.reports.set(s.name, s);
        if (s.status === "error") log(`${s.name} ${s.error || "failed"}`, "warn");
        else if (s.status === "skipped") log(`${s.name} skipped: ${s.error || "not ready"}`, "dim");
        else log(`${s.name} ${count(s.count)} jobs${s.status === "cached" ? " (cached)" : ""}`);
      } else if (name === "job") {
        if (ev.job.new) fresh++;
      } else if (name === "run") {
        last.run = ev.run;
        log(`matched ${count(ev.run.matched)} of ${count(ev.run.fetched)}`);
      } else if (name === "error") {
        log(`search failed: ${ev.error}`, "warn");
      }
      onEvent(name, ev);
      if (name !== "job") emit();
    }, ctrl.signal);
    if (last.run) log(`done · ${count(fresh)} new since last run`);
  } catch (err) {
    if (!ctrl.signal.aborted) log(`search failed: ${err.message}`, "warn");
    throw err;
  } finally {
    if (current === ctrl) {
      current = null;
      last.running = false;
      last.finishedAt = Date.now();
      if (last.run) synced(last.finishedAt);
      emit();
    }
  }
  return ctrl.signal.aborted ? null : last.run;
}

export function stop() {
  current?.abort();
}

export function signal() {
  return current?.signal;
}
