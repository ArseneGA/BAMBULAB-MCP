-- Passkeys (WebAuthn) : approbation du lancement et de l'arrêt par une clé que seul l'humain détient
-- (docs/SECURITY.md § 13). Migration ADDITIVE : deux tables nouvelles, aucune table existante modifiée.
--
-- RIEN DE SECRET ici : une passkey stocke l'identifiant public du credential et sa CLÉ PUBLIQUE (COSE). La clé
-- privée ne quitte jamais l'authentificateur de l'humain (Touch ID, Windows Hello, clé de sécurité, gestionnaire
-- de mots de passe). Lire cette table ne permet pas d'approuver quoi que ce soit.
--   credential_id : identifiant public du credential, base64url
--   public_key    : clé publique COSE (octets)
--   sign_count    : dernier compteur de signatures vu (un compteur qui ne progresse pas est refusé)
--   credential    : enregistrement complet du credential (JSON : drapeaux, AAGUID, transports), sans secret
--   revoked_at    : non NULL = révoquée (conservée pour l'audit, plus acceptée)
CREATE TABLE passkeys (
    id              TEXT PRIMARY KEY,
    credential_id   TEXT NOT NULL UNIQUE,
    label           TEXT NOT NULL,
    public_key      BLOB NOT NULL,
    sign_count      INTEGER NOT NULL DEFAULT 0,
    credential      TEXT NOT NULL,
    rp_id           TEXT NOT NULL,
    created_at      TEXT NOT NULL,
    created_by      TEXT NOT NULL DEFAULT '',
    last_used_at    TEXT,
    revoked_at      TEXT
);

-- Réglage unique : « exiger une passkey ». Absent ou 0 (défaut) : le code de lancement et le bouton simple
-- restent disponibles, comme avant. 1 : seule une passkey approuve (la page, la vue MCP App et le code refusent).
CREATE TABLE passkey_settings (
    id              INTEGER PRIMARY KEY CHECK (id = 1),
    require_passkey INTEGER NOT NULL DEFAULT 0,
    updated_at      TEXT NOT NULL
);
