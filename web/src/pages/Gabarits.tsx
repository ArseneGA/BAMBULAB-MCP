import { useMemo, useState } from 'react';
import { Link } from 'react-router-dom';
import { Button, EmptyState, Field, Input, PageHeader, Panel, Select, Tag, useToast } from '../components';
import { LibraryNav } from './LibraryNav';
import { describeError, useAsync } from '../api/hooks';
import { BASE } from '../api/client';
import { initialForm, libraryApi, num, paramsFromForm } from '../api/libraryApi';
import type {
  CalibrationDoc, CalibrationKey, CreateOutput, DiameterMeasure, GenerateOutput, Measures, ParamSpec, RecordOutput, TemplateInfo,
} from '../api/libraryTypes';
import { Async, Notice, fmtNum } from './common';
import './gabarits.css';

const LEVEL: Record<string, { label: string; tone?: 'ok' | 'warn' }> = {
  measured: { label: 'calibré / mesuré', tone: 'ok' }, calculated: { label: 'calculé', tone: 'ok' }, heuristic: { label: 'recommandation', tone: 'warn' }, indicative: { label: 'indicatif', tone: 'warn' },
};

const AUTO_LABEL = (a: string) => {
  const [k, v] = a.split(':');
  if (k === 'fit' || k === 'fitside') return `jeu « ${v} » calculé si vide`;
  if (k === 'gap_pip') return 'jeu imprimé en place calculé si vide';
  if (k === 'hole') return 'diamètre compensé selon l\'imprimante';
  if (k === 'insert') return 'dimension d\'insert calculée si vide';
  return 'calculé si vide';
};

/* ---------- formulaire de paramètres ---------- */
function ParamField({ sp, value, error, onChange }: { sp: ParamSpec; value: string; error?: string; onChange: (v: string) => void }) {
  const hint = [sp.unit, sp.min !== undefined && sp.max !== undefined ? `${sp.min} à ${sp.max}` : undefined].filter(Boolean).join(' · ');
  const label = `${sp.name}${hint ? ` (${hint})` : ''}`;
  return (
    <div className="gab-param">
      <Field label={label}>
        {sp.type === 'enum' ? (
          <Select value={value} onChange={(e) => onChange(e.target.value)} options={[{ value: '', label: `défaut : ${String(sp.default)}` }, ...(sp.options ?? []).map((o) => ({ value: o, label: o }))]} />
        ) : sp.type === 'boolean' ? (
          <Select value={value} onChange={(e) => onChange(e.target.value)} options={[{ value: '', label: `défaut : ${String(sp.default)}` }, { value: 'true', label: 'oui' }, { value: 'false', label: 'non' }]} />
        ) : (
          <Input inputMode="decimal" value={value} placeholder={sp.auto ? `auto (défaut ${String(sp.default)})` : String(sp.default)} aria-invalid={error ? true : undefined} onChange={(e) => onChange(e.target.value)} />
        )}
      </Field>
      <p className="gab-hint">{sp.description}{sp.auto && <> <Tag tone="accent">{AUTO_LABEL(sp.auto)}</Tag></>}</p>
      {error && <p className="gab-err" role="alert">{error}</p>}
    </div>
  );
}

