-- Companies the user watches (mostly startups), keyed by
-- dedup.NormCompany(name). The record is JSON so it can grow without
-- migrations; name is a column for listing and prefix lookups.
CREATE TABLE companies (
    key        TEXT PRIMARY KEY,
    name       TEXT NOT NULL,
    json       BLOB NOT NULL,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);
