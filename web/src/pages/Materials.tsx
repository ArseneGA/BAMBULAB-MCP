import { useMemo, useState } from 'react';
import { PendingBanner } from './common';
import type { ReactNode } from 'react';
import { Button, EmptyState, Field, List, Panel, Select, Tabs, Tag } from '../components';
import { demoMaterials, demoPrinter } from '../data/demo';
import type { DemoMaterial } from '../data/demo';
import './materials.css';

const Unknown = () => <span className="unknown">inconnu</span>;
const val = (v: ReactNode | null | undefined) => (v === null || v === undefined || v === '' ? <Unknown /> : <>{v}</>);
const range = (r: [number, number] | null, unit: string) => (r ? <span className="mono">{r[0]} à {r[1]} {unit}</span> : <Unknown />);
const bool = (b: boolean | null) => (b === null ? <Unknown /> : b ? 'oui' : 'non');

/** Source et date des recommandations fabricant : DÉMO, fictives. */
const demoSources: Record<string, { source: string; date: string } | null> = {
  mat1: { source: 'Fiche technique fabricant démo A (fictive)', date: '2026-09-14' },
  mat2: { source: 'Fiche technique fabricant démo B (fictive)', date: '2026-08-02' },
  mat3: null,
  mat4: { source: 'Page produit fabricant démo C (fictive)', date: '2026-07-21' },
  mat5: null,
};

function MaterialSheet({ m }: { m: DemoMaterial }) {
  const src = demoSources[m.id];
  const compatTone = m.p1sCompatibility === 'compatible déclaré' ? 'ok' : m.p1sCompatibility === 'inconnu' ? undefined : 'warn';
  return (
    <div className="stack">
      <Panel title={`Fiche matériau — ${m.family}`} actions={<Tag>démo</Tag>}>
        <dl className="kv">
          <dt>Famille</dt><dd>{m.family}</dd>
          <dt>Fabricant</dt><dd>{val(m.maker)}</dd>
          <dt>Référence</dt><dd className="mono">{val(m.reference)}</dd>
          <dt>Couleur / formulation</dt><dd>{val(m.colour)}</dd>
          <dt>Diamètre</dt><dd className="mono">{m.diameterMm === null ? <Unknown /> : `${m.diameterMm} mm`}</dd>
        </dl>
        <p className="src-note" style={{ marginTop: 'var(--sp-3)' }}>La famille seule ne garantit pas les propriétés de cette bobine.</p>
      </Panel>
      <Panel title="Recommandations fabricant">
        <dl className="kv">
          <dt>Température buse</dt><dd>{range(m.nozzleTempC, '°C')}</dd>
          <dt>Température plateau</dt><dd>{range(m.bedTempC, '°C')}</dd>
          <dt>Diamètre de buse minimal</dt><dd className="mono">{m.nozzleMinMm === null ? <Unknown /> : `${m.nozzleMinMm} mm`}</dd>
          <dt>Enceinte conseillée</dt><dd>{bool(m.enclosureAdvised)}</dd>
          <dt>Buse durcie requise</dt><dd>{bool(m.hardenedNozzleRequired)}</dd>
          <dt>Compatibilité P1S</dt><dd><Tag tone={compatTone}>{m.p1sCompatibility}</Tag></dd>
          <dt>Source</dt><dd>{src ? src.source : <Unknown />}</dd>
          <dt>Date de la source</dt><dd className="mono">{src ? src.date : <Unknown />}</dd>
        </dl>
        <p className="src-note" style={{ marginTop: 'var(--sp-3)' }}>Aucun paramètre n'est repris d'une autre référence.</p>
      </Panel>
      <Panel title="Bobine et calibrations">
        <dl className="kv">
          <dt>État de la bobine</dt><dd>{val(m.spoolState)}</dd>
          <dt>Calibrations</dt>
          <dd>
            {m.calibrations.length === 0 ? <span className="unknown">aucune calibration enregistrée</span> : (
              <ul style={{ margin: 0, paddingLeft: 'var(--sp-4)' }}>
                {m.calibrations.map((c) => <li key={c.date}><span className="mono">{c.date}</span> — {c.note}</li>)}
              </ul>
            )}
          </dd>
        </dl>
      </Panel>
    </div>
  );
}

