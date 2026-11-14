-- Replace the time-based feed cursor (003) with a per-consumer delivery
-- log. A timestamp can't tell apart jobs first seen in the same second,
-- and it loses jobs from a failed batch once they drop out of the source.
-- Each row is one job's delivery state for one consumer: 'pending' when
-- it went out in a batch that wasn't accepted, 'delivered' once it was.
DROP TABLE IF EXISTS feed_cursors;

CREATE TABLE feed_deliveries (
    cursor     TEXT    NOT NULL,
    key        TEXT    NOT NULL,
    state      TEXT    NOT NULL CHECK (state IN ('pending', 'delivered')),
    updated_at INTEGER NOT NULL,
    PRIMARY KEY (cursor, key)
);
