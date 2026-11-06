-- Schéma initial. Les horodatages sont en TEXT RFC 3339 UTC.

CREATE TABLE collections (
    id         TEXT PRIMARY KEY,
    name       TEXT NOT NULL,
    parent_id  TEXT REFERENCES collections(id),
    created_at TEXT NOT NULL
);

CREATE TABLE models (
    id            TEXT PRIMARY KEY,
    collection_id TEXT REFERENCES collections(id),
    name          TEXT NOT NULL,
    tags          TEXT NOT NULL DEFAULT '[]',   -- JSON array
    dimensions    TEXT,                         -- JSON {x,y,z} ou NULL = inconnu
    created_at    TEXT NOT NULL,
    updated_at    TEXT NOT NULL,
    deleted_at    TEXT                          -- corbeille
);
CREATE INDEX idx_models_collection ON models(collection_id);

CREATE TABLE source_files (
    id           TEXT PRIMARY KEY,
    model_id     TEXT NOT NULL REFERENCES models(id),
    name         TEXT NOT NULL,
    format       TEXT NOT NULL CHECK (format IN ('stl','obj','step','3mf')),
    size_bytes   INTEGER NOT NULL,
    sha256       TEXT NOT NULL CHECK (length(sha256) = 64),
    availability TEXT NOT NULL CHECK (availability IN ('linux','windows_only')),
    origin_path  TEXT,
    created_at   TEXT NOT NULL
);
CREATE INDEX idx_source_files_model ON source_files(model_id);
CREATE INDEX idx_source_files_sha ON source_files(sha256);   -- doublons exacts

CREATE TABLE model_versions (
    id              TEXT PRIMARY KEY,
    model_id        TEXT NOT NULL REFERENCES models(id),
    parent_id       TEXT REFERENCES model_versions(id),
    label           TEXT NOT NULL,
    note            TEXT NOT NULL DEFAULT '',
    source_file_ids TEXT NOT NULL DEFAULT '[]',          -- JSON array
    created_at      TEXT NOT NULL
);
CREATE INDEX idx_versions_model ON model_versions(model_id);

-- Une version est immuable : aucune mise à jour ni suppression.
CREATE TRIGGER model_versions_no_update BEFORE UPDATE ON model_versions
BEGIN SELECT RAISE(ABORT, 'model_versions est immuable'); END;
CREATE TRIGGER model_versions_no_delete BEFORE DELETE ON model_versions
BEGIN SELECT RAISE(ABORT, 'model_versions est immuable'); END;

CREATE TABLE jobs (
    id               TEXT PRIMARY KEY,
    kind             TEXT NOT NULL,
    class            TEXT NOT NULL CHECK (class IN ('heavy','light')),
    state            TEXT NOT NULL CHECK (state IN ('queued','running','succeeded','failed','cancelled','unknown')),
    progress         REAL NOT NULL DEFAULT 0,
    message          TEXT NOT NULL DEFAULT '',
    logs             TEXT NOT NULL DEFAULT '[]',        -- JSON array borné
    params           TEXT,
    result           TEXT,
    error            TEXT,                               -- JSON {code,message}
    idempotency_key  TEXT,
    attempts         INTEGER NOT NULL DEFAULT 0,
    cancel_requested INTEGER NOT NULL DEFAULT 0,
    worker_id        TEXT,
    created_at       TEXT NOT NULL,
    started_at       TEXT,
    heartbeat_at     TEXT,
    finished_at      TEXT
);
CREATE UNIQUE INDEX idx_jobs_idempotency ON jobs(idempotency_key) WHERE idempotency_key IS NOT NULL;
CREATE INDEX idx_jobs_state ON jobs(state, class, created_at);

-- Entités à documents JSON (jalons suivants : CRUD typé par package).
CREATE TABLE materials          (id TEXT PRIMARY KEY, doc TEXT NOT NULL, updated_at TEXT NOT NULL);
CREATE TABLE printer_models     (id TEXT PRIMARY KEY, doc TEXT NOT NULL, updated_at TEXT NOT NULL);
CREATE TABLE printers           (id TEXT PRIMARY KEY, doc TEXT NOT NULL, updated_at TEXT NOT NULL);
CREATE TABLE print_jobs         (id TEXT PRIMARY KEY, doc TEXT NOT NULL, updated_at TEXT NOT NULL);
CREATE TABLE print_confirmations(id TEXT PRIMARY KEY, doc TEXT NOT NULL, updated_at TEXT NOT NULL);
CREATE TABLE manufacturing_goals(id TEXT PRIMARY KEY, doc TEXT NOT NULL, updated_at TEXT NOT NULL);

-- Journal d'audit des opérations (mutations).
CREATE TABLE audit_log (
    seq         INTEGER PRIMARY KEY AUTOINCREMENT,
    at          TEXT NOT NULL,
    actor       TEXT NOT NULL,
    operation   TEXT NOT NULL,
    entity_type TEXT NOT NULL,
    entity_id   TEXT NOT NULL,
    detail      TEXT                                    -- JSON
);
CREATE INDEX idx_audit_entity ON audit_log(entity_type, entity_id);
