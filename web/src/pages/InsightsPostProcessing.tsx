import { Fragment, useState } from 'react';
import { Button, Field, Input, Panel, Select, Tag } from '../components';
import { describeError } from '../api/client';
import { insightsApi } from '../api/insights';
import type { PostAdvice } from '../api/insights';
import { Notice, fmtNum } from './common';

const MATERIALS = ['PLA', 'PETG', 'ABS', 'ASA', 'TPU', 'PC', 'PA', 'PLA-CF', 'PETG-CF', 'PA-CF'].map((m) => ({ value: m, label: m }));

/** Conseils de finition : appelle POST /api/post-processing (outil post_processing_advice). */
export function InsightsPostProcessing() {
  const [material, setMaterial] = useState('PLA');
  const [finish, setFinish] = useState('lisse');
  const [geometry, setGeometry] = useState('');
  const [usage, setUsage] = useState('');
  const [inserts, setInserts] = useState('');
  const [res, setRes] = useState<PostAdvice>();
  const [err, setErr] = useState<string>();
  const [busy, setBusy] = useState(false);
  const go = async () => {
    setBusy(true); setErr(undefined);
    try {
      const insert_sizes = inserts.split(/[ ,;]+/).map((s) => s.trim()).filter(Boolean);
      setRes(await insightsApi.postProcessing({ material, finish, geometry, usage, insert_sizes: insert_sizes.length ? insert_sizes : undefined }));
    } catch (e) { setErr(describeError(e)); setRes(undefined); } finally { setBusy(false); }
  };
  return (
    <div className="stack">
      <Panel title="Finition d'une pièce" actions={<Button variant="primary" size="sm" onClick={go} disabled={busy}>Conseiller</Button>}>
        <div className="row-wrap">
          <Field label="Matière"><Select value={material} onChange={(e) => setMaterial(e.target.value)} options={MATERIALS} /></Field>
          <Field label="Finition voulue"><Input value={finish} onChange={(e) => setFinish(e.target.value)} placeholder="lisse, mat, peinte, brillante, transparente, recuit, collage, inserts" /></Field>
          <Field label="Géométrie"><Input value={geometry} onChange={(e) => setGeometry(e.target.value)} placeholder="parois fines, détails fins, cavités…" /></Field>
          <Field label="Usage"><Input value={usage} onChange={(e) => setUsage(e.target.value)} placeholder="extérieur, chaleur, tolérances serrées, flexible…" /></Field>
          <Field label="Inserts (tailles)"><Input value={inserts} onChange={(e) => setInserts(e.target.value)} placeholder="M3, M4" /></Field>
        </div>
      </Panel>
      {err && <Notice tone="err" title="Impossible">{err}</Notice>}
      {res && (
        <>
          <Notice tone="info" title={`${res.family} · niveau ${res.level} (données ${res.data_version})`}>Essayez chaque technique sur une pièce témoin imprimée dans la même matière.</Notice>
          {res.steps.map((s) => (
            <Panel key={s.technique} title={`${s.order}. ${s.title}`} actions={<Tag tone={s.applies === 'yes' ? 'ok' : 'warn'}>{s.applies === 'yes' ? 'conseillé' : 'limité / à tester'}</Tag>}>
              <ol className="ins-steps">{s.steps.map((t, i) => <li key={i}>{t}</li>)}</ol>
              {s.parameters && <dl className="kvl">{Object.entries(s.parameters).map(([k, v]) => <Fragment key={k}><dt>{k}</dt><dd>{v}</dd></Fragment>)}</dl>}
              {s.notes?.map((n) => <p key={n} className="hint">{n}</p>)}
              {s.hazards && s.hazards.length > 0 && <Notice tone="warn" title="Sécurité"><ul className="ins-notes">{s.hazards.map((h) => <li key={h}>{h}</li>)}</ul></Notice>}
            </Panel>
          ))}
          {res.inserts && res.inserts.length > 0 && (
            <Panel title="Inserts thermo-sertis (trou à prévoir)">
              <div className="ins-table-wrap"><table className="ins-table" aria-label="Inserts">
                <thead><tr><th scope="col">Taille</th><th scope="col">Trou</th><th scope="col">Profondeur</th><th scope="col">Plot</th><th scope="col">Paroi min.</th><th scope="col">Fer</th></tr></thead>
                <tbody>{res.inserts.map((i) => <tr key={i.size}><th scope="row">{i.size}</th><td>{fmtNum(i.hole_diameter_mm, 2)} mm</td><td>{fmtNum(i.hole_depth_mm, 1)} mm</td><td>{fmtNum(i.boss_outer_diameter_mm, 1)} mm</td><td>{fmtNum(i.min_wall_around_hole_mm, 1)} mm</td><td>{i.soldering_iron_start_temperature ?? '—'}</td></tr>)}</tbody>
              </table></div>
            </Panel>
          )}
          {res.not_recommended && res.not_recommended.length > 0 && (
            <Panel title="Déconseillé pour cette matière">
              <ul className="ins-notes">{res.not_recommended.map((r) => <li key={r.technique}><strong>{r.title}</strong> : {r.reason}</li>)}</ul>
            </Panel>
          )}
          {res.honesty && res.honesty.length > 0 && <Notice tone="info" title="Honnêteté"><ul className="ins-notes">{res.honesty.map((h) => <li key={h}>{h}</li>)}</ul></Notice>}
          <Notice tone="warn" title="À savoir avant de commencer"><ul className="ins-notes">{res.warnings.map((w) => <li key={w}>{w}</li>)}</ul></Notice>
        </>
      )}
    </div>
  );
}
