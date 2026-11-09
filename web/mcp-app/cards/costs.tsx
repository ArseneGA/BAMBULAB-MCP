import { useState } from 'react';
import type { CostEstimate, Dashboard, DashRow, EstimateOutput } from '../../src/api/insights';
import type { CardProps } from './call';
import { fmtGrams, fmtHours, fmtMinutes, fmtMoney, fmtMonth, fmtPct, isRec, num, str } from './format';
import { LiteButton, LiteCard, LiteGauge, LiteState, LiteStat, LiteStats, LiteTable } from './lite';

/** Carte Coûts : estimate_cost (ventilation matière / électricité / usure), get_project_costs (par projet), get_usage_dashboard (tuiles + barres par période). */
export type CostsData =
  | { kind: 'estimate'; out: EstimateOutput }
  | { kind: 'projects'; label: string; currency: string; projects: DashRow[] }
  | { kind: 'dashboard'; dash: Dashboard };

export function normalizeCosts(s: unknown): CostsData | undefined {
  if (!isRec(s)) return undefined;
  if (isRec(s.estimate) && typeof s.estimate.total === 'number' && Array.isArray(s.estimate.filament)) return { kind: 'estimate', out: s as unknown as EstimateOutput };
  if (isRec(s.totals) && Array.isArray(s.by_period) && Array.isArray(s.by_material)) return { kind: 'dashboard', dash: s as unknown as Dashboard };
  if (Array.isArray(s.projects) && typeof s.currency === 'string') return { kind: 'projects', label: str(s.label) ?? 'Coût par projet', currency: s.currency, projects: s.projects as DashRow[] };
  return undefined;
}

export interface Bar { label: string; value: number; title: string }

/** Graphique à barres SVG (aucune dépendance). Valeurs nulles : barre plate ; tout à zéro : axe seul. */
export function LiteBars({ bars, label }: { bars: Bar[]; label: string }) {
  const W = 320, H = 130, top = 12, bottom = 20, left = 4;
  const max = Math.max(0, ...bars.map((b) => b.value));
  const bw = bars.length ? (W - left * 2) / bars.length : 0;
  const bwi = Math.min(34, bw * 0.62);
  return (
    <svg className="lc-bars" viewBox={`0 0 ${W} ${H}`} role="img" aria-label={label}>
      <line className="lc-axis" x1={left} x2={W - left} y1={H - bottom} y2={H - bottom} />
      {bars.map((b, i) => {
        const h = max > 0 ? Math.max(b.value > 0 ? 2 : 0, (b.value / max) * (H - bottom - top)) : 0;
        const x = left + i * bw + (bw - bwi) / 2;
        return (
          <g key={`${b.label}-${i}`}>
            <rect className="lc-bar" x={x} y={H - bottom - h} width={bwi} height={h} rx={3}><title>{b.title}</title></rect>
            {bars.length <= 12 && <text x={x + bwi / 2} y={H - 6} textAnchor="middle">{b.label}</text>}
          </g>
        );
      })}
    </svg>
  );
}

const sumPct = (parts: Array<[string, number]>) => parts.reduce((a, [, v]) => a + v, 0);

