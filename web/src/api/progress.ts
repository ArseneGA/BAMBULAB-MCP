/**
 * Avancement des travaux longs (découpe, dépôt sur l'imprimante, import, export) et de l'impression en cours.
 *
 * Code défensif : le serveur expose (ou exposera) dans les statuts de travaux les champs stables
 *   percent (0..100), done_bytes, total_bytes, eta_seconds, phase, phase_label.
 * On les utilise s'ils existent ; sinon on retombe sur ce qui existe déjà :
 *   - Job.progress (ratio 0..1) et Job.message ;
 *   - JobProgress (percent, phase, bytes_done, bytes_total, eta_seconds, queue_position, stalled, estimated) ;
 *   - PrintStatus / PrinterStatus (progress_percent, remaining_minutes, layer, total_layers).
 */

export interface Progress {
  /** 0..100 entier ; undefined quand l'avancement est inconnu (barre indéterminée). */
  percent?: number;
  /** « Découpe par Bambu Studio », « Envoi FTPS vers l'imprimante »… */
  phaseLabel?: string;
  doneBytes?: number;
  totalBytes?: number;
  /** Secondes restantes, si le serveur (ou l'imprimante) les donne. */
  etaSeconds?: number;
  /** Le pourcentage est une estimation par le temps, pas une mesure. */
  estimated?: boolean;
  stalled?: boolean;
  queuePosition?: number;
  state?: string;
  terminal: boolean;
  failed: boolean;
}

type Raw = Record<string, unknown>;
const num = (v: unknown): number | undefined => (typeof v === 'number' && Number.isFinite(v) ? v : undefined);
const str = (v: unknown): string | undefined => (typeof v === 'string' && v.trim() !== '' ? v : undefined);
const clampPct = (n: number) => Math.max(0, Math.min(100, Math.round(n)));

/** Étiquettes des phases connues (le serveur peut déjà les fournir en clair via phase_label). */
const PHASES: Record<string, string> = {
  queued: "En file d'attente", slicing: 'Découpe par Bambu Studio', validating: 'Validation', uploading: "Envoi vers l'imprimante",
  importing: 'Import des modèles', exporting: "Préparation de l'archive", analyzing: 'Analyse géométrique', rendering: 'Calcul des aperçus', printing: 'Impression',
};
const TERMINAL = new Set(['succeeded', 'failed', 'cancelled', 'unknown', 'COMPLETED', 'FAILED', 'CANCELLED']);
const FAILED = new Set(['failed', 'cancelled', 'unknown', 'FAILED', 'CANCELLED']);

/** Normalise un statut de travail (Job, JobProgress, ou tout objet portant les champs stables) en Progress. */
export function normalizeProgress(input: unknown): Progress {
  const r = (input && typeof input === 'object' ? input : {}) as Raw;
  const state = str(r.state);
  const terminal = state !== undefined && TERMINAL.has(state);
  const failed = state !== undefined && FAILED.has(state);

  const doneBytes = num(r.done_bytes) ?? num(r.bytes_done);
  const totalBytes = num(r.total_bytes) ?? num(r.bytes_total);

  let percent: number | undefined;
  const stable = num(r.percent);
  const ratio = num(r.progress);
  if (stable !== undefined) percent = clampPct(stable);
  else if (num(r.progress_percent) !== undefined) percent = clampPct(num(r.progress_percent)!);
  else if (doneBytes !== undefined && totalBytes !== undefined && totalBytes > 0) percent = clampPct((doneBytes / totalBytes) * 100);
  else if (ratio !== undefined) percent = clampPct(ratio <= 1 ? ratio * 100 : ratio);
  if (state === 'succeeded' || state === 'COMPLETED') percent = 100;
  // Un travail en file n'a pas encore d'avancement mesuré.
  const queuePosition = num(r.queue_position);

  let eta = num(r.eta_seconds);
  if (eta === undefined && num(r.remaining_minutes) !== undefined && num(r.remaining_minutes)! >= 0) eta = num(r.remaining_minutes)! * 60;
  if (eta !== undefined && eta < 0) eta = undefined;

  const phase = str(r.phase);
  const phaseLabel = str(r.phase_label) ?? (phase ? (PHASES[phase] ?? phase) : undefined) ?? (state === 'queued' ? PHASES.queued : undefined) ?? str(r.message);

  return { percent: terminal && !failed ? 100 : percent, phaseLabel, doneBytes, totalBytes, etaSeconds: terminal ? undefined : eta, estimated: r.estimated === true, stalled: r.stalled === true, queuePosition, state, terminal, failed };
}

