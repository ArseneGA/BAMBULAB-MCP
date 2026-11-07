-- Export de projet (internal/export) : archives zip partageables produites par un travail asynchrone.
-- Le zip vit dans <données>/exports/<id>.zip (jamais dans les blobs) et est supprimé à l'expiration.
-- Aucun secret ici : ni chemin du serveur, ni donnée d'imprimante.

CREATE TABLE project_exports (
    id          TEXT PRIMARY KEY,
    job_id      TEXT NOT NULL DEFAULT '',
    state       TEXT NOT NULL,                 -- queued | ready | failed | expired
    title       TEXT NOT NULL DEFAULT '',
    file_name   TEXT NOT NULL DEFAULT '',      -- nom proposé au téléchargement
    size_bytes  INTEGER NOT NULL DEFAULT 0,
    sha256      TEXT NOT NULL DEFAULT '',      -- empreinte de l'archive
    doc         TEXT NOT NULL DEFAULT '{}',    -- JSON : résumé (pièces, fichiers, avertissements)
    error       TEXT NOT NULL DEFAULT '',
    created_at  TEXT NOT NULL,
    expires_at  TEXT NOT NULL
);
CREATE INDEX idx_project_exports_created ON project_exports(created_at);
