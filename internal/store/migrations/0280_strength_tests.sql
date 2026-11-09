-- Tests de résistance (internal/strength) : un enregistrement par calcul, rattaché à la VERSION du modèle testée.
-- Migration ADDITIVE : une table et deux index nouveaux, aucune table existante modifiée.
--
-- RIEN DE SECRET ici : entrées du calcul (charges, appuis, matière, réglages, avec leur origine user | assumed | default),
-- résultat (contraintes, marge, verdict) et empreinte de l'image PNG conservée dans le stockage de blobs.
--   assumed       : 1 si une valeur d'entrée est une hypothèse et non une donnée de l'humain
--   input_json    : entrées normalisées (JSON)
--   result_json   : résultat complet (JSON)
--   png_sha256    : carte de contraintes PNG (blob)
CREATE TABLE strength_tests (
    id            TEXT PRIMARY KEY,
    model_id      TEXT NOT NULL,
    version_id    TEXT NOT NULL DEFAULT '',
    job_id        TEXT NOT NULL DEFAULT '',
    created_at    TEXT NOT NULL,
    material      TEXT NOT NULL DEFAULT '',
    verdict       TEXT NOT NULL DEFAULT '',
    safety_factor REAL NOT NULL DEFAULT 0,
    max_stress_mpa REAL NOT NULL DEFAULT 0,
    max_disp_mm   REAL NOT NULL DEFAULT 0,
    assumed       INTEGER NOT NULL DEFAULT 0,
    input_json    TEXT NOT NULL,
    result_json   TEXT NOT NULL,
    png_sha256    TEXT NOT NULL DEFAULT ''
);

CREATE INDEX strength_tests_model ON strength_tests(model_id, created_at);
CREATE INDEX strength_tests_version ON strength_tests(version_id);
