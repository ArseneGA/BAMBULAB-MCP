# Lot qualité 3D : CI, golden files, maillages sales, benchmarks

## CI (`.github/workflows/ci.yml`)
Sur push et pull request vers `main`, sans secret : `go` (build web puis `go vet`, `go test -short ./... -timeout 30m`, Go lu dans `go.mod`),
`python` (uv + extra `cad`, `pytest` de `workers/geometry`, cache uv), `web` (Node 22.12, `npm ci`, `npm run build`, `npm test`), `docker`
(build sans push, `WITH_STUDIO=0`). Les tests réels Studio sont ignorés par `-short` ou l'absence du binaire.

## Golden files des gabarits
`workers/geometry/tests/golden/<gabarit>.json` : résumé déterministe (volume, aire, boîte englobante, pièces, solides, composants, étanchéité,
empreinte arrondie) des 17 gabarits aux paramètres par défaut. Aucun STL stocké. `tests/test_golden.py` compare avec tolérance (volume et boîte
0,5 %, aire 2 % car dépendante de la tessellation). Régénération volontaire, après relecture du diff :

    cd workers/geometry && uv run --extra cad python scripts/regen_golden.py [gabarit ...]

## Maillages sales (`internal/mesh/dirty_test.go`, `testdata/dirty_*.stl`)
Générés par code (`BAMBU_GEN_TESTDATA=1 go test -run GenerateDirty ./internal/mesh`) : trou, non-manifold, normales inversées, une face
retournée, triangle dupliqué, triangles dégénérés, auto-intersection, éclat flottant, soupe non soudée. Détecté aujourd'hui : tout sauf trois
points, laissés en `t.Skip("à implémenter : ...")` pour l'agent maillage :
- compteur dédié des triangles dupliqués (aujourd'hui vus comme non-manifold) ;
- avertissement dédié d'éclat flottant (aujourd'hui seulement « N composantes connexes ») ;
- détection d'auto-intersections (deux cubes interpénétrés : volume compté deux fois).

## Benchmarks (`internal/mesh/bench_test.go`)
Sphère UV fermée, `go test -run '^$' -bench . -benchtime 1x ./internal/mesh`, Intel Core Ultra 9 285H, Windows, Go 1.27, une itération.

| Benchmark | Triangles | Temps | Mémoire allouée | Allocations |
|---|---|---|---|---|
| Analyze | 100 k | 0,67 s | 63 Mo | 201 k |
| Analyze | 1 M | 5,6 s | 665 Mo | 2,0 M |
| Analyze | 5 M | 38 s | 3,3 Go | 10,1 M |
| Fabricability (sans orientation) | 100 k | 1,0 s | 116 Mo | 321 k |
| Fabricability (sans orientation) | 1 M | 18,8 s | 1,2 Go | 3,1 M |
| Fabricability (sans orientation) | 5 M | 569 s | 6,4 Go | 16,3 M |
| Fabricability + orientation | 100 k | 1,5 s | 138 Mo | 324 k |
| Fabricability + orientation | 1 M | 25 s | 1,4 Go | 3,1 M |
| Weld (soupe de triangles) | 100 k | 0,94 s | 17 Mo | 50 k |
| Weld (soupe de triangles) | 1 M | 13,1 s | 215 Mo | 504 k |

Constats pour l'optimisation : `Analyze` est à peu près linéaire (≈ 7 µs/triangle) ; la fabricabilité est sur-linéaire (×19 de 100 k à 1 M,
×30 de 1 M à 5 M) alors que l'épaisseur n'échantillonne que 2000 rayons : le coût vient de la construction du BVH et des rayons sur gros
maillage. Un maillage de 5 M triangles demande ~10 min et 6 Go ; envisager une décimation préalable au-delà de ~1 M.

### Après optimisation (lot maillage)

Structures plates (soudure à adressage ouvert en int32, arêtes en clés uint64 triées, composantes par racine d'union-find), BVH à sélection rapide partagé entre l'épaisseur et le contrôle des auto-intersections, échantillons d'épaisseur au prorata de l'aire. Mêmes machine et commande (`-benchtime 1x -benchmem`).

| Benchmark | Triangles | Temps avant → après | Mémoire allouée avant → après |
|---|---|---|---|
| Analyze | 1 M | 5,6 s → 0,28 s | 665 Mo → 97 Mo |
| Analyze | 5 M | 38 s → 1,9 s | 3,3 Go → 496 Mo |
| Fabricability (sans orientation) | 1 M | 18,8 s → 1,7 s | 1,2 Go → 172 Mo |
| Fabricability (sans orientation) | 5 M | 569 s → 12,4 s | 6,4 Go → 1,15 Go |
| Fabricability + orientation | 1 M | 25 s → 2,1 s | 1,4 Go → 173 Mo |
| Weld (soupe) | 1 M | 13,1 s → 0,25 s | 215 Mo → 112 Mo |

Pic de tas mesuré (`TestAnalyzeMemory5M`, `BAMBU_BIGMESH=1`) : Analyze 5 M triangles = 545 Mio (maillage compris) contre 1 094 Mio avant ; 1 M = 109 Mio contre 232 Mio. Les 12,4 s de Fabricability 5 M comprennent 5 s de budget d'auto-intersections (contrôle alors partiel et annoncé). Au-delà de 1 M triangles, la notation des orientations travaille sur un échantillon annoncé dans le rapport.

## Code mort retiré
`workers/geometry/src/bambu_geometry/calibration.py` : ligne `pv = pv.union(_dots(...)) if False else pv` (jamais exécutée) supprimée ;
`_dots` reste utilisé ailleurs.
