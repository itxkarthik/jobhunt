# jobhunt

**Search every legal job source at once from your terminal, and track your applications in one place.**

jobhunt is a single Go binary for students and early-career engineers looking for **internships, new-grad and entry-level roles**. It's tuned for India by default, and the US, Japan, Europe and remote jobs work too. One search queries about ten job sources in parallel, merges duplicate postings, scores every job 0–100 for how well it fits what you asked for, and remembers what you've already seen. A tracker and a watch mode keep your hunt organised from the first save to the offer.

## Why it exists

Hunting for an internship usually means checking a dozen sites every day: company career pages, remote boards, HN's monthly hiring thread, LinkedIn, Internshala and others. The same job shows up on several of them, most listings are senior roles, and you lose track of where you applied. jobhunt fixes that:

| Problem | What jobhunt does |
|---|---|
| Too many sites | It queries them all concurrently with one command, and prints ready-made links for the sites that forbid automation |
| Duplicates | It merges the same posting found on different sources |
| Noise (senior roles, wrong country) | It classifies each job's type, level, mode, area and visa support, then filters and ranks them |
| "Have I seen this?" | It marks jobs that are new since your last search with ★, and `--new-only` shows just those |
| "Where did I apply?" | A built-in tracker: saved → applied → OA → interview → offer / rejected, with notes and history |
| Checking every day | `watch` re-runs your search and sends a desktop notification when new matches appear |

It's **legal and polite by design**:
- It only uses public APIs, company job-board APIs (Greenhouse, Lever, Ashby) and RSS feeds.
- It respects each source's terms and rate limits, and identifies itself with an honest User-Agent.
- It never scrapes LinkedIn, Indeed, Naukri, Internshala or Glassdoor. For those it prints pre-filled browser search links instead.

## Quick start

Needs Go 1.27+ (see `go.mod`) and Linux, macOS or Windows.

```sh
git clone https://github.com/itxkarthik/jobhunt && cd jobhunt
make install                    # → ~/.local/bin/jobhunt  (PREFIX=/other/dir make install)
jobhunt config init             # writes ~/.config/jobhunt/config.yaml (optional)

# 1. Search
jobhunt search --country IN --type internship --area backend

# 2. Save that search so you don't retype it
jobhunt preset save india-backend -c IN -t internship -a backend

# 3. Browse it interactively and save/apply with one key
jobhunt tui -p india-backend

# 4. Track applications (the KEY comes from the results table; 4 characters are enough)
jobhunt tracker set 53be applied --note "applied via careers page"
jobhunt tracker

# 5. Get notified about new matches
jobhunt watch -p india-backend --every 6h --min-score 60
```

None of the free sources needs an API key, so it works straight away.

If you get `command not found: jobhunt`, `~/.local/bin` isn't on your PATH. Add `export PATH="$HOME/.local/bin:$PATH"` to your shell rc, or run `./bin/jobhunt` after `make build`. Re-run `make install` after pulling changes.

## Reading the output

```
$ jobhunt search -c IN -t internship -a backend
#   SCORE  KEY           AGE  TITLE                          COMPANY           LOCATION       MODE    TYPE        AREAS             SOURCE     URL
1★  91     53be5e30a44f  12h  Full Stack Developer Intern    ASG Company       remote-global  remote  internship  backend,frontend  himalayas  https://…
2★  84     700f30786f16  5d   Internship- AI & Growth Eng…   Scalanova Health  India          remote  internship  backend,ml-ai     himalayas  https://…
…
19 matching of 11347 fetched

Sources:
  greenhouse ok       8194  (6.2s)
  himalayas  cached    100
  arbeitnow  skipped     0  does not cover these countries/modes
…
Also search (open in browser; these sites don't allow automated access):
  LinkedIn     https://www.linkedin.com/jobs/search/?keywords=backend+intern&geoId=102713980&f_E=1
  Internshala  https://internshala.com/internships/keywords-backend
…
```