/** « 9 h 38 », « 12 min », « 45 s », « moins d'une minute ». */
export function formatEta(seconds?: number): string | undefined {
  if (seconds === undefined || !Number.isFinite(seconds) || seconds < 0) return undefined;
  const s = Math.round(seconds);
  if (s < 45) return s <= 1 ? 'moins d\'une minute' : `${s} s`;
  const totalMin = Math.round(s / 60);
  if (totalMin < 60) return `${totalMin} min`;
  const h = Math.floor(totalMin / 60), m = totalMin % 60;
  return `${h} h ${String(m).padStart(2, '0')}`;
}

/** « 12,4 Mio / 48 Mio » pour un transfert ; undefined sans total. */
export function formatBytesProgress(done?: number, total?: number): string | undefined {
  if (total === undefined || total <= 0) return undefined;
  const f = (n: number) => (n >= 1 << 20 ? `${(n / (1 << 20)).toFixed(1).replace('.', ',')} Mio` : n >= 1 << 10 ? `${Math.round(n / (1 << 10))} Kio` : `${n} o`);
  return `${f(done ?? 0)} / ${f(total)}`;
}

/** Durée en minutes -> « 24 h 43 ». */
export const formatMinutes = (min?: number) => (min === undefined || !Number.isFinite(min) || min < 0 ? undefined : formatEta(min * 60));

/** Heure d'arrivée estimée « terminé vers 18 h 20 » (heure locale du navigateur). */
export function finishAt(etaSeconds?: number, now = new Date()): string | undefined {
  if (etaSeconds === undefined || etaSeconds < 0) return undefined;
  const d = new Date(now.getTime() + etaSeconds * 1000);
  return d.toLocaleTimeString('fr-FR', { hour: '2-digit', minute: '2-digit' }).replace(':', ' h ');
}

/**
 * Source d'avancement d'une impression (PrintJob) pour <ProgressBar> :
 *  - en découpe ou en dépôt : le JobProgress de /api/print-jobs (percent, phase, eta_seconds, queue_position…) ;
 *  - en impression : l'état lu sur l'imprimante (live), sinon la dernière observation du service (job.print).
 */
export function printSource(
  job: { state: string; print?: { percent: number; layer: number; total_layers: number; remaining_minutes: number } },
  prog?: object,
  live?: { reachable?: boolean; progress_percent?: number; remaining_minutes?: number; layer?: number; total_layers?: number },
): Record<string, unknown> {
  const printing = job.state === 'PRINTING' || job.state === 'START_REQUESTED';
  if (!printing) return { ...(prog ?? {}), state: (prog as { state?: string } | undefined)?.state ?? 'running' };
  const useLive = !!live && live.reachable !== false && live.progress_percent !== undefined;
  const pct = useLive ? live!.progress_percent : job.print?.percent;
  const remaining = useLive ? live!.remaining_minutes : job.print?.remaining_minutes;
  const layer = useLive ? live!.layer : job.print?.layer;
  const total = useLive ? live!.total_layers : job.print?.total_layers;
  const p = prog as { percent?: number; eta_seconds?: number } | undefined;
  return {
    state: 'running',
    percent: pct ?? p?.percent,
    remaining_minutes: remaining !== undefined && remaining >= 0 ? remaining : undefined,
    eta_seconds: p?.eta_seconds,
    phase_label: total ? `Impression · couche ${layer ?? 0} sur ${total}` : job.state === 'START_REQUESTED' ? 'Démarrage demandé' : 'Impression',
  };
}
