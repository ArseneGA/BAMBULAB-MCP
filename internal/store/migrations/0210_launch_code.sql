-- Code de lancement personnel (docs/SECURITY.md § 12). Une seule ligne : le code (6 à 8 chiffres) n'est
-- JAMAIS stocké en clair, seulement son empreinte argon2id salée. Seuls la page humaine /setup/launch-code
-- (navigateur enregistré) et la commande « bambud launch-code » (terminal de l'humain) l'écrivent ; aucun
-- outil MCP ne lit ni n'écrit cette table.
--   failures        : échecs consécutifs depuis la dernière réussite (3 = verrouillage global)
--   locked_at       : non NULL = verrouillé ; seul un déverrouillage humain l'efface
--   last_attempt_at : délai minimal entre deux essais
CREATE TABLE launch_code (
    id              INTEGER PRIMARY KEY CHECK (id = 1),
    code_hash       TEXT NOT NULL,
    created_at      TEXT NOT NULL,
    updated_at      TEXT NOT NULL,
    failures        INTEGER NOT NULL DEFAULT 0,
    locked_at       TEXT,
    last_attempt_at TEXT
);
