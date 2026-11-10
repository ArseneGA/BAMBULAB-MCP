/**
 * Table de libellés UNIQUE de Printalk (G10 UX4) : site web, pages serveur (par miroir) et cartes affichées dans Claude
 * écrivent le même mot pour le même état. Plus aucune table locale (STATE_LABEL, QSTATE, STATUS, OUTCOME, statePill).
 * Le terme technique reste disponible dans `tech` (infobulle, « Détails techniques »), jamais comme libellé principal.
 */
export type LabelTone = 'ok' | 'warn' | 'err' | undefined;
/** Classe de pastille du site (`.pill.<cls>`) : run = en cours, ok, wait = attente, fail = échec, '' = neutre. */
export type PillClass = 'run' | 'ok' | 'wait' | 'fail' | '';
export interface Label { label: string; tone?: LabelTone; pill: PillClass; tech?: string }

const L = (label: string, pill: PillClass = '', tech?: string): Label => ({ label, pill, tone: pill === 'ok' || pill === 'run' ? 'ok' : pill === 'wait' ? 'warn' : pill === 'fail' ? 'err' : undefined, tech });

/** États d'une impression préparée côté serveur (PrintState). */
export const PRINT_STATE: Record<string, Label> = {
  DRAFT: L('Brouillon'),
  PREPARED: L('Préparée'),
  SLICING: L('Préparation en cours', 'wait', 'découpe'),
  VALIDATED: L('Prête à envoyer'),
  UPLOADING: L('Envoi en cours', 'wait'),
  UPLOADED: L('Envoyée à l\'imprimante', '', 'UPLOADED'),
  AWAITING_PRINT_CONFIRMATION: L('Attend votre confirmation', 'wait'),
  START_REQUESTED: L('Lancement demandé', 'run'),
  PRINTING: L('Impression en cours', 'run'),
  COMPLETED: L('Terminée', 'ok'),
  FAILED: L('Échec', 'fail'),
  CANCELLED: L('Annulée'),
  START_UNKNOWN: L('Lancement à vérifier', 'fail', 'START_UNKNOWN'),
  START_ABORTED_BEFORE_SEND: L('Lancement abandonné', 'wait'),
};

/** États d'une entrée de la file d'attente. */
export const QUEUE_STATE: Record<string, Label> = {
  slicing: L('Préparation', 'wait', 'découpe'),
  needs_assignments: L('Bobines à choisir', 'wait'),
  waiting: L('En attente'),
  uploading: L('Envoi en cours', 'wait'),
  ready: L('Prête : attend votre accord', 'ok'),
  printing: L('En cours', 'run'),
  done: L('Terminée', 'ok'),
  failed: L('Échec', 'fail'),
  removed: L('Retirée'),
};

/** Reste d'une bobine. */
export const SPOOL_STATUS: Record<string, Label> = {
  ok: L('Suffisante', 'ok'), low: L('Presque vide', 'wait'), empty: L('Vide', 'fail'), archived: L('Archivée'),
};

/** Issue technique d'une impression du journal (le verdict humain est une autre notion). */
export const LOG_OUTCOME: Record<string, Label> = {
  success: L('Terminée', 'ok'), partial: L('Partielle', 'wait'), failed: L('Échec', 'fail'), cancelled: L('Annulée'),
};

/** Résultat réel consigné par l'humain pour une variante (essai de réglages). */
export const RESULT_OUTCOME: Record<string, Label> = {
  success: L('Réussi', 'ok'), partial: L('Partiel', 'wait'), failed: L('Raté', 'fail'),
};

/** Suivi d'un travail de fond (import, préparation, envoi) affiché par les cartes. */
export const JOB_STATE: Record<string, Label> = {
  queued: L('En file d\'attente'), pending: L('En attente'), running: L('En cours', 'run'), succeeded: L('Terminé', 'ok'), complete: L('Terminé', 'ok'), done: L('Terminé', 'ok'),
  failed: L('Échec', 'fail'), canceled: L('Annulé'), cancelled: L('Annulé'), canceling: L('Annulation…', 'wait'),
};

/** Essai de réglages : préparation des variantes (un essai a des variantes). */
export const SLICE_STATE: Record<string, Label> = {
  pending: L('En attente'), queued: L('En file'), running: L('Préparation en cours', 'wait', 'découpe'),
  succeeded: L('Terminée', 'ok'), failed: L('Échec', 'fail'), cancelled: L('Annulée', 'wait'),
};
export const EXPERIMENT_STATE: Record<string, Label> = {
  running: L('Préparations en cours', 'wait'), complete: L('Terminé', 'ok'), partial: L('Terminé avec échecs', 'wait'),
};

