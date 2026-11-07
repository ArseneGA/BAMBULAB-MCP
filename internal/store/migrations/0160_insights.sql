-- Coût, journal des impressions, tableau de bord (internal/insights). Numéro 0160 réservé au chantier C.

-- Réglages (prix de l'énergie, prix des matériaux, usure...) : un document JSON par clé. Aucun secret.
CREATE TABLE insight_settings (
    key        TEXT PRIMARY KEY,
    doc        TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

-- Fiche de journal : une par impression terminée, échouée ou annulée (créée à la demande depuis print_jobs).
-- Les colonnes indexées servent aux filtres et au tableau de bord ; doc porte la fiche complète.
CREATE TABLE print_logs (
    id              TEXT PRIMARY KEY,
    print_job_id    TEXT NOT NULL UNIQUE,
    printer_id      TEXT NOT NULL DEFAULT '',
    model_id        TEXT NOT NULL DEFAULT '',
    material_family TEXT NOT NULL DEFAULT '',
    outcome         TEXT NOT NULL,                 -- success | partial | failed | cancelled
    finished_at     TEXT NOT NULL,
    doc             TEXT NOT NULL,
    created_at      TEXT NOT NULL,
    updated_at      TEXT NOT NULL
);
CREATE INDEX idx_print_logs_finished ON print_logs(finished_at);
CREATE INDEX idx_print_logs_context  ON print_logs(material_family, printer_id);
