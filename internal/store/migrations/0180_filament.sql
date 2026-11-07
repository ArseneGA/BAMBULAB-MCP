-- Bobines, AMS et file d'attente d'impressions (chantier E, internal/filament).

-- Inventaire des bobines. doc = JSON complet (matière, marque, couleur, poids, prix/kg, séchage, emplacement...).
-- slot_key = « imprimante|emplacement » (ex. pr_ab12|ams1:2) quand la bobine est chargée : un emplacement ne porte
-- qu'une bobine active à la fois.
CREATE TABLE spools (
    id         TEXT PRIMARY KEY,
    doc        TEXT NOT NULL,
    slot_key   TEXT NOT NULL DEFAULT '',
    archived   INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    rev        INTEGER NOT NULL DEFAULT 1
);
CREATE UNIQUE INDEX idx_spools_slot ON spools(slot_key) WHERE slot_key <> '' AND archived = 0;

-- Consommation d'une impression par bobine (un enregistrement par couple impression / bobine : idempotent).
-- grams = masse retirée de la bobine ; estimated = 1 si impression interrompue (au prorata de l'avancement).
-- price_per_kg et cost sont figés au moment du décompte (lisibles par le calcul de coût, chantier C).
CREATE TABLE spool_usage (
    print_job_id TEXT NOT NULL,
    spool_id     TEXT NOT NULL,
    filament_slot INTEGER NOT NULL DEFAULT 0,
    grams        REAL NOT NULL,
    estimated    INTEGER NOT NULL DEFAULT 0,
    price_per_kg REAL,
    cost         REAL,
    note         TEXT NOT NULL DEFAULT '',
    recorded_at  TEXT NOT NULL,
    PRIMARY KEY (print_job_id, spool_id)
);
CREATE INDEX idx_spool_usage_spool ON spool_usage(spool_id);

-- File d'attente. Une ligne = une impression (print_job_id) à préparer ; le lancement n'est JAMAIS automatique :
-- chaque démarrage passe par la confirmation humaine (/confirm). doc = JSON (imprimante, affectations, note).
CREATE TABLE print_queue (
    id           TEXT PRIMARY KEY,
    print_job_id TEXT NOT NULL,
    position     INTEGER NOT NULL,
    doc          TEXT NOT NULL,
    removed_at   TEXT,
    created_at   TEXT NOT NULL,
    updated_at   TEXT NOT NULL
);
CREATE UNIQUE INDEX idx_print_queue_job ON print_queue(print_job_id) WHERE removed_at IS NULL;

-- Réglages du chantier (marge d'alerte de manque, etc.).
CREATE TABLE filament_settings (
    key        TEXT PRIMARY KEY,
    value      TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
