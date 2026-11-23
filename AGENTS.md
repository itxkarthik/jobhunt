# AGENTS.md

Instructions for AI coding agents (Claude Code, Codex, Copilot and others) working in this repository. Humans should read [README.md](README.md) and [CONTRIBUTING.md](CONTRIBUTING.md).

## Project

`jobhunt` is a single-binary Go CLI and TUI. It searches many legal job sources concurrently, then classifies, filters, dedupes and ranks the results (0–100). It also tracks applications in SQLite, watches for new matches with desktop notifications, keeps a list of startups to watch (funding, headcount, boards, published contacts), and serves a local web UI, a JSON API, an NDJSON feed and MCP tools for agents. Users are students looking for internships and entry-level roles; the default country is India.

- Module: `github.com/itxkarthik/jobhunt`, Go 1.27+.
- Libraries: Cobra; Bubble Tea, Bubbles and Lip Gloss **v2** (`charm.land/*/v2`); `modernc.org/sqlite`, which is pure Go with no cgo; `golang.org/x/sync/errgroup`; `golang.org/x/time/rate`; `gopkg.in/yaml.v3`.

## Commands

```sh
make            # fmt + vet + test + build → bin/jobhunt
make test       # go test -race -count=1 ./...
make lint       # golangci-lint, pinned to CI's version (LINT_VERSION in the Makefile), config in .golangci.yml
make install    # → ~/.local/bin/jobhunt (PREFIX= to override)
go fix -diff ./...   # must print nothing
./bin/jobhunt search -c IN -t internship -a backend   # end-to-end check (needs network)
./bin/jobhunt sources --check-boards                   # verify companies.yaml slugs live
git tag v0.8.0 && git push origin v0.8.0              # release: .github/workflows/release.yml runs GoReleaser (.goreleaser.yaml)
```

Before you say a change is done, run `make test`, `make lint`, `gofmt -l .` and `go fix -diff ./...`. All must be clean. CI (`.github/workflows/ci.yml`) runs the same checks on every pull request.

## Layout

```
cmd/jobhunt/        Cobra commands: search, tui, tracker, preset, export, watch, feed, serve, mcp,
                    companies (set/show/rm/import/sync/links), profile, digest, hide, sources, config
internal/model/     Job, Filters, enums; geo.go = country/region/location parsing
internal/config/    config.yaml + JOBHUNT_* env overrides; config.example.yaml is embedded
internal/app/       wiring; catalog.go is the registry of every source; filters.go = FilterInput
                    parsing shared by CLI flags and the API; imports.go = company importers;
                    profile.go, digest.go
internal/sources/   Source interface + optional interfaces, Client (httpx.go), SanitizeJob, text helpers
  <name>/           one package per source, each with testdata/ fixtures
  ats/              Greenhouse, Lever, Ashby, Workable, Recruitee + companies.yaml
  edgar/ mca/       company importers: SEC Form D rounds, MCA registrations (data.gov.in API)
  hn/               HN jobs, and company leads from the same thread
  links/            browser links for sites that forbid automation (never fetched)
  sourcetest/       fixture HTTP server for tests
internal/search/    engine: concurrent fetch, cache/TTL rules, stale fallback, health,
                    sanitize → classify → filter → dedup → rank → hidden/seen
internal/classify/  token-based keyword classifier; rules in keywords.yaml
internal/dedup/     NormCompany/NormTitle, Jaccard merge, Key()
internal/rank/      Weights + Ranker
internal/company/   companies list: Clean (validate + sanitize), Merge, ParseFilter, outreach NewDraft
internal/store/     SQLite; migrations/NNN_*.sql applied in order via PRAGMA user_version
internal/tui/       Bubble Tea v2 model (tui.go)
internal/feed/      headless v1 NDJSON events, signed webhook sink (docs/feed.schema.json)
internal/server/    `jobhunt serve`: security middleware, REST API, SSE (docs/api.md)
internal/web/       embedded web UI: plain ES modules, no build step, no CDN
internal/export/    csv/json/md writers
internal/notify/    notify-send / osascript
internal/browser/   open http(s) URLs
internal/mcpserver/ `jobhunt mcp`: tools for agents (docs/examples/mcp.json)
.claude/agents/     company-scout agent prompts that use `jobhunt mcp`
```

## Hard rules

1. **Legal sources only.**
   - Never add a scraper for LinkedIn, Indeed, Naukri, Internshala, Glassdoor, X or any site whose terms or `robots.txt` forbid automated access. Add a link in `internal/sources/links` instead.
   - Check `robots.txt` and terms before adding any source, and note them in the package doc comment.