function Estimate({ out }: { out: EstimateOutput }) {
  const e = out.estimate; const cur = e.currency || 'EUR';
  const tot = e.total || sumPct([['f', e.filament_total], ['e', e.electricity_total], ['w', e.wear_total]]);
  const share = (v: number) => (tot > 0 ? (v / tot) * 100 : 0);
  const line = (label: string, v: number, hint: string) => (
    <div className="lc-split-line"><span>{label}</span><LiteGauge value={share(v)} label={`${label} : ${Math.round(share(v))} % du coût`} /><b>{fmtMoney(v, cur)}<span className="lc-dim"> · {hint}</span></b></div>
  );
  const a: CostEstimate | undefined = out.actual;
  return (
    <LiteCard title="Coût d'une impression" sub={e.label || undefined} label="Estimation du coût">
      <LiteStats>
        <LiteStat label="Total estimé" value={fmtMoney(e.total, cur)} hint={a ? `réel estimé ${fmtMoney(a.total, cur)}` : undefined} />
        <LiteStat label="Matière" value={fmtGrams(e.grams_total) ?? '—'} hint={fmtMoney(e.filament_total, cur)} />
        <LiteStat label="Temps machine" value={fmtMinutes(Math.round(e.machine_seconds / 60)) ?? '—'} hint={`${fmtHours(e.wear.machine_hours)} d'usure`} />
      </LiteStats>
      <div className="lc-split" aria-label="Ventilation du coût">
        {line('Matière', e.filament_total, fmtGrams(e.grams_total) ?? '')}
        {line('Électricité', e.electricity_total, `${+e.electricity.kwh.toFixed(2)} kWh`.replace('.', ','))}
        {line('Usure', e.wear_total, `${fmtHours(e.wear.machine_hours)}`)}
      </div>
      {e.filament.length > 0 && (
        <LiteTable label="Détail du filament">
          <thead><tr><th>Bobine / matière</th><th className="lc-num">Masse</th><th className="lc-num">Prix/kg</th><th className="lc-num">Montant</th></tr></thead>
          <tbody>{e.filament.map((f, i) => <tr key={i}><td>{f.material}{f.family && f.family !== f.material ? ` (${f.family})` : ''}</td><td className="lc-num">{fmtGrams(f.grams)}</td><td className="lc-num">{fmtMoney(f.price_per_kg, cur)}</td><td className="lc-num">{fmtMoney(f.amount, cur)}</td></tr>)}</tbody>
        </LiteTable>
      )}
      {(e.assumptions?.length ?? 0) > 0 && <details><summary className="lc-note" style={{ cursor: 'pointer' }}>Hypothèses ({e.assumptions.length})</summary><ul className="lc-warns" style={{ marginTop: 6 }}>{e.assumptions.map((x, i) => <li key={i} style={{ color: 'inherit' }}>{x}</li>)}</ul></details>}
      <p className="lc-note">{out.note || 'Toujours une estimation : puissances et prix par défaut indicatifs tant que les réglages de coût ne sont pas saisis.'}</p>
    </LiteCard>
  );
}

function Projects({ d }: { d: Extract<CostsData, { kind: 'projects' }> }) {
  const total = d.projects.reduce((a, p) => a + (p.cost ?? 0), 0);
  const max = Math.max(0, ...d.projects.map((p) => p.cost ?? 0));
  if (d.projects.length === 0) return <LiteCard title="Coût par projet" sub={d.label} label="Coût par projet"><LiteState kind="empty">Aucune impression terminée sur la période.</LiteState></LiteCard>;
  return (
    <LiteCard title="Coût par projet" sub={d.label} label="Coût par projet">
      <LiteStats>
        <LiteStat label="Coût réel estimé" value={fmtMoney(total, d.currency)} hint={`${d.projects.length} projet(s)`} />
        <LiteStat label="Impressions" value={d.projects.reduce((a, p) => a + (p.prints ?? 0), 0)} />
        <LiteStat label="Matière" value={fmtGrams(d.projects.reduce((a, p) => a + (p.grams ?? 0), 0)) ?? '—'} />
      </LiteStats>
      <ul className="lc-list" aria-label="Projets">
        {d.projects.map((p) => (
          <li key={p.key} className="lc-row" style={{ gridTemplateColumns: 'minmax(0,1fr) auto' }}>
            <div className="lc-row-main">
              <span className="lc-row-name">{p.name || p.key}</span>
              <span className="lc-row-meta">{p.prints} impression(s) · {fmtHours(p.hours)} · {fmtGrams(p.grams)}{p.pending_jobs ? ` · ${p.pending_jobs} préparée(s) ≈ ${fmtMoney(p.pending_estimate ?? 0, d.currency)}` : ''}</span>
              <LiteGauge value={max > 0 ? ((p.cost ?? 0) / max) * 100 : 0} label={`Coût de ${p.name || p.key}`} />
            </div>
            <b className="lc-mono">{fmtMoney(p.cost ?? 0, d.currency)}</b>
          </li>
        ))}
      </ul>
      <p className="lc-note">Coûts estimés (électricité, usure et matière aux prix saisis ou par défaut).</p>
    </LiteCard>
  );
}

