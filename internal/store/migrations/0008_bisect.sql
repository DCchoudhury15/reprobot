-- A report may name a good commit: an older commit without the bug. The
-- worker then bisects to the first commit with the crash and stores the
-- bisect next to the result: its verdict, the bisect as JSON, and the
-- signed bisect as the exact text that was signed.
ALTER TABLE reports ADD COLUMN good_commit text;
ALTER TABLE results ADD COLUMN bisect_state text;
ALTER TABLE results ADD COLUMN bisect jsonb;
ALTER TABLE results ADD COLUMN bisect_envelope text;