function MachineSheet() {
  const p = demoPrinter;
  return (
    <Panel title={`Fiche machine — ${p.model}`} actions={<Tag>démo</Tag>}>
      <dl className="kv">
        <dt>Modèle</dt><dd>{p.model}</dd>
        <dt>Firmware</dt><dd className="mono">{val(p.firmware)}</dd>
        <dt>Buse</dt><dd>{p.nozzle ? <span className="mono">{p.nozzle.diameterMm} mm, {p.nozzle.type}</span> : <Unknown />}</dd>
        <dt>Plateau</dt><dd>{val(p.plate)}</dd>
        <dt>AMS</dt><dd>{val(p.ams)}</dd>
        <dt>Voie LAN / Developer Mode</dt><dd><Unknown /></dd>
        <dt>Capacités vérifiées</dt><dd><span className="unknown">aucune vérification enregistrée</span></dd>
      </dl>
      <p className="src-note" style={{ marginTop: 'var(--sp-3)' }}>Buse : valeur de démonstration, à confirmer sur la machine réelle.</p>
    </Panel>
  );
}

/* ---------- Objectifs de finition (décomposition SIMULÉE) ---------- */
type Dim = 'materiau' | 'fonction' | 'finition' | 'contraintes';
type Prio = 'non-negociable' | 'haute' | 'moyenne' | 'basse';
const dimLabel: Record<Dim, string> = { materiau: 'Matériau', fonction: 'Fonction', finition: 'Finition', contraintes: 'Contraintes de production' };
const dimOrder: Dim[] = ['materiau', 'fonction', 'finition', 'contraintes'];
const prioOptions = [
  { value: 'non-negociable', label: 'Non négociable' }, { value: 'haute', label: 'Haute' },
  { value: 'moyenne', label: 'Moyenne' }, { value: 'basse', label: 'Basse' },
];
const rules: Array<{ dim: Dim; re: RegExp }> = [
  { dim: 'materiau', re: /(^|[^a-zà-ÿ])(pla|petg|abs|asa|tpu|nylon|pa-?cf|matière|matiere|filament|souple|flexible|rigide|bobine)([^a-zà-ÿ]|$)/i },
  { dim: 'contraintes', re: /(heure|minute|temps|rapide|buse|p1s|ams|support|\bmm\b|taille|volume|budget|plateau)/i },
  { dim: 'finition', re: /(lisse|\bmat\b|brillant|poli|transparent|translucide|opaque|couleur|surface|aspect|rendu|texture|ponç|esthétique|finition)/i },
  { dim: 'fonction', re: /(résist|resist|supporte|charge|clip|emboît|emboit|étanche|etanche|glisse|frott|flexion|usage|assembl|tient|solide|précis|precis)/i },
];
interface Crit { id: string; dim: Dim | 'preciser'; text: string; prio: Prio; evaluable: boolean; why: string }

function decompose(text: string): Crit[] {
  const parts = text.split(/[.;\n]|,\s|\bet\b|\bmais\b/i).map((s) => s.trim()).filter((s) => s.length > 2);
  return parts.map((t, i) => {
    const hit = rules.find((r) => r.re.test(t));
    const dim: Crit['dim'] = hit ? hit.dim : 'preciser';
    const fam = demoMaterials.filter((m) => new RegExp(`(^|[^a-z])${m.family}([^a-z]|$)`, 'i').test(t));
    const nonNeg = /(impératif|obligatoire|doit|absolument|indispensable)/i.test(t);
    let evaluable = false;
    let why = "Objectif non encore évaluable : aucune mesure, règle ni donnée de calibration n'est associée à ce critère.";
    if (dim === 'materiau' && fam.length) { evaluable = true; why = `Filtrable par famille : ${fam.length} fiche(s) concernée(s) dans la liste de démonstration. Compatibilité réelle non évaluée.`; }
    if (dim === 'preciser') why = 'Phrase non rattachée à un critère : à reformuler ou à préciser.';
    return { id: `c${i}`, dim, text: t, prio: nonNeg ? 'non-negociable' : 'moyenne', evaluable, why };
  });
}

