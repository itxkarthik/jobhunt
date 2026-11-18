-- Normalized company of each stored job (dedup.NormCompany), so a
-- company's open jobs can be found without decoding every record. Rows
-- written before this migration get it the next time they're seen.
ALTER TABLE jobs ADD COLUMN company_key TEXT;
CREATE INDEX jobs_company ON jobs (company_key, updated_at);
