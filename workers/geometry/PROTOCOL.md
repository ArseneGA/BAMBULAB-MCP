# Protocole du worker géométrie

Appel : `uv run --project workers/geometry python -m bambu_geometry <commande>` ; JSON UTF-8 sur stdin ; une seule ligne JSON sur stdout ; logs/diagnostics sur stderr.

Réponse : `{"ok":true,"result":{…}}` ou `{"ok":false,"error":{"code":"…","message":"…","details":{…}}}`. Sortie 0 (ok), 1 (erreur métier), 2 (erreur interne).

## step_import
Requête : `{"path":"x.step","out_dir":"…","tolerance_mm":0.05,"angular_tolerance_rad":0.2}` (tolérances optionnelles).
Résultat : `source{path,sha256,size_bytes}`, `units{output:"mm",source_declared,note}`, `tessellation{tolerance_mm,angular_tolerance_rad,triangles}`, `objects[]{index,name,volume_mm3,area_mm2,bbox_mm{min,max,size},tessellation{triangles,degenerate_triangles,open_edges,watertight}}`, `files{stl,3mf}`, `level:"calculé"`, `assumptions[]`.
Erreurs : `invalid_params`, `file_not_found`, `step_backend_missing`, `corrupt_file`.

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

Erreurs communes : `invalid_json`, `unknown_command`, `internal_error`.
