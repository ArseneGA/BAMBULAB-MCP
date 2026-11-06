-- Moteur d'objectifs et d'expériences (internal/goals). Numéro 0100 réservé
-- pour éviter les collisions avec les autres chantiers.

-- Un objectif structuré (immuable ; un nouvel appel crée un nouvel objectif).
CREATE TABLE goal_sets (
    id            TEXT PRIMARY KEY,
    model_id      TEXT,
    original_text TEXT NOT NULL,
    doc           TEXT NOT NULL,                 -- JSON : critères, ambiguïtés, conflits
    created_at    TEXT NOT NULL
);

-- Révisions de profil de découpe (immuables). overrides = liste cumulée.
CREATE TABLE slicing_profiles (
    id         TEXT PRIMARY KEY,
    parent_id  TEXT REFERENCES slicing_profiles(id),
    doc        TEXT NOT NULL,                    -- JSON : machine, process, filaments, overrides
    created_at TEXT NOT NULL
);
CREATE INDEX idx_slicing_profiles_parent ON slicing_profiles(parent_id);
CREATE TRIGGER slicing_profiles_no_update BEFORE UPDATE ON slicing_profiles
BEGIN SELECT RAISE(ABORT, 'slicing_profiles est immuable'); END;
CREATE TRIGGER slicing_profiles_no_delete BEFORE DELETE ON slicing_profiles
BEGIN SELECT RAISE(ABORT, 'slicing_profiles est immuable'); END;

-- Expérience : modèle figé (version + empreinte), base figée, objectif.
CREATE TABLE experiments (
    id               TEXT PRIMARY KEY,
    goal_id          TEXT REFERENCES goal_sets(id),
    model_id         TEXT NOT NULL,
    model_version_id TEXT NOT NULL,
    doc              TEXT NOT NULL,              -- JSON : source, base, budget, notes
    created_at       TEXT NOT NULL
);
CREATE INDEX idx_experiments_model ON experiments(model_id);

CREATE TABLE experiment_candidates (
    id            TEXT PRIMARY KEY,
    experiment_id TEXT NOT NULL REFERENCES experiments(id),
    idx           INTEGER NOT NULL,
    label         TEXT NOT NULL,
    spec          TEXT NOT NULL,                 -- JSON : surcharges, orientation, justification
    job_id        TEXT,
    state         TEXT NOT NULL DEFAULT 'pending', -- pending|queued|running|succeeded|failed|cancelled
    metrics       TEXT,                          -- JSON, rempli après découpe
    artifact_sha  TEXT,
    error         TEXT,
    updated_at    TEXT NOT NULL,
    UNIQUE (experiment_id, idx)
);

-- Variante appliquée à un modèle (historique ; la dernière ligne fait foi).
CREATE TABLE applied_variants (
    id               TEXT PRIMARY KEY,
    model_id         TEXT NOT NULL,
    model_version_id TEXT NOT NULL,
    experiment_id    TEXT NOT NULL,
    candidate_id     TEXT NOT NULL,
    profile_id       TEXT NOT NULL REFERENCES slicing_profiles(id),
    applied_at       TEXT NOT NULL
);
CREATE INDEX idx_applied_model ON applied_variants(model_id, applied_at);

-- Retours d'expérience, avec leur contexte de comparabilité.
CREATE TABLE print_results (
    id              TEXT PRIMARY KEY,
    experiment_id   TEXT NOT NULL,
    candidate_id    TEXT NOT NULL,
    model_id        TEXT NOT NULL,
    material_family TEXT NOT NULL,
    filament        TEXT NOT NULL,
    machine         TEXT NOT NULL,
    doc             TEXT NOT NULL,               -- JSON complet
    created_at      TEXT NOT NULL
);
CREATE INDEX idx_results_context ON print_results(material_family, machine);
