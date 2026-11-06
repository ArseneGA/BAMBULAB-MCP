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
  | 'materials' | 'printer_status' | 'printer_upload' | 'print_start' | 'print_progress'
  | 'windows_agent' | 'zone_overrides' | 'experiments_list' | 'analyze' | 'export_files' | 'record_result_ui';

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
    today: 'Cinq fiches matériau fictives ; toute valeur absente reste « inconnu ».',
    needs: "GET/POST/PUT /api/materials (fiche : famille, fabricant, référence, température buse/plateau, enceinte, buse durcie, compatibilité machine, état de bobine, calibrations) ; GET /api/machines (fiche machine P1S/A1/X1C...).",
    milestone: 'matériaux (internal/contracts/material.go à exposer)',
  },
  {
    id: 'printer_status', screen: 'Impression',
    today: 'Imprimante P1S simulée (état constant).',
    needs: "GET /api/printers, GET /api/printers/{id}/status -> état, fraîcheur (updated_at), température, progression, AMS, erreurs HMS ; flux SSE GET /api/printers/{id}/events (MQTT LAN, J7).",
    milestone: 'imprimante LAN (internal/printer, J7)',
  },
  {
    id: 'printer_upload', screen: 'Impression',
    today: 'Envoi simulé avec minuterie.',
    needs: "POST /api/printers/{id}/upload { artifact_id, idempotency_key } -> { job_id } ; validation préalable POST /api/artifacts/{id}/validate { printer_id } -> rapport et compatibilité ; artefact slicé : GET /api/artifacts/{sha} (le candidat expose artifact_sha256).",
    milestone: 'imprimante LAN (internal/printer, J7)',
  },
  {
    id: 'print_start', screen: 'Impression',
    today: 'Confirmation « plateau vide » + lancement simulés ; aucune commande envoyée.',
    needs: "POST /api/printers/{id}/start { remote_ref, plate, material, confirmation_token } : le jeton de confirmation vient de contracts.confirmation (usage unique, lié à l'empreinte du fichier, de l'imprimante, du plateau et de la matière).",
    milestone: 'imprimante LAN (internal/printer, J7)',
  },
  {
    id: 'print_progress', screen: 'Impression',
    today: 'Suivi simulé (couches, températures, perte réseau, échec).',
    needs: "GET /api/prints/{id} -> machine d'états contracts/print.go (état, couche, ETA, erreurs) + POST /api/prints/{id}/cancel.",
    milestone: 'imprimante LAN (internal/printer, J7)',
  },
  {
    id: 'windows_agent', screen: 'Barre du haut',
    today: "Pastille « Windows » : affichée hors ligne tant que /api/capabilities annonce windows_agent indisponible.",
    needs: "GET /api/agents -> { id, name, online, last_seen, indexed_files, pending_copies } ; POST /api/agents/{id}/copy { file_ids[] } pour demander la copie d'un fichier windows_only.",
    milestone: 'agent Windows (cmd/bambu-agent)',
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
