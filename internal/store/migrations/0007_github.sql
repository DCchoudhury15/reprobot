-- A report that came from a GitHub private vulnerability report keeps where
-- it came from, so the verdict can be posted back as an advisory comment.
-- The comment is a small queue of its own: attempts and a retry time, and
-- the time it was posted.
ALTER TABLE reports ADD COLUMN github_installation bigint;
ALTER TABLE reports ADD COLUMN github_repo text;
ALTER TABLE reports ADD COLUMN github_ghsa text;
ALTER TABLE reports ADD COLUMN github_comment_attempts int NOT NULL DEFAULT 0;
ALTER TABLE reports ADD COLUMN github_comment_after timestamptz;
ALTER TABLE reports ADD COLUMN github_commented_at timestamptz;
ALTER TABLE reports ADD COLUMN github_comment_error text;
CREATE INDEX reports_github_pending ON reports (github_comment_after)
    WHERE github_ghsa IS NOT NULL AND github_commented_at IS NULL;
