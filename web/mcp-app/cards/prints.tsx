import { Star } from 'lucide-react';
import type { LogEntry } from '../../src/api/insights';
import type { QueueEntry } from '../../src/api/filamentApi';
import type { HistoryReply, PrintJob } from '../../src/api/printTypes';
import { useToolCall, type CardProps } from './call';
import { fmtDate, fmtGrams, fmtMinutes, fmtMoney, fmtSeconds, isRec, num } from './format';
import { LiteBadge, LiteBox, LiteButton, LiteCard, LiteState, LiteStat, LiteStats, toneOfState, type Tone } from './lite';

/**
 * Carte Impressions : list_print_history (historique), list_print_queue (file), get_print_log (fiche) et list_print_log (journal).
 * Clic sur une impression : get_print_status (carte de suivi). Lecture seule : aucune commande d'impression.
 */
export type PrintsData =
  | { kind: 'history'; reply: HistoryReply }
  | { kind: 'queue'; queue: QueueEntry[]; note?: string }
  | { kind: 'log'; entry: LogEntry }
  | { kind: 'logs'; entries: LogEntry[]; total: number };

export function normalizePrints(s: unknown): PrintsData | undefined {
  if (!isRec(s)) return undefined;
  if (Array.isArray(s.print_jobs) && isRec(s.counts)) return { kind: 'history', reply: s as unknown as HistoryReply };
  if (Array.isArray(s.queue) && s.queue.every((q) => isRec(q) && typeof q.print_job_id === 'string')) return { kind: 'queue', queue: s.queue as QueueEntry[], note: typeof s.note === 'string' ? s.note : undefined };
  if (Array.isArray(s.entries) && typeof s.total === 'number' && s.entries.every((e) => isRec(e) && typeof e.print_job_id === 'string')) return { kind: 'logs', entries: s.entries as LogEntry[], total: s.total };
  if (typeof s.print_job_id === 'string' && typeof s.outcome === 'string' && isRec(s.settings)) return { kind: 'log', entry: s as unknown as LogEntry };
  return undefined;
}

const STATE_FR: Record<string, string> = {
  DRAFT: 'Brouillon', PREPARED: 'Préparée', SLICING: 'Découpe', VALIDATED: 'Validée', UPLOADING: 'Envoi', UPLOADED: 'Envoyée', AWAITING_PRINT_CONFIRMATION: 'Attend ta confirmation',
  START_REQUESTED: 'Démarrage demandé', PRINTING: 'En impression', COMPLETED: 'Terminée', FAILED: 'Échec', CANCELLED: 'Annulée', START_UNKNOWN: 'Démarrage incertain',
};
const VIEW_FR: Record<string, string> = { all: 'Toutes', active: 'En cours', finished: 'Terminées', failed: 'Échecs', archived: 'Archivées', trash: 'Corbeille' };
const OUTCOME_FR: Record<string, string> = { success: 'Réussie', partial: 'Partielle', failed: 'Ratée', cancelled: 'Annulée' };
const OUTCOME_TONE: Record<string, Tone> = { success: 'ok', partial: 'warn', failed: 'fail', cancelled: 'off' };
const QUEUE_FR: Record<string, string> = { slicing: 'Découpe', needs_assignments: 'Affectation manquante', waiting: 'En attente', uploading: 'Envoi', ready: 'Prête : attend l\'accord humain', printing: 'En impression', done: 'Terminée', failed: 'Échec', removed: 'Retirée' };

/** Durée mesurée d'une impression terminée : de la bascule en PRINTING à l'état terminal, d'après les événements. */
export function jobDurationMinutes(j: PrintJob): number | undefined {
  const ev = j.events ?? [];
  const start = ev.find((e) => e.to === 'PRINTING');
  const end = [...ev].reverse().find((e) => ['COMPLETED', 'FAILED', 'CANCELLED'].includes(e.to));
  if (!start || !end) return undefined;
  const d = (Date.parse(end.at) - Date.parse(start.at)) / 60000;
  return Number.isFinite(d) && d >= 0 ? d : undefined;
}

function Stars({ n }: { n: number }) {
  return <span className="lc-dim" aria-label={`Note ${n} sur 5`} style={{ display: 'inline-flex', gap: 1 }}>{[1, 2, 3, 4, 5].map((i) => <Star key={i} size={11} aria-hidden fill={i <= n ? 'currentColor' : 'none'} />)}</span>;
}

