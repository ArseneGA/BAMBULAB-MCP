# Catalogue officiel des filaments Bambu (B3)

Date : 2026-11-10. Idée B3 du backlog (« Couleurs justes partout »). Aucune API d'IA : données embarquées et calcul déterministe.

## 1. Ce que c'est

Un catalogue **embarqué dans le binaire** (`internal/filament/catalog/data/catalog.json`, 160 Kio) : aucune lecture réseau au runtime, aucune dépendance à Bambu Studio sur le serveur. Il répond à : « quelle est la couleur officielle Bambu la plus proche de ce hex ? », « quelles finitions existent en PETG ? », « cette matière passe-t-elle sur ma buse ? », « à quelle température imprimer ? ».

| Contenu | Volume |
|---|---|
| Gammes (PLA Basic, PLA Matte, PLA Silk+, PETG HF, PETG Translucent, ABS, ASA, TPU 95A HF, PA6-CF, PC FR, PVA, supports...) | 48 |
| Couleurs officielles (hex, noms FR/EN/DE/ES/IT, dégradés et bobines multicolores) | 342 |

Par classe de matière (couleurs) : PLA 183, PETG 63, TPU 38, ABS 24, PA 11, ASA 8, PC 7, supports 6, PET 1, PPS 1.
Par niveau de confiance : 342 couleurs et 48 gammes en `official_profile`, 0 en `official_site`, 0 en `estimated` (voir limites § 6).

## 2. Sources et niveaux de confiance

| Niveau | Sens | Utilisé pour |
|---|---|---|
| `official_profile` | Lu dans les profils officiels livrés avec Bambu Studio (`resources/profiles/BBL`, version **02.09.00.74**) | Couleurs (hex, noms), `filament_id`, matière, températures de buse et de plateau, ventilateur, densité, diamètre, débit volumique max, température de plateau de séchage, dureté de buse requise (`required_nozzle_HRC`), buses déconseillées (`info/nozzle_incompatibles.json`) |
| `official_site` | Relevé sur le site ou le wiki officiel de Bambu Lab | Plages de séchage sur plateau chauffant (12 h) : PLA 65-75, PETG 75-85, ABS/ASA 90-100, TPU 80-90 °C |
| `estimated` | Déduit, à vérifier | Aucune entrée aujourd'hui |

Chaque gamme porte `source` (`kind`, `ref` = fichier de profil, `profile_version`, `retrieved` = date de relevé) ; chaque couleur porte `confidence`. Le fichier `filaments_color_codes.json` de Bambu Studio date du 2026-11-02 ; relevé le 2026-11-10.

**Relevé web.** Le wiki officiel (`wiki.bambulab.com`) et le guide PDF (`cdn1.bambulab.com`) ont répondu HTTP 402/403 au téléchargement automatique le 2026-11-10 : leur contenu n'a pas pu être lu en direct. Seule la plage de séchage sur plateau a été rapportée par une recherche web (résumé de la page `bambu-filament-drying-cover`) : elle est donc marquée `official_site` avec un libellé « indicatif » et rattachée à son URL. Aucune autre valeur ne vient du web. Aucun texte protégé n'est copié : seulement des faits (noms, hex, températures).

**Valeurs jamais inventées.** Poids net d'une bobine, prix, séchage PA/PC (aucune plage relevée), noms français manquants (107 couleurs n'ont pas de nom FR dans le profil officiel : le nom anglais est alors affiché) : ces champs sont absents, la réponse indique `not_available` et l'interface écrit « non disponible ». La compatibilité AMS par gamme n'est pas dans les profils : elle n'est pas affirmée.

## 3. Régénérer les données

```
python scripts/gen_filament_catalog.py "C:\Program Files\Bambu Studio\resources\profiles\BBL"
go test ./internal/filament/catalog/
```

Le script (Python standard, hors ligne) lit `filament/filaments_color_codes.json`, résout l'héritage `inherits` des presets `Bambu * @base` (rattachés par `filament_id`), lit `resources/info/nozzle_incompatibles.json` et la version de `BBL.json`. Les profils Bambu Studio sont sous licence AGPL-3.0 ; seuls des faits (noms, valeurs numériques) sont repris, pas de code ni de texte de description. À refaire à chaque nouvelle version de Bambu Studio (la ligne `profile_version` du catalogue le dit).

