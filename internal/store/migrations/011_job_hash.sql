-- A hash of each stored job's content (not its score, which drifts as the
-- job ages), so a search rewrites only the records that changed. When a
-- job was last seen is in seen.last_seen.
ALTER TABLE jobs ADD COLUMN hash TEXT NOT NULL DEFAULT '';
