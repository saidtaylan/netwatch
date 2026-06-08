-- 003_users_partial_index.sql — Fix users_username index to exclude tombstones.
--
-- Problem: the expression index `json_extract(payload, '$.username')` evaluates
-- against tombstone rows whose payload is set to 'null' (fixed from '' in
-- storage.go). json_extract('null','$.username') returns NULL and does not
-- violate UNIQUE, but some older SQLite builds still surface "malformed JSON"
-- when evaluating expression indexes.
--
-- Solution: convert the unique index to a PARTIAL index (WHERE tombstone = 0)
-- so SQLite never evaluates json_extract on tombstone rows at all.

DROP INDEX IF EXISTS users_username;
CREATE UNIQUE INDEX IF NOT EXISTS users_username
    ON users(json_extract(payload, '$.username'))
    WHERE tombstone = 0;
