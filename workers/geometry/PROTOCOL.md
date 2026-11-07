# Protocole du worker géométrie

Appel : `uv run --project workers/geometry python -m bambu_geometry <commande>` ; JSON UTF-8 sur stdin ; une seule ligne JSON sur stdout ; logs/diagnostics sur stderr.

Réponse : `{"ok":true,"result":{…}}` ou `{"ok":false,"error":{"code":"…","message":"…","details":{…}}}`. Sortie 0 (ok), 1 (erreur métier), 2 (erreur interne).

## step_import
Requête : `{"path":"x.step","out_dir":"…","tolerance_mm":0.05,"angular_tolerance_rad":0.2}` (tolérances optionnelles).
Résultat : `source{path,sha256,size_bytes}`, `units{output:"mm",source_declared,note}`, `tessellation{tolerance_mm,angular_tolerance_rad,triangles}`, `objects[]{index,name,volume_mm3,area_mm2,bbox_mm{min,max,size},tessellation{triangles,degenerate_triangles,open_edges,watertight}}`, `files{stl,3mf}`, `level:"calculé"`, `assumptions[]`.
Erreurs : `invalid_params`, `file_not_found`, `step_backend_missing`, `corrupt_file`, `too_complex`.

## parametric
Requête : `{"template":"clamp|bracket|box|spacer|hook","params":{…},"out_dir":"…","name":"…","nozzle_diameter":0.4}`.
Paramètres (mm) :
- clamp : `tube_diameter`*, `clearance`(0.3), `wall`(3), `width`(15), `hinge`("screw"|"none"), `screw_hole_diameter`(3.4), `gap`(2), `opening`.
- bracket : `leg_a`*, `leg_b`*, `thickness`(4), `width`(20), `hole_diameter`(4.5), `holes_a`(1), `holes_b`(1).
- box : `inner_x|y|z`*, `wall`(2), `floor`, `lid_thickness`(2), `lid_clearance`(0.3), `lip_height`(3).
- spacer : `outer_diameter`*, `inner_diameter`, `height`*.
- hook : `width`(20), `plate_height`(40), `thickness`(4), `arm_length`(30), `tip_height`(12), `hole_diameter`(4.5), `holes`(2).
Résultat : `template`, `params`, `parts[]{name,volume_mm3,solids,tessellation}`, `bbox_mm`, `derived`, `assumptions[]`, `warnings[]`, `files{stl,step,source}`, `level:"calculé"`, `validity`.
Erreurs : `invalid_params`, `cad_backend_missing`, `cad_failure`.

## library_list
Requête : `{}`. Résultat : `{count, templates[]}`, chaque gabarit : `id, version, schema_hash, title, category, summary, description, tags[], params[]{name,type:"number|integer|enum|boolean",unit,default,min,max,options,description,auto}`.
Le champ `auto` indique la règle de calcul d'ingénierie appliquée par le côté Go quand le paramètre est omis (`fit:<ajustement>`, `fitside:<ajustement>`, `gap_pip`, `hole:<orientation>`, `insert:diameter|depth|boss`) : voir `internal/library/rules.go`. Ne requiert pas CadQuery.

## library_build
Requête : `{"template":"<id>","params":{…},"out_dir":"…","name":"…","nozzle_diameter":0.4,"thumbnail":true}`. Tous les paramètres ont un défaut ; un paramètre inconnu, hors bornes, d'un mauvais type ou une combinaison impossible donne `invalid_params`.
Résultat : `template, template_version, params` (complets), `parts[]{name,volume_mm3,solids,valid,tessellation}`, `bbox_mm`, `derived`, `assumptions[]`, `warnings[]`, `files{stl,step,source,thumbnail}`, `level:"calculé"`. La vignette est un SVG isométrique. Gabarits : `clamp, bracket, box, spacer, hook, washer, mounting_plate, wall_mount, handle, hook_reinforced, hinge_print_in_place, hinge_pin, snap_clip, enclosure, spur_gear, gear_pair, thread`. Un nouveau gabarit = une entrée `register(Template(...))` dans `lib_templates.py` (schéma + fonction de construction) ; incrémenter `version` à chaque changement de géométrie ou de paramètres.
Erreurs : `invalid_params`, `cad_backend_missing`, `cad_failure`.

## library_thumbnail
Requête : `{"template":"<id>","params":{…}}`. Résultat : `{template, svg}` (tessellation grossière, aucun fichier écrit).

## calibration_set
Requête : `{"out_dir":"…","kinds":["fit_comb","hole_shaft","shrink_cube","bridge_overhang","temperature"],"name_prefix":"calib","nozzle_diameter":0.4,"clearance_gaps":[…],"hole_diameters":[…],"bridge_spans":[…],"overhang_angles":[…],"temperatures":[…],"temperature_mode":"series|tower"}` (tout est facultatif).
Résultat : `pieces[]{kind,name,description,measure[],files{stl,step},parts[],bbox_mm,info,warnings}` ; pièces de température : `nozzle_temperature_c` et `slicing_overrides` (série) ou `segments`, `layer_changes`, `gcode_hint` (tour).

## ping
Requête : `{}`. Importe réellement CadQuery/OCP (vérification de santé, utilisée par `GET /api/capabilities`, résultat mis en cache côté bambud).
Résultat : `{"cadquery":"<version>","python":"<version>"}`. Erreur : `cad_unavailable` (CadQuery/OCP absent ou non chargeable).

## Exécution par bambud
`uv run --frozen --no-dev --extra cad --project <dir> python -m bambu_geometry <commande>` : jamais d'écriture de `uv.lock`, pas de groupe `dev`. L'environnement virtuel est celui de `UV_PROJECT_ENVIRONMENT` s'il est défini ; à défaut, `<data-dir>/venvs/geometry` (sauf si `<dir>/.venv` existe). `PYTHONDONTWRITEBYTECODE=1` : bambud n'écrit rien dans `<dir>`.

## Confinement des chemins
Le worker n'est pas un bac à sable : l'appelant (bambud) DOIT confiner `path` (lecture) et `out_dir` (écriture) à des dossiers autorisés avant l'appel et ne jamais les tirer tels quels d'une entrée utilisateur. Côté worker : `name` doit respecter `^[A-Za-z0-9._-]{1,64}$` et les fichiers de sortie sont vérifiés (`resolve`) sous `out_dir`.
Plafonds : `step_import` — `tolerance_mm` dans [0,001 ; 10], `angular_tolerance_rad` dans [0,01 ; 1,6], valeurs finies (sinon `invalid_params`), au plus 5 000 000 de triangles (sinon `too_complex`) ; `parametric` — `holes`/`holes_a`/`holes_b` entiers de 0 à 50.

Erreurs communes : `invalid_json`, `unknown_command`, `internal_error`.
