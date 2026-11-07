-- Chantier G : bibliothèque de gabarits paramétriques (internal/library) et calibration (internal/calibration).

-- Variantes nommées réutilisables d'un gabarit (save_as_template) : un gabarit + des paramètres figés.
CREATE TABLE part_template_variants (
    id               TEXT PRIMARY KEY,               -- identifiant lisible (slug) : « collier-tube-32 »
    base_template    TEXT NOT NULL,                  -- gabarit intégré dont elle dérive
    template_version TEXT NOT NULL,                  -- version du gabarit au moment de l'enregistrement
    title            TEXT NOT NULL,
    description      TEXT NOT NULL DEFAULT '',
    params           TEXT NOT NULL,                  -- JSON : paramètres figés
    source_model_id  TEXT,
    created_at       TEXT NOT NULL
);

-- Modèles générés par la bibliothèque : de quoi retrouver gabarit, version et paramètres exacts d'une version de modèle.
CREATE TABLE part_template_models (
    version_id       TEXT PRIMARY KEY,               -- version de modèle du catalogue
    model_id         TEXT NOT NULL,
    template         TEXT NOT NULL,                  -- gabarit intégré
    variant_id       TEXT,                           -- variante utilisée, le cas échéant
    template_version TEXT NOT NULL,
    params           TEXT NOT NULL,                  -- JSON : paramètres résolus (après calculs et calibration)
    requested        TEXT NOT NULL,                  -- JSON : paramètres demandés par l'appelant
    calcs            TEXT NOT NULL,                  -- JSON : calculs d'ingénierie [{name,value,unit,...}]
    calibration      TEXT,                           -- JSON : provenance de la calibration utilisée
    thumbnail_sha    TEXT,                           -- vignette SVG dans le magasin de blobs
    created_at       TEXT NOT NULL
);
CREATE INDEX idx_part_template_models_model ON part_template_models(model_id);

-- Séries de pièces de calibration générées.
CREATE TABLE calibration_sets (
    id          TEXT PRIMARY KEY,
    printer_key TEXT NOT NULL DEFAULT '',
    nozzle_mm   REAL NOT NULL,
    material    TEXT NOT NULL DEFAULT '',
    filament    TEXT NOT NULL DEFAULT '',
    doc         TEXT NOT NULL,                       -- JSON : pièces (modèles, mesures attendues, profils)
    created_at  TEXT NOT NULL
);

-- Mesures saisies par l'utilisateur (immuables) et corrections calculées.
CREATE TABLE calibration_records (
    id          TEXT PRIMARY KEY,
    set_id      TEXT REFERENCES calibration_sets(id),
    printer_key TEXT NOT NULL DEFAULT '',
    nozzle_mm   REAL NOT NULL,
    material    TEXT NOT NULL DEFAULT '',
    filament    TEXT NOT NULL DEFAULT '',
    measures    TEXT NOT NULL,                       -- JSON : mesures brutes
    results     TEXT NOT NULL,                       -- JSON : corrections calculées
    notes       TEXT NOT NULL DEFAULT '',
    created_at  TEXT NOT NULL
);
CREATE INDEX idx_calibration_records_key ON calibration_records(printer_key, nozzle_mm, material, filament);
CREATE TRIGGER calibration_records_no_update BEFORE UPDATE ON calibration_records
BEGIN SELECT RAISE(ABORT, 'calibration_records est immuable'); END;
CREATE TRIGGER calibration_records_no_delete BEFORE DELETE ON calibration_records
BEGIN SELECT RAISE(ABORT, 'calibration_records est immuable'); END;

-- Calibration active par (imprimante, buse, matériau, bobine) : fusion des mesures par type, la plus récente l'emporte.
-- Les clés vides (printer_key, filament = '') signifient « toutes » : repli du plus spécifique au plus général.
CREATE TABLE calibration_profiles (
    printer_key TEXT NOT NULL DEFAULT '',
    nozzle_mm   REAL NOT NULL,
    material    TEXT NOT NULL DEFAULT '',
    filament    TEXT NOT NULL DEFAULT '',
    doc         TEXT NOT NULL,                       -- JSON : engineering.Calibration
    record_id   TEXT NOT NULL REFERENCES calibration_records(id),
    updated_at  TEXT NOT NULL,
    PRIMARY KEY (printer_key, nozzle_mm, material, filament)
);
