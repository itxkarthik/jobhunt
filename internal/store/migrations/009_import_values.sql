-- A value an importer keeps per item, e.g. a company's paid-up capital
-- at the last MCA import, to notice changes between runs.
ALTER TABLE import_items ADD COLUMN value TEXT NOT NULL DEFAULT '';
