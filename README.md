# jobhunt

Search many legal job sources at once: official APIs, public JSON endpoints and RSS feeds. Results are normalized into one schema and filtered by country, work mode, job type and CS area. Built for finding internships and entry-level roles. The default country is India.

> Status: milestone 1 of 6. Adzuna and Remotive are implemented, with a keyword classifier and CLI table output.

## Setup

```sh
make build                 # -> bin/jobhunt
./bin/jobhunt config init  # writes ~/.config/jobhunt/config.yaml
```

### API keys

| Source | Key | How to get it |
|---|---|---|
| Adzuna | `app_id` + `app_key` | Free signup at https://developer.adzuna.com/signup |
| Remotive | none | — |

Keys can go in the config file or come from env vars (`JOBHUNT_ADZUNA_APP_ID`, `JOBHUNT_ADZUNA_APP_KEY`). A source with a missing key is skipped with a message; it never fails the search.

## Usage

```sh
jobhunt search --country IN --type internship --area backend
jobhunt search -c IN,remote-global -t new-grad -k Go,Kubernetes -x senior
jobhunt search -c US -a hpc-gpu -k CUDA --posted-within 7d --refresh
```

Run `jobhunt search -h` for every filter.

**Country semantics.** `--country IN` also includes remote jobs that are open worldwide or to a region containing India (e.g. APAC). Use `--strict-country` to turn that off. Jobs whose country, type or area can't be determined are hidden unless you pass `--include-unknown`.

## Development

```sh
make test   # table-driven tests on recorded fixtures, no live network
make lint   # golangci-lint (installed on first run)
```

Layout: `cmd/jobhunt` (CLI), `internal/model` (schema + geo), `internal/sources/*` (one package per source), `internal/classify` (keyword classifier; lists in `keywords.yaml`), `internal/search` (concurrent fan-out, cache, filters), `internal/store` (SQLite).

## Data source etiquette

Each request sends an honest User-Agent and is rate-limited per source. Results are cached for 1h by default; Remotive is cached for ≥6h because its terms ask for at most ~4 fetches a day. Every listing links back to the original posting, as Remotive and Remote OK require. Sites whose terms forbid automated access (LinkedIn, Indeed, Naukri, Internshala, …) are never scraped; jobhunt prints pre-filled search links for them instead.