- **★**: you haven't seen this job in any earlier search.
- **SCORE**: 0–100 fit for *this* search. See [How the score works](#how-the-score-works).
- **KEY**: a stable ID for the job. Use it with `tracker`, `hide` and `unhide`; any unique prefix works.
- **AGE**: time since the job was posted (h, d, w).
- **AREAS**: CS areas detected from the title and description.
- **Sources**: what each source did: `ok` (fetched), `cached`, `skipped` (missing key, or it doesn't cover your filters) or `error`. A source that errors falls back to its last cached results when it has some.
- **Also search**: links to sites jobhunt can't query, pre-filled with your filters.

## Commands

```sh
# Search
jobhunt search -c IN -t internship -a backend
jobhunt search -c IN,remote-global -t new-grad -k Go,Kubernetes -x senior
jobhunt search -c JP -t internship --visa
jobhunt search -c US -a hpc-gpu -k CUDA --posted-within 7d --sort date
jobhunt search ... --new-only        # only ★ jobs
jobhunt search ... --refresh         # bypass the cache
jobhunt search ... -n -1             # all rows (default 50)
jobhunt search ... --include-unknown # keep jobs whose type/area/country is unclear
jobhunt search ... --strict-country  # drop worldwide/regional remote jobs
jobhunt search ... --show-hidden     # include jobs you hid
jobhunt search ... --no-links        # skip the browser links

# Presets: named filter sets
jobhunt preset save india-backend -c IN -t internship -a backend
jobhunt search -p india-backend -k Go      # flags you pass override the preset
jobhunt preset list | show <name> | delete <name>

# Interactive TUI
jobhunt tui -p india-backend

# Tracker
jobhunt tracker set <key> saved|applied|oa|interview|offer|rejected [--note "…"]
jobhunt tracker                            # list (--status applied,interview to filter)
jobhunt tracker show <key>                 # details, notes and history
jobhunt tracker rm <key>
jobhunt hide <key> / jobhunt unhide <key>  # hidden jobs never show up again

# Export (format from the file extension, or --format)
jobhunt export -p india-backend -o jobs.csv      # csv | json | md
jobhunt export --tracked -o applications.md      # your tracker instead of a search

# Watch with desktop notifications (notify-send on Linux, osascript on macOS)
jobhunt watch -p india-backend --every 6h --min-score 60
jobhunt watch -p india-backend --once            # one check (cron / systemd timer)

# Sources and config
jobhunt sources                 # health: last run, status, job count, recent errors
jobhunt sources --check-boards  # find company boards whose slug went stale
jobhunt config init | path
```

Every command has `--help` with examples.

### Filters

These work on `search`, `tui`, `export`, `watch` and `preset save`.

| Flag | Values |
|---|---|
| `-c, --country` | `IN US JP UK DE CA SG NL AU AE remote-global` (default from config: `IN`) |
| `-m, --mode` | `remote hybrid onsite` |
| `-t, --type` | `internship new-grad full-time part-time contract` |
| `-a, --area` | `backend frontend full-stack mobile ml-ai data-engineering data-science devops systems hpc-gpu security embedded qa research blockchain gamedev` |
| `-e, --experience` | `intern entry mid senior` |
| `-k, --keywords` | a job must mention at least one, e.g. `-k Go,Rust` |
| `-x, --exclude` | drop jobs mentioning any, e.g. `-x senior,"10+ years"` |
| `--posted-within` | `24h 3d 7d 30d` |
| `--visa` | only jobs that mention visa sponsorship |
| `--min-salary N --currency INR` | minimum advertised annual salary |
| `-p, --preset` | start from a saved preset |

**Country rule:** `--country IN` also includes remote jobs that are open worldwide or to a region containing India (APAC, Asia), because you can do those from India. Use `--strict-country` to drop them.

**Unknowns:** jobs whose type, area or country couldn't be worked out are dropped unless you pass `--include-unknown`.

### TUI keys

| Key | Action |
|---|---|
| `tab` / `shift+tab` | switch pane: filters → results → detail |
| `↑↓` / `jk` | move, or scroll the detail pane |
| `space` / `enter` | toggle a filter in the sidebar (re-searches) |
| `/` | edit keywords (comma-separated), `enter` to search, `esc` to cancel |
| `s` | sort: score → date → company |
| `o` | open the job in your browser |
| `S` / `a` | save to the tracker / mark applied |
| `h` | hide the job |
| `r` | refetch, bypassing the cache |
| `?` | help |
| `q` | quit |

## Sources

| Source | Needs a key? | Notes |
|---|---|---|
| Greenhouse, Lever, Ashby, Workable, Recruitee | no | about 90 company boards in `internal/sources/ats/companies.yaml`: big tech (Stripe, Databricks, Rubrik…), Indian startups (Razorpay, CRED, Groww, Sarvam AI, DevRev, Glance, SpotDraft, Pocket FM…) and remote-first startups (Supabase, PostHog, Linear, Vercel, Zapier…). Only boards hiring in your countries are fetched, plus remote-first boards unless you pass `--strict-country` |
| HN "Who is Hiring" | no | the latest monthly thread, via the Algolia API |
| Himalayas | no | remote jobs; the application link is used unchanged, as its terms require |
| RemoteOK, Remotive, We Work Remotely, Arbeitnow | no | remote and EU jobs, always linked back to the source. Remotive is cached for at least 6h (its terms) |
| JSearch | yes, `JOBHUNT_JSEARCH_API_KEY` | **opt-in**, freemium. Brings in LinkedIn and Indeed listings through Google Jobs. Takes a key from [OpenWeb Ninja](https://www.openwebninja.com) (`ak_…`, used directly) or from RapidAPI; jobhunt detects which. 1 request per country per search; set `sources.jsearch.ttl: 24h` to stretch a free quota |
| X recent search | yes, `JOBHUNT_X_BEARER_TOKEN` | **opt-in**, pay-per-use (~$0.005 per post read) |
| Adzuna | yes, `JOBHUNT_ADZUNA_APP_ID` / `_APP_KEY` | **opt-in**, paid after the trial |
| LinkedIn, Indeed, Naukri, Internshala, Glassdoor, Wellfound, YC startup jobs, Work at a Startup, Cutshort, Instahyre, TokyoDev, Japan Dev, Google Jobs, X | – | **links only**, never fetched |

**Not included, on purpose:** jobhunt doesn't scrape LinkedIn or X, because both sites' terms forbid it. Their listings come in legally through JSearch (LinkedIn via Google Jobs) and the official X API. SmartRecruiters is skipped because its robots.txt disallows all crawlers.

To turn on an opt-in source:
1. Set `sources.<name>.enabled: true` in the config.
2. Give it a key, either in the config or in the env var.

Keys in env vars are never written to disk.

## Configuration

`~/.config/jobhunt/config.yaml` is optional; `jobhunt config init` writes a commented starter file (mode 0600). To use a different file, set `JOBHUNT_CONFIG=/path/to/config.yaml`.

```yaml
cache_ttl: 1h          # don't refetch a source within this window
timeout: 20s           # per-source fetch timeout
defaults:              # used when you don't pass the flag
  countries: [IN]
  types: [internship, new-grad]
  limit: 50
ranking:               # max points per score component; should sum to 100
  match: 35
  recency: 25
  level: 25
  fit: 10
  info: 5
# keywords_file: ~/.config/jobhunt/keywords.yaml     # your own classifier rules
# companies_file: ~/.config/jobhunt/companies.yaml   # your own company boards
sources:
  greenhouse: { enabled: true, ttl: 6h }   # any source: enabled, ttl, max_pages…
  jsearch:    { enabled: false, api_key: "" }
```

You can customise two lists by copying them and pointing the config at your copy:
- **Your own target companies:** copy `internal/sources/ats/companies.yaml`. Each entry gives a name, an ATS (greenhouse, lever, ashby, workable or recruitee), the board slug and the hiring countries. The slug is the company's ID in its careers-page URL, e.g. `jobs.ashbyhq.com/<slug>`, `jobs.lever.co/<slug>`, `boards.greenhouse.io/<slug>`, `apply.workable.com/<slug>` or `<slug>.recruitee.com`. Run `jobhunt sources --check-boards` afterwards to confirm the slugs work.
- **Better classification:** copy `internal/classify/keywords.yaml`, which holds the keyword rules for job types, experience, work modes, visa and areas.

## How it works

```
 flags / preset ──► Filters
                      │
       ┌──────────────┴───────────────┐   all sources run concurrently
       ▼              ▼               ▼
   Greenhouse     Himalayas   …   HN Who's Hiring
   each one: has key? covers these filters? → fresh cache? → fetch
             (rate limit · timeout · retries on 429/5xx · stale cache on error)
       └──────────────┬───────────────┘
                      ▼
   sanitize   strip terminal escapes & unsafe URLs from untrusted text
   classify   type · level · mode · CS areas · visa   (keywords.yaml, parallel)
   filter     your flags + country rule
   dedup      same company + ≥85% similar title + compatible location → one job
   rank       0–100 score
   track      drop hidden · record seen · ★ new
                      ▼
          table │ TUI │ export │ watch notification
```

1. **Fetch.** Each source is a plugin. It's skipped when it needs a key you haven't set or doesn't cover your filters; for example, Arbeitnow is EU-only. Results are cached in SQLite for a per-source TTL, so repeat searches take well under a second. A slow or broken source never breaks the search: it's reported in the Sources table and its last cached results are used.
2. **Classify.** Token-based keyword rules read each title and description to decide the job type, experience level, work mode, CS areas and visa sponsorship. Anything unclear stays `unknown` rather than being guessed.
3. **Dedup.** Company names are normalised ("Acme Technologies Pvt Ltd" → "acme") and so are titles ("Sr. SWE (Remote)" → "senior software engineer"). Matching postings are merged, keeping the most complete record and listing every source it came from.
4. **Rank** (below).
5. **Track.** Every result is recorded in the `seen` table. A job that first appears in this run gets ★, and hidden jobs are removed.

### How the score works

| Component | Max | Full points when… |
|---|---|---|
| Match | 35 | your keywords appear in the **title** (in the description scores less) and the job is in your areas |
| Recency | 25 | posted just now; the points **halve every 7 days** |
| Level | 25 | it's an internship, new-grad or entry-level role. Unknown level scores half, mid-level 20%, senior 0 |
| Fit | 10 | it's in your exact country and mode. Worldwide or regional remote jobs score 70% |
| Info | 5 | the posting states a salary and/or visa sponsorship |

You can change the weights under `ranking:` in the config.

### Where your data lives

Everything stays on your machine, in `~/.local/share/jobhunt/jobhunt.db` (SQLite, in a folder only you can read). It holds:
- the fetch cache and source health
- seen jobs
- the tracker with its notes and history
- hidden jobs and presets

The only network traffic is the search requests to the sources listed above.

## Security

- **Untrusted text is sanitised.** Job postings come from the open internet (HN comments, X posts), so before anything is printed, exported or stored, jobhunt:
  - strips terminal escape sequences, other control characters and bidi-override characters
  - drops any URL that isn't `http(s)`
- **Safe exports.** CSV cells that would run as spreadsheet formulas (`=`, `+`, `-`, `@`) are neutralised, and Markdown links are escaped.
- **Secrets stay out of the repo.** API keys go in your config (0600) or in env vars, and they're redacted from error messages. `config.yaml`, `.env` and `*.db` are gitignored.
- **No shell.** Opening a link or sending a notification runs a fixed program with arguments, never through a shell, and only `http(s)` URLs are opened.
- **Checked in CI-style tooling.** `govulncheck` reports no known vulnerabilities, and `make lint` runs gosec, noctx, rowserrcheck, sqlclosecheck and more.

## Troubleshooting

| Symptom | Fix |
|---|---|
| The first search takes 20–60s | That's normal: about 65 company boards are fetched. Later searches come from the cache and take under a second |
| Few or no results | Try `--include-unknown`, widen the areas (`-a backend,full-stack`), add `-c remote-global`, or check the **Also search** links. India internships are rare on remote boards |
| A source shows `error` | Run `jobhunt sources` to see recent errors. For company boards, `jobhunt sources --check-boards` finds slugs that moved |
| `skipped: missing key` | That source is opt-in: add its key, or ignore it |
| A job is in the wrong area | Copy `keywords.yaml`, adjust the rules and set `keywords_file` in the config |
| No desktop notification | Install `libnotify` (`notify-send`) on Linux, or use `--no-notify` and read the terminal output |
| `watch` refuses `--every 5m` | The minimum is 15m, to respect the sources' rate limits |

## Development

```sh
make          # fmt + vet + test + build
make test     # every test with -race; recorded fixtures only, no network
make lint     # golangci-lint (installed on first run)
```

```
cmd/jobhunt/        CLI commands (Cobra)
internal/model/     Job, Filters, enums, country/location parsing
internal/config/    config.yaml loading, env overrides
internal/sources/   Source interface, HTTP client, sanitising, one package per source
internal/classify/  keyword classifier (keywords.yaml)
internal/search/    concurrent engine: cache, health, filter, dedup, rank, seen
internal/dedup/     cross-source merging
internal/rank/      0–100 scoring
internal/store/     SQLite: cache, health, seen, tracker, hidden, presets (migrations/)
internal/tui/       Bubble Tea v2 interface
internal/export/    CSV / JSON / Markdown
internal/notify/    desktop notifications
internal/browser/   open URLs
```

### Adding a source

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
   - Return `*sources.PartialError` when only some pages fail.
2. Use the shared `*sources.Client` for HTTP. It sets the User-Agent, rate limiting, a body size limit, retries with backoff on 429/5xx, and honours `Retry-After`.
3. Save a real response in `testdata/` and write a table-driven test with `sourcetest.Serve`. Tests must never touch the network.
4. Register the source in `internal/app/catalog.go` and add its block to `internal/config/config.example.yaml`.

Read the site's terms and robots.txt first. If they don't allow automated access, add a link in `internal/sources/links` instead.

### Running watch in the background (systemd)

`~/.config/systemd/user/jobhunt-watch.service`:

```ini
[Unit]
Description=jobhunt watch

[Service]
ExecStart=%h/.local/bin/jobhunt watch --preset india-backend --every 6h --min-score 60
Restart=on-failure

[Install]
WantedBy=default.target
```

```sh
make install
systemctl --user enable --now jobhunt-watch
journalctl --user -u jobhunt-watch -f
```

## Contributing

Contributions are welcome: new legal sources, company boards and classifier rules. See [CONTRIBUTING.md](CONTRIBUTING.md). AI coding agents should read [AGENTS.md](AGENTS.md).

## License

[MIT](LICENSE) © 2026 itxkarthik
