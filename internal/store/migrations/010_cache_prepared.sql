-- Cached jobs are stored cleaned (sources.SanitizeJob), with what the
-- classifier decided for each one, so a cached search skips both steps.
-- class_version identifies the classifier rules and logic that produced
-- class_json; '' means jobs_json predates this and isn't cleaned yet.
ALTER TABLE fetch_cache ADD COLUMN class_version TEXT NOT NULL DEFAULT '';
ALTER TABLE fetch_cache ADD COLUMN class_json BLOB;
