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

export type PendingId = 'zone_overrides';

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

export const PENDING: PendingNeed[] = [
  {
    id: 'zone_overrides', screen: 'Préparation',
    today: "L'objet ou la sous-partie se sélectionne (sous-parties stables du serveur) et les réglages se modifient à la main avec leur provenance, mais ils valent pour la pièce entière : une zone d'un critère est consignée, sans variante propre.",
    needs: "Variantes par zone et réglages réellement limités à un objet : un profil de découpe (machine, process, filament) est global ; Bambu Studio porte les réglages par objet dans le projet 3MF (métadonnées d'objet), que ni modify_slicing_profile ni la génération de variantes n'écrivent aujourd'hui. Il faudrait des surcharges ciblées par objet dans les révisions de profil et leur écriture dans le 3MF découpé.",
    milestone: 'objectifs (suite) : surcharges par objet dans le 3MF',
  },
];

export const pendingFor = (screen: PendingNeed['screen']) => PENDING.filter((p) => p.screen === screen);
