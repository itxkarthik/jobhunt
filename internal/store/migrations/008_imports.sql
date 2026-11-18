-- Items an importer has finished (an EDGAR day or filing, an HN thread,
-- an MCA snapshot), so repeat runs skip them without a request.
CREATE TABLE import_items (
    source TEXT NOT NULL,
    item   TEXT NOT NULL,
    at     INTEGER NOT NULL,
    PRIMARY KEY (source, item)
) WITHOUT ROWID;
