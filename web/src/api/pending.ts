/**
 * Fonctions de l'interface qui n'ont PAS encore d'endpoint HTTP côté bambud.
 *
 * Règle : on n'invente aucun endpoint. Chaque besoin est déclaré ici avec ce que
 * l'écran attend. Les écrans concernés restent alimentés par les données de
 * démonstration (`data/demo.ts`) sous un bandeau « Bientôt branché » ; quand le
 * serveur expose l'endpoint, on remplace l'entrée par un vrai appel dans
 * `client.ts` et on la retire d'ici.
 *
 * Branché depuis le jalon 3 (retiré de cette liste) : objectifs (/api/goal-criteria,
 * /api/goals), expériences et comparaison (/api/experiments, /compare), application
 * de variante (/api/variants/apply), découpe réelle par Bambu Studio.
 */

export type PendingId =
  | 'materials' | 'print_cancel_reconcile' | 'windows_agent_ui' | 'zone_overrides' | 'experiments_list' | 'analyze' | 'export_files' | 'record_result_ui';

export interface PendingNeed {
  id: PendingId;
  /** Écran concerné. */
  screen: 'Préparation' | 'Comparaison' | 'Matériaux' | 'Impression' | 'Barre du haut' | 'Fiche modèle';
  /** Ce que l'écran affiche ou déclenche aujourd'hui. */
  today: string;
  /** Besoin côté serveur : outil MCP / endpoint HTTP à exposer, avec forme attendue. */
  needs: string;
  /** Jalon qui doit le fournir. */
  milestone: string;
}

export const PENDING: PendingNeed[] = [
  {
    id: 'zone_overrides', screen: 'Préparation',
    today: "Objectif et variantes sont réels, mais les réglages restent globaux à la pièce : pas de sélection d'objet ou de zone, pas d'édition manuelle des réglages effectifs avec leur provenance.",
    needs: "GET /api/models/{id}/parts (sous-parties stables : nœuds du GLB, id + libellé) ; POST /api/profiles/modify existe côté serveur mais n'est pas encore utilisé par l'interface (édition de profil : réglages effectifs, provenance profil/objet/zone, base_revision) ; variantes par zone côté goals.",
    milestone: 'objectifs (suite) : zones et édition de profil',
  },
  {
    id: 'experiments_list', screen: 'Comparaison',
    today: "L'écran s'ouvre sur ?experiment=<id> ou sur la dernière expérience créée dans ce navigateur (localStorage).",
    needs: 'GET /api/experiments?model_id=... -> liste { id, goal_id, model_version_id, created_at, état global } pour retrouver une expérience passée.',
    milestone: 'objectifs (suite)',
  },
  {
    id: 'record_result_ui', screen: 'Comparaison',
    today: "Le retour d'expérience après impression (record_print_result, mémoire des essais) n'a pas d'écran.",
    needs: "Écran de saisie branché sur POST /api/print-results (déjà exposé) ; lecture de la mémoire : GET /api/print-memory?model_id=... (équivalent de get_print_memory, non exposé en HTTP).",
    milestone: 'objectifs (suite) / impression',
  },
  {
    id: 'materials', screen: 'Matériaux',
    today: "Modèles d'imprimantes réels (/api/printer-models) ; aucune fiche matériau : la base internal/engineering (matériaux, retrait, propriétés) n'est pas publiée en HTTP.",
    needs: "GET /api/materials, GET /api/materials/{id} (famille, propriétés, retrait, sources et niveau de confiance de internal/engineering) ; fiches de bobines et calibrations (état de bobine, historique) non modélisées côté serveur ; presets filament de Bambu Studio : GET /api/profiles/filaments.",
    milestone: "matériaux (exposer internal/engineering)",
  },
  {
    id: 'print_cancel_reconcile', screen: 'Impression',
    today: "L'interface suit, valide, envoie et demande la confirmation ; elle ne propose ni annulation d'une impression en cours ni réconciliation d'un lancement incertain.",
    needs: "Écrans pour POST /api/print-jobs/{id}/cancel (une impression en cours exige un stop_confirmation_id approuvé par l'humain, donc une page /confirm dédiée à l'arrêt) et POST /api/print-jobs/{id}/reconcile ; aujourd'hui seulement via les outils MCP cancel_print et reconcile_print.",
    milestone: "impression (suite)",
  },
  {
    id: 'windows_agent_ui', screen: 'Barre du haut',
    today: "La pastille Windows reflète capabilities.agents (en ligne, Bambu Studio ouvert ou installé) ; pas d'écran d'appairage, d'inventaire ni de demande de copie.",
    needs: "UI pour POST /api/agents/pairing, GET /api/agents/{id}/inventory et POST /api/agents/{id}/inventory/select (copie des fichiers windows_only vers le stockage Linux), et GET /api/agent/tasks pour suivre les copies.",
    milestone: "agent Windows (suite)",
  },
  {
    id: 'analyze', screen: 'Fiche modèle',
    today: "L'analyse stockée est affichée ; le recalcul (unité confirmée, fabricabilité par machine) n'est pas déclenchable depuis l'interface.",
    needs: "POST /api/models/{id}/analyze { file_id?, unit?: mm|cm|m|inch, machine?: p1s|p1p|x1c|a1|a1mini } -> AnalyzeOutput (comme l'outil MCP analyze_geometry), pour confirmer une unité supposée et afficher la fabricabilité.",
    milestone: 'catalogue (exposition HTTP de analyze_geometry)',
  },
  {
    id: 'export_files', screen: 'Fiche modèle',
    today: "Aucun téléchargement de l'original ni export groupé.",
    needs: 'GET /api/files/{file_id}/download (original, empreinte en en-tête) et POST /api/export { model_ids[] } -> archive ; renommage/déplacement physiques volontairement absents (catalogue seulement).',
    milestone: 'catalogue (export groupé, dossier §14.2)',
  },
];

export const pendingFor = (screen: PendingNeed['screen']) => PENDING.filter((p) => p.screen === screen);
