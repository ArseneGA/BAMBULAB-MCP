-- Jalon 2 : catalogue documentaire (dossier §14.2 / §15.4).
-- Numéro 0020 : laisse de la place aux migrations des autres jalons.

ALTER TABLE models ADD COLUMN description TEXT NOT NULL DEFAULT '';
ALTER TABLE models ADD COLUMN license     TEXT NOT NULL DEFAULT '';
ALTER TABLE models ADD COLUMN provenance  TEXT;                        -- JSON {source,origin_path,...}

ALTER TABLE source_files ADD COLUMN rel_path        TEXT NOT NULL DEFAULT '';  -- structure relative d'origine
ALTER TABLE source_files ADD COLUMN origin_mtime_ns INTEGER;
ALTER TABLE source_files ADD COLUMN unit            TEXT NOT NULL DEFAULT '';  -- unité confirmée par l'utilisateur ('' = inconnue)
CREATE INDEX idx_source_files_origin ON source_files(origin_path);
CREATE INDEX idx_models_deleted ON models(deleted_at);

-- Produits dérivés (jamais à la place d'un original) : analyse, aperçu GLB,
-- sorties du worker STEP. Clé = empreinte du fichier source.
CREATE TABLE derived_assets (
    source_sha TEXT NOT NULL,
    kind       TEXT NOT NULL,        -- analysis | preview_glb | step_stl | step_3mf
    state      TEXT NOT NULL CHECK (state IN ('ok','pending','error','backend_missing')),
    blob_sha   TEXT,                 -- blob dérivé (GLB, STL, 3MF)
    doc        TEXT,                 -- JSON (analyse, métadonnées)
    note       TEXT NOT NULL DEFAULT '',
    updated_at TEXT NOT NULL,
    PRIMARY KEY (source_sha, kind)
);
CREATE INDEX idx_derived_blob ON derived_assets(blob_sha);

-- Index d'import incrémental : un chemin d'origine déjà vu (taille + mtime + sha256).
CREATE TABLE import_index (
    origin_path    TEXT PRIMARY KEY,
    size_bytes     INTEGER NOT NULL,
    mtime_ns       INTEGER NOT NULL,
    sha256         TEXT NOT NULL,
    source_file_id TEXT NOT NULL REFERENCES source_files(id),
    copied         INTEGER NOT NULL,
    seen_at        TEXT NOT NULL
);

-- Aperçus d'opérations (jeton à usage unique, stocké haché).
CREATE TABLE op_previews (
    token_hash TEXT PRIMARY KEY,
    plan       TEXT NOT NULL,
    created_at TEXT NOT NULL,
    expires_at TEXT NOT NULL,
    used_at    TEXT
);
