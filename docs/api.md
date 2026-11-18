# jobhunt local API

`jobhunt serve` runs a JSON API on your machine. It sits over the same search engine and database as the CLI, and the web UI uses only this API, so anything the UI can do, a script or an agent can do too.

```sh
jobhunt serve                     # http://127.0.0.1:7777, prints a fresh token
JOBHUNT_SERVE_TOKEN=… jobhunt serve --addr 127.0.0.1:8080
```

API version: **1** (`GET /api/meta` → `api_version`).

## Authentication and security

Every `/api/` request needs the bearer token:

```sh
curl -H "Authorization: Bearer $TOKEN" http://127.0.0.1:7777/api/health
```

- **Token:** by default a random token is generated on each start and printed in the terminal. For a stable token, set `serve.token` in the config or `JOBHUNT_SERVE_TOKEN` (16+ characters).
  - The token is never accepted in the query string.
  - The web UI receives it in the URL fragment (`/#token=…`), which browsers don't send to servers or leak in `Referer`.
- **Loopback only by default.** `--addr` can bind elsewhere, but jobhunt prints a warning. Clients that use another Host name must be allowed with `--allow-host`.
- **Host check:** requests whose `Host` header isn't `localhost`, `127.0.0.1`, `[::1]` or an allowed host get `403`. This blocks DNS-rebinding attacks from websites you visit.
- **Cross-origin writes** (judged by `Sec-Fetch-Site` and `Origin`) get `403`, and request bodies must be `application/json`.
- **Response headers:** every response carries a strict `Content-Security-Policy`, `X-Frame-Options: DENY`, `nosniff` and `Referrer-Policy: no-referrer`. API responses are `Cache-Control: no-store`.
- **Treat job text as untrusted.** It comes from the open internet. jobhunt strips control and bidi characters and keeps only http(s) URLs, but you should still escape it before rendering it as HTML.

## Conventions

- Request and response bodies are JSON. Unknown request fields are rejected with 400, so typos fail loudly.
- Errors look like `{"error": "message"}`. The status codes used are 400, 401, 403, 404, 405, 409, 413, 415 and 500.
  - A 500 never includes internal details; those go to the server log.
- `{key}` accepts any unique prefix of a job key, as the CLI does. An unknown key gives 404; an ambiguous one gives 409.
- Times are RFC 3339 in UTC. Empty lists are `[]`, never `null`.
- Only one search runs at a time. Concurrent search requests wait their turn.

### Filters

Searches and presets use the same filter object as the CLI flags. Empty fields take the defaults from your config (for example `countries: ["IN"]`).

```json
{
  "countries": ["IN", "remote-global"],
  "modes": ["remote"],
  "types": ["internship", "new-grad"],
  "areas": ["backend", "devops"],
  "experience": ["intern", "entry"],
  "keywords": ["Go"],
  "exclude": ["senior"],
  "posted_within": "7d",
  "visa": false,
  "min_salary": 0,
  "currency": "INR",
  "strict_country": false,
  "include_unknown": false
}
```

`GET /api/meta` lists the allowed values.

## Endpoints

| Method and path | Does |
|---|---|
| `GET /api/health` | `{"ok": true}` |
| `GET /api/meta` | API and feed versions, default filters, allowed values for each option |
| `POST /api/search` | Run a search: filters plus `refresh`, `new_only`, `show_hidden`, `sort` (`score` or `date`) and `limit` (0 means all) |
| `GET /api/events?…` | The same search, streamed as Server-Sent Events (see below) |
| `GET /api/links?…` | Pre-filled searches on sites jobhunt can't query, for the filters in the query string (same names as `/api/events`). Runs no search. |
| `GET /api/jobs/{key}` | One job, plus its tracker `status` if tracked |
| `POST /api/jobs/{key}/hide` | Hide a job from future results (`204`) |
| `DELETE /api/jobs/{key}/hide` | Unhide it (`204`) |
| `GET /api/tracker[?status=applied,interview]` | Tracked jobs, most recently updated first |
| `PUT /api/tracker/{key}` | `{"status": "applied", "note": "…"}`: set the status, append the note, return the row |
| `DELETE /api/tracker/{key}` | Stop tracking (`204`; the history is kept) |
| `GET /api/tracker/{key}/events` | Status history: `[{at, status, note}]` |
| `GET /api/presets` | Saved presets: `[{name, filters, updated_at}]` |
| `GET /api/presets/{name}` | One preset |
| `PUT /api/presets/{name}` | Save filters under a name: letters, digits, `.`, `_` and `-`, up to 64 characters |
| `DELETE /api/presets/{name}` | Delete it (`204`) |
| `GET /api/sources` | Every known source, including disabled ones: `description`, `credential` (the config field it needs, never its value), `enabled` and `ready` state, `not_ready` (for example which key is missing), last run, last success and recent errors |

Tracker statuses are `saved`, `applied`, `oa`, `interview`, `offer` and `rejected`.

### `POST /api/search`

```sh
curl -s -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"types":["internship"],"areas":["backend"],"limit":20}' \
  http://127.0.0.1:7777/api/search
```

```json
{
  "run_at": "2026-11-15T10:00:00Z",
  "fetched": 12097,
  "matched": 19,
  "jobs": [{"key": "53be5e30a44f", "title": "…", "company": "…", "url": "https://…", "score": 88.3, "status": "saved", "…": "…"}],
  "sources": [{"name": "greenhouse", "status": "ok", "count": 8194, "duration_ms": 6200}],
  "links": [{"site": "LinkedIn", "url": "https://www.linkedin.com/jobs/search/?…"}]
}
```

- **`jobs`:** each job has the same fields as a `job` object in [`feed.schema.json`](feed.schema.json), plus its tracker `status`.
- **`sources`:** a source that was skipped or failed may include a `link`, a browser fallback for that source.
- **`links`:** pre-filled searches on sites jobhunt can't query.

### `GET /api/events`

This runs the same search as `POST /api/search`, but streams it as `text/event-stream`. The query parameters use the same names as the JSON fields, including `limit` (the run event's `matched` still counts every match). Lists can be comma-separated or repeated, e.g. `?countries=IN,US&areas=backend&areas=devops&posted_within=7d&refresh=true`.

Each event's name is its feed type, and its `data` is the same v1 JSON as a [`jobhunt feed`](feed.schema.json) line:

```
event: source
data: {"v":1,"type":"source","run_id":"…","at":"…","source":{"name":"greenhouse","status":"ok","count":8194,"duration_ms":6200}}

event: job
data: {"v":1,"type":"job","run_id":"…","at":"…","job":{"key":"53be5e30a44f","title":"…","score":88.3,"…":"…"}}

event: run
data: {"v":1,"type":"run","run_id":"…","at":"…","run":{"fetched":12097,"matched":19,"emitted":19,"source_errors":0}}
```

- **Order:** a `source` event goes out as each source finishes, then come the `job` events, then a final `run` event.
- **Failure:** a search that fails ends with an `error` event instead.
- **Keepalive:** comment lines (`: keepalive`) keep idle connections open.
- **Cancellation:** closing the connection cancels the search.
- **Browser clients:** `EventSource` can't send the `Authorization` header, so read the stream with `fetch()` instead.
- **Extras:** the stream carries feed events only. For the "also search" links, call `GET /api/links` with the same query. For tracker statuses, use `GET /api/tracker`.
