import { useEffect, useMemo, useState } from 'react';
import type { ReactNode } from 'react';
import { Link, useNavigate, useSearchParams } from 'react-router-dom';
import { ArrowRight, FileWarning, GitCompare, RotateCcw, Send } from 'lucide-react';
import { Button, Panel, Segmented, Slider, Tag, Toggle, Tree, useToast } from '../components';
import type { TreeNode } from '../components';
import { Viewer3D } from '../viewer/LazyViewer3D';
import { partList } from '../viewer/geometry';
import { dimsLabel } from './modelBits';
import { MODS, PROFILE, fmtValue, getModel, labels, loadPrep, same, savePrep, seamLabel, supportLabel, workingRev } from './prepData';
import type { Overrides, SeamMode, Settings, SupportMode } from './prepData';
import './model.css';
import './prepare.css';

const KEYS: Array<keyof Settings> = ['layer', 'walls', 'infill', 'supports', 'seam', 'mods'];
type OvMap = Record<string, Overrides>; // 'obj' ou id de sous-partie

export function PreparePage() {
  const [sp] = useSearchParams();
  const m = getModel(sp.get('model') ?? 'm01');
  return <PrepareView key={m.id} id={m.id} />;
}

function PrepareView({ id }: { id: string }) {
  const m = getModel(id);
  const nav = useNavigate();
  const toast = useToast();
  const parts = useMemo(() => partList(m.geometry, m.dims), [m]);
  const initial = useMemo(() => loadPrep(id), [id]);
  const [sel, setSel] = useState<string>('obj');
  const [ov, setOv] = useState<OvMap>((): OvMap => { const r: OvMap = {}; if (initial) r.obj = initial.objectOverrides; return r; });
  const [goal, setGoal] = useState(initial?.goal ?? '');
  const winOnly = m.sync === 'windows-only';
  const base = m.versions[0].label;
  const work = workingRev(m);

  useEffect(() => { savePrep(id, { objectOverrides: ov.obj ?? {}, goal }); }, [id, ov.obj, goal]);

  const zoneLabel = (s: string) => (s === 'obj' ? m.name : parts.find((p) => p.id === s)?.label ?? s);
  const scopeName = (s: string) => (s === 'obj' ? `Objet « ${m.name} »` : `Zone « ${zoneLabel(s)} »`);

  /** Valeur effective et provenance pour la sélection courante. */
  const resolve = <K extends keyof Settings>(scope: string, k: K): { value: Settings[K]; from: string; own: boolean } => {
    if (ov[scope]?.[k] !== undefined) return { value: ov[scope][k] as Settings[K], from: scope === 'obj' ? "défini sur l'objet" : `défini sur la zone « ${zoneLabel(scope)} »`, own: true };
    if (scope !== 'obj' && ov.obj?.[k] !== undefined) return { value: ov.obj[k] as Settings[K], from: "hérité de l'objet", own: false };
    return { value: PROFILE.values[k], from: `hérité du profil « ${PROFILE.name} »`, own: false };
  };
  const setKey = <K extends keyof Settings>(k: K, v: Settings[K]) => setOv((o) => ({ ...o, [sel]: { ...o[sel], [k]: v } }));
  const clearKey = (k: keyof Settings) => setOv((o) => { const n = { ...o[sel] }; delete n[k]; return { ...o, [sel]: n }; });

  const diffs = Object.entries(ov).flatMap(([scope, o]) => KEYS.filter((k) => o[k] !== undefined).map((k) => {
    const parent = scope !== 'obj' && ov.obj?.[k] !== undefined ? (ov.obj[k] as Settings[typeof k]) : PROFILE.values[k];
    return { scope, k, from: parent, to: o[k] as Settings[typeof k] };
  })).filter((d) => !same(d.from, d.to));

  const modified = (s: string) => !!ov[s] && Object.keys(ov[s]).length > 0;
  const tree: TreeNode[] = [{
    id: 'obj',
    label: <span className="pr-node"><strong>{m.name}</strong><span className="pr-sub">objet</span>{modified('obj') && <Tag tone="accent">modifié</Tag>}</span>,
    children: parts.map((p) => ({ id: p.id, label: <span className="pr-node"><span>{p.label}</span><span className="pr-sub">zone</span>{modified(p.id) && <Tag tone="accent">modifié</Tag>}</span> })),
  }];

  const reset = () => { setOv({}); toast('info', `Copie de travail ${work} réinitialisée sur ${base} — simulation.`); };

  return (
    <>
      <div className="mdl-head">
        <div>
          <nav className="mdl-crumb" aria-label="Fil d'Ariane"><Link to="/bibliotheque">Bibliothèque</Link><span aria-hidden>/</span><Link to={`/modele/${m.id}`}>{m.name}</Link><span aria-hidden>/</span><span>Préparation</span></nav>
          <h1>Préparation</h1>
        </div>
        <div className="mdl-actions">
          <Button icon={<GitCompare size={15} />} onClick={() => nav(`/comparaison?model=${m.id}`)}>Créer des variantes</Button>
          <Button variant="primary" icon={<Send size={15} />} disabled={winOnly} onClick={() => nav('/impression')}>Valider et envoyer</Button>
        </div>
      </div>

      {winOnly && (
        <div className="notice notice-warn pr-notice" role="status"><FileWarning size={16} aria-hidden /><div><h3>Fichier non copié sur Linux</h3><p>La préparation est bloquée tant que le fichier n'est pas copié. Valeurs de démonstration, rien n'est envoyé.</p></div></div>
      )}

      <div className="pr">
        <div className="pr-main">
          <div className="pr-view"><Viewer3D geometry={m.geometry} dims={m.dims} selectedPartId={sel === 'obj' ? null : sel} onSelectPart={(p) => setSel(p ?? 'obj')} /></div>
          <p className="hint">Cliquez une sous-partie dans le viewer ou dans l'arbre ; un clic dans le vide revient à l'objet entier. Géométrie générée à partir du type de forme de la démo. Boîte englobante <span className="mono">{dimsLabel(m.dims)}</span>.</p>

          <div className="pr-pair">
            <Panel title="Objets et zones" flush>
              <Tree label="Objets et zones" nodes={tree} selectedId={sel} onSelect={setSel} />
            </Panel>
            <Panel title={`Différences avec le profil (${diffs.length})`}>
              {diffs.length === 0 ? <p className="pr-empty">Aucune différence : tous les réglages sont hérités du profil.</p> : (
                <ul className="pr-diffs">
                  {diffs.map((d) => (
                    <li key={d.scope + d.k}>
                      <span className="pr-diff-h"><strong>{labels[d.k]}</strong><span className="pr-sub">{scopeName(d.scope)}</span></span>
                      <span className="mono pr-diff-v">{fmtValue(d.k, d.from)} <ArrowRight size={12} aria-hidden /> {fmtValue(d.k, d.to)}</span>
                    </li>
                  ))}
                </ul>
              )}
            </Panel>
          </div>
        </div>

        <div className="pr-side">
          <Panel title="Révision de travail">
            <p className="pr-rev"><span className="mono">copie de travail {base} → {work}</span></p>
            <p className="hint">L'original <span className="mono">{base}</span> (empreinte <span className="mono">{m.versions[0].sha256Short}…</span>) est conservé et n'est jamais modifié. Les changements s'écrivent dans <span className="mono">{work}</span>. {diffs.length} réglage(s) modifié(s).</p>
            <Button size="sm" variant="ghost" icon={<RotateCcw size={14} />} onClick={reset} disabled={diffs.length === 0}>Revenir à {base}</Button>
          </Panel>

          <Panel title="Réglages">
            <div className="pr-sel" role="status" aria-live="polite">
              <span className="eyebrow">Sélection courante</span>
              <strong>{scopeName(sel)}</strong>
              <span className="pr-sub">Les réglages ci-dessous ne s'appliquent qu'à cette sélection.</span>
              {sel !== 'obj' && <Button size="sm" variant="ghost" onClick={() => setSel('obj')}>Sélectionner l'objet entier</Button>}
            </div>

            <Row k="layer" r={resolve(sel, 'layer')} clear={clearKey}>
              <Slider label="Hauteur de couche" value={resolve(sel, 'layer').value} onChange={(v) => setKey('layer', v)} min={0.08} max={0.32} step={0.04} unit="mm" />
            </Row>
            <Row k="walls" r={resolve(sel, 'walls')} clear={clearKey}>
              <Slider label="Parois" value={resolve(sel, 'walls').value} onChange={(v) => setKey('walls', v)} min={1} max={6} />
            </Row>
            <Row k="infill" r={resolve(sel, 'infill')} clear={clearKey}>
              <Slider label="Remplissage" value={resolve(sel, 'infill').value} onChange={(v) => setKey('infill', v)} min={5} max={40} step={5} unit="%" />
            </Row>
            <Row k="supports" r={resolve(sel, 'supports')} clear={clearKey}>
              <span className="pr-lbl">Supports</span>
              <Segmented<SupportMode> label="Supports" value={resolve(sel, 'supports').value} onChange={(v) => setKey('supports', v)} options={(Object.keys(supportLabel) as SupportMode[]).map((v) => ({ value: v, label: supportLabel[v] }))} />
            </Row>
            <Row k="seam" r={resolve(sel, 'seam')} clear={clearKey}>
              <span className="pr-lbl">Coutures</span>
              <Segmented<SeamMode> label="Coutures" value={resolve(sel, 'seam').value} onChange={(v) => setKey('seam', v)} options={(Object.keys(seamLabel) as SeamMode[]).map((v) => ({ value: v, label: seamLabel[v] }))} />
            </Row>
            <Row k="mods" r={resolve(sel, 'mods')} clear={clearKey}>
              <span className="pr-lbl">Modificateurs</span>
              <div className="pr-mods">
                {MODS.map((mod) => {
                  const cur = resolve(sel, 'mods').value;
                  return <Toggle key={mod} label={mod} checked={cur.includes(mod)} onChange={(on) => setKey('mods', on ? [...cur, mod] : cur.filter((x) => x !== mod))} />;
                })}
              </div>
            </Row>
            <p className="hint">Peinture exacte des supports et coutures : module à venir, non simulé ici.</p>
          </Panel>

          <Panel title="Objectif de finition">
            <label className="pr-goal">
              <span className="sr-only">Objectif de finition</span>
              <textarea className="input" rows={3} value={goal} onChange={(e) => setGoal(e.target.value)} placeholder="Ex. : face avant lisse, charnière solide, emboîtement serré" />
            </label>
            <p className="hint">Texte libre, conservé tel quel avec la copie de travail. Il n'est pas interprété ni converti en réglages.</p>
          </Panel>

          <Panel title="Profil de base">
            <dl className="dl">
              <dt>Profil</dt><dd>{PROFILE.name}</dd>
              <dt>Chaîne</dt><dd>{PROFILE.chain.join(' → ')}</dd>
              {KEYS.map((k) => <ProfileRow key={k} k={k} />)}
            </dl>
            <p className="hint">Valeurs de démonstration, non issues d'un profil Bambu Studio réel.</p>
          </Panel>
        </div>
      </div>
    </>
  );
}

function ProfileRow({ k }: { k: keyof Settings }) {
  return <><dt>{labels[k]}</dt><dd className="mono">{fmtValue(k, PROFILE.values[k])}</dd></>;
}

function Row({ k, r, clear, children }: { k: keyof Settings; r: { from: string; own: boolean }; clear: (k: keyof Settings) => void; children: ReactNode }) {
  return (
    <div className="pr-row">
      {children}
      <div className="pr-prov">
        <span>{r.own ? <Tag tone="accent">{r.from}</Tag> : r.from}</span>
        {r.own && <button type="button" className="pr-reset" onClick={() => clear(k)}>Rétablir</button>}
      </div>
    </div>
  );
}
