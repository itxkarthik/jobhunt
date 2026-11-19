# Contributing to jobhunt

Thanks for helping. jobhunt is a small Go CLI, and contributions of any size are welcome: a new job source, a company board, a classifier rule, a bug fix or a docs fix.

## Ground rules

These keep the project legal, safe and easy to maintain. Pull requests that break them can't be merged.

1. **No scraping where it isn't allowed.** Only use public APIs, ATS job-board APIs or RSS feeds whose terms permit automated access.
   - Read the site's terms and `robots.txt` before adding a source.
   - LinkedIn, Indeed, Naukri, Internshala and Glassdoor forbid it. Sites like those get a browser link in `internal/sources/links`, never a fetcher.
   - SmartRecruiters is excluded because its `robots.txt` disallows crawlers.
2. **Respect each source's terms.** Rate limits, minimum refresh intervals (`MinTTL`), link-back and attribution rules all apply, and the source's package doc comment must say what its terms require.
3. **No secrets in the repo.** API keys come from `~/.config/jobhunt/config.yaml` or `JOBHUNT_*` env vars. Never commit keys, `config.yaml`, `.env` or `*.db`, and never put keys in test fixtures.
4. **Tests never touch the network.** Record a real response into `testdata/`, trim it, remove anything personal, and serve it with `sourcetest.Serve`.
5. **Treat fetched data as untrusted.** Every job passes through `sources.SanitizeJob` in the engine, and every company record through `company.Clean`. Don't print, export or render source text by any path that skips them.
6. **Contacts only as published.** A contact is stored only when the person published it themselves, always with the `source_url` of that page. No guessed addresses, email finders or enrichment services, and nothing that sends email or messages: jobhunt drafts outreach for the user to send.
7. **The feed and API are contracts.** `docs/feed.schema.json` and `feed.Version` change together (adding a field is fine; removing, renaming or retyping one needs a version bump), and every endpoint is documented in `docs/api.md`.

## Setup

You need Go 1.27+ (see `go.mod`).

```sh
git clone https://github.com/itxkarthik/jobhunt && cd jobhunt
make          # fmt + vet + test + build → bin/jobhunt
make lint     # golangci-lint v2 with the repo's .golangci.yml (installed on first run)
make install  # → ~/.local/bin/jobhunt
```

Before opening a pull request, all of these must pass:

```sh
make test                 # go test -race ./...
make lint                 # 0 issues
go fix -diff ./...        # no output (code uses current Go idioms)
gofmt -l .                # no output
```

## Common contributions

### Add a company board

Most startups post on Greenhouse, Lever, Ashby, Workable or Recruitee. To add one:

1. Find the slug in the company's careers URL:
   - `boards.greenhouse.io/<slug>`
   - `jobs.lever.co/<slug>`
   - `jobs.ashbyhq.com/<slug>`
   - `apply.workable.com/<slug>`
   - `<slug>.recruitee.com`
2. Add a line to `internal/sources/ats/companies.yaml` with `countries` set to the ISO codes it hires in, plus `remote: true` if it's remote-first.
3. Run `make build && ./bin/jobhunt sources --check-boards` and confirm the board shows `ok` with a job count.

Only add boards that list real jobs for the company itself. Agencies that repost other companies' jobs make the results worse.

### Add a job source

1. Create `internal/sources/<name>/` with a type that implements:
   ```go
   type Source interface {
       Name() string
       Supports(f model.Filters) bool
       Fetch(ctx context.Context, f model.Filters) ([]model.Job, error)
   }
   ```
   Add any optional interfaces it needs: `Readier` (missing key), `Linker` (browser fallback), `RateLimited`, `MinTTLer` / `DefaultTTLer` (cache rules), `Timeouter`, `CacheKeyer`. Return `*sources.PartialError` when only some pages fail.
2. Use the shared `*sources.Client` for every HTTP request. It sets the User-Agent, rate limiting, a body size limit and retries with backoff on 429/5xx.
3. Save a real response in `testdata/` and write a table-driven test with `sourcetest.Serve`.
4. Register it in `internal/app/catalog.go` and `internal/config/config.example.yaml`.
5. Paid or key-based sources must be **off by default** and report a missing key via `Ready()`.

### Add a company importer

Importers fill the companies list from sources jobhunt may fetch automatically (official filings, public APIs). Look at `internal/sources/edgar` and `internal/sources/mca`:

1. Check the source's terms and `robots.txt`, and write down what they allow in the package doc comment, with the date you checked.
2. Use the shared `*sources.Client`, stay well under the published rate limit, and keep a position (cursor, date or ID) so each run fetches only what's new.
3. Return records with a `SourceURL` on every round, and store them with `app.ImportCompany`, which runs `company.Clean` and merges with what's already known.
4. Wire it into `internal/app/imports.go`, `jobhunt companies import` and `companies sync`. If it needs a key or contact, it stays skipped until configured, with the setting in `config.example.yaml`.
5. Test it offline against recorded fixtures with `sourcetest.Serve`.

### Change the web UI

The web UI in `internal/web/static` is plain ES modules embedded in the binary: no build step, no framework and nothing loaded from a CDN.

- Build DOM with `h()` from `dom.js` and text nodes. Never use `innerHTML`; job and company text is untrusted.
- Pass every link through `safeURL` (http and https only).
- New API endpoints go through `s.handle` and `decode` in `internal/server`, need tests for missing and wrong tokens (401) and foreign origins (403), and are documented in `docs/api.md`.
- Check both themes and a phone-width window.

### Improve classification

Rules live in `internal/classify/keywords.yaml`. Add a case to the classifier's table tests showing the job that was misclassified before your change and is correct after it.

## Code style

- Run `gofmt` and follow the lint config. Prefer the standard library (`slices`, `maps`, `min`/`max`, `strings.Cut`) over hand-written loops; `go fix` applies these for you.
- Return errors with context (`fmt.Errorf("x: %w", err)`). One failing source must never fail the whole search.
- Pass `context.Context` to anything that does I/O.
- Keep comments about *why*, not *what*.
- Use table-driven tests.
- **Database changes go in a new migration** (`internal/store/migrations/00N_name.sql`). Never edit an existing migration: users' databases have already applied it.
- Don't change how `dedup.Key` is computed without a migration plan. Tracker entries and hidden jobs are stored by that key.

## Commits and pull requests

- Use [Conventional Commits](https://www.conventionalcommits.org/): `feat(sources): …`, `fix(tui): …`, `docs: …`, `test: …`, `chore: …`.
- Keep each pull request focused on one source, feature or fix.
- In the description, say what changed and why. For a new source, also link its terms or API docs and say how you verified that automated access is allowed.
- Update `README.md` when you change user-facing behaviour (flags, sources, config), and keep commands and flags in the docs matching `--help`.

## Reporting bugs and security issues

- **Bugs:** open an issue with the command you ran, what you expected, what happened, and the output of `jobhunt sources`. Remove API keys from anything you paste.
- **Security issues** (for example a way for a job posting to inject terminal escapes, or a leaked key): don't open a public issue. Contact the maintainer privately through GitHub.

## License

By contributing, you agree that your contributions are licensed under the [MIT License](LICENSE).
