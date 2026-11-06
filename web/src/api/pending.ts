/**
 * Fonctions de l'interface qui n'ont PAS encore d'endpoint HTTP côté bambud.
 *
 * Règle du jalon 3 : on n'invente aucun endpoint. Chaque besoin est déclaré ici
 * avec ce que l'écran attend, et l'écran correspondant reste alimenté par les
 * données de démonstration (`data/demo.ts`) avec un bandeau « Bientôt branché ».
 * Quand le serveur expose l'endpoint, on remplace l'entrée par un vrai appel dans
 * `client.ts` et on retire l'entrée de cette liste.
 *
 * Ce fichier est la liste de travail du branchement : un besoin = une ligne.
 */

export type PendingId =
  | 'goals' | 'variants' | 'slicing' | 'materials' | 'printer_status' | 'printer_upload' | 'print_start' | 'print_progress'
  | 'windows_agent' | 'project_state' | 'preview_capture' | 'analyze' | 'export_files';

export interface PendingNeed {
  id: PendingId;
  /** Écran concerné. */
  screen: 'Préparation' | 'Comparaison' | 'Matériaux' | 'Impression' | 'Barre du haut' | 'Fiche modèle';
  /** Ce que l'écran affiche ou déclenche aujourd'hui en démonstration. */
  today: string;
  /** Besoin côté serveur : outil MCP du dossier §6 / endpoint HTTP à exposer, avec forme attendue. */
  needs: string;
  /** Jalon qui doit le fournir. */
  milestone: string;
}

export const PENDING: PendingNeed[] = [
  {
    id: 'goals', screen: 'Préparation',
    today: 'Objectif de finition en texte libre et réglages par objet/zone, conservés en sessionStorage.',
    needs: "MCP set_manufacturing_goals ; HTTP GET/PUT /api/projects/{id}/goals -> { goal_text, criteria[], ambiguities[], revision } ; GET /api/projects/{id} (révision, objets, zones, réglages effectifs avec provenance profil/objet/zone).",
    milestone: 'objectifs et variantes (internal/goals)',
  },
  {
    id: 'project_state', screen: 'Préparation',
    today: "Arbre objet/zones issu d'une forme de démonstration, pas du fichier réel.",
    needs: "GET /api/models/{id}/parts -> sous-parties stables (nœuds du GLB : id + libellé) et GET/PUT /api/projects/{id}/overrides avec expected_revision (conflit 409 si révision concurrente).",
    milestone: 'objectifs et variantes (internal/goals)',
  },
  {
    id: 'variants', screen: 'Comparaison',
    today: 'Trois variantes fictives, mesures calculées par une formule de démonstration.',
    needs: "MCP create_experiment / compare_variants / apply_variant ; HTTP POST /api/experiments, GET /api/experiments/{id} -> variantes { id, label, overrides, job_id, metrics{time_s, filament_g, layers, support_volume_mm3}, estimated:true } ; POST /api/variants/{id}/apply { expected_project_revision }.",
    milestone: 'objectifs et variantes (internal/goals)',
  },
  {
    id: 'slicing', screen: 'Comparaison',
    today: "Estimations de temps/matière/supports de démonstration, étiquetées « démo ».",
    needs: "POST /api/slice { model_id, version_id, machine, process, filament, overrides } -> { job_id } (Bambu Studio CLI, J5) ; résultat du job : métriques réelles du 3MF slicé + artifact_id + avertissements ; GET /api/artifacts/{id}.",
    milestone: 'branchement du slicing (internal/slicer + agent Windows)',
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
    today: "Envoi simulé avec minuterie.",
    needs: "POST /api/printers/{id}/upload { artifact_id, idempotency_key } -> { job_id } ; validation préalable POST /api/artifacts/{id}/validate { printer_id } -> rapport et compatibilité.",
    milestone: 'imprimante LAN (internal/printer, J7)',
  },
  {
    id: 'print_start', screen: 'Impression',
    today: "Confirmation « plateau vide » + lancement simulés ; aucune commande envoyée.",
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
    id: 'preview_capture', screen: 'Fiche modèle',
    today: "Bouton « Préparer » mène à l'écran de démonstration.",
    needs: "POST /api/models/{id}/prepare { version_id } -> project_id (crée le projet de préparation lié à la version immuable).",
    milestone: 'objectifs et variantes (internal/goals)',
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