type Metric = 'cost' | 'hours' | 'grams';
function Usage({ d }: { d: Dashboard }) {
  const [metric, setMetric] = useState<Metric>('cost');
  const t = d.totals; const cur = d.currency || 'EUR';
  const val = (r: DashRow) => (metric === 'cost' ? r.cost : metric === 'hours' ? r.hours : r.grams) ?? 0;
  const fmt = (v: number) => (metric === 'cost' ? fmtMoney(v, cur) : metric === 'hours' ? fmtHours(v) : fmtGrams(v)) ?? '';
  const bars: Bar[] = d.by_period.map((r) => ({ label: d.period === 'month' ? fmtMonth(r.key) : r.key.slice(5) || r.key, value: val(r), title: `${r.name || r.key} : ${fmt(val(r))}` }));
  const noData = t.prints === 0;
  return (
    <LiteCard title="Tableau de bord d'usage" sub={d.label} label="Tableau de bord d'usage">
      {noData ? <LiteState kind="empty">Aucune impression sur cette période.</LiteState> : (
        <>
          <LiteStats>
            <LiteStat label="Impressions" value={t.prints} hint={`${t.succeeded} réussie(s)${t.failed ? ` · ${t.failed} échec(s)` : ''}`} />
            <LiteStat label="Réussite" value={fmtPct(num(t.success_rate) !== undefined ? t.success_rate! * (t.success_rate! <= 1 ? 100 : 1) : undefined) ?? '—'} tone={t.failed > 0 ? 'warn' : undefined} />
            <LiteStat label="Heures machine" value={fmtHours(t.machine_hours) ?? '—'} />
            <LiteStat label="Matière" value={fmtGrams(t.filament_grams) ?? '—'} />
            <LiteStat label="Coût estimé" value={fmtMoney(t.cost, cur)} />
          </LiteStats>
          <div className="lc-chips" role="group" aria-label="Grandeur du graphique">
            {([['cost', 'Coût'], ['hours', 'Heures'], ['grams', 'Matière']] as Array<[Metric, string]>).map(([k, l]) => <LiteButton key={k} pressed={metric === k} onClick={() => setMetric(k)}>{l}</LiteButton>)}
          </div>
          {bars.length > 0 && <LiteBars bars={bars} label={`${metric === 'cost' ? 'Coût' : metric === 'hours' ? 'Heures machine' : 'Matière'} par période`} />}
          {d.by_material.length > 0 && (
            <LiteTable label="Détail par matière">
              <thead><tr><th>Matière</th><th className="lc-num">Impressions</th><th className="lc-num">Masse</th><th className="lc-num">Coût</th></tr></thead>
              <tbody>{d.by_material.map((r) => <tr key={r.key}><td>{r.name || r.key}</td><td className="lc-num">{r.prints}</td><td className="lc-num">{fmtGrams(r.grams)}</td><td className="lc-num">{fmtMoney(r.cost, cur)}</td></tr>)}</tbody>
            </LiteTable>
          )}
          {d.by_printer.length > 1 && (
            <LiteTable label="Détail par imprimante">
              <thead><tr><th>Imprimante</th><th className="lc-num">Impressions</th><th className="lc-num">Heures</th><th className="lc-num">Coût</th></tr></thead>
              <tbody>{d.by_printer.map((r) => <tr key={r.key}><td>{r.name || r.key}</td><td className="lc-num">{r.prints}</td><td className="lc-num">{fmtHours(r.hours)}</td><td className="lc-num">{fmtMoney(r.cost, cur)}</td></tr>)}</tbody>
            </LiteTable>
          )}
        </>
      )}
      {(d.notes?.length ?? 0) > 0 && <p className="lc-note">{d.notes[0]}</p>}
      <p className="lc-note">Les coûts sont des estimations.</p>
    </LiteCard>
  );
}

export function CostsCard({ data }: CardProps<CostsData>) {
  if (data.kind === 'estimate') return <Estimate out={data.out} />;
  if (data.kind === 'projects') return <Projects d={data} />;
  return <Usage d={data.dash} />;
}
