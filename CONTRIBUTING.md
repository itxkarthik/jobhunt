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
5. **Treat fetched data as untrusted.** Every job passes through `sources.SanitizeJob` in the engine. Don't print or export source text by any path that skips it.

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

The steps are in the README under **Adding a source**. In short:
1. Create `internal/sources/<name>/` implementing `sources.Source`, plus any of the optional interfaces it needs (`Readier`, `Linker`, `RateLimited`, `MinTTLer`, `DefaultTTLer`, `Timeouter`, `CacheKeyer`).
2. Use the shared `*sources.Client` for every HTTP request.
3. Add a fixture and a table-driven test.
4. Register it in `internal/app/catalog.go` and `internal/config/config.example.yaml`.
5. Paid or key-based sources must be **off by default** and report a missing key via `Ready()`.

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
- Update `README.md` when you change user-facing behaviour (flags, sources, config).

## Reporting bugs and security issues

- **Bugs:** open an issue with the command you ran, what you expected, what happened, and the output of `jobhunt sources`. Remove API keys from anything you paste.
- **Security issues** (for example a way for a job posting to inject terminal escapes, or a leaked key): don't open a public issue. Contact the maintainer privately through GitHub.

## License

By contributing, you agree that your contributions are licensed under the [MIT License](LICENSE).
