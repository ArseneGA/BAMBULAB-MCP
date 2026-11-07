import { useEffect, useState } from 'react';
import { Button, Field, Input, Panel, Select, Toggle, useToast } from '../components';
import { describeError } from '../api/client';
import { useAsync } from '../api/hooks';
import { insightsApi } from '../api/insights';
import type { CostEstimate, CostSettings } from '../api/insights';
import { Async, Notice, fmtNum } from './common';

const MODELS = [
  { value: 'p1s', label: 'P1S' }, { value: 'p1p', label: 'P1P' }, { value: 'x1c', label: 'X1 Carbon' }, { value: 'x1e', label: 'X1E' }, { value: 'a1', label: 'A1' },
  { value: 'a1mini', label: 'A1 mini' }, { value: 'h2d', label: 'H2D' }, { value: 'h2s', label: 'H2S' }, { value: 'p2s', label: 'P2S' },
];
const FAMILIES = ['PLA', 'PLA-CF', 'PETG', 'PETG-CF', 'ABS', 'ASA', 'TPU', 'PC', 'PA', 'PA-CF', 'PVA', 'other'];
const num = (s: string) => (s.trim() === '' ? NaN : Number(s.replace(',', '.')));

function Settings({ st, reload }: { st: CostSettings; reload: () => void }) {
  const toast = useToast();
  const [cur, setCur] = useState(st.currency);
  const [kwh, setKwh] = useState(String(st.electricity_per_kwh));
  const [prices, setPrices] = useState<Record<string, string>>({});
  const [wear, setWear] = useState(String(st.machine_wear_per_hour));
  const [nozzle, setNozzle] = useState(String(st.nozzle_wear_per_hour));
  const [pm, setPm] = useState('p1s');
  const [pw, setPw] = useState({ pre: '', run: '' });
  const [hist, setHist] = useState(false);
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    setCur(st.currency); setKwh(String(st.electricity_per_kwh)); setWear(String(st.machine_wear_per_hour)); setNozzle(String(st.nozzle_wear_per_hour));
    setPrices(Object.fromEntries(FAMILIES.map((f) => [f, String(st.filament_per_kg[f] ?? '')])));
  }, [st]);
  const dflt = (k: string) => st.defaulted_prices?.includes(k);
  const save = async () => {
    setBusy(true);
    try {
      const filament_per_kg: Record<string, number> = {};
      for (const f of FAMILIES) {
        const v = num(prices[f] ?? '');
        if (Number.isFinite(v) && v !== st.filament_per_kg[f]) filament_per_kg[f] = v;
      }
      const power_overrides = Number.isFinite(num(pw.pre)) || Number.isFinite(num(pw.run))
        ? { [pm]: { preheat_w: Number.isFinite(num(pw.pre)) ? num(pw.pre) : 0, print_w: Number.isFinite(num(pw.run)) ? num(pw.run) : 0 } } : undefined;
      const k = num(kwh), w = num(wear), n = num(nozzle);
      if (![k, w, n].every(Number.isFinite)) throw new Error('Prix du kWh et usure : nombres attendus.');
      await insightsApi.saveSettings({ currency: cur, electricity_per_kwh: k, machine_wear_per_hour: w, nozzle_wear_per_hour: n, filament_per_kg, power_overrides, apply_to_history: hist });
      toast('ok', hist ? 'Prix enregistrés, historique recalculé' : 'Prix enregistrés'); reload();
    } catch (e) { toast('err', describeError(e)); } finally { setBusy(false); }
  };
  return (
    <Panel title="Prix et puissances" actions={<Button variant="primary" size="sm" onClick={save} disabled={busy}>Enregistrer</Button>}>
      <div className="stack">
        {(st.defaulted_prices?.length ?? 0) > 0 && <Notice tone="warn" title="Valeurs par défaut indicatives">{st.note} Encore par défaut : {st.defaulted_prices!.join(', ')}.</Notice>}
        <div className="row-wrap">
          <Field label="Devise (3 lettres)"><Input value={cur} onChange={(e) => setCur(e.target.value.toUpperCase())} maxLength={3} /></Field>
          <Field label={`Prix du kWh${dflt('electricity') ? ' (défaut indicatif)' : ''}`}><Input inputMode="decimal" value={kwh} onChange={(e) => setKwh(e.target.value)} /></Field>
          <Field label="Usure machine / heure"><Input inputMode="decimal" value={wear} onChange={(e) => setWear(e.target.value)} /></Field>
          <Field label="Usure buse / heure"><Input inputMode="decimal" value={nozzle} onChange={(e) => setNozzle(e.target.value)} /></Field>
        </div>
        <fieldset className="ins-prices"><legend>Prix du kg par famille de matière</legend>
          {FAMILIES.map((f) => <Field key={f} label={`${f}${dflt(f) ? ' · défaut' : ''}`}><Input inputMode="decimal" value={prices[f] ?? ''} onChange={(e) => setPrices((p) => ({ ...p, [f]: e.target.value }))} /></Field>)}
        </fieldset>
        <fieldset className="ins-prices"><legend>Puissance mesurée (prise mesurante) — remplace la valeur indicative</legend>
          <Field label="Modèle"><Select value={pm} onChange={(e) => setPm(e.target.value)} options={MODELS} /></Field>
          <Field label="Chauffe (W)"><Input inputMode="decimal" value={pw.pre} onChange={(e) => setPw({ ...pw, pre: e.target.value })} placeholder={String(st.power_overrides?.[pm]?.preheat_w ?? 'indicatif')} /></Field>
          <Field label="Impression PLA (W)"><Input inputMode="decimal" value={pw.run} onChange={(e) => setPw({ ...pw, run: e.target.value })} placeholder={String(st.power_overrides?.[pm]?.print_w ?? 'indicatif')} /></Field>
        </fieldset>
        <Toggle checked={hist} onChange={setHist} label="Recalculer aussi le coût de toutes les fiches du journal" />
        <p className="hint" style={{ margin: 0 }}>Aucun secret ici : des prix, des watts, une devise.</p>
      </div>
    </Panel>
  );
}

