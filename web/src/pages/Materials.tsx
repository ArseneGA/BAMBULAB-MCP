import { useState } from 'react';
import { Link } from 'react-router-dom';
import { ExternalLink } from 'lucide-react';
import { Field, Input, Panel, SearchInput, Tabs, Tag } from '../components';
import { api } from '../api/client';
import { useAsync } from '../api/hooks';
import type { PrinterModel } from '../api/printTypes';
import type { MaterialDetail, MaterialProperty } from '../api/extraTypes';
import { Async, Notice, fmtNum } from './common';
import './materials.css';

const Unknown = () => <span className="unknown">inconnu</span>;
const yesNo = (b?: boolean) => (b === undefined ? <Unknown /> : b ? 'oui' : 'non');

function ModelSheet({ m }: { m: PrinterModel }) {
  const v = m.build_volume;
  return (
    <Panel title={m.name} actions={<Tag>{m.id}</Tag>}>
      <dl className="kvl">
        <dt>Volume d'impression</dt><dd className="mono">{v ? `${fmtNum(v.x, 0)} × ${fmtNum(v.y, 0)} × ${fmtNum(v.z, 0)} mm` : <Unknown />}</dd>
        <dt>Buses</dt><dd className="mono">{m.nozzle_diameters_mm?.length ? m.nozzle_diameters_mm.map((n) => `${n} mm`).join(', ') : <Unknown />}</dd>
        <dt>Plateaux</dt><dd>{m.plates?.length ? m.plates.join(', ') : <Unknown />}</dd>
        <dt>Enceinte fermée</dt><dd>{yesNo(m.enclosed)}</dd>
        <dt>Plateau chauffant</dt><dd>{yesNo(m.heated_bed)}</dd>
        <dt>Température plateau max</dt><dd className="mono">{m.max_bed_temp_c !== undefined ? `${m.max_bed_temp_c} °C` : <Unknown />}</dd>
        <dt>Température buse max</dt><dd className="mono">{m.max_nozzle_temp_c !== undefined ? `${m.max_nozzle_temp_c} °C` : <Unknown />}</dd>
        <dt>Source</dt><dd>{m.source ?? <Unknown />}</dd>
      </dl>
      <p className="src-note" style={{ marginTop: 'var(--sp-3)' }}>Une valeur absente est inconnue : elle n'est jamais déduite d'un autre modèle.</p>
    </Panel>
  );
}

const PROP_LABEL: Record<string, string> = {
  density: 'Masse volumique', modulus_xy: 'Module de traction (XY)', modulus_z: 'Module de traction (Z)', tensile_xy: 'Résistance en traction (XY)', tensile_z: 'Résistance en traction (Z)',
  elongation_xy: 'Allongement à la rupture (XY)', elongation_z: 'Allongement à la rupture (Z)', bending_modulus_xy: 'Module de flexion (XY)', bending_modulus_z: 'Module de flexion (Z)',
  bending_strength_xy: 'Résistance en flexion (XY)', bending_strength_z: 'Résistance en flexion (Z)', hdt_1_8mpa: 'Température de fléchissement (1,8 MPa)', hdt_0_45mpa: 'Température de fléchissement (0,45 MPa)',
  max_overhang_deg: 'Surplomb recommandé', max_bridge_mm: 'Pont recommandé', shrinkage_pct: 'Retrait', snap_strain_allow: 'Déformation admissible (clips)',
};
const LEVEL: Record<string, { label: string; tone?: 'ok' | 'warn' }> = {
  measured: { label: 'fiche fabricant', tone: 'ok' }, calculated: { label: 'calculé', tone: 'ok' }, heuristic: { label: 'recommandation', tone: 'warn' }, indicative: { label: 'indicatif', tone: 'warn' },
};
const fmtProp = (p: MaterialProperty) => (p.value === null ? null : `${fmtNum(p.value, 3)}${p.tol !== undefined ? ` ± ${fmtNum(p.tol, 3)}` : ''} ${p.unit}`);

function MaterialSheet({ id }: { id: string }) {
  const st = useAsync((s) => api.material(id, s), [id]);
  return (
    <Async state={st} label="Chargement de la fiche">
      {(d) => <MaterialBody d={d} />}
    </Async>
  );
}

