-- Cached normalized results per source and query.
CREATE TABLE fetch_cache (
    source     TEXT NOT NULL,
    query_key  TEXT NOT NULL,
    fetched_at INTEGER NOT NULL, -- unix seconds
    jobs_json  BLOB NOT NULL,
    PRIMARY KEY (source, query_key)
);

-- One row per source per search run, for `jobhunt sources`.
CREATE TABLE source_runs (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    source      TEXT NOT NULL,
    run_at      INTEGER NOT NULL,
    status      TEXT NOT NULL, -- ok | cached | skipped | error
    count       INTEGER NOT NULL DEFAULT 0,
    error       TEXT NOT NULL DEFAULT '',
    duration_ms INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX source_runs_source_run_at ON source_runs (source, run_at DESC);