function ResultView({ r, onSave }: { r: CreateOutput; onSave: (name: string) => Promise<void> }) {
  const [name, setName] = useState('');
  const [busy, setBusy] = useState(false);
  return (
    <Panel title="Pièce créée" actions={<Link to={`/modele/${r.model_id}`}>Ouvrir le modèle</Link>}>
      <div className="gab-result">
        {r.thumbnail_url && <img className="gab-thumb" src={BASE + r.thumbnail_url} alt="Vignette de la pièce générée" width={160} height={160} />}
        <div className="stack">
          <p>
            Gabarit <span className="mono">{r.template}</span> v{r.template_version}{r.variant ? <> (variante <span className="mono">{r.variant}</span>)</> : null}.
            {r.bbox_mm?.size && <> Encombrement <span className="mono">{r.bbox_mm.size.map((v) => fmtNum(v, 1)).join(' × ')} mm</span>.</>}
          </p>
          <Notice tone={r.calibration.applied ? 'info' : 'warn'} title={r.calibration.applied ? 'Calibration appliquée' : 'Valeurs génériques'}>{r.calibration.provenance}. {r.calibration.note}</Notice>
          {r.warnings.length > 0 && <Notice title="Avertissements"><>{r.warnings.join(' ')}</></Notice>}
        </div>
      </div>
      {r.auto.length > 0 && (
        <table className="gab-table" aria-label="Paramètres calculés">
          <thead><tr><th scope="col">Paramètre</th><th scope="col">Valeur</th><th scope="col">Confiance</th><th scope="col">Origine</th></tr></thead>
          <tbody>
            {r.auto.map((a) => (
              <tr key={a.param}>
                <th scope="row">{a.param}{a.nominal !== undefined && <span className="mono sub">nominal {String(a.nominal)} {a.unit}</span>}</th>
                <td className="mono">{fmtNum(a.value, 3)} {a.unit}</td>
                <td><Tag tone={LEVEL[a.level]?.tone}>{LEVEL[a.level]?.label ?? a.level}</Tag></td>
                <td className="hint">{a.source}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      {r.calcs.filter((c) => c.name === 'snap_fit').map((c) => (
        <p key={c.name} className="gab-hint">Clip : déformation {fmtNum(Number(c.extra?.strain), 4)} pour {fmtNum(Number(c.extra?.strain_allowable), 4)} admissible — {c.extra?.ok ? 'OK' : 'à revoir'}.</p>
      ))}
      <details className="gab-details"><summary>Hypothèses ({r.assumptions.length})</summary><ul>{r.assumptions.map((a, i) => <li key={i}>{a}</li>)}</ul></details>
      <div className="row-wrap">
        <Input aria-label="Nom de la variante" placeholder="Nom de variante, ex. Collier tube 32" value={name} onChange={(e) => setName(e.target.value)} />
        <Button size="sm" disabled={!name.trim() || busy} onClick={async () => { setBusy(true); try { await onSave(name.trim()); setName(''); } finally { setBusy(false); } }}>Enregistrer comme variante</Button>
      </div>
    </Panel>
  );
}

function TemplatesTab() {
  const state = useAsync((s) => libraryApi.templates(s), []);
  const toast = useToast();
  const [sel, setSel] = useState<string>();
  const [form, setForm] = useState<Record<string, string>>({});
  const [ctx, setCtx] = useState({ name: '', material: 'PLA', nozzle: '0.4', printer: '', filament: '' });
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [busy, setBusy] = useState(false);
  const [result, setResult] = useState<CreateOutput>();

  const pick = (t: TemplateInfo) => { setSel(t.id); setForm(initialForm(t)); setErrors({}); setResult(undefined); };

  return (
    <Async state={state} label="Chargement des gabarits">
      {(list) => {
        const tpl = list.templates.find((t) => t.id === sel);
        const cats = list.categories;
        return (
          <div className="gab-layout">
            <Panel title="Gabarits">
              {cats.map((c) => (
                <section key={c} className="gab-cat">
                  <h3>{c}</h3>
                  <ul className="gab-cards">
                    {list.templates.filter((t) => t.category === c).map((t) => (
                      <li key={t.id}>
                        <button type="button" className="gab-card" aria-pressed={t.id === sel} onClick={() => pick(t)}>
                          <img src={BASE + t.thumbnail_url} alt="" width={56} height={56} loading="lazy" />
                          <span><strong>{t.title}</strong>{t.kind === 'variant' && <Tag>variante</Tag>}<span className="gab-sum">{t.summary}</span></span>
                        </button>
                      </li>
                    ))}
                  </ul>
                </section>
              ))}
            </Panel>
            <div className="stack">
              {!tpl ? (
                <EmptyState title="Choisissez un gabarit" text="Les paramètres, leurs bornes et les calculs d'ingénierie associés s'affichent ici." />
              ) : (
                <>
                  <Panel title={`${tpl.title}`} actions={<Tag>v{tpl.version}</Tag>}>
                    <p className="sim-note">{tpl.description}</p>
                    <div className="gab-form">
                      {tpl.params.map((sp) => (
                        <ParamField key={sp.name} sp={sp} value={form[sp.name] ?? ''} error={errors[sp.name]} onChange={(v) => setForm((f) => ({ ...f, [sp.name]: v }))} />
                      ))}
                    </div>
                    <h4 className="gab-sub">Contexte d'impression</h4>
                    <div className="gab-ctx">
                      <Field label="Nom du modèle"><Input value={ctx.name} placeholder="automatique" onChange={(e) => setCtx({ ...ctx, name: e.target.value })} /></Field>
                      <Field label="Matériau"><Input value={ctx.material} onChange={(e) => setCtx({ ...ctx, material: e.target.value })} /></Field>
                      <Field label="Buse (mm)"><Input inputMode="decimal" value={ctx.nozzle} onChange={(e) => setCtx({ ...ctx, nozzle: e.target.value })} /></Field>
                      <Field label="Imprimante (calibration)"><Input value={ctx.printer} placeholder="même nom que pour la calibration" onChange={(e) => setCtx({ ...ctx, printer: e.target.value })} /></Field>
                      <Field label="Bobine (calibration)"><Input value={ctx.filament} onChange={(e) => setCtx({ ...ctx, filament: e.target.value })} /></Field>
                    </div>
                    <Button variant="primary" disabled={busy} onClick={async () => {
                      const { params, errors: errs } = paramsFromForm(tpl.params, form);
                      setErrors(errs);
                      if (Object.keys(errs).length) return;
                      setBusy(true);
                      try {
                        const r = await libraryApi.create({ template: tpl.id, params, name: ctx.name.trim() || undefined, material: ctx.material.trim() || undefined, nozzle_diameter: num(ctx.nozzle), printer: ctx.printer.trim() || undefined, filament: ctx.filament.trim() || undefined });
                        setResult(r);
                        toast('ok', `Modèle ${r.model_id} créé`);
                      } catch (e) { toast('err', describeError(e)); } finally { setBusy(false); }
                    }}>{busy ? 'Génération…' : 'Créer la pièce'}</Button>
                  </Panel>
                  {result && <ResultView r={result} onSave={async (name) => {
                    try { const s = await libraryApi.saveVariant(result.model_id, name); toast('ok', s.note); state.reload(); } catch (e) { toast('err', describeError(e)); }
                  }} />}
                </>
              )}
            </div>
          </div>
        );
      }}
    </Async>
  );
}

/* ---------- calibration ---------- */
function DocView({ d }: { d: CalibrationDoc }) {
  const off = (l?: { offset_mm: number; slope: number }) => (l ? `${fmtNum(l.offset_mm, 3)} mm${l.slope ? ` + ${fmtNum(l.slope, 4)}·d` : ''}` : undefined);
  const rows: Array<[string, string | undefined]> = [
    ['Jeu glissant', d.slides_gap_mm !== undefined ? `${fmtNum(d.slides_gap_mm, 2)} mm` : undefined],
    ['Jeu serré', d.snug_gap_mm !== undefined ? `${fmtNum(d.snug_gap_mm, 2)} mm` : undefined],
    ['Compensation de trou (vertical)', off(d.hole_vertical)], ['Compensation de trou (horizontal)', off(d.hole_horizontal)], ['Arbres', off(d.shaft)],
    ['Échelle de retrait XY / Z', d.shrink_scale_xy !== undefined || d.shrink_scale_z !== undefined ? `×${fmtNum(d.shrink_scale_xy ?? 1, 4)} / ×${fmtNum(d.shrink_scale_z ?? 1, 4)}` : undefined],
    ['Meilleure température', d.best_temp_c !== undefined ? `${fmtNum(d.best_temp_c, 0)} °C` : undefined],
    ['Pont propre', d.bridge_max_span_mm !== undefined ? `${fmtNum(d.bridge_max_span_mm, 0)} mm` : undefined],
    ['Surplomb propre', d.overhang_max_deg !== undefined ? `${fmtNum(d.overhang_max_deg, 0)}°` : undefined],
  ];
  return (
    <>
      <p className="gab-hint">{d.provenance}</p>
      <dl className="kvl">{rows.filter(([, v]) => v).map(([k, v]) => <><dt key={`k${k}`}>{k}</dt><dd key={`v${k}`} className="mono">{v}</dd></>)}</dl>
    </>
  );
}

interface Row { nominal: string; measured: string; axis: 'vertical' | 'horizontal' }

function RowsEditor({ title, rows, setRows, axis }: { title: string; rows: Row[]; setRows: (r: Row[]) => void; axis?: boolean }) {
  return (
    <fieldset className="gab-rows">
      <legend>{title}</legend>
      {rows.map((r, i) => (
        <div key={i} className="gab-row">
          <Input aria-label="Diamètre nominal (mm)" placeholder="nominal" inputMode="decimal" value={r.nominal} onChange={(e) => setRows(rows.map((x, j) => (j === i ? { ...x, nominal: e.target.value } : x)))} />
          <Input aria-label="Diamètre mesuré (mm)" placeholder="mesuré" inputMode="decimal" value={r.measured} onChange={(e) => setRows(rows.map((x, j) => (j === i ? { ...x, measured: e.target.value } : x)))} />
          {axis && <Select aria-label="Axe" value={r.axis} onChange={(e) => setRows(rows.map((x, j) => (j === i ? { ...x, axis: e.target.value as Row['axis'] } : x)))} options={[{ value: 'vertical', label: 'vertical' }, { value: 'horizontal', label: 'horizontal' }]} />}
          <Button size="sm" variant="ghost" onClick={() => setRows(rows.filter((_, j) => j !== i))}>Retirer</Button>
        </div>
      ))}
      <Button size="sm" onClick={() => setRows([...rows, { nominal: '', measured: '', axis: 'vertical' }])}>Ajouter une mesure</Button>
    </fieldset>
  );
}

const toDia = (rows: Row[], axis: boolean): DiameterMeasure[] =>
  rows.flatMap((r) => { const n = num(r.nominal), m = num(r.measured); return n !== undefined && m !== undefined ? [axis ? { nominal: n, measured: m, axis: r.axis } : { nominal: n, measured: m }] : []; });

function CalibrationTab() {
  const toast = useToast();
  const [k, setK] = useState({ printer: '', nozzle: '0.4', material: 'PLA', filament: '' });
  const key = useMemo<CalibrationKey>(() => ({ printer: k.printer.trim() || undefined, nozzle_mm: num(k.nozzle) ?? 0.4, material: k.material.trim(), filament: k.filament.trim() || undefined }), [k]);
  const [gen, setGen] = useState<GenerateOutput>();
  const [busy, setBusy] = useState(false);
  const [m, setM] = useState({ slides: '', snug: '', cx: '', cy: '', cz: '', temp: '', bridge: '', overhang: '' });
  const [holes, setHoles] = useState<Row[]>([]);
  const [shafts, setShafts] = useState<Row[]>([]);
  const [rec, setRec] = useState<RecordOutput>();
  const active = useAsync((s) => (key.material ? libraryApi.calibration(key, s) : Promise.resolve(undefined)), [key.printer, key.nozzle_mm, key.material, key.filament]);

  const submit = async () => {
    const meas: Measures = {};
    const set = <K extends keyof Measures>(kk: K, v: Measures[K] | undefined) => { if (v !== undefined) meas[kk] = v; };
    set('fit_slides_gap_mm', num(m.slides)); set('fit_snug_gap_mm', num(m.snug));
    const cube = [num(m.cx), num(m.cy), num(m.cz)];
    if (cube.every((v) => v !== undefined)) meas.cube_mm = cube as number[];
    set('best_temp_c', num(m.temp)); set('bridge_max_span_mm', num(m.bridge)); set('overhang_max_deg', num(m.overhang));
    const h = toDia(holes, true), s = toDia(shafts, false);
    if (h.length) meas.holes = h;
    if (s.length) meas.shafts = s;
    setBusy(true);
    try {
      const r = await libraryApi.recordCalibration(key, meas, gen?.set_id);
      setRec(r); active.reload(); toast('ok', 'Calibration enregistrée');
    } catch (e) { toast('err', describeError(e)); } finally { setBusy(false); }
  };

  return (
    <div className="gab-layout">
      <div className="stack">
        <Panel title="Machine et matériau">
          <div className="gab-ctx">
            <Field label="Imprimante"><Input value={k.printer} placeholder="ex. P1S atelier" onChange={(e) => setK({ ...k, printer: e.target.value })} /></Field>
            <Field label="Buse (mm)"><Input inputMode="decimal" value={k.nozzle} onChange={(e) => setK({ ...k, nozzle: e.target.value })} /></Field>
            <Field label="Matériau"><Input value={k.material} onChange={(e) => setK({ ...k, material: e.target.value })} /></Field>
            <Field label="Bobine (facultatif)"><Input value={k.filament} placeholder="marque + couleur" onChange={(e) => setK({ ...k, filament: e.target.value })} /></Field>
          </div>
          <Button variant="primary" disabled={busy || !key.material} onClick={async () => {
            setBusy(true);
            try { const g = await libraryApi.generateCalibration(key); setGen(g); toast('ok', `${g.pieces.length} pièce(s) de calibration ajoutées au catalogue`); } catch (e) { toast('err', describeError(e)); } finally { setBusy(false); }
          }}>Générer les pièces de calibration</Button>
          {gen && (
            <div className="stack" style={{ marginTop: 'var(--sp-3)' }}>
              <ul className="gab-pieces">
                {gen.pieces.map((p) => (
                  <li key={p.model_id}><Link to={`/modele/${p.model_id}`}><strong>{p.name}</strong></Link> <Tag>{p.kind}</Tag><span className="gab-hint">{p.measure.join(' ; ')}</span></li>
                ))}
              </ul>
              <p className="gab-hint">{gen.instructions.join(' ')}</p>
            </div>
          )}
        </Panel>
        <Panel title="Calibration active pour cette clé">
          <Async state={active} label="Recherche de la calibration" empty={(d) => (!d ? <p className="gab-hint">Saisissez un matériau.</p> : null)}>
            {(d) => (d?.resolution.calibration ? <DocView d={d.resolution.calibration} /> : <p className="gab-hint">{d?.resolution.note}</p>)}
          </Async>
        </Panel>
      </div>
      <div className="stack">
        <Panel title="Mesures">
          <p className="sim-note">Renseignez ce que vous avez mesuré ; les champs vides sont ignorés. Les corrections valent pour cette imprimante, cette buse et ce matériau (et cette bobine si indiquée).</p>
          <div className="gab-ctx">
            <Field label="Jeu où la languette coulisse (mm)"><Input inputMode="decimal" value={m.slides} onChange={(e) => setM({ ...m, slides: e.target.value })} /></Field>
            <Field label="Jeu serré (mm), facultatif"><Input inputMode="decimal" value={m.snug} onChange={(e) => setM({ ...m, snug: e.target.value })} /></Field>
            <Field label="Cube X (mm)"><Input inputMode="decimal" value={m.cx} onChange={(e) => setM({ ...m, cx: e.target.value })} /></Field>
            <Field label="Cube Y (mm)"><Input inputMode="decimal" value={m.cy} onChange={(e) => setM({ ...m, cy: e.target.value })} /></Field>
            <Field label="Cube Z (mm)"><Input inputMode="decimal" value={m.cz} onChange={(e) => setM({ ...m, cz: e.target.value })} /></Field>
            <Field label="Meilleure température (°C)"><Input inputMode="decimal" value={m.temp} onChange={(e) => setM({ ...m, temp: e.target.value })} /></Field>
            <Field label="Pont propre jusqu'à (mm)"><Input inputMode="decimal" value={m.bridge} onChange={(e) => setM({ ...m, bridge: e.target.value })} /></Field>
            <Field label="Surplomb propre jusqu'à (°)"><Input inputMode="decimal" value={m.overhang} onChange={(e) => setM({ ...m, overhang: e.target.value })} /></Field>
          </div>
          <RowsEditor title="Trous mesurés" rows={holes} setRows={setHoles} axis />
          <RowsEditor title="Arbres mesurés" rows={shafts} setRows={setShafts} />
          <Button variant="primary" disabled={busy || !key.material} onClick={submit}>Enregistrer les mesures</Button>
        </Panel>
        {rec && (
          <Panel title="Corrections calculées">
            <ul>{rec.explanation.map((e, i) => <li key={i}>{e}</li>)}</ul>
            <h4 className="gab-sub">Effet</h4>
            <ul>{rec.effects.map((e, i) => <li key={i}>{e}</li>)}</ul>
            <p className="gab-hint">{rec.note}</p>
          </Panel>
        )}
      </div>
    </div>
  );
}

/** Bibliothèque › Pièces sur mesure (gabarits). La calibration d'une imprimante est dans Imprimantes › Calibration. */
export function GabaritsPage() {
  return (
    <>
      <PageHeader title="Pièces sur mesure" help="Créez une pièce paramétrique (gabarit) : elle rejoint la bibliothèque comme n'importe quel modèle." crumbs={[{ label: 'Bibliothèque', to: '/bibliotheque' }, { label: 'Pièces sur mesure' }]} below={<LibraryNav />} />
      <div className="page gab-page"><TemplatesTab /></div>
    </>
  );
}
export { CalibrationTab };
