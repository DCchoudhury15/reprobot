-- A report may name a fix commit. The worker then also runs the report on
-- that commit and stores the fix check next to the result: its verdict,
-- the check as JSON, and the signed check as the exact text that was signed.
ALTER TABLE reports ADD COLUMN fix_commit text;
ALTER TABLE results ADD COLUMN fix_state text;
ALTER TABLE results ADD COLUMN fix jsonb;
ALTER TABLE results ADD COLUMN fix_envelope text;
