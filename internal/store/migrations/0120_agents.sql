-- Jalon 8 : agent Windows (dossier §3, §15.2, §15.4). Numéro 0080 : laisse de la place aux autres jalons.

-- Agents appairés. Le secret n'est jamais stocké : seulement son empreinte SHA-256.
CREATE TABLE agents (
    id            TEXT PRIMARY KEY,
    name          TEXT NOT NULL,
    secret_hash   TEXT NOT NULL,
    host          TEXT NOT NULL DEFAULT '',
    agent_version TEXT NOT NULL DEFAULT '',
    protocol      INTEGER NOT NULL DEFAULT 1,
    capabilities  TEXT,                       -- JSON annoncé par l'agent (hello)
    created_at    TEXT NOT NULL,
    last_seen_at  TEXT,
    revoked_at    TEXT
);

-- Jetons d'appairage : générés par bambud, usage unique, expirent. Stockés hachés.
CREATE TABLE agent_pairings (
    token_hash TEXT PRIMARY KEY,
    created_at TEXT NOT NULL,
    expires_at TEXT NOT NULL,
    used_at    TEXT,
    agent_id   TEXT
);

-- Travaux assignés à un exécuteur distant (executor = 'windows-agent').
CREATE TABLE agent_tasks (
    id               TEXT PRIMARY KEY,
    agent_id         TEXT NOT NULL REFERENCES agents(id),
    executor         TEXT NOT NULL DEFAULT 'windows-agent',
    kind             TEXT NOT NULL,
    params           TEXT NOT NULL DEFAULT '{}',
    inputs           TEXT NOT NULL DEFAULT '[]',   -- artefacts d'entrée [{sha256,name,size}]
    state            TEXT NOT NULL CHECK (state IN ('queued','assigned','running','succeeded','failed','cancelled','unknown')),
    physical         INTEGER NOT NULL DEFAULT 0,   -- travail physique : jamais transféré automatiquement
    cancel_requested INTEGER NOT NULL DEFAULT 0,
    progress         REAL NOT NULL DEFAULT 0,
    message          TEXT NOT NULL DEFAULT '',
    result           TEXT,
    error            TEXT,
    note             TEXT NOT NULL DEFAULT '',
    replaces         TEXT,
    created_at       TEXT NOT NULL,
    assigned_at      TEXT,
    started_at       TEXT,
    finished_at      TEXT,
    updated_at       TEXT NOT NULL
);
CREATE INDEX idx_agent_tasks_agent_state ON agent_tasks(agent_id, state);

-- Transferts autorisés (agent -> serveur) : seul un envoi déclaré ici est accepté.
CREATE TABLE agent_uploads (
    agent_id   TEXT NOT NULL,
    task_id    TEXT NOT NULL,
    sha256     TEXT NOT NULL,
    size_bytes INTEGER NOT NULL,
    target     TEXT NOT NULL CHECK (target IN ('inbox','artifact')),
    root_label TEXT NOT NULL DEFAULT '',
    rel_path   TEXT NOT NULL DEFAULT '',
    name       TEXT NOT NULL DEFAULT '',
    state      TEXT NOT NULL CHECK (state IN ('pending','done')),
    updated_at TEXT NOT NULL,
    PRIMARY KEY (task_id, sha256, rel_path)
);

-- Inventaire en lecture seule des dossiers Windows autorisés.
CREATE TABLE agent_inventory (
    agent_id       TEXT NOT NULL,
    root_label     TEXT NOT NULL,
    rel_path       TEXT NOT NULL,
    size_bytes     INTEGER NOT NULL,
    mtime_ns       INTEGER NOT NULL,
    sha256         TEXT NOT NULL,
    origin_path    TEXT NOT NULL DEFAULT '',
    state          TEXT NOT NULL CHECK (state IN ('inventoried','requested','uploaded','imported','error')),
    source_file_id TEXT,
    note           TEXT NOT NULL DEFAULT '',
    updated_at     TEXT NOT NULL,
    PRIMARY KEY (agent_id, root_label, rel_path)
);
