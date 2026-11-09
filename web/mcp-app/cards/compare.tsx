import { useState } from 'react';
import { AlertTriangle, Check, Star } from 'lucide-react';
import type { ApplyOutput, CompareCandidate, CompareOutput } from '../../src/api/goalTypes';
import { useToolCall, type CardProps } from './call';
import { fmtGrams, fmtMoney, fmtSeconds, isRec, num, str, type Rec } from './format';
import { LiteBadge, LiteButton, LiteCard, LiteGauge, LiteState, LiteTable, type Tone } from './lite';

/**
 * Carte Comparaison : compare_variants. Tableau des variantes, ligne recommandée mise en avant, « Choisir cette variante » =
 * apply_variant (fixe le profil du modèle ; ne lance AUCUNE impression).
 * « Recommandée » = devant sur le plus de critères mesurés (classements du serveur), hors variante qui viole un seuil non négociable
 * ou dont la découpe n'a pas abouti : ce n'est pas un score global (le serveur n'en désigne aucun).
 */
export function normalizeCompare(s: unknown): CompareOutput | undefined {
  if (!isRec(s) || typeof s.experiment_id !== 'string' || !Array.isArray(s.candidates) || !Array.isArray(s.rankings)) return undefined;
  return s as unknown as CompareOutput;
}

const metric = (c: CompareCandidate, test: (k: string) => boolean): { value: number; unit: string } | undefined => {
  for (const [k, m] of Object.entries(c.metrics ?? {})) if (test(k) && isRec(m) && num(m.value) !== undefined) return { value: m.value, unit: str(m.unit) ?? '' };
  return undefined;
};
const kTime = (k: string) => k === 'print_time_s';
const kGrams = (k: string) => k === 'filament_g';
const kOver = (k: string) => k === 'unsupported_overhang_mm2';
const kCost = (k: string) => /(^|_)(cost|cout|price)/i.test(k);
const kTrans = (k: string) => /translu|transpar/i.test(k);
const kRigid = (k: string) => /rigid|stiff|raideur/i.test(k);
const toPct = (m: { value: number; unit: string }) => (m.unit === '%' || m.value > 1 ? m.value : m.value * 100);

/** Variante recommandée : la plus souvent première des classements (égalités exclues), sans violation ni échec de découpe. */
export function recommended(d: CompareOutput): string | undefined {
  const ok = new Set(d.candidates.filter((c) => c.state === 'succeeded' && !c.violates_non_negotiable).map((c) => c.id));
  const firsts = new Map<string, number>();
  for (const r of d.rankings) {
    if (r.all_tied || !r.order?.length) continue;
    const top = r.order.find((o) => ok.has(o.candidate_id));
    if (top) firsts.set(top.candidate_id, (firsts.get(top.candidate_id) ?? 0) + 1);
  }
  let best: string | undefined; let n = 0; let tie = false;
  for (const [id, c] of firsts) { if (c > n) { best = id; n = c; tie = false; } else if (c === n) tie = true; }
  return tie ? undefined : best;
}
const firstsOf = (d: CompareOutput, id: string) => d.rankings.filter((r) => !r.all_tied && r.order?.[0]?.candidate_id === id).length;

const warningsOf = (c: CompareCandidate): string[] => {
  const w: string[] = [];
  const extra = (c as unknown as Rec).warnings;
  if (Array.isArray(extra)) for (const x of extra) if (typeof x === 'string') w.push(x);
  if (c.error) w.push(c.error);
  if (c.violates_non_negotiable) w.push('Viole un seuil non négociable.');
  for (const t of c.threshold_checks ?? []) if (!t.pass) w.push(`${t.label} : ${+t.value.toFixed(2)} ${t.op} ${t.limit} non respecté.`);
  return w;
};
const STATE: Record<string, [string, Tone]> = { succeeded: ['Découpée', 'ok'], failed: ['Échec de découpe', 'fail'], cancelled: ['Annulée', 'fail'], pending: ['En attente', 'warn'], queued: ['En file', 'warn'], running: ['En cours', 'run'] };

