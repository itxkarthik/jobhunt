-- Named positions for `jobhunt feed --cursor`: the run time of the last
-- batch delivered to each consumer. Jobs first seen after it are pending.
CREATE TABLE feed_cursors (
    name         TEXT PRIMARY KEY,
    delivered_at INTEGER NOT NULL,
    updated_at   INTEGER NOT NULL
);