2. **Never commit or print secrets.**
   - Keys live in `~/.config/jobhunt/config.yaml` or env vars: `JOBHUNT_JSEARCH_API_KEY`, `JOBHUNT_X_BEARER_TOKEN`, `JOBHUNT_ADZUNA_APP_ID`, `JOBHUNT_ADZUNA_APP_KEY`, `JOBHUNT_DATAGOV_API_KEY`, `JOBHUNT_EDGAR_USER_AGENT` (has the user's contact email), `JOBHUNT_WEBHOOK_URL`, `JOBHUNT_WEBHOOK_SECRET`, `JOBHUNT_SERVE_TOKEN`.
   - Don't echo key values in commands or output; redact them.
   - Don't put keys on a command line where a header file or env var works instead.
3. **Paid or quota-limited sources stay opt-in.** That means `enabled: false` in the example config, and `Ready()` returns an error without a key. Don't make extra live requests to them while testing; each one spends the user's quota.
4. **Tests are offline.** Use `sourcetest.Serve` with recorded fixtures. No real network, no real user DB (`store.Open(":memory:")`), no real config.
5. **Untrusted text.** All fetched jobs go through `sources.SanitizeJob` in the search engine (`Engine.prepare`, right after a fetch and before the cache stores them) before anything is printed, exported or stored. Keep it that way. URLs must stay `http(s)`-only. Bump `search.sanitizeVersion` when `SanitizeJob` changes, so cached jobs are cleaned again.
6. **Database.** Schema changes need a **new** migration file; never edit an applied one. Changing `dedup.Key` invalidates users' tracker and hidden keys, so don't do it casually.
7. **The feed event format is a public contract.** `docs/feed.schema.json`, `feed.Version` and `internal/feed/testdata/batch.ndjson` change together, and `TestSchemaMatchesTypes` keeps them in sync with the Go types. Adding a field is fine. Removing, renaming or retyping one, including on `model.Job`, needs a `feed.Version` bump. Webhook errors must never print the URL path or query (they often hold tokens), and secrets come only from config or env.
8. **The local server stays locked down.** In `internal/server`, keep all of these:
   - loopback by default, plus the Host allowlist (it stops DNS rebinding);
   - the bearer token on every `/api` route (never from the query string), compared in constant time;
   - `http.CrossOriginProtection`, JSON-only request bodies and the CSP and security headers;
   - generic 500 responses that never include internal errors.
   New endpoints go through `s.handle` and `decode`, and get tests for 401/403. Job and company text is untrusted: never put it in HTML without escaping.
   In `internal/web`, build DOM with `h()` and text nodes (no `innerHTML`), pass every link through `safeURL`, and load nothing from outside the binary.
9. **Don't touch the user's environment** (`~/.bashrc`, `~/.config/jobhunt/config.yaml`, `~/.local/share/jobhunt/`) unless asked.
10. **Companies and contacts.**
   - Store a contact only when the person published it themselves, always with the `source_url` it came from. Rounds need a `source_url` too; `company.Clean` enforces both.
   - No guessed emails, email finders or enrichment services. Never fetch Crunchbase, Tracxn, Wellfound, LinkedIn, Y Combinator's directory, Inc42, Entrackr, Glassdoor or AmbitionBox; `companies links` gives browser links instead.
   - jobhunt drafts outreach but never sends email, messages or applications.

## Conventions

- Every source implements `sources.Source{Name, Supports, Fetch}`. Optional interfaces:
  - `Readier`: missing key.
  - `Linker`: fallback URL.
  - `RateLimited`.
  - `MinTTLer`: a hard minimum from the source's terms; even `--refresh` honours it.
  - `DefaultTTLer`: a suggestion that `--refresh` and config `ttl` override.
  - `Timeouter`, `CacheKeyer`.
  - Return `*sources.PartialError` when some pages failed but some jobs came back.
- Unknown values stay `unknown`; don't guess. Filters drop unknowns unless `--include-unknown`.
- Pay: `SalaryMin`/`SalaryMax` are per `PayPeriod` (empty means a year). Sources keep every period they're given; `Paid` is false only when a posting says it's unpaid. Show pay with `Job.Pay()` (and `pay()` in `internal/web/static/js/format.js`, which must match it).
- Country semantics: `--country IN` also matches worldwide-remote and region codes containing IN (`model.CountryAllows`) unless `StrictCountry`. ATS boards with `remote: true` are fetched for any non-strict search.
- Errors are wrapped with `%w`. Every I/O call takes a `context.Context`. A failing source must never fail the search.
- Use table-driven tests. Prefer the standard library (`slices`, `maps`, `min`/`max`, `strings.Cut`, `b.Loop`).
- Commits follow Conventional Commits (`feat(sources): …`, `fix(tui): …`).

## Gotchas

- **Lip Gloss v2:** `Width`/`Height` include the border and padding, unlike v1.
- **Bubble Tea v2:** `View()` returns `tea.View` (alt screen is set on the view); keys are `tea.KeyPressMsg`; space is `"space"`.
- **In-memory SQLite** uses one connection (`SetMaxOpenConns(1)`). Close `rows` before running another query, or the call deadlocks (see `Store.latestRuns`).
- **Remotive** has `MinTTL` of 6h, required by its terms. Its public API returns only about 10 jobs.
- **JSearch keys:** `ak_…` is an OpenWeb Ninja key and goes to `api.openwebninja.com` with `x-api-key`. Anything else goes through RapidAPI. Each country is one request.
- **ATS slugs go stale** when companies switch ATS. Use `jobhunt sources --check-boards`.
- The classifier runs once per fetched job (results are cached with a version from `keywords.yaml` and `classify.logicVersion`; bump `logicVersion` when `Classify` decides differently for the same rules). Real descriptions are several KB, and a refresh classifies about 15k jobs. Keep it token-based (no per-job regex loops). The two "N years" regexes run only on `yearSnippets`, never on a whole description, and the pay regexes (`classify/pay.go`) only on short windows that start at a currency sign or an `amount_hints` word with a pay word before it. `BenchmarkClassify`, `BenchmarkClassifyExperience` (a long posting) and `BenchmarkSearch` (a whole search, cached and fresh) guard this.