export function CompareCard({ app, data }: CardProps<CompareOutput>) {
  const [cur, setCur] = useState<CompareOutput>(data);
  const [applied, setApplied] = useState<ApplyOutput>();
  const { busy, error, run } = useToolCall(app);
  const cands = cur.candidates;
  const best = recommended(cur);
  const has = (t: (k: string) => boolean) => cands.some((c) => metric(c, t));
  const showCost = has(kCost), showTrans = has(kTrans), showRigid = has(kRigid);
  const money = (c: CompareCandidate) => { const m = metric(c, kCost); return m ? fmtMoney(m.value, m.unit === '$' ? 'USD' : 'EUR') : undefined; };

  const choose = async (c: CompareCandidate) => {
    const r = await run(`a:${c.id}`, 'apply_variant', { experiment_id: cur.experiment_id, candidate_id: c.id, expected_revision: cur.model_version_id });
    const sc = isRec(r) ? r.structuredContent : undefined;
    if (isRec(sc) && typeof sc.profile_revision === 'string') setApplied(sc as unknown as ApplyOutput);
  };
  const refresh = async () => {
    const r = await run('refresh', 'compare_variants', { experiment_id: cur.experiment_id });
    const n = normalizeCompare(isRec(r) ? r.structuredContent : undefined);
    if (n) setCur(n);
  };

  if (cands.length === 0) return <LiteCard title="Comparaison des variantes" label="Comparaison"><LiteState kind="empty">Aucune variante à comparer pour cette expérience.</LiteState></LiteCard>;

  return (
    <LiteCard title="Comparaison des variantes" sub={`Expérience ${cur.experiment_id} · ${cands.length} candidat(s)`} label="Comparaison des variantes"
      aside={!cur.complete ? <LiteBadge tone="warn">{(cur.pending?.length ?? 0)} découpe(s) en attente</LiteBadge> : undefined}>
      <LiteTable label="Variantes">
        <thead><tr>
          <th>Variante</th><th className="lc-num">Temps</th><th className="lc-num">Matière</th>
          {showCost && <th className="lc-num">Coût</th>}<th className="lc-num">Surplomb non supporté</th>
          {showTrans && <th>Translucidité</th>}{showRigid && <th>Rigidité</th>}
        </tr></thead>
        <tbody>
          {cands.map((c) => {
            const t = metric(c, kTime), g = metric(c, kGrams), o = metric(c, kOver), tr = metric(c, kTrans), rg = metric(c, kRigid);
            const warns = warningsOf(c);
            const [stLabel, stTone] = STATE[c.state] ?? [c.state, 'off' as Tone];
            const isBest = c.id === best;
            return (
              <tr key={c.id} className={isBest ? 'lc-hl' : undefined} aria-current={isBest ? 'true' : undefined}>
                <td>
                  <div className="lc-row-name">{c.label}</div>
                  <div className="lc-tags" style={{ marginTop: 4 }}>
                    {isBest && <LiteBadge tone="accent" title={`Devant sur ${firstsOf(cur, c.id)} critère(s) mesuré(s) ; pas un score global`}><Star size={11} aria-hidden /> Recommandée</LiteBadge>}
                    {c.kind === 'reference' && <LiteBadge>Référence</LiteBadge>}
                    <LiteBadge tone={stTone}>{stLabel}</LiteBadge>
                  </div>
                  {warns.length > 0 && <ul className="lc-warns" style={{ marginTop: 6 }}>{warns.map((w, i) => <li key={i}><AlertTriangle size={13} aria-hidden /> <span>{w}</span></li>)}</ul>}
                  <div style={{ marginTop: 8 }}>
                  <LiteButton className="lc-wide" kind={isBest ? 'primary' : 'ghost'} busy={busy === `a:${c.id}`} disabled={busy !== null || c.state !== 'succeeded' || applied?.candidate_id === c.id}
                    onClick={() => choose(c)} title={c.state !== 'succeeded' ? 'Cette variante n\'a pas été découpée avec succès' : 'Fixe cette variante comme profil du modèle (n\'imprime rien)'}>
                    {applied?.candidate_id === c.id ? <><Check size={14} aria-hidden /> Choisie</> : 'Choisir cette variante'}
                  </LiteButton>
                  </div>
                </td>
                <td className="lc-num">{t ? fmtSeconds(t.value) : '—'}</td>
                <td className="lc-num">{g ? fmtGrams(g.value) : '—'}</td>
                {showCost && <td className="lc-num">{money(c) ?? '—'}</td>}
                <td className="lc-num">{o ? (o.value === 0 ? 'aucun' : `${+o.value.toFixed(1)} mm²`.replace('.', ',')) : '—'}</td>
                {showTrans && <td style={{ minWidth: 80 }}>{tr ? <><LiteGauge value={toPct(tr)} label={`Translucidité de ${c.label}`} /><span className="lc-note">{Math.round(toPct(tr))} %</span></> : '—'}</td>}
                {showRigid && <td style={{ minWidth: 80 }}>{rg ? <><LiteGauge value={toPct(rg)} label={`Rigidité de ${c.label}`} /><span className="lc-note">{Math.round(toPct(rg))} %</span></> : '—'}</td>}
              </tr>
            );
          })}
        </tbody>
      </LiteTable>
      {applied && <LiteState kind="empty">Variante « {applied.label} » appliquée au modèle (révision de profil {applied.profile_revision}). Aucune impression lancée.</LiteState>}
      {error && <LiteState kind="error">{error}</LiteState>}
      {!cur.complete && <div><LiteButton busy={busy === 'refresh'} disabled={busy !== null} onClick={refresh}>Actualiser les mesures</LiteButton></div>}
      {cur.rankings.length > 0 && <p className="lc-note">Classements : {cur.rankings.map((r) => `${r.label} (${r.better === 'lower' ? 'plus bas' : 'plus haut'} = mieux)`).join(' · ')}</p>}
      <p className="lc-note">Estimations du slicer et heuristiques géométriques ; la recommandation compte les critères où la variante est première, sans score global.</p>
    </LiteCard>
  );
}