interface Row { id: string; name: string; tone: Tone; label: string; meta: string[]; end: Array<string | undefined>; note?: string; rating?: number }

function Rows({ rows, busy, disabled, open, label }: { rows: Row[]; busy: string | null; disabled: boolean; open: (id: string) => void; label: string }) {
  return (
    <ul className="lc-list" aria-label={label}>
      {rows.map((r, i) => (
        <li key={`${r.id}-${i}`}>
          <button type="button" className="lc-row" disabled={disabled} aria-busy={busy === r.id} onClick={() => open(r.id)} aria-label={`Suivi de ${r.name}`}>
            <LiteBox seed={r.name} size={40} label={`silhouette de ${r.name}`} />
            <span className="lc-row-main">
              <span className="lc-row-name">{r.name}</span>
              <span className="lc-row-meta">{r.meta.filter(Boolean).join(' · ')}</span>
              {r.note && <span className="lc-row-meta" style={{ fontStyle: 'italic' }}>« {r.note} »</span>}
            </span>
            <span className="lc-row-end">
              <LiteBadge tone={r.tone}>{r.label}</LiteBadge>
              {r.end.filter(Boolean).map((e, k) => <span key={k}>{e}</span>)}
              {r.rating ? <Stars n={r.rating} /> : null}
            </span>
          </button>
        </li>
      ))}
    </ul>
  );
}

const logRow = (e: LogEntry): Row => ({
  id: e.print_job_id, name: e.name || e.model_name || e.print_job_id, tone: OUTCOME_TONE[e.outcome] ?? 'off', label: OUTCOME_FR[e.outcome] ?? e.outcome,
  meta: [e.printer_model ?? '', e.material_family ?? '', fmtDate(e.finished_at) ?? ''],
  end: [fmtSeconds(e.actual?.seconds ?? e.estimated?.seconds), fmtGrams(e.actual?.grams ?? e.estimated?.grams), fmtMoney(e.actual_cost?.total ?? e.planned_cost?.total, e.actual_cost?.currency ?? e.planned_cost?.currency)],
  note: e.comment, rating: e.rating,
});

