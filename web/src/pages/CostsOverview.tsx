import { useEffect, useMemo, useState } from 'react';
import { Button, Field, Input, useToast } from '../components';
import { describeError } from '../api/client';
import { useAsync } from '../api/hooks';
import { insightsApi } from '../api/insights';
import type { CostSettings, DashRow } from '../api/insights';
import { Async, fmtNum } from './common';

const sym = (c?: string) => (!c || c === 'EUR' ? '€' : c);
const monthKey = (d: Date) => `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}`;
const iso = (d: Date) => `${monthKey(d)}-${String(d.getDate()).padStart(2, '0')}`;
const monthLabel = (key: string, fallback: string) => {
  const m = /^(\d{4})-(\d{2})/.exec(key);
  if (!m) return fallback;
  const l = new Date(Number(m[1]), Number(m[2]) - 1, 1).toLocaleDateString('fr-FR', { month: 'short' });
  return l.charAt(0).toUpperCase() + l.slice(1);
};

/** Dépenses par mois : barres SVG, valeur lisible au-dessus de chaque barre et titre au survol. */
export function SpendChart({ rows, currency }: { rows: DashRow[]; currency?: string }) {
  const W = 560, H = 240, pl = 34, pb = 26, pt = 18;
  const max = Math.max(10, ...rows.map((r) => r.cost));
  const top = Math.ceil(max / 20) * 20 || 20;
  const ticks = [0, 1, 2, 3].map((i) => Math.round((top / 3) * i));
  const n = Math.max(rows.length, 1);
  const step = (W - pl) / n, bw = Math.min(44, step * 0.7);
  return (
    <svg className="chart" viewBox={`0 0 ${W} ${H}`} role="img" aria-label={`Dépenses par mois en ${currency ?? 'euros'} : ${rows.map((r) => `${monthLabel(r.key, r.name)} ${fmtNum(r.cost, 0)}`).join(', ')}`}>
      {ticks.map((v) => { const y = pt + (H - pt - pb) * (1 - v / top); return <g key={v}><line className="g" x1={pl} x2={W} y1={y} y2={y} /><text x={0} y={y + 4}>{v}</text></g>; })}
      {rows.map((r, i) => {
        const h = (H - pt - pb) * (r.cost / top), x = pl + i * step + (step - bw) / 2, y = H - pb - h;
        return (
          <g key={r.key}>
            <rect className={`b${i === rows.length - 1 ? ' cur' : ''}`} x={x} y={y} width={bw} height={Math.max(h, 1)} rx={7}><title>{`${monthLabel(r.key, r.name)} : ${fmtNum(r.cost, 2)} ${sym(currency)}`}</title></rect>
            <text x={x + bw / 2} y={H - 8} textAnchor="middle">{monthLabel(r.key, r.name)}</text>
            <text x={x + bw / 2} y={y - 6} textAnchor="middle" className="chart-val">{Math.round(r.cost)}</text>
          </g>
        );
      })}
    </svg>
  );
}

function QuickTariffs({ st, reload }: { st: CostSettings; reload: () => void }) {
  const toast = useToast();
  const [kwh, setKwh] = useState(String(st.electricity_per_kwh));
  const [petg, setPetg] = useState(String(st.filament_per_kg.PETG ?? ''));
  const [pla, setPla] = useState(String(st.filament_per_kg.PLA ?? ''));
  const [wear, setWear] = useState(String(st.machine_wear_per_hour));
  const [busy, setBusy] = useState(false);
  useEffect(() => { setKwh(String(st.electricity_per_kwh)); setPetg(String(st.filament_per_kg.PETG ?? '')); setPla(String(st.filament_per_kg.PLA ?? '')); setWear(String(st.machine_wear_per_hour)); }, [st]);
  const n = (s: string) => (s.trim() === '' ? NaN : Number(s.replace(',', '.')));
  const save = async () => {
    setBusy(true);
    try {
      const k = n(kwh), w = n(wear);
      if (![k, w].every(Number.isFinite)) throw new Error('Prix du kWh et usure : nombres attendus.');
      const filament_per_kg: Record<string, number> = {};
      if (Number.isFinite(n(petg)) && n(petg) !== st.filament_per_kg.PETG) filament_per_kg.PETG = n(petg);
      if (Number.isFinite(n(pla)) && n(pla) !== st.filament_per_kg.PLA) filament_per_kg.PLA = n(pla);
      await insightsApi.saveSettings({ electricity_per_kwh: k, machine_wear_per_hour: w, filament_per_kg });
      toast('ok', 'Tarifs enregistrés.'); reload();
    } catch (e) { toast('err', describeError(e)); } finally { setBusy(false); }
  };
  const c = sym(st.currency);
  return (
    <section className="panel" aria-label="Réglages des tarifs">
      <h3 className="panel-title">Réglages</h3>
      <div className="settings">
        <Field label={`Électricité, prix du kWh (${c})`}><Input className="num" inputMode="decimal" value={kwh} onChange={(e) => setKwh(e.target.value)} /></Field>
        <Field label={`PETG, prix au kg (${c})`}><Input className="num" inputMode="decimal" value={petg} onChange={(e) => setPetg(e.target.value)} /></Field>
        <Field label={`PLA, prix au kg (${c})`}><Input className="num" inputMode="decimal" value={pla} onChange={(e) => setPla(e.target.value)} /></Field>
        <Field label={`Usure machine par heure (${c})`}><Input className="num" inputMode="decimal" value={wear} onChange={(e) => setWear(e.target.value)} /></Field>
      </div>
      {(st.defaulted_prices?.length ?? 0) > 0 && <p className="sub">Prix encore par défaut (indicatifs) : {st.defaulted_prices!.join(', ')}.</p>}
      <div className="actions"><Button variant="secondary" disabled={busy} onClick={save}>Enregistrer</Button></div>
      <p className="sub">Les autres matières et les puissances se règlent dans l'onglet « Prix et coût ».</p>
    </section>
  );
}

