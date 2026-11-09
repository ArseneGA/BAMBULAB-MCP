# Petits bugs à corriger

Liste des petits défauts remarqués à l'usage. Une ligne par bug : ce qu'on voit, la cause probable, la piste de correction.
Cocher quand c'est corrigé (avec le commit).

## Ouverts

- [ ] **F1 — Carte de progression en double** (2026-11-09, Claude Desktop, dépôt sur la P1S)
  - Vu : pendant un `upload_print_artifact`, une deuxième carte « Travail upload_print_artifact » apparaît au bout d'un moment sous la première ; les deux avancent en parallèle (49 % / 50 %).
  - Cause probable : `wait_for_job` rend la main avant la fin du travail (délai d'attente atteint) et Claude le rappelle ; chaque appel affiche une nouvelle carte, l'ancienne reste vivante.
  - Piste : une seule carte vivante par travail — l'ancienne carte se fige en « suivi repris plus bas » dès qu'un nouvel appel suit le même job (ou la carte suit le job jusqu'au bout sans nouvel appel) ; vérifier aussi le délai d'attente de `wait_for_job` pour les gros dépôts.

## Corrigés

_(vide)_