## 4. Paquet `internal/filament/catalog` (`filcatalog`)

Fonctions pures, testées (`catalog_test.go`) : `Default()` (catalogue embarqué), `Search(Query)` (gamme ou classe, finition FR/EN, texte de nom insensible aux accents et à la casse, `Hex` + `MaxDelta`, buse), `Nearest(hex, famille)`, `Distance` (ΔE CIE76, mêmes repères que `plan_colors` : exact <= 12, proche <= 30, approximatif <= 55), `Family.CompatibleWith(Nozzle)` (acier trempé requis, diamètre ou type de buse Standard / High Flow / E3D High Flow déconseillé), `RunSearch`, `Entry`.

## 5. Intégration

**Outils MCP** (lecture seule, `Title`, `ReadOnlyHint`, `outputSchema`) : `search_filament_catalog` (sans critère : aperçu des gammes ; `hex` : plus proches classées par ΔE ; `nozzle_diameter_mm` + `hardened_nozzle` : gammes utilisables), `get_filament_catalog_entry` (fiche d'une couleur `GFA00-10300` ou d'une gamme `GFA00` / « PLA Basic », verdict de compatibilité de buse, sources). Aucune action sur l'imprimante. Ajoutés aux garde-fous : `spec_conformance_test.go`, `secrets_test.go` (`TestFilamentCatalogToolsCarryNoSecret`), `noapprove_test.go` (outils présents, en lecture seule, énumérés avec arguments d'approbation forgés).

**Bobines (sans migration)** : champ facultatif `catalog_color_id` dans le document JSON de la bobine (`update_spool`, carte-formulaire, API web) : préremplit marque, nom, couleur, diamètre, translucidité encore vides ; `-` détache. `list_spools` renvoie `official_color` : l'entrée rattachée (`linked: true`) ou, sinon, la plus proche (suggestion, ΔE <= 30, jamais stockée). Migration 0330 **non nécessaire**.

**`plan_colors`** : chaque emplacement porte `wanted_official` (couleur officielle la plus proche de la couleur voulue) et la note « aucune bobine » la cite. **`list_filaments`** : description renvoyée vers le catalogue ; la liste des presets, elle, reste celle de Bambu Studio.

**API web** (lecture seule) : `GET /api/filament-catalog`, `/colors?family=&finish=&text=&hex=&max_delta_e=&nozzle=&hardened=1&limit=`, `/entry?id=`.

**Interface** : sélecteur de couleur et de finition officiels dans la boîte « Nouvelle bobine » (page Bobines, composant isolé `web/src/pages/FilamentCatalog.tsx`) ; onglet « Filaments Bambu » de la page Matériaux (fiches par gamme : températures, plateaux, ventilateur, densité, acier trempé, sources). `Shell.tsx`, `main.tsx` et `nav.ts` ne sont pas touchés.

**Aperçu réaliste (N2).** Les hex du catalogue et la finition (`finish` : mat, soie, translucide, métal, paillettes, marbre, glow, galaxie, bois, aero, carbone, verre) sont l'entrée prévue pour les matériaux du rendu ; N2 n'est pas modifié ici.

## 6. Limites

- Données de couleur uniquement pour les gammes Bambu (342 couleurs). Les filaments tiers livrés dans les profils BBL (Polymaker PolyLite et PolyTerra, Overture, eSUN, Generic) n'ont pas de table de couleurs officielle : hors catalogue.
- Le niveau `official_site` est limité à la plage de séchage ; le wiki, la boutique et les fiches TDS n'ont pas pu être téléchargés (HTTP 402/403). Poids net de bobine, prix, compatibilité AMS par gamme, séchage PA/PC : « non disponible ».
- Gammes à finition ambiguë (Basic, Lite, Pure, Tough, Palette) : finition « standard ».
- Le hex est celui du profil Bambu Studio (couleur nominale de l'icône), pas une mesure du filament imprimé ; l'écart ΔE est un CIE76 simple, pas un rendu physique.
- Le « diamètre » vient du profil (1,75 mm ; 2,85 mm pour TPU 75A, déduit du nom du preset).
