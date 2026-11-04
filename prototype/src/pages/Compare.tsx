import { useMemo, useState } from 'react';
import { Link, useNavigate, useSearchParams } from 'react-router-dom';
import { AlertTriangle, Check, FileWarning, SlidersHorizontal } from 'lucide-react';
import { Button, Panel, Segmented, Tag, Toggle, useToast } from '../components';
import { Viewer3D } from '../viewer/LazyViewer3D';
import type { CameraState } from '../viewer/LazyViewer3D';
import { dimsLabel } from './modelBits';
import { PROFILE, VARIANTS, estimate, fmtNum, fmtTime, fmtValue, getModel, labels, loadPrep, workingRev } from './prepData';
import type { Settings, Variant } from './prepData';
import './model.css';
import './compare.css';

type Mode = 'side' | 'switch';
const KEYS: Array<keyof Settings> = ['layer', 'walls', 'infill', 'supports', 'seam'];

export function ComparePage() {
  const [sp] = useSearchParams();
  const m = getModel(sp.get('model') ?? 'm01');
  return <CompareView key={m.id} id={m.id} />;
}

function CompareView({ id }: { id: string }) {
  const m = getModel(id);
  const nav = useNavigate();
  const toast = useToast();
  const [mode, setMode] = useState<Mode>('side');
  const [active, setActive] = useState<Variant['id']>('A');
  const [cam, setCam] = useState<CameraState | null>(null);
  const [simConflict, setSimConflict] = useState(false);
  const [expected, setExpected] = useState(workingRev(m));
  const [applied, setApplied] = useState<{ v: Variant['id']; rev: string } | null>(null);
  const [conflict, setConflict] = useState<{ expected: string; current: string } | null>(null);
  const base = m.versions[0].label;
  const work = workingRev(m);
  const current = simConflict ? `${base}.2` : work;
  const winOnly = m.sync === 'windows-only';

  /* Base = profil + réglages d'objet de la copie de travail (mémoire de session de la Préparation). */
  const prep = useMemo(() => loadPrep(id), [id]);
  const baseSettings: Settings = useMemo(() => ({ ...PROFILE.values, ...(prep?.objectOverrides ?? {}) }), [prep]);
  const rows = useMemo(() => VARIANTS.map((v) => {
    const s: Settings = { ...baseSettings, ...v.delta };
    return { v, s, e: estimate(m, s) };
  }), [m, baseSettings]);
  const cur = rows.find((r) => r.v.id === active)!;
  const differs = (k: keyof Settings) => new Set(rows.map((r) => JSON.stringify(r.s[k]))).size > 1;

  const apply = () => {
    if (current !== expected) { setConflict({ expected, current }); return; }
    setConflict(null);
    const rev = `${base}.${Number(expected.split('.')[1]) + 1}`;
    setApplied({ v: active, rev });
    toast('ok', `Variante ${active} appliquée : ${expected} → ${rev} — simulation.`);
  };
  const reread = () => { setExpected(current); setConflict(null); toast('info', `Révision ${current} relue. Vous pouvez réappliquer.`); };

  const metric = (label: string, f: (r: (typeof rows)[number]) => string) => (
    <tr><th scope="row">{label}<span className="cp-est">estimation slicer (démo)</span></th>{rows.map((r) => <td key={r.v.id} className="mono">{f(r)}</td>)}</tr>
  );

  return (
    <>
      <div className="mdl-head">
        <div>
          <nav className="mdl-crumb" aria-label="Fil d'Ariane"><Link to="/bibliotheque">Bibliothèque</Link><span aria-hidden>/</span><Link to={`/modele/${m.id}`}>{m.name}</Link><span aria-hidden>/</span><span>Comparaison</span></nav>
          <h1>Comparaison</h1>
        </div>
        <div className="mdl-actions">
          <Button icon={<SlidersHorizontal size={15} />} onClick={() => nav(`/preparation?model=${m.id}`)}>Retour à la préparation</Button>
        </div>
      </div>

      {winOnly && (
        <div className="notice notice-warn cp-gap" role="status"><FileWarning size={16} aria-hidden /><div><h3>Fichier non copié sur Linux</h3><p>Les variantes sont affichées à titre indicatif ; leur application reste bloquée tant que le fichier n'est pas copié.</p></div></div>
      )}

      <div className="cp-bar">
        <Segmented<Mode> label="Mode d'affichage" value={mode} onChange={setMode} options={[{ value: 'side', label: 'Côte à côte' }, { value: 'switch', label: 'Bascule' }]} />
        {mode === 'switch' && <Segmented<Variant['id']> label="Variante affichée" value={active} onChange={setActive} options={VARIANTS.map((v) => ({ value: v.id, label: v.name }))} />}
        <span className="cp-sub">Base : copie de travail <span className="mono">{work}</span> ({prep ? 'avec vos réglages de préparation' : 'profil seul'}), original <span className="mono">{base}</span> conservé.</span>
      </div>

      {mode === 'side' ? (
        <div className="cp-views">
          {rows.map((r) => (
            <div key={r.v.id} className={`cp-cell${active === r.v.id ? ' cp-cell-on' : ''}`}>
              <button type="button" className="cp-pick" aria-pressed={active === r.v.id} onClick={() => setActive(r.v.id)}>
                <strong>{r.v.name}</strong><span className="cp-sub">{r.v.intent}</span>
              </button>
              <div className="cp-view"><Viewer3D compact geometry={m.geometry} dims={m.dims} camera={cam} onCameraChange={setCam} /></div>
            </div>
          ))}
        </div>
      ) : (
        <div className="cp-cell cp-cell-on">
          <div className="cp-view cp-view-big"><Viewer3D geometry={m.geometry} dims={m.dims} camera={cam} onCameraChange={setCam} /></div>
        </div>
      )}
      <p className="hint">Géométrie <span className="mono">{dimsLabel(m.dims)}</span> générée à partir du type de forme de la démo : les réglages ne changent pas la forme, l'aperçu est donc identique entre variantes.
        {mode === 'side' ? ' En côte à côte, les trois viewers partagent la même caméra : déplacez-en un, les autres suivent.' : ' La caméra est conservée en changeant de variante.'}</p>

      <div className="cp">
        <Panel title="Réglages comparés" flush>
          <div className="cp-scroll">
            <table className="cp-table">
              <thead><tr><th scope="col"><span className="sr-only">Réglage</span></th>{rows.map((r) => <th scope="col" key={r.v.id} aria-current={active === r.v.id}>{r.v.name}</th>)}</tr></thead>
              <tbody>
                {KEYS.map((k) => (
                  <tr key={k} className={differs(k) ? 'cp-diff' : undefined}>
                    <th scope="row">{labels[k]}{differs(k) && <span className="cp-est">diffère</span>}</th>
                    {rows.map((r) => <td key={r.v.id} className="mono">{fmtValue(k, r.s[k])}</td>)}
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </Panel>

        <Panel title="Mesures disponibles d'un slicer" flush>
          <div className="cp-scroll">
            <table className="cp-table">
              <thead><tr><th scope="col"><span className="sr-only">Mesure</span></th>{rows.map((r) => <th scope="col" key={r.v.id}>{r.v.name}</th>)}</tr></thead>
              <tbody>
                {metric('Temps estimé', (r) => fmtTime(r.e.minutes))}
                {metric('Matière', (r) => `${fmtNum(r.e.grams)} g · ${fmtNum(r.e.metres)} m`)}
                {metric('Nombre de couches', (r) => String(r.e.layers))}
                {metric('Volume de supports', (r) => `${fmtNum(r.e.supportCm3)} cm³`)}
                <tr className="cp-unk"><th scope="row">Résistance mécanique</th>{rows.map((r) => <td key={r.v.id}><Tag tone="warn">à vérifier par essai</Tag></td>)}</tr>
                <tr className="cp-unk"><th scope="row">Aspect de surface</th>{rows.map((r) => <td key={r.v.id}><Tag tone="warn">à vérifier par essai</Tag></td>)}</tr>
              </tbody>
            </table>
          </div>
          <p className="hint cp-pad">Valeurs calculées par une formule de démonstration, pas par un slicer. Résistance et aspect ne s'évaluent pas par aperçu : aucun chiffre n'est avancé. Aucune variante n'est désignée meilleure ; le choix vous revient.</p>
        </Panel>
      </div>

      <Panel title="Appliquer une variante">
        <div className="cp-apply">
          <div className="cp-apply-info">
            <Segmented<Variant['id']> label="Variante à appliquer" value={active} onChange={setActive} options={VARIANTS.map((v) => ({ value: v.id, label: v.id }))} />
            <dl className="dl">
              <dt>Variante</dt><dd>{cur.v.name} — {cur.v.intent}</dd>
              <dt>Révision attendue</dt><dd className="mono">{expected}</dd>
              <dt>Révision courante</dt><dd className="mono">{current}</dd>
            </dl>
            <Toggle label="Simuler une modification concurrente (la révision courante change)" checked={simConflict} onChange={(v) => { setSimConflict(v); setConflict(null); setApplied(null); }} />
          </div>
          <div className="cp-apply-act">
            <Button variant="primary" icon={<Check size={15} />} disabled={winOnly} onClick={apply}>Appliquer cette variante</Button>
            <span className="cp-sub">Écrit dans la copie de travail seulement si la révision attendue est toujours la courante.</span>
          </div>
        </div>
        {conflict && (
          <div className="notice notice-err cp-gap" role="alert">
            <AlertTriangle size={16} aria-hidden />
            <div>
              <h3>Conflit de révision : rien n'a été appliqué</h3>
              <p>La variante a été construite sur <span className="mono">{conflict.expected}</span>, mais le projet est maintenant en <span className="mono">{conflict.current}</span>. Relisez la révision courante avant de réappliquer, pour ne pas écraser le changement concurrent.</p>
              <div className="acts"><Button size="sm" onClick={reread}>Relire {conflict.current}</Button><Button size="sm" variant="ghost" onClick={() => setConflict(null)}>Fermer</Button></div>
            </div>
          </div>
        )}
        {applied && (
          <div className="notice cp-gap" role="status">
            <Check size={16} aria-hidden />
            <div><h3>Variante {applied.v} appliquée</h3><p>Nouvelle révision de travail <span className="mono">{applied.rev}</span> (simulation). L'original <span className="mono">{base}</span> reste inchangé.</p>
              <div className="acts"><Button size="sm" onClick={() => nav(`/preparation?model=${m.id}`)}>Ouvrir en préparation</Button></div></div>
          </div>
        )}
      </Panel>
    </>
  );
}
