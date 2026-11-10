-- G9, additive : suivi de projet (projets de premier rang, pièces, notes, déclinaisons) et verdict d'impression
-- (docs/specs/G9-suivi-de-projet.md § 5). Aucun DROP, aucun renommage, aucune donnée migrée : des tables nouvelles,
-- des colonnes NOT NULL DEFAULT sur print_logs (sans effet sur l'existant) et des index.
-- Les colonnes de camera_photos (journal photo à chaque 10 %, D8) relèvent de la migration 0310, pas de celle-ci.

-- Projet : regroupe des pièces (modèles) avec une quantité voulue ; parent_id = sous-projet / assemblage (D9).
-- status : idea | design | printing | done | in_use | abandoned (transitions libres ; abandoned et in_use posent closed_at).
-- Un statut peut être SUGGÉRÉ par le calcul, jamais appliqué sans appel explicite.
CREATE TABLE projects (
    id            TEXT PRIMARY KEY,
    name          TEXT NOT NULL,
    description   TEXT NOT NULL DEFAULT '',
    status        TEXT NOT NULL DEFAULT 'idea' CHECK (status IN ('idea','design','printing','done','in_use','abandoned')),
    collection_id TEXT REFERENCES collections(id),      -- adoption facultative d'une collection
    parent_id     TEXT REFERENCES projects(id),         -- NULL = projet racine
    due_date      TEXT,
    created_at    TEXT NOT NULL,
    updated_at    TEXT NOT NULL,
    closed_at     TEXT,
    archived      INTEGER NOT NULL DEFAULT 0,           -- archivage seulement : aucune suppression définitive
    rev           INTEGER NOT NULL DEFAULT 1
);
CREATE INDEX idx_projects_status ON projects(status, updated_at);
CREATE INDEX idx_projects_parent ON projects(parent_id);

CREATE TABLE project_parts (
    project_id TEXT NOT NULL REFERENCES projects(id),
    model_id   TEXT NOT NULL REFERENCES models(id),
    quantity   INTEGER NOT NULL DEFAULT 1 CHECK (quantity BETWEEN 1 AND 1000),
    role       TEXT NOT NULL DEFAULT '',
    position   INTEGER NOT NULL DEFAULT 0,
    added_at   TEXT NOT NULL,
    PRIMARY KEY (project_id, model_id)
);
CREATE INDEX idx_project_parts_model ON project_parts(model_id);

-- Notes et étiquettes libres d'un projet ou d'une impression (PJ12 s'y branchera ; PJ11 y range la note d'un verdict).
-- Pour une pièce, les étiquettes restent dans models.tags. Aucun secret ici : la documentation avertit.
CREATE TABLE entity_notes (
    id         TEXT PRIMARY KEY,
    kind       TEXT NOT NULL CHECK (kind IN ('project','print')),
    entity_id  TEXT NOT NULL,
    text       TEXT NOT NULL DEFAULT '',
    tags       TEXT NOT NULL DEFAULT '[]',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE INDEX idx_entity_notes_entity ON entity_notes(kind, entity_id);

-- Déclinaisons d'un projet / assemblage selon un objectif (PJ13, vague 3) : tables créées ici, vides.
-- Les totaux (temps, grammes, coût, solidité) ne sont JAMAIS stockés : recalculés à la demande.
CREATE TABLE project_variants (
    id          TEXT PRIMARY KEY,
    project_id  TEXT NOT NULL REFERENCES projects(id),
    name        TEXT NOT NULL,
    goal_set_id TEXT REFERENCES goal_sets(id),
    goal_kind   TEXT NOT NULL DEFAULT 'custom',         -- fast | strong | balanced | all_fast | custom
    chosen_at   TEXT,
    created_at  TEXT NOT NULL,
    updated_at  TEXT NOT NULL,
    rev         INTEGER NOT NULL DEFAULT 1
);
CREATE INDEX idx_project_variants_project ON project_variants(project_id);

CREATE TABLE project_variant_parts (
    variant_id              TEXT NOT NULL REFERENCES project_variants(id),
    model_id                TEXT NOT NULL REFERENCES models(id),
    profile_id              TEXT REFERENCES slicing_profiles(id),
    experiment_candidate_id TEXT REFERENCES experiment_candidates(id),
    applied_variant_id      TEXT REFERENCES applied_variants(id),
    PRIMARY KEY (variant_id, model_id)
);

-- Verdict de l'humain sur une impression (PJ11) et suivi (PJ2). Outcome (état technique) n'est pas écrasé.
--   verdict        : '' (pas encore jugé) | success | failed | redo
--   fail_reason    : stringing | warping | adhesion | spaghetti | layer_shift | under_extrusion | nozzle_clog | other
--   verdict_source : human_card | human_web | claude_on_request
--   origin         : printalk | external (impression déclarée hors Printalk, PJ12)
--   part_count     : nombre d'exemplaires de la pièce sur la plaque
--   reached_100    : 1 quand l'impression est allée jusqu'à 100 % (D7 : « imprimée », verdict ou pas)
ALTER TABLE print_logs ADD COLUMN verdict TEXT NOT NULL DEFAULT '';
ALTER TABLE print_logs ADD COLUMN fail_reason TEXT NOT NULL DEFAULT '';
ALTER TABLE print_logs ADD COLUMN verdict_at TEXT NOT NULL DEFAULT '';
ALTER TABLE print_logs ADD COLUMN verdict_source TEXT NOT NULL DEFAULT '';
ALTER TABLE print_logs ADD COLUMN origin TEXT NOT NULL DEFAULT 'printalk';
ALTER TABLE print_logs ADD COLUMN part_count INTEGER NOT NULL DEFAULT 1;
ALTER TABLE print_logs ADD COLUMN reached_100 INTEGER NOT NULL DEFAULT 0;
CREATE INDEX idx_print_logs_model ON print_logs(model_id, finished_at);
CREATE INDEX idx_print_logs_verdict ON print_logs(verdict, finished_at);