export function PrintsCard({ app, data, onOpen }: CardProps<PrintsData>) {
  const { busy, error, run } = useToolCall(app, onOpen);
  const open = (id: string) => { void run(id, 'get_print_status', { print_job_id: id }); };
  const dis = busy !== null;

  if (data.kind === 'history') {
    const r = data.reply;
    const jobs = r.print_jobs.length ? r.print_jobs : (r.groups ?? []).flatMap((g) => g.jobs);
    const rows: Row[] = jobs.map((j) => {
      const prog = r.progress?.[j.id];
      const active = ['PRINTING', 'UPLOADING', 'SLICING', 'START_REQUESTED'].includes(j.state);
      const pct = num(prog?.percent) ?? num(j.print?.percent);
      const dur = jobDurationMinutes(j);
      return {
        id: j.id, name: j.name || j.id, tone: toneOfState(j.state), label: STATE_FR[j.state] ?? j.state,
        meta: [j.summary?.printer_model ?? '', j.summary?.filament_presets?.[0] ?? '', fmtDate(j.updated_at) ?? ''],
        end: [active && pct !== undefined ? `${Math.round(pct)} %` : undefined, active && j.print?.remaining_minutes ? `reste ${fmtMinutes(j.print.remaining_minutes)}` : undefined, !active ? fmtMinutes(dur) : undefined],
      };
    });
    const counts = Object.entries(r.counts ?? {}).filter(([, n]) => typeof n === 'number');
    return (
      <LiteCard title="Impressions" sub={`${VIEW_FR[r.view] ?? r.view} · ${rows.length} impression(s)`} label="Historique des impressions" aside={counts.slice(0, 4).map(([k, n]) => <LiteBadge key={k}>{VIEW_FR[k] ?? k} {n}</LiteBadge>)}>
        {rows.length === 0 ? <LiteState kind="empty">Aucune impression dans cette vue.</LiteState> : <Rows rows={rows} busy={busy} disabled={dis} open={open} label="Impressions" />}
        {error && <LiteState kind="error">{error}</LiteState>}
        <p className="lc-note">Choisir une impression ouvre son suivi.</p>
      </LiteCard>
    );
  }

  if (data.kind === 'queue') {
    const rows: Row[] = data.queue.map((q) => ({
      id: q.print_job_id, name: q.file_name || q.print_job_id, tone: toneOfState(q.state), label: QUEUE_FR[q.state] ?? q.state,
      meta: [`Rang ${q.position}`, q.printer_id, q.message ?? ''], end: [fmtMinutes(q.estimated_minutes), fmtGrams(q.estimated_grams)], note: q.note,
    }));
    return (
      <LiteCard title="File d'attente" sub={`${rows.length} élément(s)`} label="File d'attente d'impressions">
        {rows.length === 0 ? <LiteState kind="empty">La file est vide.</LiteState> : <Rows rows={rows} busy={busy} disabled={dis} open={open} label="File d'attente" />}
        {error && <LiteState kind="error">{error}</LiteState>}
        <p className="lc-note">Une impression « prête » ne démarre qu'après l'accord humain à la page de confirmation.</p>
      </LiteCard>
    );
  }

  if (data.kind === 'logs') {
    const rows = data.entries.map(logRow);
    return (
      <LiteCard title="Journal des impressions" sub={`${rows.length} sur ${data.total}`} label="Journal des impressions">
        {rows.length === 0 ? <LiteState kind="empty">Aucune fiche dans le journal.</LiteState> : <Rows rows={rows} busy={busy} disabled={dis} open={open} label="Fiches du journal" />}
        {error && <LiteState kind="error">{error}</LiteState>}
      </LiteCard>
    );
  }

  const e = data.entry;
  const cost = e.actual_cost ?? e.planned_cost;
  const dEst = e.estimated?.seconds, dAct = e.actual?.seconds;
  const s = e.settings ?? {};
  const delta = (v?: number) => (v === undefined ? undefined : `${v > 0 ? '+' : ''}${Math.round(v)} % vs estimé`);
  return (
    <LiteCard title={e.name || e.model_name || e.print_job_id} sub={[e.printer_model, fmtDate(e.finished_at)].filter(Boolean).join(' · ')} label="Fiche d'une impression"
      aside={<LiteBadge tone={OUTCOME_TONE[e.outcome] ?? 'off'}>{OUTCOME_FR[e.outcome] ?? e.outcome}</LiteBadge>}>
      <LiteStats>
        <LiteStat label="Durée" value={fmtSeconds(dAct ?? dEst) ?? '—'} hint={dAct !== undefined && dEst !== undefined ? `estimée ${fmtSeconds(dEst)}${e.duration_delta_pct !== undefined ? ` · ${delta(e.duration_delta_pct)}` : ''}` : dEst !== undefined ? 'estimée' : undefined} />
        <LiteStat label="Matière" value={fmtGrams(e.actual?.grams ?? e.estimated?.grams) ?? '—'} hint={e.mass_delta_pct !== undefined ? delta(e.mass_delta_pct) : undefined} />
        <LiteStat label={e.actual_cost ? 'Coût réel estimé' : 'Coût estimé'} value={fmtMoney(cost?.total, cost?.currency) ?? '—'} />
      </LiteStats>
      {(e.rating || e.comment) && <p className="lc-note" style={{ color: 'var(--c-ink2)', fontSize: 13 }}>{e.rating ? <Stars n={e.rating} /> : null}{e.comment ? ` « ${e.comment} »` : ''}</p>}
      {(e.defects?.length ?? 0) > 0 && <div className="lc-chips" aria-label="Défauts">{e.defects!.map((d) => <LiteBadge key={d} tone="warn">{d}</LiteBadge>)}</div>}
      <p className="lc-note">{[s.process, s.filaments?.join(' + '), s.nozzle_mm ? `buse ${s.nozzle_mm} mm` : '', s.layers ? `${s.layers} couches` : '', s.supports ? 'avec supports' : ''].filter(Boolean).join(' · ')}</p>
      <div><LiteButton busy={busy === e.print_job_id} disabled={dis} onClick={() => open(e.print_job_id)}>Ouvrir le suivi</LiteButton></div>
      {error && <LiteState kind="error">{error}</LiteState>}
    </LiteCard>
  );
}