/** Vue d'ensemble (maquette « Coûts ») : mois en cours, répartition matière / électricité, histogramme des 6 derniers mois, tarifs rapides. */
export function CostsOverview() {
  const range = useMemo(() => { const to = new Date(); const from = new Date(to.getFullYear(), to.getMonth() - 5, 1); return { from: iso(from), to: iso(to), period: 'month' }; }, []);
  const dash = useAsync((s) => insightsApi.dashboard(range, s), [range.from]);
  const log = useAsync((s) => insightsApi.log({ limit: 200 }, s), []);
  const settings = useAsync((s) => insightsApi.settings(s), []);
  const cur = monthKey(new Date()), prev = monthKey(new Date(new Date().getFullYear(), new Date().getMonth() - 1, 1));
  const split = useMemo(() => {
    let filament = 0, electricity = 0, wear = 0, grams = 0, kwh = 0, n = 0;
    for (const e of log.data?.entries ?? []) {
      if (!e.finished_at.startsWith(cur)) continue;
      const c = e.actual_cost ?? e.planned_cost; if (!c) continue;
      filament += c.filament_total; electricity += c.electricity_total; wear += c.wear_total; grams += c.grams_total; kwh += c.electricity.kwh; n++;
    }
    return { filament, electricity, wear, grams, kwh, n };
  }, [log.data, cur]);
  const rows = dash.data?.by_period ?? [];
  const thisM = rows.find((r) => r.key.startsWith(cur));
  const lastM = rows.find((r) => r.key.startsWith(prev));
  const currency = dash.data?.currency;
  const delta = thisM && lastM ? thisM.cost - lastM.cost : undefined;
  return (
    <>
      <div className="stats">
        <div className="panel stat"><div className="overline">Ce mois</div><div className="v">{fmtNum(thisM?.cost ?? 0, 2)}<small>{sym(currency)}</small></div><div className="d">{delta === undefined ? 'estimation à partir de vos tarifs' : `${delta >= 0 ? '+' : '−'} ${fmtNum(Math.abs(delta), 2)} ${sym(currency)} par rapport au mois précédent`}</div></div>
        <div className="panel stat"><div className="overline">Matière</div><div className="v">{fmtNum(split.filament, 2)}<small>{sym(currency)}</small></div><div className="d">{fmtNum(split.grams / 1000, 2)} kg</div></div>
        <div className="panel stat"><div className="overline">Électricité</div><div className="v">{fmtNum(split.electricity, 2)}<small>{sym(currency)}</small></div><div className="d">{fmtNum(split.kwh, 0)} kWh</div></div>
        <div className="panel stat"><div className="overline">Coût moyen</div><div className="v">{thisM && thisM.prints > 0 ? fmtNum(thisM.cost / thisM.prints, 2) : '—'}<small>{sym(currency)}</small></div><div className="d">par impression{split.wear > 0 ? ` · usure ${fmtNum(split.wear, 2)} ${sym(currency)}` : ''}</div></div>
      </div>
      <div className="grid costgrid">
        <section className="panel" aria-label="Dépenses par mois">
          <div className="panel-h"><h3>Dépenses par mois</h3><span className="sub">en {currency === 'EUR' || !currency ? 'euros' : currency}</span></div>
          <Async state={dash} label="Calcul des dépenses" empty={(d) => d.by_period.length === 0 ? <p className="sub">Aucune impression sur cette période.</p> : null}>
            {(d) => <SpendChart rows={d.by_period} currency={d.currency} />}
          </Async>
        </section>
        <Async state={settings} label="Lecture des tarifs">{(st) => <QuickTariffs st={st} reload={settings.reload} />}</Async>
      </div>
    </>
  );
}
