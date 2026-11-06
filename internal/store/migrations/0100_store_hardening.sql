-- Durcissement : version optimiste des documents, confirmation à usage unique
-- garantie par la base, immuabilité des fichiers sources.

ALTER TABLE materials           ADD COLUMN rev INTEGER NOT NULL DEFAULT 0;
ALTER TABLE printer_models      ADD COLUMN rev INTEGER NOT NULL DEFAULT 0;
ALTER TABLE printers            ADD COLUMN rev INTEGER NOT NULL DEFAULT 0;
ALTER TABLE print_jobs          ADD COLUMN rev INTEGER NOT NULL DEFAULT 0;
ALTER TABLE print_confirmations ADD COLUMN rev INTEGER NOT NULL DEFAULT 0;
ALTER TABLE manufacturing_goals ADD COLUMN rev INTEGER NOT NULL DEFAULT 0;

-- Colonne dédiée : le passage NULL -> date est atomique (UPDATE ... WHERE used_at IS NULL).
ALTER TABLE print_confirmations ADD COLUMN used_at TEXT;

-- Une confirmation utilisée ne peut plus être modifiée ni supprimée.
CREATE TRIGGER print_confirmations_used_no_update BEFORE UPDATE ON print_confirmations
WHEN OLD.used_at IS NOT NULL
BEGIN SELECT RAISE(ABORT, 'confirmation déjà utilisée : immuable'); END;
CREATE TRIGGER print_confirmations_used_no_delete BEFORE DELETE ON print_confirmations
WHEN OLD.used_at IS NOT NULL
BEGIN SELECT RAISE(ABORT, 'confirmation déjà utilisée : immuable'); END;

-- Le contenu d'un fichier source est immuable (id, format, taille, empreinte, date) ; nom, modèle, unité et disponibilité restent modifiables (catalogue).
CREATE TRIGGER source_files_no_update BEFORE UPDATE OF id, format, size_bytes, sha256, created_at ON source_files
BEGIN SELECT RAISE(ABORT, 'source_files est immuable'); END;
CREATE TRIGGER source_files_no_delete BEFORE DELETE ON source_files
BEGIN SELECT RAISE(ABORT, 'source_files est immuable'); END;
