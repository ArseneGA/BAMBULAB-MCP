-- Caméra (instantanés, détection d'échec) et notifications (chantier D). Numéro 0170 réservé à ce chantier.
-- Aucun secret ici : les codes d'accès, URLs de sujet ntfy, URLs de webhook, clés HMAC et mots de passe SMTP
-- vivent dans le coffre chiffré (ou des variables d'environnement), jamais dans la base.

-- Photos prises par la caméra (le contenu JPEG est dans le magasin de blobs, adressé par sha256).
CREATE TABLE camera_photos (
    id           TEXT PRIMARY KEY,
    printer_id   TEXT NOT NULL,
    print_job_id TEXT,                         -- impression concernée (photo de fin, alerte), sinon NULL
    kind         TEXT NOT NULL,                -- snapshot | confirm | final | alert
    sha256       TEXT NOT NULL,
    size         INTEGER NOT NULL,
    taken_at     TEXT NOT NULL
);
CREATE INDEX idx_camera_photos_job ON camera_photos(print_job_id, kind);
CREATE INDEX idx_camera_photos_printer ON camera_photos(printer_id, taken_at);
CREATE INDEX idx_camera_photos_sha ON camera_photos(sha256);

-- Réglages (document JSON par clé) : détection d'échec (intervalle, seuil...).
CREATE TABLE camera_settings (
    key        TEXT PRIMARY KEY,
    doc        TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

-- Alertes de détection d'échec (explicables : le rapport complet est conservé).
CREATE TABLE camera_alerts (
    id           TEXT PRIMARY KEY,
    printer_id   TEXT NOT NULL,
    print_job_id TEXT NOT NULL,
    at           TEXT NOT NULL,
    score        REAL NOT NULL,
    level        TEXT NOT NULL,
    report       TEXT NOT NULL,                -- JSON : signaux, valeurs, poids
    photo_sha256 TEXT
);
CREATE INDEX idx_camera_alerts_job ON camera_alerts(print_job_id, at);

-- Canaux de notification (configuration SANS secret).
CREATE TABLE notify_channels (
    id         TEXT PRIMARY KEY,
    name       TEXT NOT NULL UNIQUE,
    kind       TEXT NOT NULL,                  -- ntfy | webhook | email
    enabled    INTEGER NOT NULL DEFAULT 1,
    events     TEXT NOT NULL DEFAULT '[]',     -- JSON : évènements abonnés ; vide = tous
    config     TEXT NOT NULL DEFAULT '{}',     -- JSON : hôte/port/utilisateur/destinataires... jamais de secret
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

-- Journal des envois (sans contenu secret).
CREATE TABLE notify_log (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    at         TEXT NOT NULL,
    channel_id TEXT NOT NULL,
    event      TEXT NOT NULL,
    ok         INTEGER NOT NULL,
    error      TEXT
);
CREATE INDEX idx_notify_log_at ON notify_log(at);
