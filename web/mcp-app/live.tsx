import { useCallback, useEffect, useRef, useState } from 'react';
import { AlertTriangle, Camera, Check, OctagonX, Pause, Play, RefreshCw } from 'lucide-react';
import { BrowserLink, CodeForm, StopConfirm, Warning, outcomeMessage, type AppLike, type CodeApproval } from './launch';

/**
 * Carte de suivi en direct (ui://bambu/print-live.html), liée à get_print_status et wait_for_print_change.
 *
 * Aucun accès réseau (CSP vide) : tout passe par app.callServerTool.
 *  - image de la caméra : outil réservé get_live_frame, toutes les ~2,5 s. Le serveur ne fait qu'UNE capture par fenêtre
 *    de 2 s et par imprimante quel que soit le nombre de vues : ce rythme ne charge pas le port 6000 de la P1S ;
 *  - état : outil réservé get_live_state, toutes les 5 s ; arrêt à l'état terminal, puis résumé final (durée, grammes, photo) ;
 *  - tout s'arrête quand la vue n'est pas visible (document.visibilityState, IntersectionObserver) et après idleMs sans
 *    interaction (bouton « Reprendre ») ; les boucles sont séquentielles (jamais deux requêtes de la même sorte en vol) ;
 *  - « Arrêter » appelle cancel_print : il ne fait que CRÉER la demande d'arrêt ; l'humain la valide avec son code de
 *    lancement (champ de StopConfirm) ou sur le lien du navigateur. La carte n'arrête jamais rien d'elle-même.
 *    « Reprendre » (impression en pause) appelle resume_print, qui crée de même une demande validée par l'humain.
 *
 * Code DÉFENSIF : tout champ peut manquer (ancien serveur, imprimante injoignable) ; une ligne absente disparaît, jamais d'erreur.
 */

interface ToolResultLike { structuredContent?: unknown; content?: Array<{ type: string; text?: string }>; isError?: boolean }
type Rec = Record<string, unknown>;

export interface LiveTemps { nozzle?: number; nozzleTarget?: number; bed?: number; bedTarget?: number; chamber?: number }
export interface LiveStage { label: string; done: boolean }
export interface LiveAlert { kind: 'blocked' | 'attention' | 'unreachable' | 'error'; text: string; action?: string }
export interface LiveFinal { outcome: string; duration?: string; grams?: number; gramsBasis?: string; finishedAtLocal?: string; photoSrc?: string; photoNote?: string }
export interface Live {
  jobId: string; printerId?: string; name?: string;
  state: string; printerState?: string; display?: string;
  /** 0..100, ou null quand rien ne le donne (barre indéterminée). */
  percent: number | null;
  layer?: number; totalLayers?: number; heightMm?: number; heightTotalMm?: number;
  remainingMin?: number; endsAtLocal?: string;
  temps?: LiveTemps;
  stages: LiveStage[]; currentStage?: string;
  alerts: LiveAlert[];
  paused: boolean; terminal: boolean; failed: boolean; stale: boolean;
  cameraAvailable?: boolean;
  final?: LiveFinal;
  next?: string; lastError?: string;
}

const isRec = (v: unknown): v is Rec => typeof v === 'object' && v !== null && !Array.isArray(v);
const num = (...vs: unknown[]): number | undefined => { for (const v of vs) if (typeof v === 'number' && Number.isFinite(v)) return v; return undefined; };
const str = (...vs: unknown[]): string | undefined => { for (const v of vs) if (typeof v === 'string' && v.trim() !== '') return v; return undefined; };
const textOf = (r: ToolResultLike) => (r.content ?? []).filter((c) => c.type === 'text').map((c) => c.text ?? '').join('\n').trim();
const sc = <T,>(r: unknown): T | undefined => ((r as ToolResultLike | undefined)?.structuredContent as T | undefined);

