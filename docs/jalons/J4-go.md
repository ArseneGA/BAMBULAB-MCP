# J4 (Go) — maillage et ingénierie

Périmètre : `internal/mesh` (lecture STL/OBJ/3MF, analyse, fabricabilité, export GLB/STL) et `internal/engineering` (calculs purs + matériaux). Dépendances : bibliothèque standard uniquement (`archive/zip`, `encoding/xml`, `encoding/json`, `embed`), aucune dépendance au reste du dépôt.

## internal/mesh

| Fichier | Contenu |
|---|---|
| `mesh.go` | `Vec3`, `Mesh`, `Matrix` (3x4, convention 3MF), `Object`, `Model`, limites de sécurité |
| `stl.go` | STL ASCII et binaire. Binaire reconnu par la taille exacte `84 + 50·n` (un en-tête « solid » ne suffit pas), ASCII sinon ; tronqué → `ErrTruncated` ; unité `unknown` + hypothèse mm signalée |
| `obj.go` | `v`/`f`, polygones triangulés en éventail, indices négatifs, plusieurs `o`/`g` |
| `threemf.go` | zip + XML 3D Core : objets, composants (y compris `p:path` de l'extension Production Bambu), transformations de composants et d'items de build, unités → mm, métadonnées, objets de type support. Fichiers non interprétés (`Metadata/*.config`, miniatures, G-code) listés dans `Model.Extras` |
| `analysis.go` | bbox, volume signé, aire, sommets soudés (grille, tolérance), arêtes ouvertes / non manifold / incohérentes, triangles dégénérés, composantes connexes, normales inversées ; plausibilité d'unité |
| `fab.go`, `bvh.go`, `hull.go` | tient sur le plateau (et tourné de 90°), surplombs (aire, régions groupées), épaisseur par rayons contre la normale (BVH, pondérée par l'aire, confiance), orientations (6 axes + faces de la coque convexe approchée) — tout étiqueté `heuristic` |
| `export.go` | GLB 2.0 (Z-haut → Y-haut, mm → m, extras documentant les conversions), décimation par regroupement de sommets signalée « APERÇU », STL binaire |
| `read.go` | `Parse`, `ReadFile`, `SHA256Hex`, `SHA256Reader` |
| `gen.go` | générateurs de maillages (cube, cylindre, bloc troué, deux cubes, T, extrusion) |

### API publique résumée

```go
mesh.Parse(name string, data []byte) (*Model, error)   // détection par contenu
mesh.ReadFile(path string) (*Model, error)
mesh.ParseSTL / ParseOBJ / ParseThreeMF([]byte) (*Model, error)

(*Model).Analyze(AnalyzeOptions) *ModelReport          // par objet + ensemble
mesh.Analyze(*Mesh, AnalyzeOptions) *Report
mesh.Fabricability(*Mesh, FabOptions) (*FabReport, error)
(*Model).GLB(GLBOptions) / (*Mesh).GLB(name, GLBOptions) (*GLBResult, error)
mesh.WriteSTLBinary(io.Writer, *Mesh) / STLBytes(*Mesh)
mesh.SHA256Hex([]byte) / SHA256Reader(io.Reader)
```

Erreurs : `ErrFormat`, `ErrTruncated`, `ErrEmpty` (utiliser `errors.Is`).

### Limites connues

- Les surplombs, épaisseurs et orientations sont des heuristiques ; aucune n'est une garantie d'imprimabilité ni de résistance.
- Épaisseur : distance au premier triangle contre la normale ; surestime les parois obliques, peu fiable si le maillage n'est pas étanche (confiance abaissée).
- Coque convexe approchée : points extrêmes dans ~300 directions, puis enveloppe incrémentale.
- OBJ : éventail correct pour les polygones convexes seulement. 3MF : matériaux, couleurs, textures, `slice` et extensions non lus (avertissement), le fichier original doit rester la référence.
- Le GLB n'a pas de normales (ombrage plat par le viewer) ni de couleurs.
- Volumes cibles : valeurs nominales Bambu (`TargetP1S`, `P1P`, `X1C`, `A1`, `A1Mini`), sans zones d'exclusion.

## internal/engineering

Chaque fonction retourne un struct qui embarque `Result{Value, Unit, Range?, Assumptions, Validity, Level, Sources, Warnings}`. `Level` ∈ `measured | calculated | heuristic | indicative`. Une donnée matériau `indicative` rend le résultat `heuristic`. Entrées invalides → erreur enveloppant `ErrInvalidInput`. Rien n'est une certification (`Disclaimer`).

| Fonction | Résumé |
|---|---|
| `FitClearance(FitParams)` | jeu diamétral : libre 0,40 / glissant 0,25 / serré 0,10 / press −0,10 mm (buse 0,4), +0,25·(buse−0,4), +0,05 axe horizontal, + retrait·dimension, plage ±0,05 |
| `HoleCompensation(HoleParams)` | d + δ (0,15 vertical, 0,25 horizontal, échelle buse, +0,05 si d<3) + retrait |
| `Shrinkage`, `ScaleForShrinkage` | retrait % (indicatif) ; D/(1−s/100) |
| `WallCheck(e, lw)` | périmètres complets, reste, Arachne (largeur dans [0,6 ; 1,5]·lw), avertissements |
| `OverhangBridge(OverhangParams)` | OK ≤ 45°, dégradé jusqu'au max matériau (TDS), supports au-delà ; pont vs `max_bridge_mm` |
| `BeamStress(BeamParams)` | console / appuis simples, charge ponctuelle / répartie, rectangle / cercle / tube ; σ=M·c/I, flèche, SF dans les directions XY et Z (anisotropie), avertissement si Z |
| `SnapFit(SnapParams)` | ε=1,5·h·y/L², y_max, force de flexion, force d'insertion (μ, angle) |
| `ScrewBoss(size, kind, material)` | trou vis autotaraudeuse / insert à chaud M2–M8, plot, profondeur |
| `TorqueScrew` | T = K·F·d |
| `Materials()`, `GetMaterial`, `MaterialIDs` | base embarquée `data/materials.json` |

Cas de test calculés à la main (reprenant `docs/research/engineering-reference.md`) : console rectangle 20×10, L=100, F=10 N, E=2750 → σ=3,0 MPa, δ=0,72727 mm, SF=25,333 (XY) / 22,667 (Z) ; appuis simples cercle Ø10, L=100, F=50 N → σ=12,7324 MPa, δ=0,77166 mm ; retrait PLA 0,3 %, 100 mm → 100,3009 mm ; snap h=2, y=1, L=10 → ε=0,03 ; paroi 1,2 mm / ligne 0,42 → 2 périmètres, reste 0,36 ; vis F=500 N, d=3 mm, K=0,2 → 300 N·mm.

### Matériaux et sources

Date de relevé : 2026-11-06. PLA (PLA Basic), PETG (PETG Basic) + PETG-HF, ABS, ASA, TPU-95A, PC, PA-CF (PA6-CF), PLA-CF, PETG-CF, PVA : valeurs lues directement dans les fiches techniques Bambu Lab (PDF), niveau `measured`, avec version de la fiche (V1.0 à V3.0). `PA` (non chargé) : valeurs entièrement `indicative` (littérature, aucune fiche Bambu). Retrait et déformation admissible de clipsage : `indicative` pour tous (la fiche ne les donne pas). `null` = inconnu (ex. PVA : propriétés mécaniques N/A).

URL (CDN officiel Bambu Lab) :

- PLA Basic V2.0 : https://cdn.shopify.com/s/files/1/0380/5149/files/Bambu_PLA_Basic_Technical_Data_Sheet.pdf
- PETG Basic V2.0 : https://cdn.shopify.com/s/files/1/0380/5149/files/Bambu_PETG_Basic_Technical_Data_Sheet.pdf
- PETG HF V1.0 : https://cdn.shopify.com/s/files/1/0584/7236/6216/files/Bambu_PETG_HF_Technical_Data_Sheet.pdf
- ABS V2.0 : https://cdn.shopify.com/s/files/1/0584/7236/6216/files/Bambu_ABS_Technical_Data_Sheet.pdf
- ASA V3.0 : https://cdn.shopify.com/s/files/1/0645/5876/0155/files/Bambu_ASA_Technical_Data_Sheet.pdf
- TPU 95A V3.0 : https://cdn.shopify.com/s/files/1/0645/5876/0155/files/Bambu_TPU_95A_Technical_Data_Sheet.pdf
- PC V3.0 : https://cdn.shopify.com/s/files/1/0645/5876/0155/files/Bambu_PC_Technical_Data_Sheet.pdf
- PA6-CF V3.0 : https://cdn.shopify.com/s/files/1/0584/7236/6216/files/Bambu_PA6-CF_Technical_Data_Sheet.pdf
- PLA-CF V2.0 : https://cdn.shopify.com/s/files/1/0380/5149/files/Bambu_PLA-CF_Technical_Data_Sheet.pdf
- PETG-CF V2.0 : https://cdn.shopify.com/s/files/1/0380/5149/files/Bambu_PETG-CF_Technical_Data_Sheet.pdf
- PVA V1.0 : https://cdn.shopify.com/s/files/1/0584/7236/6216/files/Bambu_PVA_Technical_Data_Sheet.pdf

Écarts avec `docs/research/engineering-reference.md` (valeurs lues sur copies secondaires) : PLA tensile 39/35 confirmé ; PETG HF 34/23 et flexion 64/48 confirmés ; la fiche PLA-CF indique une densité de 1,22 (la référence indicative disait 1,3). La fiche PETG-CF V2.0 contient un texte d'introduction manifestement copié d'un autre produit ; les valeurs chiffrées ont été retenues telles quelles. Les valeurs sont typiques et sur éprouvettes sèches ; la TDS ASA/ABS précise l'état sec. À revérifier contre la fiche de la bobine réelle.

### Limites

- `max_overhang_deg` et `max_bridge_mm` sont des réglages recommandés par le fabricant, pas des mesures normées.
- Poutres : théorie d'Euler-Bernoulli, pièce pleine, statique, sans concentration de contrainte ; la résistance utilisée est la résistance en flexion de l'éprouvette imprimée, ce qui ne remplace pas un essai.
- Jeux et compensations de trous : ordres de grandeur à calibrer par un essai d'assemblage.
- Tailles de vis/inserts : dimensions typiques ; vérifier la fiche de l'insert réel.
