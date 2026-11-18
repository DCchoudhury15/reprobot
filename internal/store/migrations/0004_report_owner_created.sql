-- The submit rate limit counts a token's reports of the last hour.
CREATE INDEX reports_owner_created_at_idx ON reports (owner, created_at);
