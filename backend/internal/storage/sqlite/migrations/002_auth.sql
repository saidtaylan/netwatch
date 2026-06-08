-- 002_auth.sql — Authentication & frontend settings tables (B28).
--
-- Same generic schema as 001_initial.sql:
--   id           TEXT PRIMARY KEY     — unique identifier (UUID or slug)
--   payload      BLOB                 — JSON-encoded domain object
--   seq          INTEGER NOT NULL     — Lamport timestamp (LWW)
--   updated_at   TEXT    NOT NULL     — ISO-8601 wall clock
--   updated_by   TEXT    NOT NULL     — node name (LWW tiebreaker)
--   tombstone    INTEGER NOT NULL DEFAULT 0  — soft delete flag
--
-- Additional indexes:
--   * users_username — unique constraint on json_extract(payload, '$.username')

CREATE TABLE IF NOT EXISTS users (
    id          TEXT    PRIMARY KEY,
    payload     BLOB    NOT NULL,
    seq         INTEGER NOT NULL,
    updated_at  TEXT    NOT NULL,
    updated_by  TEXT    NOT NULL,
    tombstone   INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS users_seq ON users(seq);
CREATE UNIQUE INDEX IF NOT EXISTS users_username ON users(json_extract(payload, '$.username'));

CREATE TABLE IF NOT EXISTS frontend_settings (
    id          TEXT    PRIMARY KEY,
    payload     BLOB    NOT NULL,
    seq         INTEGER NOT NULL,
    updated_at  TEXT    NOT NULL,
    updated_by  TEXT    NOT NULL,
    tombstone   INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS frontend_settings_seq ON frontend_settings(seq);
