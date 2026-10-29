# jobhunt

One Go binary that searches many legal job sources at once, then dedupes, ranks and tracks the results. It is built for a CS student hunting internships and entry-level roles (India by default; US, JP, EU and remote also work).

```
$ jobhunt search --country IN --type internship --area backend
#   SCORE  KEY           AGE  TITLE                               COMPANY         LOCATION   MODE    TYPE        SOURCE
1★  91     96acad3a8e04  12h  Full Stack Developer Intern         ASG Company     remote     remote  internship  himalayas
2★  84     759040c4ff0b  5d   Internship- AI & Growth Engineering Scalanova       India      remote  internship  himalayas
…
19 matching of 11347 fetched
```

- **Many sources fetched concurrently.** Every source runs with its own rate limit, timeout and retries. If one source fails, the rest of the search still works.
- **No scraping.** jobhunt only uses public APIs, ATS job boards and RSS feeds, honours each source's terms, and sends an honest User-Agent. Sites that forbid automated access (LinkedIn, Indeed, Naukri, Internshala, Glassdoor…) get pre-filled browser search links instead.
- **Classified, deduplicated and ranked.**
  - Type, level, work mode, CS area and visa sponsorship come from YAML keyword rules.
  - Postings repeated across sources are merged.
  - Each job gets a 0–100 score.
- **TUI, tracker, presets, export, and watch mode** with desktop notifications.

## Install

Needs Go 1.27+ (see `go.mod`).

```sh
git clone https://github.com/itxkarthik/jobhunt && cd jobhunt
make build            # → bin/jobhunt   (or: make install → $GOBIN/jobhunt)
./bin/jobhunt config init   # writes ~/.config/jobhunt/config.yaml
```

| Make target | What it does |
|---|---|
| `make build` | build `bin/jobhunt` |
| `make test` | all tests with `-race`; fixtures only, no network |
| `make lint` | golangci-lint (installs it on first run) |
| `make` | fmt + vet + test + build |

## Commands

```sh
# Search (filters are flags; the default country comes from config: IN)
jobhunt search -c IN -t internship -a backend
jobhunt search -c IN,remote-global -t new-grad -k Go,Kubernetes -x senior
jobhunt search -c JP -t internship --visa
jobhunt search -c US -a hpc-gpu -k CUDA --posted-within 7d --sort date
jobhunt search ... --new-only        # only jobs never shown before (★)
jobhunt search ... --refresh         # bypass the cache
jobhunt search ... -n -1             # all rows, not just 50
jobhunt search ... --include-unknown # keep jobs whose type/area/country is unclear
jobhunt search ... --strict-country  # drop worldwide-remote jobs

# Presets
jobhunt preset save india-backend -c IN -t internship -a backend
jobhunt search -p india-backend            # flags you add override the preset
jobhunt preset list | show <name> | delete <name>

# TUI
jobhunt tui -p india-backend

# Tracker (keys come from the KEY column; a unique prefix is enough)
jobhunt tracker set 96ac saved
jobhunt tracker set 96ac applied --note "referral"
jobhunt tracker set 96ac oa            # saved → applied → oa → interview → offer | rejected
jobhunt tracker                        # list; --status applied,interview to filter
jobhunt tracker show 96ac              # notes + history
jobhunt hide 96ac / jobhunt unhide 96ac

# Export
jobhunt export -p india-backend -o jobs.csv     # format from the extension: csv | json | md
jobhunt export --tracked -o applications.md

# Watch + desktop notifications (notify-send on Linux, osascript on macOS)
jobhunt watch -p india-backend --every 6h --min-score 60
jobhunt watch -p india-backend --once           # single check, for cron/systemd

# Sources
jobhunt sources                 # health: last run, count, recent errors
jobhunt sources --check-boards  # find stale Greenhouse/Lever/Ashby slugs
```

### Filters

| Flag | Values |
|---|---|
| `-c, --country` | `IN US JP UK DE CA SG NL AU AE remote-global` |
| `-m, --mode` | `remote hybrid onsite` |
| `-t, --type` | `internship new-grad full-time part-time contract` |
| `-a, --area` | `backend frontend full-stack mobile ml-ai data-engineering data-science devops systems hpc-gpu security embedded qa research blockchain gamedev` |
| `-e, --experience` | `intern entry mid senior` |
| `-k, --keywords` / `-x, --exclude` | free text; a job must match at least one keyword and no exclusions |
| `--posted-within` | `24h 3d 7d 30d` |
| `--visa`, `--min-salary N --currency INR` | |

