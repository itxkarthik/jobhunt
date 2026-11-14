-- Keep each pending job's record as it was selected for that consumer.
-- The shared jobs row is rewritten by every search (with that search's
-- score), so it can't be trusted when resending. NULL once delivered.
ALTER TABLE feed_deliveries ADD COLUMN job BLOB;
