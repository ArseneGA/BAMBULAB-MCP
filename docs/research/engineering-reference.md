# Référence du module `engineering` (à réimplémenter en Go)

Statut : spécification de référence, **aucun code Python** conservé pour ce module (décision du 2026-11-06 : tout passe en Go). Les formules ci-dessous sont des fonctions pures ; chaque cas de test a été calculé à la main puis recoupé numériquement.

## Contrat de résultat

Tout calcul retourne : `value` + `unit`, `assumptions[]`, `validity` (domaine de validité), `level` ∈ {`mesuré`, `calculé`, `heuristique`}, `warnings[]`. Jamais présenté comme une certification. Une valeur issue d'une donnée matériau `indicative` rend le résultat `heuristique` avec avertissement.

## Fonctions

### beam_stress (niveau : calculé si la propriété est mesurée, sinon heuristique)
Section rectangle b×h (h dans le plan de flexion) : I = b·h³/12, c = h/2. Cercle Ø d : I = π·d⁴/64, c = d/2.
- Console, charge F en bout : M = F·L ; δ = F·L³/(3·E·I).
- Appuis simples, charge F centrée : M = F·L/4 ; δ = F·L³/(48·E·I).
- σ = M·c/I ; facteur de sécurité SF = σ_adm / σ.
- σ_adm : résistance en flexion (sinon en traction) du matériau selon l'orientation : `xy` si la contrainte de traction de la fibre extrême est dans le plan des couches, `z` si elle est perpendiculaire aux couches (rupture inter-couches). E pris dans la même direction. Facteur d'anisotropie = σ_z/σ_xy.
- Hypothèses : poutre prismatique, petits déplacements, matériau linéaire élastique homogène, charge statique, pas de concentration de contrainte (congés, trous), pas de fluage ni température (voir HDT). Validité : L/h ≥ 5 ; SF < 1 → échec, 1–2 → insuffisant pour une pièce fonctionnelle, ≥ 2 acceptable pour du statique (seuils heuristiques).

Cas de test (E = 2750 MPa, PLA XY ; résistance flexion XY 76, Z 68 MPa) :
1. Console, rect b=20, h=10, L=100 mm, F=10 N : I = 1666,667 mm⁴ ; M = 1000 N·mm ; σ = **3,0 MPa** ; δ = **0,72727 mm** ; SF_xy = **25,333** ; SF_z = **22,667**.
2. Appuis simples, cercle d=10, L=100, F=50 N : I = 490,8739 mm⁴ ; M = 1250 N·mm ; σ = **12,7324 MPa** ; δ = **0,77166 mm** ; SF_xy = **5,969**.

