# J4 — Worker géométrie (Python, périmètre réduit)

Date : 2026-11-06. Cap de l'orchestrateur : tout en Go sauf ce qui n'a pas d'équivalent Go. Le worker Python `workers/geometry` ne garde que :

1. `step_import` : STEP -> tessellation OpenCASCADE à tolérance contrôlée -> STL binaire (tous solides) + 3MF (un objet par solide, mm) + métadonnées (objets, volume, aire, bbox, contrôle watertight/arêtes ouvertes de la tessellation, unité source lue dans l'en-tête, sha256). Sans OCP : `step_backend_missing`.
2. `parametric` : gabarits CadQuery `clamp`, `bracket`, `box` (boîtier + couvercle), `spacer`, `hook` -> STL + STEP + `*.source.json` (paramètres + hypothèses) ; avertissements (parois < 2 × buse, perçages trop serrés...). Sans CadQuery : `cad_backend_missing`.

Retirés (réécriture Go) : import STL/OBJ/3MF, `derive_preview`, `analyze`, `engineering`, `data/materials.json`. Les formules, cas de test calculés à la main et sources matériaux sont conservés dans `docs/research/engineering-reference.md`.

## Protocole

`python -m bambu_geometry <step_import|parametric>` ; requête JSON sur stdin, une ligne JSON sur stdout (`{"ok":true,"result":…}` ou `{"ok":false,"error":{code,message,details}}`), logs sur stderr. Le fd 1 est redirigé vers stderr pendant le traitement car OpenCASCADE écrit des diagnostics en C++ sur stdout. Détail dans `workers/geometry/PROTOCOL.md`. Codes : `invalid_json`, `unknown_command`, `invalid_params`, `file_not_found`, `corrupt_file`, `step_backend_missing`, `cad_backend_missing`, `cad_failure`, `internal_error`. Code de sortie 0 / 1 (erreur métier) / 2 (interne).

## Environnement

uv, Python 3.12 (`.python-version`) ; `uv sync --extra cad` : cadquery 2.8.0 + cadquery-ocp 7.9.3 installés et fonctionnels sous Windows. Tests : `uv run python -m pytest` (13 tests, ~50 s, dont sous-processus CLI).

## Limites

- Unités STEP : OCC convertit en mm ; l'unité déclarée est lue par regex (best effort).
- Gabarits sans congés ni vérification mécanique ; `clamp` « none » (C à clipser) non vérifié élastiquement.
- Pas de fallback trimesh/build123d (inutile : CadQuery fonctionne).
