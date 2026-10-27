-- First/last time each dedup key appeared in results ("new since last run").
CREATE TABLE seen (
    key        TEXT PRIMARY KEY,
    first_seen INTEGER NOT NULL,
    last_seen  INTEGER NOT NULL
);

-- Latest merged record per key, so the tracker can show jobs after they
-- drop out of the cache.
CREATE TABLE jobs (
    key        TEXT PRIMARY KEY,
    json       BLOB NOT NULL,
    updated_at INTEGER NOT NULL
);

-- Application tracker: saved -> applied -> oa -> interview -> offer/rejected.
CREATE TABLE tracker (
    key        TEXT PRIMARY KEY,
    status     TEXT NOT NULL,
    notes      TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    applied_at INTEGER
);

CREATE TABLE tracker_events (
    id     INTEGER PRIMARY KEY AUTOINCREMENT,
    key    TEXT NOT NULL,
    at     INTEGER NOT NULL,
    status TEXT NOT NULL,
    note   TEXT NOT NULL DEFAULT ''
);
CREATE INDEX tracker_events_key ON tracker_events (key, at);

CREATE TABLE hidden (
    key TEXT PRIMARY KEY,
    at  INTEGER NOT NULL
);

CREATE TABLE presets (
    name         TEXT PRIMARY KEY,
    filters_json TEXT NOT NULL,
    updated_at   INTEGER NOT NULL
);
