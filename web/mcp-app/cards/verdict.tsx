import { useState } from 'react';
import { useToolCall, type CardApp, type CardProps } from './call';
import { fmtDate, isRec, num, str } from './format';
import { LiteBadge, LiteButton, LiteCard, LiteState, type Tone } from './lite';

/**
 * Carte Verdict (ui://bambu/verdict.html), liée à get_pending_verdicts et set_print_verdict : à la fin d'une impression,
 * l'humain tape « Réussi », « Raté » (avec la raison) ou « À refaire ». Les gestes passent par l'outil réservé à la carte
 * submit_print_verdict. Un verdict n'a AUCUN effet sur l'imprimante, la file ni la confirmation : aucun bouton de lancement.
 * Le même formulaire sert de bandeau de fin d'impression dans la carte de suivi (VerdictBanner).
 */
export interface FailReason { id: string; label: string }
export interface PendingRow { print_job_id: string; name?: string; model_name?: string; state: string; reached_100?: boolean; material_family?: string; finished_at?: string }
export interface VerdictResult {
  print_job_id: string; verdict: 'success' | 'failed' | 'redo'; fail_reason?: string; verdict_source?: string; verdict_at?: string; previous_verdict?: string;
  changed: boolean; warnings?: string[]; note: string; fail_reasons: FailReason[];
}
export type VerdictData =
  | { kind: 'pending'; pending: PendingRow[]; total: number; reasons: FailReason[]; note?: string }
  | { kind: 'result'; result: VerdictResult };

const VERDICTS = ['success', 'failed', 'redo'];

export function normalizeVerdict(s: unknown): VerdictData | undefined {
  if (!isRec(s) || !Array.isArray(s.fail_reasons)) return undefined;
  const reasons = s.fail_reasons.filter((r): r is FailReason => isRec(r) && typeof r.id === 'string' && typeof r.label === 'string');
  if (Array.isArray(s.pending) && s.pending.every((p) => isRec(p) && typeof p.print_job_id === 'string' && typeof p.state === 'string')) {
    return { kind: 'pending', pending: s.pending as PendingRow[], total: num(s.total) ?? s.pending.length, reasons, note: str(s.note) };
  }
  if (typeof s.print_job_id === 'string' && VERDICTS.includes(String(s.verdict)) && typeof s.changed === 'boolean') return { kind: 'result', result: s as unknown as VerdictResult };
  return undefined;
}

export const VERDICT_FR: Record<string, string> = { success: 'Réussie', failed: 'Ratée', redo: 'À refaire' };
const VERDICT_TONE: Record<string, Tone> = { success: 'ok', failed: 'fail', redo: 'warn' };
export const REASON_FR: Record<string, string> = {
  stringing: 'Fils et toiles', warping: 'Gauchissement', adhesion: 'Adhérence', spaghetti: 'Spaghetti', layer_shift: 'Décalage de couches',
  under_extrusion: 'Sous-extrusion', nozzle_clog: 'Buse bouchée', other: 'Autre',
};
const DEFAULT_REASONS: FailReason[] = Object.entries(REASON_FR).map(([id, label]) => ({ id, label }));

const resultOf = (r: unknown): VerdictResult | undefined => {
  const n = normalizeVerdict(isRec(r) ? r.structuredContent : undefined);
  return n?.kind === 'result' ? n.result : undefined;
};

/** Les trois gestes. « Raté » ouvre le choix de la raison ; « À refaire » accepte une raison facultative. */
export function VerdictForm({ app, jobId, reasons, current, onDone, compact }: {
  app: CardApp; jobId: string; reasons?: FailReason[]; current?: string; onDone?: (r: VerdictResult) => void; compact?: boolean;
}) {
  const { busy, error, run } = useToolCall(app);
  const [ask, setAsk] = useState<'failed' | 'redo' | null>(null);
  const [reason, setReason] = useState('');
  const [note, setNote] = useState('');
  const list = reasons && reasons.length ? reasons : DEFAULT_REASONS;
  const send = async (verdict: string, why?: string) => {
    const r = await run(verdict, 'submit_print_verdict', { print_job_id: jobId, verdict, ...(why ? { reason: why } : {}), ...(note.trim() ? { note: note.trim() } : {}) });
    const res = resultOf(r);
    if (res) { setAsk(null); setReason(''); setNote(''); onDone?.(res); }
  };
  const dis = busy !== null;
  return (
    <div className="verdict-form" role="group" aria-label={`Verdict de l'impression ${jobId}`} style={{ display: 'grid', gap: 8 }}>
      <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap' }}>
        <LiteButton kind="primary" busy={busy === 'success'} disabled={dis} pressed={current === 'success'} onClick={() => { setAsk(null); void send('success'); }}>Réussi</LiteButton>
        <LiteButton busy={busy === 'failed'} disabled={dis} pressed={current === 'failed' || ask === 'failed'} onClick={() => setAsk('failed')}>Raté</LiteButton>
        <LiteButton busy={busy === 'redo'} disabled={dis} pressed={current === 'redo' || ask === 'redo'} onClick={() => setAsk('redo')}>À refaire</LiteButton>
      </div>
      {ask && (
        <div style={{ display: 'grid', gap: 6 }}>
          <label className="lc-note" style={{ display: 'grid', gap: 4 }}>
            {ask === 'failed' ? 'Pourquoi a-t-elle raté ?' : 'Pourquoi la refaire ? (facultatif)'}
            <select aria-label="Raison" value={reason} onChange={(e) => setReason(e.target.value)} disabled={dis}>
              <option value="">{ask === 'failed' ? 'Raison non précisée (autre)' : 'Aucune raison'}</option>
              {list.map((r) => <option key={r.id} value={r.id}>{REASON_FR[r.id] ?? r.label}</option>)}
            </select>
          </label>
          {!compact && (
            <label className="lc-note" style={{ display: 'grid', gap: 4 }}>
              Note (facultative, jamais de secret)
              <input aria-label="Note" type="text" maxLength={2000} value={note} onChange={(e) => setNote(e.target.value)} disabled={dis} />
            </label>
          )}
          <div style={{ display: 'flex', gap: 8 }}>
            <LiteButton kind="primary" busy={busy === ask} disabled={dis} onClick={() => void send(ask, reason || undefined)}>Enregistrer le verdict « {VERDICT_FR[ask].toLowerCase()} »</LiteButton>
            <LiteButton disabled={dis} onClick={() => setAsk(null)}>Annuler la saisie</LiteButton>
          </div>
        </div>
      )}
      {error && <LiteState kind="error">{error}</LiteState>}
    </div>
  );
}

