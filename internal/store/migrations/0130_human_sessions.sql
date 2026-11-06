-- Navigateurs « humains » enregistrés (docs/SECURITY.md). Le modèle (MCP) n'a aucun accès à ces
-- tables : seul le terminal de l'humain (bambud enroll) crée un code, seul un navigateur qui le
-- saisit reçoit un cookie de session. Codes et jetons de session ne sont stockés que hachés.

CREATE TABLE human_enroll_codes (
    code_hash  TEXT PRIMARY KEY,
    created_at TEXT NOT NULL,
    expires_at TEXT NOT NULL,
    used_at    TEXT
);

CREATE TABLE human_sessions (
    id           TEXT PRIMARY KEY,
    token_hash   TEXT NOT NULL UNIQUE,
    label        TEXT NOT NULL DEFAULT '',
    created_at   TEXT NOT NULL,
    last_seen_at TEXT NOT NULL,
    expires_at   TEXT NOT NULL,
    revoked_at   TEXT
);

-- Aperçus PNG générés par capture_preview : seuls ces blobs sont servis par GET /api/previews/{sha}.
CREATE TABLE print_previews (
    sha256     TEXT PRIMARY KEY,
    created_at TEXT NOT NULL
);
