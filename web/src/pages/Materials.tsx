import { useState } from 'react';
import { Link } from 'react-router-dom';
import { Panel, Tabs, Tag } from '../components';
import { api } from '../api/client';
import { useAsync } from '../api/hooks';
import type { PrinterModel } from '../api/printTypes';
import { Async, Notice, PendingBanner, fmtNum } from './common';
import './materials.css';

const Unknown = () => <span className="unknown">inconnu</span>;
const yesNo = (b?: boolean) => (b === undefined ? <Unknown /> : b ? 'oui' : 'non');

function ModelSheet({ m }: { m: PrinterModel }) {
  const v = m.build_volume;
  return (
    <Panel title={m.name} actions={<Tag>{m.id}</Tag>}>
      <dl className="kv">
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

export function MaterialsPage() {
  const [tab, setTab] = useState<'machines' | 'materiaux'>('machines');
  const models = useAsync((s) => api.printerModels(s), []);
  const printers = useAsync((s) => api.printers(s), []);
  const [sel, setSel] = useState<string>('');
  return (
    <>
      <div className="page-head"><span className="eyebrow">Espace</span><h1>Matériaux et machines</h1><p>Modèles d'imprimantes Bambu Lab connus du service (semés depuis les profils de Bambu Studio) et imprimantes enregistrées. Une valeur absente reste « inconnu ».</p></div>
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
            <PendingBanner screen="Matériaux" partial />
            <Notice title="Fiches matériaux non exposées par le serveur">
              La base de matériaux et de propriétés (internal/engineering) sert aux calculs de comparaison, mais aucune route HTTP ne la publie encore : aucune fiche n'est affichée plutôt que des valeurs inventées. Les matières se saisissent à l'envoi d'une impression (libellé libre de la bobine chargée) ; la famille du matériau d'un objectif se choisit dans la préparation.
            </Notice>
          </>
        )}
      </div>
    </>
  );
}