function ResultLine({ r }: { r: VerdictResult }) {
  return (
    <p className="lc-note" role="status">
      <LiteBadge tone={VERDICT_TONE[r.verdict]}>{VERDICT_FR[r.verdict]}</LiteBadge>{' '}
      {r.fail_reason ? `${REASON_FR[r.fail_reason] ?? r.fail_reason} · ` : ''}{r.note}{(r.warnings ?? []).map((w) => ` ${w}`).join('')}
    </p>
  );
}

export function VerdictCard({ app, data }: CardProps<VerdictData>) {
  const [done, setDone] = useState<Record<string, VerdictResult>>({});
  const [editing, setEditing] = useState<string | null>(null);
  if (data.kind === 'result') {
    const r = done[data.result.print_job_id] ?? data.result;
    return (
      <LiteCard title="Verdict d'impression" sub={r.print_job_id} label="Verdict d'une impression" aside={<LiteBadge tone={VERDICT_TONE[r.verdict]}>{VERDICT_FR[r.verdict]}</LiteBadge>}>
        <ResultLine r={r} />
        <p className="lc-note">Le verdict est modifiable ensuite ; il n'a aucun effet sur l'imprimante, la file ni la confirmation.</p>
        <VerdictForm app={app} jobId={r.print_job_id} reasons={r.fail_reasons} current={r.verdict} onDone={(x) => setDone((d) => ({ ...d, [x.print_job_id]: x }))} />
      </LiteCard>
    );
  }
  const left = data.pending.filter((p) => !done[p.print_job_id]);
  return (
    <LiteCard title="Impressions à juger" sub={data.note ?? `${data.total} impression(s) sans verdict`} label="Impressions terminées sans verdict">
      {data.pending.length === 0
        ? <LiteState kind="empty">Aucune impression terminée n'attend de verdict.</LiteState>
        : (
          <ul className="lc-list" aria-label="Impressions sans verdict">
            {data.pending.map((p) => {
              const r = done[p.print_job_id];
              const label = p.model_name || p.name || p.print_job_id;
              return (
                <li key={p.print_job_id} className="lc-row" aria-label={`Impression ${label}`} style={{ display: 'grid', gap: 8 }}>
                  <span className="lc-row-main">
                    <span className="lc-row-name">{label}</span>
                    <span className="lc-row-meta">{[p.state === 'COMPLETED' ? 'Terminée' : 'Échec', p.material_family, fmtDate(p.finished_at)].filter(Boolean).join(' · ')}</span>
                  </span>
                  {r && editing !== p.print_job_id
                    ? <span><ResultLine r={r} /><LiteButton onClick={() => setEditing(p.print_job_id)}>Modifier</LiteButton></span>
                    : <VerdictForm app={app} jobId={p.print_job_id} reasons={data.reasons} current={r?.verdict} compact onDone={(x) => { setDone((d) => ({ ...d, [x.print_job_id]: x })); setEditing(null); }} />}
                </li>
              );
            })}
          </ul>
        )}
      {data.pending.length > 0 && <p className="lc-note">{left.length === 0 ? 'Tout est jugé.' : `${left.length} restante(s).`} Un verdict n'a aucun effet sur l'imprimante, la file ni la confirmation.</p>}
    </LiteCard>
  );
}

/** Bandeau de fin d'impression de la carte de suivi : trois gestes, rien d'autre. */
export function VerdictBanner({ app, jobId }: { app: CardApp; jobId: string }) {
  const [res, setRes] = useState<VerdictResult>();
  const [editing, setEditing] = useState(false);
  return (
    <div className="live-verdict" role="region" aria-label="Verdict de l'impression" style={{ display: 'grid', gap: 8, marginTop: 8 }}>
      <h2 style={{ margin: 0, fontSize: 14 }}>Comment s'est passée l'impression ?</h2>
      {res && !editing
        ? <div><ResultLine r={res} /><LiteButton onClick={() => setEditing(true)}>Modifier le verdict</LiteButton></div>
        : <VerdictForm app={app} jobId={jobId} current={res?.verdict} compact onDone={(r) => { setRes(r); setEditing(false); }} />}
    </div>
  );
}
