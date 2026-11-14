-- The crash site (first project function plus the text of its crashing
-- line) splits reports that share a signature but crash on different lines.
ALTER TABLE results ADD COLUMN site text;
