# Bambu Studio CLI — note de recherche (consultée le 2026-11-06)

Légende : [V] fait vérifié (source citée) · [D] déduction · [?] inconnue.

## 1. Installation locale (poste Windows)
- [V] `C:\Program Files\Bambu Studio\bambu-studio.exe`, FileVersion **02.08.02.61** (propriété de l'exe). `resources\profiles\BBL.json` : version `02.08.00.05`.
- [V] `bambu-studio.exe --help` : **sortie vide**, code retour 0, en redirection bash ET PowerShell `Start-Process -RedirectStandardOutput`. Cause probable [D] : exe sous-système GUI sans console. Voir `bambu-studio-help.txt`. L'aide se lit donc dans le source / le wiki.
- [V] Dernière release stable (API GitHub `releases/latest`, 2026-11-06) : **v02.09.01.70**, publiée 2026-11-03, SHA du tag `56a57d00a591360cf41bf21383915ee5bf357be5`. La version locale (02.08.02.61, 2026-08-21) est en retard d'une mineure. Autres tags : v02.08.04.61 (`f5e51a20bd328d4a043af9344825d7347270c2c9`), v02.08.04.57 (pre-release). Sources : https://api.github.com/repos/bambulab/BambuStudio/releases , /tags .
- [V] Assets Linux de v02.09.01.70 : `BambuStudio_ubu22_v02.09.01.70-20261103183404.AppImage` et `BambuStudio_ubu24-v02.09.01.70-…AppImage` (+ .dmg, .exe, .zip win). v02.08.04.57 a aussi un AppImage Fedora. Pas de Flatpak dans les assets.

## 2. Options CLI
Sources : https://github.com/bambulab/BambuStudio/wiki/Command-Line-Usage (2026-11-06) ; `src/BambuStudio.cpp` (master, 9284 lignes). Usage : `Usage: bambu-studio [ OPTIONS ] [ file.3mf/file.stl ... ]`.
- [V] Options du wiki : `--debug 0..5`, `--load-settings "machine.json;process.json"`, `--load-filaments "f1.json;;f3.json"` (entrées vides = garde celles du 3mf), `--outputdir dir`, `--arrange 0|1|autre`, `--orient`, `--scale f`, `--export-3mf file.3mf`, `--export-settings`, `--export-slicedata dir`, `--load-slicedata dir`, `--info`, `--pipe pipename` (progression), `--slice N` (0 = toutes plaques, i = plaque i), `--uptodate`, `-h/--help`.
- [V] Priorité (wiki) : `--clé=valeur` > fichiers `--load-settings/--load-filaments` > valeurs du 3mf. Exemple : `--curr-bed-type "Cool Plate"`.
- [V, tiers non officiel] https://printago.io/blog/bambu-studio-cli-reference : `--allow-newer-file` (3mf d'un Studio plus récent), `--min-save`, `--no-check`, `--mstpp <s>` (temps max/plaque), `--mtcpp <n>` (triangles max), `--skip-useless-pick`, `--datadir`, `--check-preset` (≥2.8.2). `--allow-newer-file` et `uptodate_*` sont aussi dans le source ; le reste : [?] à confirmer sur le build local (aide invisible).
- Exemple officiel : `./bambu-studio --orient --arrange 1 --load-settings "machine.json;process.json" --load-filaments "filament.json" --slice 2 --debug 2 --export-3mf output.3mf boat.stl`.
- Commande type pour l'archi [D] : `bambu-studio --slice 1 --load-settings "machine.json;process.json" --load-filaments "filament.json" --allow-newer-file --outputdir <tmp> --export-3mf out.gcode.3mf model.3mf`.

## 3. Presets
- [V] Wiki : les JSON machine/process/filament du CLI doivent être des **configs complètes**, pas les fichiers de `resources/profiles/BBL/{machine,process,filament}` (qui utilisent `inherits`).
- [V] Fichiers locaux : `machine\Bambu Lab P1S 0.4 nozzle.json` = `"inherits":"fdm_bbl_3dp_001_common"`, `"from":"system"`, `"instantiation":"true"`, `"printer_model":"Bambu Lab P1S"`, `default_print_profile "0.20mm Standard @BBL X1C"`, `default_filament_profile "Bambu PLA Basic @BBL P1S 0.4 nozzle"`. `process\0.20mm Standard @BBL X1C.json` hérite de `fdm_process_single_0.20`. `filament\Bambu PLA Basic @BBL P1S 0.4 nozzle.json` hérite de `Bambu PLA Basic @base`. `machine\Bambu Lab P1S.json` = `machine_model` (métadonnées : nozzle_diameter `0.4;0.2;0.6;0.8`, model_id C12, matériaux par défaut, lit Textured PEI).
- [D] Il faut **aplatir** la chaîne `inherits` (remonter jusqu'aux `fdm_*`) avant `--load-settings`. Aucune doc officielle de l'aplatissement ; alternatives : `--export-settings`, ou 3mf d'un projet réel (contient `Metadata/project_settings.config`) exporté depuis la GUI.
- [V] Forum https://forum.bambulab.com/t/cli-cannot-apply-external-presets/197842 : un utilisateur rapporte que le CLI ignorait ses presets (filament_id "unknown", 0 g) ; seul un 3mf avec presets embarqués fonctionnait. Non résolu.
- Profils locaux (comptes bruts, variantes buse incluses) : 603 fichiers `machine`, 278 `process`, 1908 `filament`. Données utilisateur : `%APPDATA%\BambuStudio\` (`user\`, `system\BBL`, `printers\`, `BambuStudio.conf`, `log`).

## 4. Sortie et codes retour
- [V] `--export-3mf x.gcode.3mf` produit un .gcode.3mf ; le G-code brut est `Metadata/plate_N.gcode` dans l'archive (printago, tiers).
- [V] Source (`record_exit_reson`) : écrit `result.json` dans `--outputdir` (sinon cwd) : `return_code`, `error_string`, `plate_index`, `times{prepare_time,export_time,export_3mf_time,…}`, `peak_rss_mb`, `layer_height`, `wall_loops`, `sparse_infill_density`, `sliced_plates[]` (objets avec bbox ; `filaments[]` : id, filament_id, total_used_g, main_used_g).
- [V] Codes (src/libslic3r/Utils.hpp) : 0 succès ; -1 environnement ; -2 paramètres invalides ; -3 fichier introuvable ; -4 ordre des fichiers (3mf en premier) ; -5 config invalide ; -6 données ; -7 techno imprimante ; -13 export 3mf ; -14 mémoire ; -17 process incompatible ; -18 valeurs 3mf invalides ; -21 arrange échoué ; -24 version de fichier non supportée ; -50 plaque vide / aucun objet entièrement dedans ; -51 validation ; -52 objets partiellement dehors ; -58 temps de slice dépassé ; -59 triangles dépassés ; -61 filament vs plateau ; -63/-64 collisions ; -100 erreur de slicing ; -101…-105 chemins G-code (conflit, zone non imprimable, hors plateau) ; -108 multi-buses. Messages dans le tableau `cli_errors`.
- [?] Le wiki ne documente ni codes ni result.json. Le code retour processus n'a pas été observé en exécution (aucun slicing lancé).

## 5. Linux / headless
- [V] Forum (lien ci-dessus) : AppImage headless → "Wayland: Failed to connect to display", `glfwInit return error, code 65544` ; Flatpak idem. Pas de solution confirmée.
- [V, tiers] Printago affirme un export PNG headless via OSMesa (non officiel, ne couvre pas `--slice`).
- [?] Aucune source ne confirme que `xvfb-run` rend `--slice` fonctionnel. [D] plausible : `xvfb-run -a ./BambuStudio.AppImage --appimage-extract-and-run …` ; **à tester** (non testé ici). Fallback : utiliser le Windows local en sous-processus (sans console, donc lire `result.json` + fichiers).
- [D] Isoler chaque slice dans un répertoire temporaire (printago : des centaines de Mo de temp), `--mstpp` comme timeout (sinon aucun timeout intégré).

## 6. Modèles dans `resources\profiles\BBL\machine` (install locale 02.08.02.61)
14 `machine_model` : A1 mini, A1, A2L, H2C, H2D, H2D Pro, H2S, P1P, P1S, P2S, X1, X1 Carbon, X1E, X2D. Buses 0.4/0.2/0.6/0.8 pour tous.
Volumes lus dans les fichiers « 0.4 nozzle » :
- A1 mini 180x180x180 ; A1 256x256x256 ; A2L 330x320x325 ; H2C 330x320 ; H2D / H2D Pro 350x320 (double extrudeur, base common_002 345x320x325) ; H2S 340x320x340 ; P2S 256x256x256 ; X2D 256x256x261.
- P1S, P1P, X1, X1C, X1E : area héritée de `fdm_bbl_3dp_001_common` = 256x256. [?] hauteur non explicite (fdm_machine_common = 250 ; nominal Bambu 256) → à vérifier sur le config complet.
