import { useEffect, useMemo, useState } from 'react';
import { Link } from 'react-router-dom';
import { CheckCircle2, Layers, Plus, Scissors, Trash2 } from 'lucide-react';
import { Button, Field, Input, Panel, Select, Tag, Toggle } from '../components';
import { api, describeError } from '../api/client';
import { useAsync } from '../api/hooks';
import { optNum, plateApi } from '../api/plates';
import type { PieceSettings, PlatePlan, PlateSliceOutput } from '../api/plates';
import { Notice } from './common';
import './plates.css';

/**
 * Composer un plateau de plusieurs pièces : choix des pièces, quantités, réglages PAR pièce, calcul du plan
 * (rapport « tient / ne tient pas » avant toute découpe), aperçu du plateau, puis découpe — une impression par plaque,
 * chacune suivant la chaîne habituelle (validation, dépôt, accord humain sur la page de confirmation).
 */
interface Row { uid: string; modelId: string; qty: string; label: string; infill: string; walls: string; layer: string; support: boolean }
const uid = () => Math.random().toString(36).slice(2, 8);
const blank = (modelId = ''): Row => ({ uid: uid(), modelId, qty: '1', label: '', infill: '', walls: '', layer: '', support: false });

export function PlatePanel({ initialModelId }: { initialModelId?: string }) {
  const models = useAsync((s) => api.models({ limit: 200, sort: 'name' }, s), []);
  const printers = useAsync((s) => api.printers(s), []);
  const [rows, setRows] = useState<Row[]>(() => [blank(initialModelId)]);
  const [printerId, setPrinterId] = useState('');
  const [groupBy, setGroupBy] = useState(false);
  const [busy, setBusy] = useState<'plan' | 'slice' | null>(null);
  const [error, setError] = useState<string>();
  const [plan, setPlan] = useState<PlatePlan>();
  const [sliced, setSliced] = useState<PlateSliceOutput>();
  const [plate, setPlate] = useState(1);
  const [img, setImg] = useState<{ url: string; legend: string[] }>();

  useEffect(() => { if (!printerId && printers.data?.printers.length) setPrinterId(printers.data.printers[0].id); }, [printers.data, printerId]);
  const setRow = (id: string, p: Partial<Row>) => { setRows((r) => r.map((x) => (x.uid === id ? { ...x, ...p } : x))); setPlan(undefined); setSliced(undefined); };

  useEffect(() => {
    if (!plan) { setImg(undefined); return; }
    const ac = new AbortController();
    plateApi.previewPlan(plan.id, plate, ac.signal).then((o) => setImg({ url: o.url, legend: o.legend }), (e) => { if ((e as Error).name !== 'AbortError') setError(describeError(e)); });
    return () => ac.abort();
  }, [plan, plate]);

  const parsed = useMemo(() => {
    const out: Array<{ item: { model_id: string; quantity: number; label?: string; settings?: PieceSettings }; err?: string }> = [];
    for (const r of rows) {
      const q = optNum(r.qty), inf = optNum(r.infill), w = optNum(r.walls), l = optNum(r.layer);
      let err: string | undefined;
      if (!r.modelId) err = 'Choisissez une pièce.';
      else if (q === null || q === undefined || q < 1 || q > 100 || !Number.isInteger(q)) err = 'Quantité : entier de 1 à 100.';
      else if (inf === null || (inf !== undefined && (inf < 0 || inf > 100))) err = 'Remplissage : 0 à 100 %.';
      else if (w === null || (w !== undefined && (w < 1 || !Number.isInteger(w)))) err = 'Parois : entier d’au moins 1.';
      else if (l === null || (l !== undefined && (l < 0.04 || l > 0.8))) err = 'Hauteur de couche : 0,04 à 0,8 mm.';
      const settings: PieceSettings = {};
      if (typeof inf === 'number') settings.sparse_infill_density = inf;
      if (typeof w === 'number') settings.wall_loops = w;
      if (typeof l === 'number') settings.layer_height = l;
      if (r.support) settings.enable_support = true;
      out.push({ item: { model_id: r.modelId, quantity: typeof q === 'number' ? q : 1, label: r.label.trim() || undefined, settings: Object.keys(settings).length ? settings : undefined }, err });
    }
    return out;
  }, [rows]);
  const invalid = parsed.some((p) => p.err);

  const compute = async () => {
    setBusy('plan'); setError(undefined); setSliced(undefined);
    try {
      const p = await plateApi.planPlate({ printer_id: printerId, items: parsed.map((x) => x.item), group_by_settings: groupBy || undefined });
      setPlan(p); setPlate(1);
    } catch (e) { setError(describeError(e)); } finally { setBusy(null); }
  };
  const slice = async () => {
    if (!plan) return;
    setBusy('slice'); setError(undefined);
    try { setSliced(await plateApi.slice(plan.id)); } catch (e) { setError(describeError(e)); } finally { setBusy(null); }
  };

  const modelOpts = [{ value: '', label: 'Choisir une pièce…' }, ...(models.data?.models ?? []).map((m) => ({ value: m.id, label: m.name }))];
  const noPrinter = printers.data !== undefined && printers.data.printers.length === 0;

  return (
    <Panel title="Plateau de plusieurs pièces" actions={<Layers size={15} aria-hidden />}>
      <div className="pl-form">
        <p className="hint" style={{ margin: 0 }}>Posez plusieurs pièces (et leurs exemplaires) sur le moins de plateaux possible, chacune avec ses propres réglages. Rien n'est découpé tant que vous ne le demandez pas.</p>
        <Field label="Imprimante cible">
          <Select value={printerId} onChange={(e) => { setPrinterId(e.target.value); setPlan(undefined); }} disabled={noPrinter}
            options={noPrinter ? [{ value: '', label: 'Aucune imprimante enregistrée' }] : (printers.data?.printers ?? []).map((p) => ({ value: p.id, label: `${p.name} (${p.model_name}, buse ${p.nozzle_mm} mm)` }))} />
        </Field>
        <div className="pl-rows" role="list" aria-label="Pièces du plateau">
          {rows.map((r, i) => (
            <div key={r.uid} className="pl-row" role="listitem">
              <div className="pl-row-main">
                <Field label={`Pièce ${i + 1}`}><Select value={r.modelId} onChange={(e) => setRow(r.uid, { modelId: e.target.value })} options={modelOpts} /></Field>
                <Field label="Quantité"><Input inputMode="numeric" value={r.qty} onChange={(e) => setRow(r.uid, { qty: e.target.value })} /></Field>
                <Button size="sm" variant="ghost" icon={<Trash2 size={13} />} aria-label={`Retirer la pièce ${i + 1}`} disabled={rows.length === 1} onClick={() => { setRows((x) => x.filter((y) => y.uid !== r.uid)); setPlan(undefined); }} />
              </div>
              <div className="pl-row-set">
                <Field label="Remplissage (%)"><Input inputMode="decimal" placeholder="profil" value={r.infill} onChange={(e) => setRow(r.uid, { infill: e.target.value })} /></Field>
                <Field label="Parois"><Input inputMode="numeric" placeholder="profil" value={r.walls} onChange={(e) => setRow(r.uid, { walls: e.target.value })} /></Field>
                <Field label="Couche (mm)"><Input inputMode="decimal" placeholder="profil" value={r.layer} onChange={(e) => setRow(r.uid, { layer: e.target.value })} /></Field>
                <Field label="Nom affiché"><Input placeholder="nom du modèle" value={r.label} onChange={(e) => setRow(r.uid, { label: e.target.value })} /></Field>
                <Toggle checked={r.support} onChange={(v) => setRow(r.uid, { support: v })} label="Supports" />
              </div>
              {parsed[i]?.err && <p className="hint pl-err" role="alert">{parsed[i].err}</p>}
            </div>
          ))}
        </div>
        <div className="pl-bar">
          <Button icon={<Plus size={14} />} onClick={() => { setRows((x) => [...x, blank()]); setPlan(undefined); }}>Ajouter une pièce</Button>
          <Toggle checked={groupBy} onChange={(v) => { setGroupBy(v); setPlan(undefined); }} label="Ne pas mélanger des réglages différents sur une plaque" />
          <Button variant="primary" icon={<Layers size={15} />} disabled={busy !== null || invalid || !printerId} onClick={compute}>{busy === 'plan' ? 'Calcul…' : 'Calculer le plateau'}</Button>
        </div>
        {error && <Notice tone="err" title="Opération refusée">{error}</Notice>}

        {plan && (
          <div className="pl-result">
            <Notice tone={plan.fits ? 'info' : 'warn'} icon={plan.fits ? <CheckCircle2 size={16} aria-hidden /> : undefined} title={plan.fits ? 'Tient' : 'Ne tient pas'}>{plan.report[0]?.replace(/^(NE )?TIENT : /, '')}</Notice>
            {plan.report.slice(1).map((l, i) => <p key={i} className="hint" style={{ margin: 0 }}>{l}</p>)}
            {plan.warnings?.map((w) => <p key={w} className="hint" style={{ margin: 0 }}>{w}</p>)}
            {plan.plates.length > 1 && (
              <div className="pl-tabs" role="tablist" aria-label="Plaques">
                {plan.plates.map((p) => <button key={p.index} role="tab" aria-selected={plate === p.index} className={plate === p.index ? 'on' : undefined} onClick={() => setPlate(p.index)}>Plaque {p.index} ({p.objects.length})</button>)}
              </div>
            )}
            {img && <img className="pl-img" src={img.url} alt={`Plateau ${plan.printer_model}, plaque ${plate} : ${img.legend.slice(1, 7).join(' ; ')}`} />}
            <div className="pl-bar">
              <Button variant="primary" icon={<Scissors size={15} />} disabled={!plan.fits || busy !== null || !!sliced} onClick={slice}>{busy === 'slice' ? 'Mise en file…' : `Découper (${plan.plates.length} impression${plan.plates.length > 1 ? 's' : ''})`}</Button>
              <span className="hint">Une impression par plaque : chacune se valide, se dépose puis se confirme séparément.</span>
            </div>
          </div>
        )}
        {sliced && (
          <Notice tone="info" title="Découpe en file" actions={<Link to="/impression"><Button size="sm">Suivre les impressions</Button></Link>}>
            {sliced.jobs.map((j) => <span key={j.id}><Tag>plaque {j.plate}</Tag> <span className="mono">{j.id}</span> {j.state}{' '}</span>)}
          </Notice>
        )}
      </div>
    </Panel>
  );
}
