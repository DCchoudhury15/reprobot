-- The signed result (a DSSE envelope) as the exact text that was signed.
-- It is text, not jsonb, because jsonb would reorder and rewrite it.
ALTER TABLE results ADD COLUMN envelope text;