const PRINT_STATES = new Set(['DRAFT', 'PREPARED', 'SLICING', 'VALIDATED', 'UPLOADING', 'UPLOADED', 'AWAITING_PRINT_CONFIRMATION', 'START_REQUESTED', 'PRINTING', 'COMPLETED', 'FAILED', 'CANCELLED', 'START_UNKNOWN', 'START_ABORTED_BEFORE_SEND']);
const TERMINAL = new Set(['COMPLETED', 'FAILED', 'CANCELLED']);
const STATE_LABEL: Record<string, string> = {
  DRAFT: 'Brouillon', PREPARED: 'Préparée', SLICING: 'Découpe', VALIDATED: 'Découpée', UPLOADING: 'Envoi', UPLOADED: 'Fichier déposé', AWAITING_PRINT_CONFIRMATION: 'En attente de confirmation',
  START_REQUESTED: 'Démarrage', PRINTING: 'Impression en cours', COMPLETED: 'Terminée', FAILED: 'Échec', CANCELLED: 'Annulée', START_UNKNOWN: 'Démarrage incertain', START_ABORTED_BEFORE_SEND: 'Démarrage abandonné',
};

/** Reconnaît le suivi d'une impression : résultat de get_print_status / wait_for_print_change (PrintStatus) ou de get_live_state (enveloppe). */
export function isLiveResult(raw: unknown): boolean { return normalizeLive(raw) !== undefined; }

export function normalizeLive(raw: unknown): Live | undefined {
  if (!isRec(raw)) return undefined;
  const wrapped = isRec(raw.status) && isRec(raw.status.job);
  const st = wrapped ? (raw.status as Rec) : raw;
  const extras: Rec = wrapped ? raw : {};
  const job = isRec(st.job) ? st.job : undefined;
  if (!job || typeof job.id !== 'string' || typeof job.state !== 'string') return undefined;
  if (typeof job.printer_id !== 'string' && !PRINT_STATES.has(job.state)) return undefined; // un travail de la file (get_job_status) n'est pas une impression
  const state = job.state;
  const pr = isRec(st.printer) ? st.printer : undefined;
  const print = isRec(job.print) ? job.print : undefined;
  const sr = isRec(st.stage_report) ? st.stage_report : undefined;
  const live = !!pr && pr.reachable !== false && pr.stale !== true;
  const printing = state === 'PRINTING';

  const terminal = TERMINAL.has(state) || extras.terminal === true;
  let percent = num(live && printing ? pr?.progress_percent : undefined, print?.percent, pr?.progress_percent);
  if (state === 'COMPLETED') percent = 100;
  const printerState = str(pr?.state, print?.printer_state);

  const alerts: LiveAlert[] = [];
  const alert = str(st.alert, print?.attention, print?.blocked, print?.unreachable);
  if (alert) {
    const l = alert.toLowerCase();
    alerts.push({ kind: l.startsWith('bloquée') ? 'blocked' : l.includes('action') && l.includes('écran') ? 'attention' : l.includes('injoignable') ? 'unreachable' : 'error', text: alert });
  }
  const seen = new Set<string>();
  const errs = [...(Array.isArray(pr?.error_details) ? (pr!.error_details as unknown[]) : []), ...(Array.isArray(st.causes) ? (st.causes as unknown[]) : [])];
  for (const e of errs) {
    if (!isRec(e) || e.benign === true) continue;
    const text = str(e.text, e.short);
    const key = str(e.code, e.short, e.text) ?? '';
    if (!text || seen.has(key)) continue;
    seen.add(key);
    alerts.push({ kind: 'error', text: `${str(e.short) ? `${e.short} : ` : ''}${text}`, action: str(e.action) });
  }

  const stages: LiveStage[] = [];
  for (const s of Array.isArray(sr?.stages) ? (sr!.stages as unknown[]) : []) {
    if (!isRec(s)) continue;
    const label = str(s.label, s.name);
    if (label) stages.push({ label, done: s.state === 'done' });
  }

  const temps = isRec(pr?.temps) ? (pr!.temps as Rec) : undefined;
  const fin = isRec(extras.final) ? extras.final : undefined;
  const photo = fin && isRec(fin.photo) && typeof fin.photo.data === 'string' ? fin.photo : undefined;
  return {
    jobId: job.id, printerId: str(job.printer_id), name: str(job.name),
    state, printerState, display: str(pr?.display),
    percent: percent === undefined ? null : Math.max(0, Math.min(100, Math.round(percent))),
    layer: num(live && printing && (num(pr?.total_layers) ?? 0) > 0 ? pr?.layer : undefined, print?.layer),
    totalLayers: num(live && printing && (num(pr?.total_layers) ?? 0) > 0 ? pr?.total_layers : undefined, print?.total_layers),
    heightMm: num(extras.height_mm), heightTotalMm: num(extras.height_total_mm),
    remainingMin: terminal ? undefined : num(live && printing ? pr?.remaining_minutes : undefined, print?.remaining_minutes),
    endsAtLocal: terminal ? undefined : str(extras.ends_at_local),
    temps: temps ? { nozzle: num(temps.nozzle), nozzleTarget: num(temps.nozzle_target), bed: num(temps.bed), bedTarget: num(temps.bed_target), chamber: num(temps.chamber) } : undefined,
    stages, currentStage: str(sr?.current),
    alerts,
    paused: printerState === 'paused' || alerts.some((a) => a.kind === 'attention'),
    terminal, failed: state === 'FAILED' || state === 'CANCELLED',
    stale: pr ? (pr.stale === true || pr.reachable === false) : false,
    cameraAvailable: typeof extras.camera_available === 'boolean' ? extras.camera_available : undefined,
    final: fin ? {
      outcome: str(fin.outcome) ?? (state === 'COMPLETED' ? 'completed' : state === 'FAILED' ? 'failed' : 'cancelled'),
      duration: str(fin.duration), grams: num(fin.grams), gramsBasis: str(fin.grams_basis), finishedAtLocal: str(fin.finished_at_local),
      photoSrc: photo ? `data:${str(photo.mime) ?? 'image/jpeg'};base64,${photo.data as string}` : undefined, photoNote: str(fin.photo_note),
    } : undefined,
    next: str(job.next), lastError: str(job.last_error),
  };
}

