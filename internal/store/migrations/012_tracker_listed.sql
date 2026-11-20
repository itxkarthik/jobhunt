-- When a tracked job's posting last showed up in fetched results, so the
-- tracker can say when a posting seems to have closed. Rows already tracked
-- start as listed now.
ALTER TABLE tracker ADD COLUMN listed_at INTEGER;
UPDATE tracker SET listed_at = CAST(strftime('%s', 'now') AS INTEGER);

-- Small named values, such as when the database was last pruned.
CREATE TABLE meta (
    key   TEXT PRIMARY KEY,
    value INTEGER NOT NULL
);