By default, `--country IN` also includes remote jobs that are open worldwide or to a region containing India (APAC, Asia). Use `--strict-country` to drop those.

### TUI keys

| Key | Action |
|---|---|
| `tab` / `shift+tab` | switch pane: filters, results, detail |
| `↑↓` / `jk` | move / scroll |
| `space` | toggle a filter in the sidebar (re-searches) |
| `/` | edit keywords |
| `s` | sort: score → date → company |
| `o` | open in browser |
| `S` / `a` | save / mark applied |
| `h` | hide |
| `r` | refetch, bypassing the cache |
| `?` | help |
| `q` | quit |

## Sources

| Source | Key | Notes |
|---|---|---|
| Greenhouse, Lever, Ashby | – | about 65 company boards in `internal/sources/ats/companies.yaml`; you can override them with `~/.config/jobhunt/companies.yaml` |
| HN "Who is Hiring" | – | latest monthly thread via the Algolia API |
| Himalayas | – | remote; `applicationLink` is used unchanged, as its terms require |
| RemoteOK, Remotive, We Work Remotely, Arbeitnow | – | remote/EU; they are linked back to as their terms require; Remotive is cached for at least 6h |
| JSearch (RapidAPI) | `JOBHUNT_JSEARCH_API_KEY` | **opt-in**, freemium. Brings in LinkedIn and Indeed listings via Google Jobs |
| X recent search | `JOBHUNT_X_BEARER_TOKEN` | **opt-in**, pay-per-use (~$0.005 per post) |
| Adzuna | `JOBHUNT_ADZUNA_APP_ID/APP_KEY` | **opt-in**, paid after the trial |
| LinkedIn, Indeed, Naukri, Internshala, Glassdoor, Wellfound, Cutshort, Instahyre, TokyoDev, Japan Dev, Google Jobs, X | – | **links only**: pre-filled search URLs printed after each search |

To turn on an opt-in source, set `sources.<name>.enabled: true` in the config and give it a key, either in the config or in the env var. Secrets never go in the repo: `config.yaml`, `.env` and `*.db` are gitignored.

## How it works

```
flags/preset → Filters
   │
   ├─ for each source (concurrently, errgroup):
   │     Ready? Supports(filters)? → cache hit (SQLite, per-source TTL) ? → Fetch
   │     under timeout + rate limit + retries; on error serve stale cache;
   │     record health
   ▼
classify (keywords.yaml: type, level, mode, areas, visa; parallel)
   → filter → dedup (same company + Jaccard ≥0.85 title + compatible location)
   → rank (match 35 · recency 25 · level 25 · fit 10 · info 5, set in config)
   → drop hidden → mark seen / ★ new → table | TUI | export | watch
```

Data lives in `~/.local/share/jobhunt/jobhunt.db`. It holds the fetch cache, source health, seen jobs, the tracker and its history, hidden jobs and presets.

## Adding a source

1. Create `internal/sources/<name>/` with a type that implements:
   ```go
   type Source interface {
       Name() string
       Supports(f model.Filters) bool
       Fetch(ctx context.Context, f model.Filters) ([]model.Job, error)
   }
   ```
   Optional interfaces:
   - `Ready() error`: report a missing key.
   - `SearchURL(f)`: a browser fallback link.
   - `Rate()`: the rate limit.
   - `MinTTL()` / `DefaultTTL()`: cache rules.
   - `Timeout()`, `CacheKey(f)`.
   - Return `*sources.PartialError` when some pages fail.
2. Use the shared `*sources.Client` for HTTP. It sets the User-Agent, rate limiting, retries with backoff on 429/5xx, and honours `Retry-After`.
3. Record a response in `testdata/` and write a table-driven test with `sourcetest.Serve`. Tests must never touch the network.
4. Register the source in `internal/app/catalog.go` and add its block to `internal/config/config.example.yaml`.

Check the site's terms and robots.txt first. If automated access isn't allowed, add a link in `internal/sources/links` instead.

## Running watch in the background

`~/.config/systemd/user/jobhunt-watch.service`:

```ini
[Unit]
Description=jobhunt watch

[Service]
ExecStart=%h/go/bin/jobhunt watch --preset india-backend --every 6h --min-score 60
Restart=on-failure

[Install]
WantedBy=default.target
```

```sh
systemctl --user enable --now jobhunt-watch
journalctl --user -u jobhunt-watch -f
```