function Objectives() {
  const [text, setText] = useState('');
  const [crits, setCrits] = useState<Crit[] | null>(null);
  const grouped = useMemo(() => (crits ? ([...dimOrder, 'preciser'] as const).map((d) => ({ d, items: crits.filter((c) => c.dim === d) })) : []), [crits]);
  const setPrio = (id: string, p: Prio) => setCrits((l) => l && l.map((c) => (c.id === id ? { ...c, prio: p } : c)));
  return (
    <div className="stack">
      <Panel title="Résultat voulu" actions={<Tag>décomposition simulée</Tag>}>
        <div className="stack">
          <Field label="Décris librement le résultat que tu veux">
            <textarea className="textarea" value={text} onChange={(e) => { setText(e.target.value); setCrits(null); }} placeholder="Exemple : une pièce qui supporte des frottements, d'aspect mat, imprimable en moins de quatre heures." />
          </Field>
          <div className="row-wrap">
            <Button variant="primary" disabled={text.trim().length < 3} onClick={() => setCrits(decompose(text))}>Décomposer en critères</Button>
            {crits && <Button variant="ghost" onClick={() => { setText(''); setCrits(null); }}>Effacer</Button>}
          </div>
          <p className="sim-note">Prototype : la décomposition est faite par de simples mots-clés, sans modèle de langage. Elle ne choisit aucun matériau et n'attribue aucun score.</p>
        </div>
      </Panel>
      {crits && crits.length === 0 && <EmptyState title="Rien à décomposer" text="Écris au moins une phrase." />}
      {crits && crits.length > 0 && (
        <div className="crit-grid">
          {grouped.filter(({ d, items }) => d !== 'preciser' || items.length > 0).map(({ d, items }) => (
            <section key={d} className="crit" aria-label={d === 'preciser' ? 'À préciser' : dimLabel[d]}>
              <h4>{d === 'preciser' ? 'À préciser' : dimLabel[d]}</h4>
              {items.length === 0 ? <p className="unknown">Aucun critère exprimé pour cette dimension.</p> : items.map((c) => (
                <div key={c.id} className="crit-item">
                  <blockquote>{c.text}</blockquote>
                  <Field label="Priorité"><Select value={c.prio} onChange={(e) => setPrio(c.id, e.target.value as Prio)} options={prioOptions} /></Field>
                  <div className="row-wrap">{c.evaluable ? <Tag tone="ok">évaluable en partie</Tag> : <Tag tone="warn">objectif non encore évaluable</Tag>}</div>
                  <p className="src-note">{c.why}</p>
                </div>
              ))}
            </section>
          ))}
        </div>
      )}
    </div>
  );
}

export function MaterialsPage() {
  const [tab, setTab] = useState<'materiaux' | 'machine' | 'objectifs'>('materiaux');
  const [sel, setSel] = useState<string | null>(null);
  const items = [...demoMaterials].sort((a, b) => a.family.localeCompare(b.family));
  const current = items.find((m) => m.id === sel);
  return (
    <>
      <PendingBanner screen="Matériaux" />
      <div className="page-head"><span className="eyebrow">Espace</span><h1>Matériaux</h1><p>Fiches matériau, fiche machine et objectifs de finition. Une valeur absente reste « inconnu » : rien n'est déduit d'une autre référence.</p></div>
      <div className="stack">
        <Tabs label="Sections" value={tab} onChange={setTab} tabs={[{ value: 'materiaux', label: 'Matériaux' }, { value: 'machine', label: 'Machine' }, { value: 'objectifs', label: 'Objectifs' }]} />
        {tab === 'materiaux' && (
          <div className="mat-layout">
            <Panel title="Bobines (démo)" flush>
              <List label="Matériaux" items={items} getKey={(m) => m.id} selectedKey={sel ?? undefined} onSelect={(m) => setSel(m.id)}
                render={(m) => <span className="mat-list-item"><strong>{m.family}</strong><span>{m.reference ?? 'référence inconnue'}</span></span>} />
            </Panel>
            {current ? <MaterialSheet m={current} /> : <EmptyState title="Aucun matériau sélectionné" text="Choisis une fiche dans la liste. Aucune n'est recommandée par défaut." />}
          </div>
        )}
        {tab === 'machine' && <MachineSheet />}
        {tab === 'objectifs' && <Objectives />}
      </div>
    </>
  );
}