### snap_fit (crochet console à section constante)
ε = 1,5·h·y / L² (h épaisseur, y flèche d'insertion, L longueur) ; y_max = ε_adm·L²/(1,5·h) ; force de flexion P = (b·h²/6)·E·ε / L.
ε_adm = déformation admissible du matériau × 0,5 si les couches sont perpendiculaires à la contrainte de flexion (crochet imprimé debout).
Test : h=2, y=1, L=10 → ε = **0,03** (= 3 %). Heuristique (niveau `heuristique`) car ε_adm est indicative.

### hole_compensation (heuristique)
d_modélisé = d_nominal + δ(orientation) + shrink ; orientation verticale (axe // Z) δ ≈ +0,15 mm ; horizontale (axe dans XY, affaissement) δ ≈ +0,25 mm (ordres de grandeur, à calibrer par retour d'impression). Les trous < 3 mm sont plus sensibles.

### fit_clearance (heuristique)
Jeu diamétral total recommandé (buse 0,4 mm) : libre ≈ 0,40 ; glissant ≈ 0,25 ; serré ≈ 0,10 mm ; correction + 0,25·(buse − 0,4) ; + dimension × shrinkage % ; plage ± 0,05 mm. Avertir que la valeur doit être validée par un essai d'assemblage.

### shrinkage
D_modélisé = D_nominal / (1 − s/100), s en % par famille matériau (voir table). Test : PLA s=0,3 %, D=100 → **100,30090 mm**. Niveau heuristique (s est indicatif).

### wall_check
n_complet = floor(e / lw) ; reste = e − n·lw. Moteur classique : reste > 0,25·lw → trou ou sur-extrusion probable. Arachne (Bambu Studio ≥ 1.7 par défaut) : n = max(1, round(e/lw)) avec largeur effective e/n tolérée dans [0,6·lw ; 1,5·lw], sinon avertir. e < 2·lw → au plus 1 périmètre, pièce fragile.
Test : e=1,2, lw=0,42 → n_complet = **2**, reste = **0,36** (> 0,105 → avertissement moteur classique).

### overhang_bridge (heuristique)
Angle mesuré depuis la verticale : OK ≤ 45° ; entre 45° et `overhang_max_deg` du matériau : qualité dégradée ; au-delà : supports. Pont : OK si longueur ≤ `bridge_max_mm` du matériau, sinon supports (valeurs de la table).

### torque_screw (facultatif)
T = K·F·d (K ≈ 0,2 par défaut, filetage lubrifié ou non à préciser). Test : F=500 N, d=3 mm, K=0,2 → **300 N·mm**. Précharge limite pour un filetage imprimé : ≤ 0,5·σ_adm·A_s (A_s = π/4·(d − 0,9382·p)²), heuristique.

### FEA simplifiée : non implémentée ; si ajoutée, étiquette `heuristique` obligatoire et maillage grossier.

## Données matériaux (à versionner en Go, par valeur : source + date + domaine + niveau)

Date de recherche : 2026-11-06. Sources initialement lues via copies secondaires (retailers) ; **vérifiées depuis les PDF officiels le même jour** (voir la section « Vérification contre les PDF officiels » en fin de document).

| Matériau | Propriété | Valeur | Niveau | Source |
|---|---|---|---|---|
| PLA (Bambu PLA Basic) | traction XY / Z | 39 / 35 MPa (autres copies : 35 ± 4) | mesure fabricant | TDS V2.0 `cdn.shopify.com/s/files/1/0380/5149/files/Bambu_PLA_Basic_Technical_Data_Sheet.pdf`, copies additive-x.com, grobotronics.com, stemfinity.com |
| PLA | flexion résistance XY / Z | 76 / 68 MPa | mesure fabricant | idem |
| PLA | module de flexion XY / Z | 2750 / 2370 MPa | mesure fabricant | idem |
| PLA | HDT (ISO 75) | 54 °C à 1,8 MPa ; 57 °C à 0,45 MPa | mesure fabricant | idem |
| PETG (Bambu PETG HF) | traction XY / Z | 34 / 23 MPa | mesure fabricant | TDS PETG HF, copie `botland.com.pl/img/art/inne/25540_karta_produktu.pdf` |
| PETG HF | flexion résistance XY / Z | 64 / 48 MPa | mesure fabricant | `3dsaga.com/en/products/bambu-lab-petg-hf` (tableau Bambu) |
| PETG HF | allongement rupture XY / Z | 8,6 / 5,1 % | mesure fabricant | botland (copie TDS) |

Valeurs **indicatives** (ordres de grandeur littérature, aucune source Bambu vérifiée → `value: null` + `indicative_value`, niveau heuristique) :

| Mat. | ρ g/cm³ | traction XY/Z MPa | flexion rés. XY/Z | E flexion XY/Z MPa | retrait % | HDT °C | ε snap | pont max mm | surplomb max ° |
|---|---|---|---|---|---|---|---|---|---|
| PLA | 1,24 | — (mesuré) | — | — | 0,3 | — | 0,02 | 50 | 55 |
| PETG | 1,27 | — (mesuré) | — | 1800/1500 | 0,4 | 70 | 0,03 | 35 | 50 |
| ABS | 1,05 | 40/28 | 65/45 | 2000/1700 | 0,8 | 90 | 0,025 | 30 | 50 |
| ASA | 1,07 | 40/28 | 65/45 | 2000/1700 | 0,6 | 90 | 0,025 | 30 | 50 |
| TPU 95A | 1,2 | 30/22 | n/a | 30/25 | 0,8 | n/a | 0,10 | 20 | 45 |
| PA | 1,1 | 45/28 | 60/36 | 1500/1100 | 1,5 | 60 | 0,04 | 25 | 45 |
| PC | 1,2 | 60/40 | 90/60 | 2200/1800 | 0,7 | 110 | 0,03 | 25 | 45 |
| PLA-CF | 1,3 | 45/25 | 80/40 | 4500/2500 | 0,15 | 55 | 0,01 | 40 | 55 |
| PETG-CF | 1,3 | 50/28 | 80/45 | 3500/2000 | 0,3 | 75 | 0,012 | 30 | 50 |

Ces lignes indicatives sont « valeur indicative » : elles ne remplacent ni la fiche de la bobine ni un essai. Une calibration par retour d'impression réelle les remplace par des valeurs `observé`.


## Vérification contre les PDF officiels (2026-11-06) : écarts avec les anciennes valeurs indicatives

Les 11 fiches techniques Bambu Lab citées ci-dessus ont été **re-téléchargées depuis leur URL officielle** (CDN Bambu Lab) puis relues avec `pdftotext -raw`. Pour chaque fiche, `docs/research/materials-tds/<id>.txt` contient l'URL, la date, l'empreinte SHA-256 du PDF et les lignes exactes des propriétés utilisées. Un test Go (`TestMeasuredValuesAreInTDSExtracts`) vérifie que **chaque valeur `measured`** de `internal/engineering/data/materials.json` se retrouve dans l'extrait de sa fiche.

Résultat : les **147 valeurs `measured` non nulles** (masse volumique, modules, résistances, allongements, flexion, HDT, avec leurs tolérances `±`) sont toutes confirmées par les PDF ; aucune n'a été rétrogradée en `indicative`. Les cellules « N/A » des fiches (TPU : flexion et HDT ; PVA : propriétés mécaniques) restent `null`.

Changements de niveau :
- `max_overhang_deg` et `max_bridge_mm` passent de `measured` à **`heuristic`** : ce sont des *réglages recommandés* par le fabricant dans son profil d'impression, pas des mesures normées. `overhang_bridge` renvoie donc toujours un résultat `heuristique`.
- `shrinkage_pct` et `snap_strain_allow` restent `indicative` (aucune fiche ne les donne).

Écarts entre les **anciennes valeurs indicatives** du tableau ci-dessus (ordres de grandeur de la littérature) et les valeurs vérifiées des fiches Bambu Lab (les valeurs de la fiche font foi ; PA non chargé reste purement indicatif, aucune fiche Bambu n'est utilisée) :

| Matériau | Propriété | Ancienne (indicative) | Fiche Bambu | Écart | Niveau actuel |
|---|---|---|---|---|---|
| PETG | ρ g/cm³ | 1.27 | 1.25 | -2 % | measured |
| PETG | E flexion XY MPa | 1800 | 1790 | -1 % | measured |
| PETG | E flexion Z MPa | 1500 | 1580 | +5 % | measured |
| PETG | HDT 1,8 MPa °C | 70 | 65 | -7 % | measured |
| PETG | pont max mm | 35 | 30 | -14 % | heuristic |
| PETG | surplomb max ° | 50 | 70 | +40 % | heuristic |
| ABS | traction XY MPa | 40 | 41 | +2 % | measured |
| ABS | traction Z MPa | 28 | 32 | +14 % | measured |
| ABS | flexion rés. XY MPa | 65 | 68 | +5 % | measured |
| ABS | E flexion XY MPa | 2000 | 1880 | -6 % | measured |
| ABS | E flexion Z MPa | 1700 | 1590 | -6 % | measured |
| ABS | HDT 1,8 MPa °C | 90 | 84 | -7 % | measured |
| ABS | pont max mm | 30 | 40 | +33 % | heuristic |
| ABS | surplomb max ° | 50 | 70 | +40 % | heuristic |
| ASA | ρ g/cm³ | 1.07 | 1.05 | -2 % | measured |
| ASA | traction XY MPa | 40 | 37 | -8 % | measured |
| ASA | traction Z MPa | 28 | 31 | +11 % | measured |
| ASA | flexion rés. Z MPa | 45 | 40 | -11 % | measured |
| ASA | E flexion XY MPa | 2000 | 1920 | -4 % | measured |
| ASA | E flexion Z MPa | 1700 | 1650 | -3 % | measured |
| ASA | HDT 1,8 MPa °C | 90 | 92 | +2 % | measured |
| ASA | pont max mm | 30 | 40 | +33 % | heuristic |
| ASA | surplomb max ° | 50 | 70 | +40 % | heuristic |
| TPU-95A | traction XY MPa | 30 | 29.6 | -1 % | measured |
| TPU-95A | traction Z MPa | 22 | 23.2 | +5 % | measured |
| TPU-95A | E flexion XY MPa | 30 | n/d (fiche : N/A) | — | ancienne valeur retirée (la fiche ne la donne pas) |
| TPU-95A | E flexion Z MPa | 25 | n/d (fiche : N/A) | — | ancienne valeur retirée (la fiche ne la donne pas) |
| TPU-95A | surplomb max ° | 45 | 70 | +56 % | heuristic |
| PC | traction XY MPa | 60 | 55 | -8 % | measured |
| PC | traction Z MPa | 40 | 34 | -15 % | measured |
| PC | flexion rés. XY MPa | 90 | 108 | +20 % | measured |
| PC | flexion rés. Z MPa | 60 | 55 | -8 % | measured |
| PC | E flexion XY MPa | 2200 | 2310 | +5 % | measured |
| PC | E flexion Z MPa | 1800 | 1620 | -10 % | measured |
| PC | HDT 1,8 MPa °C | 110 | 117 | +6 % | measured |
| PC | pont max mm | 25 | 40 | +60 % | heuristic |
| PC | surplomb max ° | 45 | 70 | +56 % | heuristic |
| PLA-CF | ρ g/cm³ | 1.3 | 1.22 | -6 % | measured |
| PLA-CF | traction XY MPa | 45 | 38 | -16 % | measured |
| PLA-CF | traction Z MPa | 25 | 26 | +4 % | measured |
| PLA-CF | flexion rés. XY MPa | 80 | 96 | +20 % | measured |
| PLA-CF | flexion rés. Z MPa | 40 | 49 | +22 % | measured |
| PLA-CF | E flexion XY MPa | 4500 | 3700 | -18 % | measured |
| PLA-CF | E flexion Z MPa | 2500 | 2260 | -10 % | measured |
| PLA-CF | HDT 1,8 MPa °C | 55 | 54 | -2 % | measured |
| PLA-CF | pont max mm | 40 | 30 | -25 % | heuristic |
| PETG-CF | ρ g/cm³ | 1.3 | 1.25 | -4 % | measured |
| PETG-CF | traction XY MPa | 50 | 59 | +18 % | measured |
| PETG-CF | traction Z MPa | 28 | 38 | +36 % | measured |
| PETG-CF | flexion rés. XY MPa | 80 | 83 | +4 % | measured |
| PETG-CF | flexion rés. Z MPa | 45 | 62 | +38 % | measured |
| PETG-CF | E flexion XY MPa | 3500 | 2890 | -17 % | measured |
| PETG-CF | E flexion Z MPa | 2000 | 1680 | -16 % | measured |
| PETG-CF | HDT 1,8 MPa °C | 75 | 68 | -9 % | measured |
| PETG-CF | surplomb max ° | 50 | 70 | +40 % | heuristic |
| PLA | pont max mm | 50 | 30 | -40 % | heuristic |

Lecture : les écarts les plus nets concernent les **réglages recommandés** (surplomb +40 à +56 % pour PETG, ABS, ASA, PC, TPU, PETG-CF ; pont : PLA 50 → 30 mm, PC 25 → 40 mm) et les variantes chargées en fibres (PETG-CF : traction Z +36 %, flexion Z +38 % ; PLA-CF et PETG-CF : module de flexion XY −17/−18 %). Pour les matériaux non chargés (PETG, ABS, ASA), les ordres de grandeur de la littérature tenaient à ±15 % près sur les propriétés mécaniques et la HDT. Les valeurs de la fiche font foi ; une valeur générique ne doit jamais remplacer la fiche du matériau réel.
