import { useEffect, useRef, useState } from 'react';
import { AlertTriangle, Check } from 'lucide-react';

/**
 * Vue « progression » (ui://bambu/progress.html), liée à get_job_status, get_upload_status et wait_for_job.
 *
 * Lecture seule : la vue n'agit sur rien, elle ne fait que rappeler l'outil d'état qui l'a ouverte pour se rafraîchir.
 * Code DÉFENSIF : les champs de progression stables (percent, done_bytes, total_bytes, eta_seconds, phase, phase_label)
 * arrivent d'une autre branche ; tant qu'ils manquent, on lit les noms actuels (progress_detail.percent, bytes_done…)
 * ou on déduit le pourcentage des octets. Un champ absent n'est jamais une erreur : la ligne correspondante disparaît.
 */

export interface AppLike {
  callServerTool(p: { name: string; arguments: Record<string, unknown> }): Promise<unknown>;
}
interface ToolResultLike { structuredContent?: unknown; content?: Array<{ type: string; text?: string }>; isError?: boolean }
type Rec = Record<string, unknown>;

export interface Prog {
  source: 'job' | 'upload';
  id: string;
  title: string;
  state?: string;
  /** 0..100, ou null quand rien ne permet de le calculer (barre indéterminée). */
  percent: number | null;
  doneBytes?: number;
  totalBytes?: number;
  etaSeconds?: number;
  phase?: string;
  phaseLabel?: string;
  terminal: boolean;
  failed: boolean;
  stalled: boolean;
  estimated: boolean;
  queuePosition?: number;
  message?: string;
}

const isRec = (v: unknown): v is Rec => typeof v === 'object' && v !== null && !Array.isArray(v);
const num = (...vs: unknown[]): number | undefined => { for (const v of vs) if (typeof v === 'number' && Number.isFinite(v)) return v; return undefined; };
const str = (...vs: unknown[]): string | undefined => { for (const v of vs) if (typeof v === 'string' && v.trim() !== '') return v; return undefined; };

const STATE_LABEL: Record<string, string> = {
  queued: 'En file d\'attente', pending: 'En attente', running: 'En cours', succeeded: 'Terminé', complete: 'Terminé', done: 'Terminé',
  failed: 'Échec', canceled: 'Annulé', cancelled: 'Annulé', canceling: 'Annulation…',
};
const PHASE_LABEL: Record<string, string> = {
  queued: 'En file d\'attente', starting: 'Démarrage', slicing: 'Découpe', uploading: 'Envoi', upload: 'Envoi', receiving: 'Réception', importing: 'Import', analysing: 'Analyse',
  analyzing: 'Analyse', preview: 'Aperçu', validating: 'Validation', finishing: 'Finalisation',
};
const TERMINAL = new Set(['succeeded', 'complete', 'done', 'failed', 'canceled', 'cancelled']);

/** Reconnaît un résultat d'outil d'état (job ou envoi) ; undefined si la forme est autre. */
export function normalizeProgress(raw: unknown): Prog | undefined {
  if (!isRec(raw)) return undefined;
  const job = isRec(raw.job) ? raw.job : undefined;
  const pd = isRec(raw.progress_detail) ? raw.progress_detail : isRec(raw.progress) ? raw.progress : undefined;
  const hasUpload = typeof raw.upload_id === 'string' && ('received' in raw || 'total_size' in raw || 'next_offset' in raw);
  const jobId = str(raw.job_id, job?.id, pd?.job_id);
  if (!hasUpload && !jobId) return undefined;
  if (!hasUpload && !job && !pd && num(raw.percent) === undefined && typeof raw.state !== 'string') return undefined;

  const source: Prog['source'] = hasUpload && !jobId ? 'upload' : 'job';
  const state = str(raw.state, job?.state, pd?.state) ?? (hasUpload ? (raw.complete === true ? 'complete' : 'running') : undefined);
  const done = num(raw.done_bytes, pd?.bytes_done, pd?.done_bytes, hasUpload ? raw.received : undefined);
  const total = num(raw.total_bytes, pd?.bytes_total, pd?.total_bytes, hasUpload ? raw.total_size : undefined);
  let percent = num(raw.percent, pd?.percent, job?.progress !== undefined && typeof job.progress === 'number' ? Math.round(job.progress * 100) : undefined);
  if (percent === undefined && done !== undefined && total !== undefined && total > 0) percent = (done / total) * 100;
  const terminal = raw.terminal === true || pd?.terminal === true || raw.complete === true || (state !== undefined && TERMINAL.has(state));
  const failed = state === 'failed' || state === 'canceled' || state === 'cancelled';
  if (terminal && !failed) percent = 100; // un travail terminé avec succès est à 100 %, quoi que dise un champ périmé
  const phase = str(raw.phase, pd?.phase);
  const title = source === 'upload'
    ? `Envoi de ${str(raw.filename) ?? 'fichier'}`
    : `Travail ${str(job?.kind, raw.kind) ?? ''}`.trim();
  const queue = num(raw.queue_position, pd?.queue_position);
  return {
    source, id: source === 'upload' ? String(raw.upload_id) : (jobId as string), title, state,
    percent: percent === undefined ? null : Math.max(0, Math.min(100, Math.round(percent))),
    doneBytes: done, totalBytes: total,
    etaSeconds: num(raw.eta_seconds, pd?.eta_seconds),
    phase, phaseLabel: str(raw.phase_label, pd?.phase_label) ?? (phase ? PHASE_LABEL[phase.toLowerCase()] ?? phase : undefined),
    terminal, failed,
    stalled: raw.stalled === true || pd?.stalled === true,
    estimated: raw.estimated === true || pd?.estimated === true,
    queuePosition: queue,
    message: str(isRec(job?.error) ? (job?.error as Rec).message : undefined, raw.message, pd?.message, job?.message),
  };
}

