-- Journal photo à chaque 10 % de progression (G9 D8, étend N19). Migration ADDITIVE : trois colonnes et un index sur
-- camera_photos, aucune table ni colonne existante modifiée.
--   percent : palier 10..100 atteint (NULL pour les photos hors paliers : confirmation, alerte, photo de fin d'un échec...)
--   status  : ok (image rangée) | unavailable (palier consigné sans image : caméra absente, hors ligne, image vide, erreur)
--   reason  : raison lisible quand status = unavailable (jamais de secret)
-- Une ligne « unavailable » a sha256 = '' et size = 0 : aucun blob ne lui correspond.
ALTER TABLE camera_photos ADD COLUMN percent INTEGER;
ALTER TABLE camera_photos ADD COLUMN status  TEXT NOT NULL DEFAULT 'ok';
ALTER TABLE camera_photos ADD COLUMN reason  TEXT NOT NULL DEFAULT '';
CREATE INDEX idx_camera_photos_progress ON camera_photos(print_job_id, percent);
