-- 001_initial.sql — Foundation tables for netwatch storage layer.
--
-- Each domain table follows the same generic schema:
--   id           TEXT PRIMARY KEY     — unique identifier (UUID or slug)
--   payload      BLOB                 — JSON-encoded domain object
--   seq          INTEGER NOT NULL     — Lamport timestamp (LWW)
--   updated_at   TEXT    NOT NULL     — ISO-8601 wall clock
--   updated_by   TEXT    NOT NULL     — node name (LWW tiebreaker)
--   tombstone    INTEGER NOT NULL DEFAULT 0  — soft delete flag
--
-- The (seq, updated_at, updated_by) tuple is the Version. Conflict
-- resolution is implemented in storage.Version.Compare; the database
-- enforces nothing — staleness checks happen in the Go layer before write.
--
-- Indexes:
--   * Primary key on id (B-tree)
--   * seq index for SinceSeq filter (used by anti-entropy push-pull)

CREATE TABLE IF NOT EXISTS slo_targets (
    id          TEXT    PRIMARY KEY,
    payload     BLOB    NOT NULL,
    seq         INTEGER NOT NULL,
    updated_at  TEXT    NOT NULL,
    updated_by  TEXT    NOT NULL,
    tombstone   INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS slo_targets_seq ON slo_targets(seq);

CREATE TABLE IF NOT EXISTS apps (
    id          TEXT    PRIMARY KEY,
    payload     BLOB    NOT NULL,
    seq         INTEGER NOT NULL,
    updated_at  TEXT    NOT NULL,
    updated_by  TEXT    NOT NULL,
    tombstone   INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS apps_seq ON apps(seq);

CREATE TABLE IF NOT EXISTS notification_channels (
    id          TEXT    PRIMARY KEY,
    payload     BLOB    NOT NULL,
    seq         INTEGER NOT NULL,
    updated_at  TEXT    NOT NULL,
    updated_by  TEXT    NOT NULL,
    tombstone   INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS notification_channels_seq ON notification_channels(seq);

CREATE TABLE IF NOT EXISTS silences (
    id          TEXT    PRIMARY KEY,
    payload     BLOB    NOT NULL,
    seq         INTEGER NOT NULL,
    updated_at  TEXT    NOT NULL,
    updated_by  TEXT    NOT NULL,
    tombstone   INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS silences_seq ON silences(seq);

CREATE TABLE IF NOT EXISTS maintenance_windows (
    id          TEXT    PRIMARY KEY,
    payload     BLOB    NOT NULL,
    seq         INTEGER NOT NULL,
    updated_at  TEXT    NOT NULL,
    updated_by  TEXT    NOT NULL,
    tombstone   INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS maintenance_windows_seq ON maintenance_windows(seq);

CREATE TABLE IF NOT EXISTS targets (
    id          TEXT    PRIMARY KEY,
    payload     BLOB    NOT NULL,
    seq         INTEGER NOT NULL,
    updated_at  TEXT    NOT NULL,
    updated_by  TEXT    NOT NULL,
    tombstone   INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS targets_seq ON targets(seq);

CREATE TABLE IF NOT EXISTS alerts (
    id          TEXT    PRIMARY KEY,
    payload     BLOB    NOT NULL,
    seq         INTEGER NOT NULL,
    updated_at  TEXT    NOT NULL,
    updated_by  TEXT    NOT NULL,
    tombstone   INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS alerts_seq ON alerts(seq);

CREATE TABLE IF NOT EXISTS alert_events (
    id          TEXT    PRIMARY KEY,
    payload     BLOB    NOT NULL,
    seq         INTEGER NOT NULL,
    updated_at  TEXT    NOT NULL,
    updated_by  TEXT    NOT NULL,
    tombstone   INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS alert_events_seq ON alert_events(seq);

CREATE TABLE IF NOT EXISTS slo_incidents (
    id          TEXT    PRIMARY KEY,
    payload     BLOB    NOT NULL,
    seq         INTEGER NOT NULL,
    updated_at  TEXT    NOT NULL,
    updated_by  TEXT    NOT NULL,
    tombstone   INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS slo_incidents_seq ON slo_incidents(seq);

CREATE TABLE IF NOT EXISTS target_states (
    id          TEXT    PRIMARY KEY,
    payload     BLOB    NOT NULL,
    seq         INTEGER NOT NULL,
    updated_at  TEXT    NOT NULL,
    updated_by  TEXT    NOT NULL,
    tombstone   INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS target_states_seq ON target_states(seq);

CREATE TABLE IF NOT EXISTS audit_log (
    id          TEXT    PRIMARY KEY,
    payload     BLOB    NOT NULL,
    seq         INTEGER NOT NULL,
    updated_at  TEXT    NOT NULL,
    updated_by  TEXT    NOT NULL,
    tombstone   INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS audit_log_seq ON audit_log(seq);
