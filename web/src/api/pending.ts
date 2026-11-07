/**
 * Fonctions de l'interface qui n'ont PAS (entièrement) d'endpoint HTTP côté bambud.
 *
 * Règle : on n'invente aucun endpoint. Chaque besoin est déclaré ici avec ce que l'écran attend, et l'écran
 * l'affiche sous un bandeau « limite connue ». Plus aucun écran n'affiche de données de démonstration.
 *
 * Branché (retiré de cette liste) : objectifs, expériences, comparaison, application de variante, découpe réelle
 * (jalon 3) ; puis matériaux (/api/materials), liste des expériences (/api/experiments), mémoire des essais et
 * saisie du résultat (/api/print-memory, /api/print-results), analyse avec unité confirmée (/api/models/{id}/analyze),
 * sous-parties (/api/models/{id}/parts), téléchargement et export (/api/files/{id}/download, /api/export),
 * réconciliation d'un lancement incertain (/api/print-jobs/{id}/reconcile) et écran de l'agent Windows (/agents).
 */

export type PendingId = string;

export interface PendingNeed {
  id: PendingId;
  /** Écran concerné. */
  screen: 'Préparation';
  /** Ce que l'écran fait aujourd'hui. */
  today: string;
  /** Besoin côté serveur : ce qu'il faudrait exposer, avec la raison du blocage. */
  needs: string;
  /** Jalon qui doit le fournir. */
  milestone: string;
}

/**
 * Plus aucune limite connue. `zone_overrides` est retiré : les réglages par objet existent maintenant, via le plateau
 * multi-pièces (POST /api/plate-plans, internal/printing/plates.go). Ils sont écrits dans Metadata/model_settings.config
 * du 3MF et relus par Bambu Studio (preuve : TestRealPerObjectInfill / TestRealPerObjectKeys). Reste hors périmètre :
 * les variantes par zone d'une même pièce dans les expériences d'objectifs (la plage de hauteur par pièce est prise en charge).
 */
export const PENDING: PendingNeed[] = [];

export const pendingFor = (screen: PendingNeed['screen']) => PENDING.filter((p) => p.screen === screen);
