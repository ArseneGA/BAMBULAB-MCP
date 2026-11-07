-- OAuth 2.1 intégré (connecteur distant de Claude). Aucun secret en clair : jetons d'accès et de rafraîchissement
-- ne sont stockés que hachés (SHA-256). Clients publics seulement (PKCE S256 obligatoire).

CREATE TABLE oauth_clients (
    client_id     TEXT PRIMARY KEY,
    client_name   TEXT NOT NULL DEFAULT '',
    redirect_uris TEXT NOT NULL,            -- tableau JSON
    created_at    TEXT NOT NULL
);

-- Une autorisation accordée par l'humain (page de consentement) : porte le jeton de rafraîchissement courant.
CREATE TABLE oauth_grants (
    id                 TEXT PRIMARY KEY,
    client_id          TEXT NOT NULL REFERENCES oauth_clients(client_id) ON DELETE CASCADE,
    scope              TEXT NOT NULL,
    resource           TEXT NOT NULL,
    human_session_id   TEXT NOT NULL DEFAULT '',
    created_at         TEXT NOT NULL,
    last_used_at       TEXT NOT NULL,
    refresh_hash       TEXT NOT NULL UNIQUE,
    prev_refresh_hash  TEXT,
    refresh_expires_at TEXT NOT NULL,
    revoked_at         TEXT
);

CREATE TABLE oauth_access_tokens (
    token_hash TEXT PRIMARY KEY,
    grant_id   TEXT NOT NULL REFERENCES oauth_grants(id) ON DELETE CASCADE,
    expires_at TEXT NOT NULL
);
CREATE INDEX idx_oauth_access_grant ON oauth_access_tokens(grant_id);