// ---- formats ----
export function fmtMin(min?: number): string | undefined {
  if (min === undefined || !(min >= 0)) return undefined;
  const m = Math.round(min);
  if (m < 60) return `${m} min`;
  return `${Math.floor(m / 60)} h ${String(m % 60).padStart(2, '0')} min`;
}
export const fmtNum = (v: number, digits = 1) => String(+v.toFixed(digits)).replace('.', ',');
const fmtTemp = (v?: number, target?: number): string => (v === undefined ? '—' : `${Math.round(v)}${target ? ` / ${Math.round(target)}` : ''} °C`);

/** Heure de fin d'après le temps restant, dans le fuseau Europe/Paris, quand le serveur ne l'a pas encore rendue. */
export function endFallback(remainingMin?: number, now = Date.now()): string | undefined {
  if (!remainingMin || remainingMin <= 0) return undefined;
  try { return new Intl.DateTimeFormat('fr-FR', { hour: '2-digit', minute: '2-digit', timeZone: 'Europe/Paris' }).format(new Date(now + remainingMin * 60_000)); } catch { return undefined; }
}

interface Frame { src: string; takenAt?: string; width?: number; height?: number }
interface StopAsk { url: string; question?: string }
interface ActionAsk { id: string; url: string; question?: string; details: string[] }
type Note = { ok: boolean; text: string };

const OUTCOME: Record<string, string> = { completed: 'Impression terminée', failed: 'Impression échouée', cancelled: 'Impression annulée' };