/** Carnet des dysfonctionnements. */
export const ISSUE_STATE: Record<string, Label> = {
  new: L('Nouveau', 'wait'), seen: L('Vu'), fixed: L('Corrigé', 'ok'), dismissed: L('Écarté'),
};

const FALLBACK: Label = { label: '', pill: '' };
const pick = (t: Record<string, Label>, k: string | undefined): Label => (k !== undefined && t[k]) || { ...FALLBACK, label: k ?? '' };
export const printStateLabel = (s?: string) => pick(PRINT_STATE, s);
export const queueStateLabel = (s?: string) => pick(QUEUE_STATE, s);
export const spoolStatusLabel = (s?: string) => pick(SPOOL_STATUS, s);
export const logOutcomeLabel = (s?: string) => pick(LOG_OUTCOME, s);
export const resultOutcomeLabel = (s?: string) => pick(RESULT_OUTCOME, s);
export const jobStateLabel = (s?: string) => pick(JOB_STATE, s);
export const sliceStateLabel = (s?: string) => pick(SLICE_STATE, s);
export const experimentStateLabel = (s?: string) => pick(EXPERIMENT_STATE, s);
export const issueStateLabel = (s?: string) => pick(ISSUE_STATE, s);

/** Minuscule initiale, pour insérer un libellé au milieu d'une phrase. */
export const lowerFirst = (s: string) => (s ? s.charAt(0).toLowerCase() + s.slice(1) : s);

/** Forme minimale de l'état d'une imprimante (compatible avec PrinterStatus du site) : évite toute dépendance au site. */
export interface PrinterLike { reachable: boolean; state: string; gcode_state?: string }
export const isPrinterPrinting = (d?: PrinterLike) => !!d && d.reachable && (d.state === 'printing' || d.state === 'paused' || ['RUNNING', 'PAUSE'].includes((d.gcode_state ?? '').toUpperCase()));

/** Pastille d'état d'une imprimante : un seul vocabulaire pour l'accueil, la page Imprimantes et les cartes. */
export function printerPill(d?: PrinterLike, failed?: boolean): { cls: PillClass; label: string } {
  if (failed && !d) return { cls: 'fail', label: 'Statut indisponible' };
  if (!d) return { cls: '', label: 'Lecture…' };
  if (!d.reachable) return { cls: 'fail', label: 'Injoignable' };
  if (isPrinterPrinting(d)) return { cls: d.state === 'paused' ? 'wait' : 'run', label: d.state === 'paused' ? 'En pause' : 'Imprime' };
  if (d.state === 'failed') return { cls: 'fail', label: 'Dernière impression en échec' };
  if (d.state === 'preparing') return { cls: 'wait', label: 'Préparation' };
  return { cls: 'ok', label: 'Prête' };
}

/** Termes techniques à ne plus afficher tels quels (test de vocabulaire) et leur équivalent en clair. */
export const GLOSSARY: Array<{ tech: string; plain: string }> = [
  { tech: 'bambud', plain: 'Printalk' },
  { tech: 'slicer / découpe', plain: 'Préparation de l\'impression (découpe)' },
  { tech: 'G-code', plain: 'Fichier d\'impression (G-code)' },
  { tech: 'Empreinte (sha256)', plain: 'Identifiant du fichier' },
  { tech: 'HMS', plain: 'Alertes de l\'imprimante' },
  { tech: 'Firmware', plain: 'Version du logiciel de l\'imprimante' },
  { tech: 'Backend CAO', plain: 'Conversion des fichiers STEP' },
  { tech: 'Matériaux / Bobines', plain: 'Matières' },
  { tech: 'Comparaison / Variantes / Expérience', plain: 'Essais de réglages' },
  { tech: 'Gabarits', plain: 'Pièces sur mesure (gabarits)' },
  { tech: 'Journal', plain: 'Impressions terminées' },
  { tech: 'Lancer', plain: 'Vérifier et lancer' },
  { tech: 'Jobs / Travaux en cours', plain: 'Préparations en cours' },
];

/** Titre d'une impression terminée affiché par la carte de suivi (issue de l'impression : completed, failed, cancelled). */
export function finalOutcomeTitle(outcome?: string, failed?: boolean): string {
  if (outcome === 'completed') return 'Impression terminée';
  if (outcome === 'failed') return 'Impression échouée';
  if (outcome === 'cancelled') return 'Impression annulée';
  return failed ? 'Impression arrêtée' : 'Impression terminée';
}
