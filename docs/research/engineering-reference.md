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

Date de recherche : 2026-11-06. Sources lues via copies secondaires (retailers) : à re-vérifier contre les PDF officiels Bambu Lab.

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