export function LiveCard({ app, first, pollMs = 5000, frameMs = 2500, frameErrMs = 10_000, idleMs = 5 * 60_000 }: {
  app: AppLike; first: Live; pollMs?: number; frameMs?: number; frameErrMs?: number; idleMs?: number;
}) {
  const [live, setLive] = useState<Live>(first);
  const liveRef = useRef(live); liveRef.current = live;
  const finalTried = useRef(false);
  useEffect(() => { setLive(first); finalTried.current = false; }, [first]); // un nouveau résultat d'outil remplace l'état

  const [frame, setFrame] = useState<Frame | null>(null);
  const [frameErr, setFrameErr] = useState<string>();
  const [stale, setStale] = useState(false);

  // ---- visibilité et inactivité ----
  const rootRef = useRef<HTMLElement>(null);
  const [visible, setVisible] = useState(() => typeof document === 'undefined' || document.visibilityState !== 'hidden');
  const [inView, setInView] = useState(true);
  const [idle, setIdle] = useState(false);
  const idleTimer = useRef<number | undefined>(undefined);
  const arm = useCallback(() => { window.clearTimeout(idleTimer.current); idleTimer.current = window.setTimeout(() => setIdle(true), idleMs); }, [idleMs]);
  const touch = useCallback(() => { setIdle(false); arm(); }, [arm]);
  useEffect(() => { arm(); return () => window.clearTimeout(idleTimer.current); }, [arm]);
  useEffect(() => {
    const f = () => { const v = document.visibilityState !== 'hidden'; setVisible(v); if (v) touch(); };
    document.addEventListener('visibilitychange', f);
    return () => document.removeEventListener('visibilitychange', f);
  }, [touch]);
  useEffect(() => {
    const el = rootRef.current;
    if (!el || typeof IntersectionObserver === 'undefined') return;
    const io = new IntersectionObserver((es) => { const e = es[es.length - 1]; if (e) setInView(e.isIntersecting); });
    io.observe(el);
    return () => io.disconnect();
  }, []);
  const watching = visible && inView && !idle;

  const printerId = live.printerId;
  const camOn = watching && !live.terminal && !!printerId && live.cameraAvailable !== false && (live.state === 'PRINTING' || live.state === 'START_REQUESTED');

  // ---- boucle de l'image ----
  useEffect(() => {
    if (!camOn) return;
    let alive = true; let timer: number | undefined;
    const tick = async () => {
      let delay = frameMs;
      try {
        const r = await app.callServerTool({ name: 'get_live_frame', arguments: { printer_id: printerId } });
        if (!alive) return;
        const res = r as ToolResultLike | undefined;
        const f = sc<Rec>(r);
        if (!res || res.isError || !f || typeof f.data !== 'string') throw new Error(res ? textOf(res) || 'Image indisponible.' : 'Image indisponible.');
        setFrame({ src: `data:${str(f.mime) ?? 'image/jpeg'};base64,${f.data}`, takenAt: str(f.taken_at_local), width: num(f.width), height: num(f.height) });
        setFrameErr(undefined);
      } catch (e) {
        if (!alive) return;
        setFrameErr((e as Error).message || 'Image indisponible.');
        delay = Math.max(frameMs, frameErrMs); // une caméra en panne n'est pas martelée
      }
      if (alive) timer = window.setTimeout(tick, delay);
    };
    void tick();
    return () => { alive = false; window.clearTimeout(timer); };
  }, [camOn, app, printerId, frameMs, frameErrMs]);

  // ---- boucle de l'état : s'arrête à l'état terminal après avoir lu le résumé final ----
  useEffect(() => {
    if (!watching) return;
    let alive = true; let timer: number | undefined;
    const run = async () => {
      let cur = liveRef.current;
      if (cur.terminal && finalTried.current) return;
      const wantFinal = cur.terminal;
      if (wantFinal) finalTried.current = true;
      try {
        const r = await app.callServerTool({ name: 'get_live_state', arguments: { print_job_id: cur.jobId, ...(wantFinal ? { with_final_photo: true } : {}) } });
        if (!alive) return;
        if ((r as ToolResultLike | undefined)?.isError) throw new Error('refus');
        const n = normalizeLive(sc(r));
        if (!n) throw new Error('forme inattendue');
        cur = n; setLive(n); setStale(false);
      } catch { if (alive) setStale(true); }
      if (!alive) return;
      if (cur.terminal) { if (!finalTried.current) timer = window.setTimeout(run, 0); return; } // terminé : une dernière lecture avec la photo de fin, puis plus rien
      timer = window.setTimeout(run, pollMs);
    };
    void run();
    return () => { alive = false; window.clearTimeout(timer); };
  }, [watching, app, pollMs, live.jobId]);

  // ---- Arrêter : crée seulement la demande d'arrêt ----
  const [stopAsk, setStopAsk] = useState<StopAsk | null>(null);
  const [stopBusy, setStopBusy] = useState(false);
  const [stopNote, setStopNote] = useState<Note>();
  const requestStop = async () => {
    if (stopBusy) return;
    setStopBusy(true); setStopNote(undefined); touch();
    try {
      const r = await app.callServerTool({ name: 'cancel_print', arguments: { print_job_id: live.jobId } });
      const s = sc<{ outcome?: string; stop_url?: string; question?: string; note?: string }>(r);
      if ((r as ToolResultLike | undefined)?.isError || !s) setStopNote({ ok: false, text: textOf((r ?? {}) as ToolResultLike) || 'Demande refusée.' });
      else if (s.outcome === 'stop_requested' && typeof s.stop_url === 'string') setStopAsk({ url: s.stop_url, question: s.question });
      else setStopNote({ ok: false, text: s.note || `Aucun arrêt demandé (${s.outcome ?? 'refusé'}).` });
    } catch (e) { setStopNote({ ok: false, text: (e as Error).message }); } finally { setStopBusy(false); }
  };

  // ---- Reprendre : crée seulement la demande de reprise ----
  const [resumeAsk, setResumeAsk] = useState<ActionAsk | null>(null);
  const [resumeBusy, setResumeBusy] = useState(false);
  const [resumeNote, setResumeNote] = useState<Note>();
  const requestResume = async () => {
    if (resumeBusy) return;
    setResumeBusy(true); setResumeNote(undefined); touch();
    try {
      const r = await app.callServerTool({ name: 'resume_print', arguments: { print_job_id: live.jobId } });
      const s = sc<Rec>(r);
      if ((r as ToolResultLike | undefined)?.isError || !s || typeof s.action_url !== 'string') setResumeNote({ ok: false, text: textOf((r ?? {}) as ToolResultLike) || 'Demande refusée.' });
      else setResumeAsk({ id: str(s.action_id) ?? '', url: s.action_url, question: str(s.question), details: Array.isArray(s.details) ? (s.details as unknown[]).filter((d): d is string => typeof d === 'string') : [] });
    } catch (e) { setResumeNote({ ok: false, text: (e as Error).message }); } finally { setResumeBusy(false); }
  };

  const L = live;
  const label = L.paused && !L.terminal ? 'En pause' : L.display && L.state === 'PRINTING' ? L.display : STATE_LABEL[L.state] ?? L.state;
  const pct = L.percent;
  const ends = L.endsAtLocal ?? endFallback(L.remainingMin);
  const remaining = fmtMin(L.remainingMin);
  const showCam = !L.terminal && (L.state === 'PRINTING' || L.state === 'START_REQUESTED' || frame !== null);
  const pausedUi = !L.terminal && (idle);
  const cls = `live${L.failed ? ' live-failed' : ''}${L.terminal && !L.failed ? ' live-done' : ''}${pct === null && !L.terminal ? ' live-indet' : ''}`;

  return (
    <section ref={rootRef} className={cls} aria-label="Suivi de l'impression"
      onPointerDown={touch} onPointerMove={touch} onKeyDown={touch} onTouchStart={touch} onWheel={touch}>
      <header className="live-head">
        <h1>{L.name ?? 'Impression'}</h1>
        <span className="live-state" role="status">
          {L.terminal && !L.failed && <Check size={14} aria-hidden />}{L.failed && <AlertTriangle size={14} aria-hidden />}{L.paused && !L.terminal && <Pause size={14} aria-hidden />}
          {label}
        </span>
      </header>

      {L.alerts.map((a, i) => (
        <p key={i} className={`live-alert live-alert-${a.kind}`} role="alert">
          <AlertTriangle size={14} aria-hidden /> <span>{a.kind === 'blocked' ? 'Impression bloquée ? ' : ''}{a.text}{a.action ? <> <span className="dim">{a.action}</span></> : null}</span>
        </p>
      ))}

      {showCam && (
        <figure className="live-cam">
          {frame ? <img src={frame.src} alt="Image de la caméra de l'imprimante" /> : <div className="live-cam-empty" role="status"><Camera size={18} aria-hidden /> {frameErr ? 'Caméra indisponible.' : 'Image en cours de chargement…'}</div>}
          {pausedUi && (
            <div className="live-cam-pause">
              <span>Actualisation en pause (inactivité).</span>
              <button type="button" className="primary" onClick={touch}><RefreshCw size={14} aria-hidden /> Reprendre</button>
            </div>
          )}
          <figcaption className="small dim">
            {frame ? `Image de ${frame.takenAt ?? 'à l\'instant'}${watching && camOn ? ', actualisée toutes les ' + fmtNum(frameMs / 1000) + ' s' : ''}.` : null}
            {frameErr && frame ? ` Dernière actualisation impossible : ${frameErr}` : frameErr ? ` ${frameErr}` : ''}
          </figcaption>
        </figure>
      )}

      <div className="live-bar" role="progressbar" aria-label="Progression" aria-valuemin={0} aria-valuemax={100} aria-valuenow={pct ?? undefined} aria-valuetext={pct === null ? 'progression inconnue' : `${pct} %`}>
        <div className="live-fill" style={pct === null ? undefined : { width: `${pct}%` }} />
      </div>
      <div className="live-line">
        <span className="live-pct">{pct === null ? '…' : `${pct} %`}</span>
        {L.totalLayers ? <span className="live-meta">couche {L.layer ?? '—'} / {L.totalLayers}</span> : null}
        {L.heightMm !== undefined && L.heightTotalMm !== undefined ? <span className="live-meta">{fmtNum(L.heightMm)} / {fmtNum(L.heightTotalMm)} mm</span> : null}
      </div>
      {(remaining || ends) && (
        <p className="live-time">
          {remaining ? <>Reste <strong>{remaining}</strong></> : null}{remaining && ends ? ' · ' : ''}{ends ? <>fin à <strong>{ends}</strong></> : null}
        </p>
      )}

      {L.temps && (
        <dl className="live-temps" aria-label="Températures">
          <div><dt>Buse</dt><dd>{fmtTemp(L.temps.nozzle, L.temps.nozzleTarget)}</dd></div>
          <div><dt>Plateau</dt><dd>{fmtTemp(L.temps.bed, L.temps.bedTarget)}</dd></div>
          {L.temps.chamber && L.temps.chamber > 0 ? <div><dt>Caisson</dt><dd>{fmtTemp(L.temps.chamber)}</dd></div> : null}
        </dl>
      )}

      {(L.stages.length > 0 || L.currentStage) && !L.terminal && (
        <details className="live-stages" open={L.stages.length <= 4}>
          <summary className="small">Étapes de démarrage{L.currentStage ? ` · en cours : ${L.currentStage}` : ''}</summary>
          <ul>{L.stages.map((s, i) => <li key={i} className={s.done ? 'done' : ''}>{s.done ? <Check size={12} aria-hidden /> : <span className="live-dot" aria-hidden />} {s.label}</li>)}</ul>
        </details>
      )}

      {L.stale && !L.terminal && <p className="live-meta dim">L'imprimante ne répond plus : dernières valeurs connues.</p>}
      {stale && !L.terminal && <p className="live-meta dim">Actualisation momentanément impossible ; dernier état affiché.</p>}
      {L.lastError && L.terminal && <p className="live-meta">{L.lastError}</p>}

      {L.terminal && (
        <div className="live-final" role="status">
          <h2>{OUTCOME[L.final?.outcome ?? ''] ?? (L.failed ? 'Impression arrêtée' : 'Impression terminée')}</h2>
          <dl className="live-temps">
            {L.final?.duration ? <div><dt>Durée réelle</dt><dd>{L.final.duration}</dd></div> : null}
            {L.final?.grams ? <div><dt>Filament</dt><dd>{fmtNum(L.final.grams)} g{L.final.gramsBasis ? <span className="dim"> ({L.final.gramsBasis})</span> : null}</dd></div> : null}
            {L.final?.finishedAtLocal ? <div><dt>Fin</dt><dd>{L.final.finishedAtLocal}</dd></div> : null}
          </dl>
          {L.final?.photoSrc ? <figure className="live-cam"><img src={L.final.photoSrc} alt="Photo de fin d'impression" /><figcaption className="small dim">Photo de fin.</figcaption></figure>
            : L.final?.photoNote ? <p className="small dim">{L.final.photoNote}</p> : null}
        </div>
      )}

      {!L.terminal && L.state === 'PRINTING' && (
        <div className="live-actions">
          {L.paused && !resumeAsk && <button type="button" className="live-btn" disabled={resumeBusy} onClick={requestResume}><Play size={14} aria-hidden /> Reprendre l'impression</button>}
          {!stopAsk && <button type="button" className="live-btn live-btn-stop" disabled={stopBusy} onClick={requestStop}><OctagonX size={14} aria-hidden /> Arrêter</button>}
          {!stopAsk && <p className="small dim live-hint">« Arrêter » crée une demande d'arrêt : rien ne s'arrête tant que vous ne l'avez pas validée (code de lancement ou lien du navigateur).</p>}
        </div>
      )}
      {resumeNote && <p className={resumeNote.ok ? 'msg' : 'msg msg-err'} role="alert"><AlertTriangle size={14} aria-hidden /> {resumeNote.text}</p>}
      {stopNote && <p className="msg msg-err" role="alert"><AlertTriangle size={14} aria-hidden /> {stopNote.text}</p>}
      {resumeAsk && <ResumeConfirm app={app} ask={resumeAsk} onClose={() => setResumeAsk(null)} />}
      {stopAsk && !L.terminal && (
        <div className="live-ask">
          <StopConfirm app={app} stopUrl={stopAsk.url} question={stopAsk.question} />
          <div className="acts"><button type="button" className="linkbtn" onClick={() => setStopAsk(null)}>Fermer cette demande</button></div>
        </div>
      )}
    </section>
  );
}

/** Demande de reprise créée par resume_print : lien du navigateur, ou code de lancement saisi par l'humain dans ce champ. */
function ResumeConfirm({ app, ask, onClose }: { app: AppLike; ask: ActionAsk; onClose: () => void }) {
  const [busy, setBusy] = useState(false);
  const [note, setNote] = useState<Note>();
  const [done, setDone] = useState(false);
  const submit = async (code: string) => {
    setBusy(true); setNote(undefined);
    try {
      const res = await app.callServerTool({ name: 'validate_action_with_code', arguments: { action_id: ask.id, code } });
      const out = sc<CodeApproval>(res);
      if (!out || (res as ToolResultLike).isError) { setNote({ ok: false, text: textOf(res as ToolResultLike) || 'Refusé.' }); return; }
      setNote({ ok: out.approved, text: outcomeMessage(out) });
      if (out.approved) setDone(true);
    } catch (e) { setNote({ ok: false, text: (e as Error).message }); } finally { setBusy(false); }
  };
  return (
    <div className="live-ask">
      <h2>Reprise de l'impression</h2>
      {ask.question && <p className="small">{ask.question}</p>}
      {ask.details.length > 0 && <ul className="small">{ask.details.map((d, i) => <li key={i}>{d}</li>)}</ul>}
      {done ? <p className="msg" role="status"><Check size={14} aria-hidden /> {note?.text ?? 'Reprise demandée.'}</p> : (
        <>
          <p className="small">Rien n'est repris tant que vous n'avez pas validé : saisissez votre code de lancement ici, ou ouvrez le lien dans votre navigateur.</p>
          <CodeForm label="Code de lancement (6 à 8 chiffres)" button="Reprendre l'impression" needPlate={false} busy={busy} onSubmit={(code) => submit(code)} />
          <Warning />
          <BrowserLink app={app} url={ask.url} text="Ouvrir la page de reprise" />
          {note && <p className="msg msg-err" role="alert"><AlertTriangle size={14} aria-hidden /> {note.text}</p>}
        </>
      )}
      <div className="acts"><button type="button" className="linkbtn" onClick={onClose}>Fermer cette demande</button></div>
    </div>
  );
}
