// The API client. The bearer token arrives in the URL fragment
// (/#token=…), which browsers never send to servers or put in Referer.
// It is kept in sessionStorage, so it lasts as long as the tab.

const KEY = "jobhunt.token";
let memToken = "";

export class APIError extends Error {
  constructor(status, message) {
    super(message);
    this.status = status;
  }
}

// takeToken moves a token from the fragment into storage and strips it
// from the address bar and history.
export function takeToken() {
  const m = /^#token=([^&/]+)/.exec(location.hash);
  if (!m) return;
  setToken(decodeURIComponent(m[1]));
  history.replaceState(null, "", `${location.pathname}${location.search}#/search`);
}

export function setToken(t) {
  memToken = t.trim();
  try { sessionStorage.setItem(KEY, memToken); } catch { /* storage blocked: memory only */ }
}

export function token() {
  if (memToken) return memToken;
  try { memToken = sessionStorage.getItem(KEY) || ""; } catch { /* ignore */ }
  return memToken;
}

function clearToken() {
  memToken = "";
  try { sessionStorage.removeItem(KEY); } catch { /* ignore */ }
}

// unauthorized is called when the server rejects the token.
let onUnauthorized = () => {};
export function whenUnauthorized(fn) { onUnauthorized = fn; }

async function send(method, path, body, signal) {
  const headers = { Authorization: `Bearer ${token()}` };
  const init = { method, headers, signal, cache: "no-store" };
  if (body !== undefined) {
    headers["Content-Type"] = "application/json";
    init.body = JSON.stringify(body);
  }
  const resp = await fetch(path, init);
  if (resp.status === 401) {
    clearToken();
    onUnauthorized();
    throw new APIError(401, "The API token is missing or wrong.");
  }
  return resp;
}

async function check(resp) {
  if (resp.ok) return resp;
  const data = await resp.json().catch(() => null);
  throw new APIError(resp.status, (data && data.error) || `HTTP ${resp.status}`);
}

// api calls a JSON endpoint and returns the decoded body (null for 204).
export async function api(method, path, body, signal) {
  const resp = await check(await send(method, path, body, signal));
  return resp.status === 204 ? null : resp.json();
}

// stream reads a Server-Sent Events response, calling onEvent(name, data)
// for each event. (EventSource can't send the Authorization header.)
export async function stream(path, onEvent, signal) {
  const resp = await check(await send("GET", path, undefined, signal));
  const reader = resp.body.pipeThrough(new TextDecoderStream()).getReader();
  let buf = "";
  for (;;) {
    const { value, done } = await reader.read();
    if (done) return;
    buf += value.replaceAll("\r\n", "\n");
    let i;
    while ((i = buf.indexOf("\n\n")) >= 0) {
      const block = buf.slice(0, i);
      buf = buf.slice(i + 2);
      let name = "message";
      const data = [];
      for (const line of block.split("\n")) {
        if (line.startsWith("event: ")) name = line.slice(7);
        else if (line.startsWith("data: ")) data.push(line.slice(6));
      }
      if (data.length) onEvent(name, JSON.parse(data.join("\n")));
    }
  }
}

// enc escapes a path segment such as a job key or preset name.
export const enc = encodeURIComponent;

// meta is fetched once and shared by the views.
let metaPromise;
export function meta() {
  metaPromise ??= api("GET", "/api/meta").catch((e) => { metaPromise = undefined; throw e; });
  return metaPromise;
}
