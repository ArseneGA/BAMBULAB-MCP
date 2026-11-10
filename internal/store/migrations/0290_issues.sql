-- Carnet de bord des dysfonctionnements (internal/issues, chantier FB0). Migration ADDITIVE : une table et deux index
-- nouveaux, aucune table existante modifiée.
--
-- Une ligne par SYMPTÔME : le même outil avec le même titre normalisé (fingerprint) n'ajoute pas de ligne, il
-- incrémente `occurrences`. Les textes sont filtrés des secrets (codes, jetons, mots de passe) AVANT d'arriver ici.
--   severity : P0 (bloquant, sécurité) | P1 (grave) | P2 (gênant) | P3 (détail)
--   state    : new -> seen -> fixed (avec fix_commit) | dismissed (écarté)
--   regressions : nombre de fois où un constat déjà corrigé est revenu
CREATE TABLE issues (
    id           TEXT PRIMARY KEY,
    fingerprint  TEXT NOT NULL UNIQUE,
    title        TEXT NOT NULL,
    severity     TEXT NOT NULL DEFAULT 'P2',
    tool         TEXT NOT NULL DEFAULT '',
    expected     TEXT NOT NULL DEFAULT '',
    observed     TEXT NOT NULL DEFAULT '',
    print_job_id TEXT NOT NULL DEFAULT '',
    printer_id   TEXT NOT NULL DEFAULT '',
    model_id     TEXT NOT NULL DEFAULT '',
    occurrences  INTEGER NOT NULL DEFAULT 1,
    regressions  INTEGER NOT NULL DEFAULT 0,
    state        TEXT NOT NULL DEFAULT 'new',
    fix_commit   TEXT NOT NULL DEFAULT '',
    created_at   TEXT NOT NULL,
    last_seen_at TEXT NOT NULL,
    updated_at   TEXT NOT NULL
);

CREATE INDEX issues_state ON issues(state, severity, last_seen_at);
CREATE INDEX issues_tool ON issues(tool);