export function fmtEta(s?: number): string | undefined {
  if (s === undefined || !(s >= 0)) return undefined;
  const t = Math.round(s);
  if (t < 60) return `${t} s`;
  if (t < 3600) return `${Math.floor(t / 60)} min ${String(t % 60).padStart(2, '0')} s`;
  return `${Math.floor(t / 3600)} h ${String(Math.floor((t % 3600) / 60)).padStart(2, '0')} min`;
}
export function fmtBytes(n?: number): string | undefined {
  if (n === undefined || !(n >= 0)) return undefined;
  if (n < 1024) return `${n} o`;
  if (n < 1048576) return `${(n / 1024).toFixed(0)} Ko`;
  return `${(n / 1048576).toFixed(n < 10485760 ? 2 : 1)} Mo`;
}

/** Outil à rappeler pour rafraîchir. */
export function refreshCall(p: Prog): { name: string; arguments: Record<string, unknown> } {
  return p.source === 'upload' ? { name: 'get_upload_status', arguments: { upload_id: p.id } } : { name: 'get_job_status', arguments: { job_id: p.id, log_tail: 1 } };
}

const sc = (r: unknown): unknown => (r as ToolResultLike | undefined)?.structuredContent;

export function ProgressCard({ app, first, pollMs = 3000 }: { app: AppLike; first: Prog; pollMs?: number }) {
  const [p, setP] = useState<Prog>(first);
  const [stale, setStale] = useState(false);
  const cur = useRef(first); cur.current = p;
  // un nouveau résultat d'outil remplace l'état
  useEffect(() => { setP(first); setStale(false); }, [first]);

  useEffect(() => {
    if (p.terminal) return;
    let alive = true;
    const id = window.setInterval(async () => {
      try {
        const r = await app.callServerTool(refreshCall(cur.current));
        if ((r as ToolResultLike | undefined)?.isError) throw new Error('refus');
        const n = normalizeProgress(sc(r));
        if (alive && n) { setP(n); setStale(false); }
      } catch { if (alive) setStale(true); }
    }, pollMs);
    return () => { alive = false; window.clearInterval(id); };
  }, [app, p.terminal, p.id, pollMs]);

  const eta = fmtEta(p.etaSeconds);
  const bytes = p.totalBytes !== undefined ? `${fmtBytes(p.doneBytes ?? 0)} / ${fmtBytes(p.totalBytes)}` : undefined;
  const stateText = p.phaseLabel ?? (p.state ? STATE_LABEL[p.state] ?? p.state : undefined);
  const cls = `prog${p.failed ? ' prog-failed' : ''}${p.terminal && !p.failed ? ' prog-done' : ''}${p.percent === null && !p.terminal ? ' prog-indet' : ''}`;
  return (
    <section className={cls} aria-label={p.title}>
      <header className="prog-head">
        <h1>{p.title}</h1>
        <span className="prog-state" role="status">
          {p.terminal && !p.failed && <Check size={14} aria-hidden />}{p.failed && <AlertTriangle size={14} aria-hidden />}
          {p.state ? STATE_LABEL[p.state] ?? p.state : 'En cours'}
        </span>
      </header>
      <div className="prog-bar" role="progressbar" aria-valuemin={0} aria-valuemax={100} aria-valuenow={p.percent ?? undefined} aria-valuetext={p.percent === null ? 'progression inconnue' : `${p.percent} %`}>
        <div className="prog-fill" style={p.percent === null ? undefined : { width: `${p.percent}%` }} />
      </div>
      <div className="prog-line">
        <span className="prog-pct">{p.percent === null ? '…' : `${p.percent} %`}</span>
        {stateText && <span className="prog-phase">{stateText}</span>}
        {p.queuePosition !== undefined && <span className="dim">position {p.queuePosition} dans la file</span>}
      </div>
      {(bytes || eta) && (
        <p className="prog-meta mono">{bytes}{bytes && eta ? ' · ' : ''}{eta ? `reste environ ${eta}` : ''}</p>
      )}
      {p.estimated && <p className="prog-meta dim">Pourcentage estimé par le temps écoulé.</p>}
      {p.stalled && <p className="prog-meta prog-warn" role="alert"><AlertTriangle size={14} aria-hidden /> Aucun signe de vie : le travail est peut-être bloqué.</p>}
      {p.message && p.terminal && <p className="prog-meta">{p.message}</p>}
      {stale && !p.terminal && <p className="prog-meta dim">Actualisation momentanément impossible ; dernier état affiché.</p>}
    </section>
  );
}
