import { useEffect, useState } from 'react';
import { Check } from 'lucide-react';
import { Field, Input, Panel, Select, Tag } from '../components';
import { useAsync } from '../api/hooks';
import { FINISHES, colorName, filcatalogApi, swatchBackground } from '../api/filcatalogApi';
import type { CatalogColor, CatalogConfidence, CatalogEntry, CatalogFamily, CatalogMatch } from '../api/filcatalogApi';
import { Async, Notice } from './common';
import './filcatalog.css';

const CONF: Record<CatalogConfidence, { label: string; tone?: 'ok' | 'warn' }> = {
  official_profile: { label: 'profil officiel Bambu Studio', tone: 'ok' }, official_site: { label: 'site officiel Bambu Lab', tone: 'ok' }, estimated: { label: 'estimé', tone: 'warn' },
};
const BED: Record<string, string> = { cool_plate: 'Cool Plate', engineering_plate: 'Engineering Plate', high_temp_plate: 'High Temp Plate', textured_pei: 'Textured PEI' };
const NA = <span className="unknown">non disponible</span>;

function Dot({ c, size = 22 }: { c: CatalogColor; size?: number }) {
  return <span className="fc-dot" style={{ background: swatchBackground(c), width: size, height: size }} role="img" aria-label={`${colorName(c)} ${c.hex.join(' ')}`} />;
}

/** Sélecteur de couleur officielle Bambu (composant isolé : inscription de bobine). */
export function CatalogColorPicker({ material, hex, selectedId, onPick }: { material?: string; hex?: string; selectedId?: string; onPick: (c: CatalogColor | undefined) => void }) {
  const [finish, setFinish] = useState('');
  const [text, setText] = useState('');
  const [nearest, setNearest] = useState(false);
  const cls = (material ?? '').split(/[\s-]/)[0].toUpperCase();
  const [family, setFamily] = useState(cls);
  const useHex = nearest && !!hex && /^#[0-9a-fA-F]{6}$/.test(hex);
  const st = useAsync((s) => filcatalogApi.colors({ family: family || undefined, finish: finish || undefined, text: text || undefined, hex: useHex ? hex : undefined, limit: 48 }, s), [family, finish, text, useHex, hex]);
  useEffect(() => { setFamily(cls); }, [cls]);
  return (
    <fieldset className="fc-picker">
      <legend>Couleur officielle Bambu</legend>
      <div className="fc-filters">
        <Field label="Matière ou gamme"><Input value={family} onChange={(e) => setFamily(e.target.value)} placeholder="PLA, PETG HF, TPU…" aria-label="Matière ou gamme" /></Field>
        <Field label="Finition"><Select value={finish} onChange={(e) => setFinish(e.target.value)} options={FINISHES} aria-label="Finition" /></Field>
        <Field label="Nom de couleur"><Input value={text} onChange={(e) => setText(e.target.value)} placeholder="rouge, jade…" aria-label="Nom de couleur" /></Field>
      </div>
      <label className="fc-near"><input type="checkbox" checked={nearest} disabled={!hex} onChange={(e) => setNearest(e.target.checked)} /> Les plus proches de {hex || 'la couleur saisie'}</label>
      <Async state={st} label="Lecture du catalogue">
        {(d) => d.colors.length === 0
          ? <p className="hint" style={{ margin: 0 }}>Aucune couleur officielle ne répond : valeur non disponible, rien n'est inventé.</p>
          : (
            <ul className="fc-grid" aria-label="Couleurs officielles">
              {d.colors.map((m: CatalogMatch) => {
                const on = m.color.id === selectedId;
                return (
                  <li key={m.color.id}>
                    <button type="button" className="fc-choice" aria-pressed={on} onClick={() => onPick(on ? undefined : m.color)} title={`${m.color.family_name} · ${m.color.hex.join(' + ')}`}>
                      <Dot c={m.color} size={28} />
                      <span className="fc-label"><strong>{colorName(m.color)}</strong><span>{m.color.family_name}{m.delta_e !== undefined ? ` · ΔE ${m.delta_e}` : ''}</span></span>
                      {on && <Check size={14} aria-hidden />}
                    </button>
                  </li>
                );
              })}
            </ul>
          )}
      </Async>
      <p className="hint" style={{ margin: 0 }}>Choisir une couleur remplit la marque, le nom et la couleur encore vides, et rattache la bobine à l'entrée du catalogue.</p>
    </fieldset>
  );
}