function MaterialBody({ d }: { d: MaterialDetail }) {
  const keys = Object.keys(d.properties).sort();
  const tds = d.sources[d.tds_source];
  return (
    <Panel title={`${d.name}`} actions={<Tag>{d.family}</Tag>}>
      <dl className="kvl" style={{ marginBottom: 'var(--sp-3)' }}>
        <dt>Fabricant</dt><dd>{d.vendor || <Unknown />}</dd>
        <dt>Fiche technique</dt><dd>{tds ? <>{tds.title} {tds.version} · consultée le <span className="mono">{tds.accessed}</span> {tds.url && <a href={tds.url} target="_blank" rel="noreferrer" aria-label="Ouvrir la fiche technique (nouvel onglet)"><ExternalLink size={12} aria-hidden /></a>}</> : <Unknown />}</dd>
        {tds?.sha256 && <><dt>Empreinte du PDF</dt><dd className="mono" title={tds.sha256}>{tds.sha256.slice(0, 16)}…</dd></>}
      </dl>
      <div className="mat-table-wrap">
        <table className="mat-table" aria-label={`Propriétés de ${d.name}`}>
          <thead><tr><th scope="col">Propriété</th><th scope="col">Valeur</th><th scope="col">Confiance</th><th scope="col">Domaine de validité</th></tr></thead>
          <tbody>
            {keys.map((k) => {
              const p = d.properties[k];
              const lvl = LEVEL[p.level];
              return (
                <tr key={k}>
                  <th scope="row">{PROP_LABEL[k] ?? k}<span className="mono sub">{k}</span></th>
                  <td className="mono">{fmtProp(p) ?? <Unknown />}</td>
                  <td>{p.value === null ? <span className="unknown">—</span> : <Tag tone={lvl?.tone}>{lvl?.label ?? p.level}</Tag>}</td>
                  <td className="hint">{p.domain}{p.note ? ` — ${p.note}` : ''}{p.value !== null && <> <span className="mono">({d.sources[p.source]?.title ?? p.source}, {p.date})</span></>}</td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
      {d.unknown_properties.length > 0 && <p className="src-note" style={{ marginTop: 'var(--sp-3)' }}>Propriétés inconnues pour ce matériau : {d.unknown_properties.map((k) => PROP_LABEL[k] ?? k).join(', ')}. Elles ne sont jamais remplacées par la valeur d'un autre matériau.</p>}
      <p className="hint" style={{ margin: 'var(--sp-3) 0 0' }}>{d.note}</p>
      <p className="hint" style={{ margin: 'var(--sp-1) 0 0' }}>{d.disclaimer}</p>
    </Panel>
  );
}

function Presets() {
  const [machine, setMachine] = useState('Bambu Lab P1S 0.4 nozzle');
  const [q, setQ] = useState('');
  const [applied, setApplied] = useState(machine);
  const st = useAsync((s) => api.filaments(applied, s), [applied]);
  return (
    <Panel title="Presets filament de Bambu Studio">
      <div className="stack">
        <Field label="Preset machine (nom exact dans Bambu Studio)"><Input value={machine} onChange={(e) => setMachine(e.target.value)} onBlur={() => setApplied(machine.trim())} onKeyDown={(e) => { if (e.key === 'Enter') setApplied(machine.trim()); }} spellCheck={false} /></Field>
        <Async state={st} label="Lecture des profils de Bambu Studio">
          {(d) => {
            const shown = d.filaments.filter((f) => f.toLowerCase().includes(q.toLowerCase()));
            return (
              <>
                <SearchInput value={q} onChange={setQ} placeholder={`Filtrer les ${d.filaments.length} presets compatibles`} aria-label="Filtrer les presets filament" />
                {shown.length === 0 ? <p className="hint" style={{ margin: 0 }}>Aucun preset ne correspond.</p> : (
                  <ul className="mat-presets" aria-label="Presets filament compatibles">{shown.slice(0, 40).map((f) => <li key={f} className="mono">{f}</li>)}</ul>
                )}
                {shown.length > 40 && <p className="hint" style={{ margin: 0 }}>{shown.length - 40} autres : affinez le filtre.</p>}
              </>
            );
          }}
        </Async>
        <p className="hint" style={{ margin: 0 }}>Liste lue dans l'installation de Bambu Studio du serveur. Un preset n'est pas une fiche matière : ses réglages ne remplacent pas la fiche technique de la bobine.</p>
      </div>
    </Panel>
  );
}

export function MaterialsPage() {
  const [tab, setTab] = useState<'machines' | 'materiaux'>('machines');
  const models = useAsync((s) => api.printerModels(s), []);
  const printers = useAsync((s) => api.printers(s), []);
  const mats = useAsync((s) => api.materials(s), []);
  const [sel, setSel] = useState<string>('');
  const [msel, setMsel] = useState<string>('');
  return (
    <>
      <div className="page-head"><span className="eyebrow">Espace</span><h1>Matériaux et machines</h1><p>Modèles d'imprimantes Bambu Lab connus du service, parc enregistré, et base de matériaux issue des fiches techniques du fabricant. Une valeur absente reste « inconnu ».</p></div>
      <div className="stack">
        <Tabs label="Sections" value={tab} onChange={setTab} tabs={[{ value: 'machines', label: 'Machines' }, { value: 'materiaux', label: 'Matériaux' }]} />
        {tab === 'machines' && (
          <>
            <Async state={models} label="Chargement des modèles d'imprimantes">
              {(d) => {
                const cur = d.models.find((m) => m.id === sel) ?? d.models[0];
                return (
                  <div className="mat-layout">
                    <Panel title={`Modèles (${d.models.length})`} flush>
                      <ul className="list" role="listbox" aria-label="Modèles d'imprimantes">
                        {d.models.map((m) => (
                          <li key={m.id} role="presentation"><button type="button" role="option" className="list-item" aria-selected={cur?.id === m.id} onClick={() => setSel(m.id)}><span className="mat-list-item"><strong>{m.name}</strong><span className="mono">{m.build_volume ? `${m.build_volume.x}×${m.build_volume.y}×${m.build_volume.z}` : '—'}</span></span></button></li>
                        ))}
                      </ul>
                    </Panel>
                    {cur && <ModelSheet m={cur} />}
                  </div>
                );
              }}
            </Async>
            <Panel title="Imprimantes enregistrées">
              <Async state={printers} label="Chargement du parc">
                {(d) => d.printers.length === 0
                  ? <p className="hint" style={{ margin: 0 }}>Aucune imprimante enregistrée. Ajoutez-en une depuis <Link to="/impression">Impression</Link>.</p>
                  : <ul style={{ margin: 0, paddingLeft: 'var(--sp-4)' }}>{d.printers.map((p) => <li key={p.id}><strong>{p.name}</strong> · {p.model_name} · buse <span className="mono">{p.nozzle_mm} mm</span> · AMS {p.ams_units} · firmware {p.firmware ?? 'inconnu'}</li>)}</ul>}
              </Async>
            </Panel>
          </>
        )}
        {tab === 'materiaux' && (
          <>
            <Async state={mats} label="Chargement des matériaux">
              {(d) => {
                const cur = d.materials.find((m) => m.id === msel) ?? d.materials[0];
                return (
                  <>
                    <Notice tone="info" title={`Base de matériaux du ${d.version}`}>{d.note} {d.disclaimer}</Notice>
                    <div className="mat-layout">
                      <Panel title={`Matériaux (${d.materials.length})`} flush>
                        <ul className="list" role="listbox" aria-label="Matériaux">
                          {d.materials.map((m) => (
                            <li key={m.id} role="presentation"><button type="button" role="option" className="list-item" aria-selected={cur?.id === m.id} onClick={() => setMsel(m.id)}><span className="mat-list-item"><strong>{m.name}</strong><span className="mono">{m.family} · {m.known_properties} connue(s){m.unknown_properties.length > 0 ? ` · ${m.unknown_properties.length} inconnue(s)` : ''}</span></span></button></li>
                          ))}
                        </ul>
                      </Panel>
                      {cur && <MaterialSheet key={cur.id} id={cur.id} />}
                    </div>
                  </>
                );
              }}
            </Async>
            <Presets />
          </>
        )}
      </div>
    </>
  );
}
