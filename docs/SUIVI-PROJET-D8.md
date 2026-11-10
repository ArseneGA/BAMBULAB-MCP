# Journal photo à chaque 10 % (G9 D8, étend N19)

Section à fusionner dans `docs/SUIVI-PROJET.md`. Spec : `docs/specs/G9-suivi-de-projet.md` § 4 D8, § 6 PJ10, CA10.5 à CA10.8.

## Ce que fait le serveur

Pendant une impression, quand la progression franchit 10, 20, … 90 %, le serveur prend **une** photo de la caméra et la range
avec l'impression (`camera_photos`, kind `progress`, colonne `percent`). Le palier 100 % est la **photo de fin existante**
(kind `final`, prise à la fin d'une impression réussie) : elle est simplement étiquetée `percent = 100`, jamais dupliquée.
Une impression menée à 100 % donne donc 10 paliers. Une impression échouée ou annulée garde ses paliers atteints ; sa photo de
fin d'échec n'est pas un palier 100 %.

- **Lecture seule** : le serveur lit la caméra, il n'envoie aucune commande à l'imprimante.
- **Une tentative par palier** : pas de rattrapage, pas de rafale. Un palier sauté (38 → 52 %) ne produit que la photo du palier
  atteint le plus récent (50 %) ; le 40 % n'est pas consigné. Après un redémarrage du serveur, les paliers déjà consignés ne sont
  pas repris.
- **Jamais bloquant** : caméra absente, hors ligne, image vide ou erreur → le palier est consigné `status = unavailable` avec
  une `reason` lisible (sans secret) et l'impression continue. La fiche affiche « photo non disponible (raison) ».
- **Stockage** : image redimensionnée à 1280 px de large au plus (jamais agrandie), JPEG qualité 80, blob sha256 du magasin
  existant (dédoublonnage). La photo 100 % (finale) est conservée telle que prise par la caméra.
- Indépendant de la détection d'échec : couper la détection ne coupe pas le journal photo (réglage `progress_photos`).

## Schéma (migration `0310_camera_progress_photos.sql`, additive)

`camera_photos` : `percent INTEGER` (10-100, NULL hors paliers), `status TEXT NOT NULL DEFAULT 'ok'` (`ok` | `unavailable`),
`reason TEXT NOT NULL DEFAULT ''`, index `(print_job_id, percent)`. Une ligne `unavailable` a `sha256 = ''` et `size = 0`.

## Lecture pour PJ5 / PJ10 / PJ1

- `camera.Service.ProgressPhotos(ctx, printJobID) ([]ProgressPhoto, error)` : paliers consignés, du plus petit au plus grand
  (`percent`, `status`, `reason`, `kind`, `sha256`, `size_bytes`, `taken_at`, `url`). Un palier absent n'a pas été atteint ou a été
  sauté ; un palier `unavailable` s'affiche « photo non disponible » avec sa raison.
- `camera.Service.ProgressStorage(ctx) (ProgressStorage, error)` : volume utilisé, plafond, niveau `ok` | `warning` (80 % du
  plafond) | `full`, et un `message` clair prêt pour le tableau de bord PJ1.
- `camera.Service.PhotosForJob` ignore les paliers indisponibles et renseigne `percent`.

## Rétention et plafond (tâche d'entretien existante, une fois par heure)

Réglages (`camera` settings, modifiables par `UpdateSettings` / `SettingsPatch`) :

| Réglage | Défaut | Sens |
|---|---|---|
| `progress_photos` | vrai | journal photo actif |
| `progress_photo_width` | 1280 | largeur maximale (320-4096) |
| `progress_photo_quality` | 80 | qualité JPEG (40-95) |
| `progress_retention_days` | 90 | paliers intermédiaires purgés au-delà (0 = illimité) |
| `progress_cap_mb` | 2048 | plafond de volume (0 = aucun) |

Ordre de purge : d'abord la rétention, puis, si le volume dépasse le plafond, les **plus anciens paliers intermédiaires**
jusqu'à repasser sous le plafond. **Jamais supprimés** : toute photo `final` / palier 100 %, les photos des impressions jugées
`success`, et toute photo que `Config.PhotoReferenced(ctx, sha256)` déclare référencée (galerie N22, projet : à brancher par
l'appelant). Un blob n'est supprimé que si plus aucune ligne ne l'utilise.

Verdict : le critère « jugée success » lit `print_logs.verdict` **seulement si la colonne existe** (migration 0300 de PJ11) ; tant
qu'elle est absente, il est ignoré (testé en simulant la colonne).

## Limites

Vérifié avec la caméra simulée uniquement ; le comportement sur une vraie caméra Bambu (latence, caméra occupée par Studio,
imprimante en link agent) n'est pas vérifié. La progression vient du champ `mc_percent` de l'état imprimante, interrogé à la
période de scan (5 s par défaut). `PhotoReferenced` n'est pas encore branché dans `internal/core` (aucune galerie ni projet
dans ce dépôt au moment de D8) : à faire avec N22 / PJ2.