function Estimate({ cur }: { cur: string }) {
  const [model, setModel] = useState('p1s');
  const [mat, setMat] = useState('PLA');
  const [grams, setGrams] = useState('50');
  const [mins, setMins] = useState('120');
  const [res, setRes] = useState<CostEstimate>();
  const [err, setErr] = useState<string>();
  const go = async () => {
    setErr(undefined);
    try {
      const g = num(grams), m = num(mins);
      if (!Number.isFinite(g) || !Number.isFinite(m)) throw new Error('Masse et durée : nombres attendus.');
      setRes((await insightsApi.estimate({ printer_model: model, duration_minutes: m, filaments: [{ material: mat, grams: g }] })).estimate);
    } catch (e) { setErr(describeError(e)); setRes(undefined); }
  };
  return (
    <Panel title="Estimer un coût" actions={<Button size="sm" onClick={go}>Estimer</Button>}>
      <div className="stack">
        <div className="row-wrap">
          <Field label="Imprimante"><Select value={model} onChange={(e) => setModel(e.target.value)} options={MODELS} /></Field>
          <Field label="Matière"><Select value={mat} onChange={(e) => setMat(e.target.value)} options={FAMILIES.filter((f) => f !== 'other').map((f) => ({ value: f, label: f }))} /></Field>
          <Field label="Masse (g)"><Input inputMode="decimal" value={grams} onChange={(e) => setGrams(e.target.value)} /></Field>
          <Field label="Durée (min, chauffe comprise)"><Input inputMode="decimal" value={mins} onChange={(e) => setMins(e.target.value)} /></Field>
        </div>
        <p className="hint" style={{ margin: 0 }}>Pour une impression déjà découpée, demandez à Claude <span className="mono">estimate_cost</span> avec son identifiant : masses et durée sont lues dans l'archive.</p>
        {err && <Notice tone="err" title="Impossible">{err}</Notice>}
        {res && (
          <div className="stack">
            <strong>{res.label}</strong>
            <p style={{ margin: 0 }}>Total <strong>{fmtNum(res.total, 2)} {cur}</strong> : filament {fmtNum(res.filament_total, 2)} + électricité {fmtNum(res.electricity_total, 2)} ({fmtNum(res.electricity.kwh, 3)} kWh) + usure {fmtNum(res.wear_total, 2)} · {fmtNum(res.wear.machine_hours, 1)} h machine.</p>
            <ul className="ins-notes">{res.assumptions.map((a) => <li key={a}>{a}</li>)}</ul>
          </div>
        )}
      </div>
    </Panel>
  );
}

export function InsightsCosts() {
  const state = useAsync((s) => insightsApi.settings(s), []);
  return (
    <Async state={state} label="Lecture des prix">
      {(st) => <div className="stack"><Estimate cur={st.currency} /><Settings st={st} reload={state.reload} /></div>}
    </Async>
  );
}
