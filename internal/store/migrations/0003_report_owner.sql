-- owner is the name of the API token that submitted the report, NULL when
-- the server runs without tokens. Reporter tokens read only their own
-- reports, so an idempotency key is unique per owner: one reporter cannot
-- learn the id of another's report by sending the same key.
ALTER TABLE reports ADD COLUMN owner text;
ALTER TABLE reports DROP CONSTRAINT reports_idempotency_key_key;
CREATE UNIQUE INDEX reports_owner_idempotency_key_idx
	ON reports ((coalesce(owner, '')), idempotency_key);