function FamilySheet({ f, entry }: { f: CatalogFamily; entry: CatalogEntry }) {
  const bed = Object.entries(f.bed_temp_c ?? {});
  return (
    <Panel title={f.name} actions={<><Tag>{f.finish_fr}</Tag> <Tag tone={CONF[f.confidence].tone}>{CONF[f.confidence].label}</Tag></>}>
      <dl className="kvl">
        <dt>Matière</dt><dd>{f.type || NA} <span className="hint">({f.class})</span></dd>
        <dt>Buse recommandée</dt><dd className="mono">{f.nozzle_temp_c !== undefined ? `${f.nozzle_temp_c} °C` : NA}{f.nozzle_temp_first_layer_c !== undefined && f.nozzle_temp_first_layer_c !== f.nozzle_temp_c ? ` (1re couche ${f.nozzle_temp_first_layer_c} °C)` : ''}</dd>
        <dt>Plateau</dt><dd className="mono">{bed.length ? bed.map(([k, v]) => `${BED[k] ?? k} ${v} °C`).join(' · ') : NA}</dd>
        <dt>Ventilateur</dt><dd className="mono">{f.fan_min_pct !== undefined ? `${f.fan_min_pct} à ${f.fan_max_pct ?? f.fan_min_pct} %` : NA}</dd>
        <dt>Masse volumique</dt><dd className="mono">{f.density_g_cm3 !== undefined ? `${f.density_g_cm3} g/cm³` : NA}</dd>
        <dt>Diamètre</dt><dd className="mono">{f.diameter_mm !== undefined ? `${f.diameter_mm} mm` : NA}</dd>
        <dt>Séchage</dt><dd>{f.drying_note ?? (f.drying_bed_temp_c !== undefined ? `plateau à ${f.drying_bed_temp_c} °C (profil)` : NA)}</dd>
        <dt>Buse acier trempé</dt><dd>{f.hardened_nozzle_required ? <Tag tone="warn">requise (HRC ≥ {f.required_nozzle_hrc ?? 40})</Tag> : 'non requise'}</dd>
        <dt>Buses déconseillées</dt><dd>{f.incompatible_nozzles && Object.keys(f.incompatible_nozzles).length ? Object.entries(f.incompatible_nozzles).map(([k, v]) => `${k} : ${v.join(', ')} mm`).join(' · ') : 'aucune signalée'}</dd>
        <dt>Source</dt><dd className="mono hint">{f.source.ref} · version {f.source.profile_version ?? '—'} · relevé le {f.source.retrieved}</dd>
      </dl>
      {(entry.colors?.length ?? 0) > 0 && (
        <ul className="fc-grid" aria-label={`Couleurs de ${f.name}`} style={{ marginTop: 'var(--sp-3)' }}>
          {entry.colors!.map((c) => (
            <li key={c.id}><span className="fc-choice static" title={c.id}><Dot c={c} size={28} /><span className="fc-label"><strong>{colorName(c)}</strong><span>{c.hex.join(' + ')}</span></span></span></li>
          ))}
        </ul>
      )}
    </Panel>
  );
}

function FamilyBody({ id }: { id: string }) {
  const st = useAsync((s) => filcatalogApi.entry(id, s), [id]);
  return <Async state={st} label="Chargement de la fiche">{(e) => <FamilySheet f={e.family} entry={e} />}</Async>;
}

/** Fiches du catalogue officiel (onglet « Filaments Bambu » de la page Matériaux). */
export function FilamentCatalogSheets() {
  const st = useAsync((s) => filcatalogApi.overview(s), []);
  const [sel, setSel] = useState('');
  const [cls, setCls] = useState('');
  return (
    <Async state={st} label="Lecture du catalogue des filaments Bambu">
      {(d) => {
        const classes = Array.from(new Set(d.families.map((f) => f.class)));
        const list = d.families.filter((f) => !cls || f.class === cls);
        const cur = list.find((f) => f.id === sel) ?? list[0];
        return (
          <>
            <Notice tone="info" title={`Catalogue officiel Bambu : ${d.catalog.families} gammes, ${d.catalog.colors} couleurs`}>
              Données embarquées (aucun appel réseau) lues dans les profils de Bambu Studio ({d.sources[0]?.profile_version ?? 'version inconnue'}). Une valeur absente reste « non disponible ».
            </Notice>
            <div className="mat-layout">
              <Panel title={`Gammes (${list.length})`} flush>
                <div style={{ padding: 'var(--sp-2) var(--sp-3)' }}>
                  <Select value={cls} onChange={(e) => setCls(e.target.value)} aria-label="Matière" options={[{ value: '', label: 'Toutes matières' }, ...classes.map((c) => ({ value: c, label: c }))]} />
                </div>
                <ul className="list" role="listbox" aria-label="Gammes de filament">
                  {list.map((f) => (
                    <li key={f.id} role="presentation"><button type="button" role="option" className="list-item" aria-selected={cur?.id === f.id} onClick={() => setSel(f.id)}><span className="mat-list-item"><strong>{f.name}</strong><span className="mono">{f.finish_fr} · {f.color_count} couleur(s){f.hardened_nozzle_required ? ' · acier trempé' : ''}</span></span></button></li>
                  ))}
                </ul>
              </Panel>
              {cur && <FamilyBody key={cur.id} id={cur.id} />}
            </div>
          </>
        );
      }}
    </Async>
  );
}

