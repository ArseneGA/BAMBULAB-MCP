-- Agent local v2 (docs/AGENT-V2.md). Migration ADDITIVE : une table neuve, aucune table existante modifiée.
--
-- Journal des autorisations de lancement signées par le serveur pour un agent (transport imprimante « via agent »).
-- Une ligne par autorisation émise : qui (agent), quoi (imprimante, empreinte du fichier, plateau), quand (émission,
-- expiration) et ce qu'il en est advenu (résultat remonté par l'agent). Le nonce est l'identifiant de la ligne :
-- il ne sert qu'une fois. Ce journal est de l'audit ; la vérification qui compte (signature, fraîcheur, nonce) est
-- refaite par l'agent, qui ne se fie pas au serveur pour cela.
--
-- Le champ « link » (direct | agent) et « agent_id » d'une imprimante vivent dans son document JSON
-- (table printers, comme tous les réglages d'imprimante) : absent = direct, donc aucune donnée existante ne change.
CREATE TABLE IF NOT EXISTS agent_v2_authz (
    id          TEXT PRIMARY KEY,
    agent_id    TEXT NOT NULL,
    printer_id  TEXT NOT NULL,
    sha256      TEXT NOT NULL,
    plate       INTEGER NOT NULL,
    issued_at   TEXT NOT NULL,
    expires_at  TEXT NOT NULL,
    result      TEXT,
    result_code TEXT,
    result_at   TEXT
);
CREATE INDEX IF NOT EXISTS idx_agent_v2_authz_printer ON agent_v2_authz(printer_id, issued_at);
