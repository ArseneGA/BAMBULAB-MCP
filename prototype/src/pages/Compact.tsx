import { useMemo, useState } from 'react';
import { Link } from 'react-router-dom';
import { ArrowUpRight, Check, FileText, Printer } from 'lucide-react';
import { Button, Select, Tabs, Tag, useToast } from '../components';
import { Viewer3D } from '../viewer/LazyViewer3D';
import { partList } from '../viewer/geometry';
import { demoMaterials, demoModels, demoPrinter } from '../data/demo';
import './compact.css';

type Turn = 't1' | 't2' | 't3' | 't4';
const pince = demoModels.find((m) => m.id === 'm01')!;
const results = ['m01', 'm12', 'm07'].map((id) => demoModels.find((m) => m.id === id)!);
const PINCE_LABEL = 'Pince de maintien 25 mm v3';

const USER_MSG: Record<Turn, string> = {
  t1: 'Retrouve ma pince.',
  t2: 'Ouvre la dernière version.',
  t3: 'Compare deux variantes de la charnière.',
  t4: "Prépare l'envoi sur la P1S.",
};
const ASSIST_MSG: Record<Turn, string> = {
  t1: "J'ai trouvé 3 modèles proches dans la bibliothèque. Choisis celui que tu veux.",
  t2: 'Voici la v3 de la pince. Tu peux la faire tourner et sélectionner une zone.',
  t3: 'Deux variantes de la charnière. Les chiffres du slicer sont des estimations de démonstration.',
  t4: 'Le fichier pince_25_v3.gcode.3mf est envoyé sur la P1S. Veux-tu lancer cette impression ?',
};
const OPEN_LINK: Record<Turn, { to: string; label: string }> = {
  t1: { to: '/bibliotheque', label: "Ouvrir dans l'atelier" },
  t2: { to: '/modele/m01', label: "Ouvrir dans l'atelier" },
  t3: { to: '/comparaison', label: "Ouvrir dans l'atelier" },
  t4: { to: '/impression', label: "Ouvrir dans l'atelier" },
};

function ContextLine({ value }: { value: string | null }) {
  return (
    <div className="cp-context" role="status">
      <span className="cp-context-k">Sélection transmise au contexte :</span>{' '}
      <strong>{value ?? 'aucune'}</strong>
    </div>
  );
}

function OpenLink({ turn }: { turn: Turn }) {
  const l = OPEN_LINK[turn];
  return <Link className="cp-open" to={l.to}>{l.label}<ArrowUpRight size={13} aria-hidden /></Link>;
}

/* ---------- Tour 1 ---------- */
function Results({ sel, onSel }: { sel: string | null; onSel: (id: string) => void }) {
  const m = sel ? demoModels.find((x) => x.id === sel) : undefined;
  return (
    <>
      <ul className="cp-results" aria-label="Résultats de recherche">
        {results.map((r) => (
          <li key={r.id}>
            <button type="button" className="cp-result" aria-pressed={sel === r.id} onClick={() => onSel(r.id)}>
              <span className="cp-result-name">{r.name}</span>
              <span className="cp-result-meta mono">{r.versions[0].label} · {r.dims.x}×{r.dims.y}×{r.dims.z} mm</span>
              <span className="cp-result-tags">{r.sync === 'windows-only' ? <Tag tone="warn">Windows seul</Tag> : <Tag>{r.collection}</Tag>}</span>
            </button>
          </li>
        ))}
      </ul>
      <ContextLine value={m ? `${m.name} ${m.versions[0].label}` : null} />
    </>
  );
}

/* ---------- Tour 2 ---------- */
function Sheet({ part, onPart }: { part: string | null; onPart: (id: string | null) => void }) {
  const parts = useMemo(() => partList(pince.geometry, pince.dims), []);
  return (
    <>
      <div className="cp-sheet">
        <div className="cp-viewer"><Viewer3D compact geometry={pince.geometry} dims={pince.dims} selectedPartId={part} onSelectPart={onPart} /></div>
        <div className="cp-side">
          <h4>{pince.name} <span className="mono">v3</span></h4>
          <dl className="cp-dims mono">
            <div><dt>X</dt><dd>{pince.dims.x} mm</dd></div>
            <div><dt>Y</dt><dd>{pince.dims.y} mm</dd></div>
            <div><dt>Z</dt><dd>{pince.dims.z} mm</dd></div>
          </dl>
          <div className="cp-sub-label">Sous-parties</div>
          <ul className="cp-parts">
            {parts.map((p) => (
              <li key={p.id}><button type="button" aria-pressed={part === p.id} onClick={() => onPart(part === p.id ? null : p.id)}>{p.label}</button></li>
            ))}
          </ul>
        </div>
      </div>
      <ContextLine value={part ? `${PINCE_LABEL} · ${parts.find((p) => p.id === part)?.label ?? part}` : PINCE_LABEL} />
    </>
  );
}

/* ---------- Tour 3 ---------- */
function Compare() {
  return (
    <>
      <table className="cp-table">
        <thead><tr><th scope="col"><span className="sr-only">Critère</span></th><th scope="col">Variante A — charnière renforcée</th><th scope="col">Variante B — charnière allégée</th></tr></thead>
        <tbody>
          <tr><th scope="row">Durée estimée <Tag tone="warn">estimation démo</Tag></th><td className="mono">1 h 42</td><td className="mono">1 h 28</td></tr>
          <tr><th scope="row">Matière estimée <Tag tone="warn">estimation démo</Tag></th><td className="mono">14,2 g</td><td className="mono">11,8 g</td></tr>
          <tr><th scope="row">Supports <Tag tone="warn">estimation démo</Tag></th><td className="mono">aucun</td><td className="mono">3 zones</td></tr>
          <tr><th scope="row">Solidité de la charnière</th><td className="cp-check">à vérifier par essai</td><td className="cp-check">à vérifier par essai</td></tr>
          <tr><th scope="row">Jeu d'emboîtement</th><td className="cp-check">à vérifier par essai</td><td className="cp-check">à vérifier par essai</td></tr>
        </tbody>
      </table>
      <p className="cp-note">Les critères non chiffrés ne sont pas calculés : ils se décident après impression d'essai.</p>
    </>
  );
}

/* ---------- Tour 4 ---------- */
function Send() {
  const toast = useToast();
  const [mat, setMat] = useState(demoMaterials[0].id);
  const [plate, setPlate] = useState('unknown');
  const [empty, setEmpty] = useState(false);
  const [done, setDone] = useState(false);
  const reset = () => { setEmpty(false); setDone(false); };
  const m = demoMaterials.find((x) => x.id === mat)!;
  const ready = empty && plate !== 'unknown';
  return (
    <>
      <div className="cp-file"><FileText size={14} aria-hidden /><span className="mono">pince_25_v3.gcode.3mf</span><Tag tone="ok">envoyé (simulé)</Tag></div>
      <dl className="cp-kv">
        <div><dt>Imprimante</dt><dd><Printer size={13} aria-hidden /> {demoPrinter.name}</dd></div>
        <div><dt>Empreinte</dt><dd className="mono">sha256 9f2c1a…</dd></div>
        <div><dt>Plateau</dt><dd><Select aria-label="Plateau" value={plate} onChange={(e) => { setPlate(e.target.value); reset(); }} options={[{ value: 'unknown', label: 'Non renseigné — à confirmer' }, { value: 'smooth', label: 'Lisse (démo)' }, { value: 'textured', label: 'Texturé (démo)' }]} /></dd></div>
        <div><dt>Matière</dt><dd><Select aria-label="Matière" value={mat} onChange={(e) => { setMat(e.target.value); reset(); }} options={demoMaterials.map((x) => ({ value: x.id, label: `${x.family} · ${x.colour ?? 'couleur inconnue'}` }))} /></dd></div>
      </dl>
      <p className="cp-note">Compatibilité P1S de la matière : {m.p1sCompatibility}. Changer l'un des éléments ci-dessus invalide la confirmation.</p>
      <label className="cp-check-row"><input type="checkbox" checked={empty} onChange={(e) => { setEmpty(e.target.checked); setDone(false); }} /> Le plateau est vide (vérifié par moi)</label>
      {done ? (
        <div className="cp-done" role="status"><Check size={14} aria-hidden /> Lancement simulé : aucune commande n'a été envoyée.</div>
      ) : (
        <div className="cp-actions">
          <Button variant="primary" disabled={!ready} onClick={() => { setDone(true); toast('info', "Simulation : aucune action réelle n'a été effectuée."); }}>Lancer</Button>
          <Button onClick={() => toast('info', 'Simulation : aucune action réelle. Impression non lancée.')}>Pas maintenant</Button>
          {!ready && <span className="cp-hint">{plate === 'unknown' ? 'Choisir le plateau et cocher « plateau vide » pour activer.' : 'Cocher « plateau vide » pour activer.'}</span>}
        </div>
      )}
    </>
  );
}

function Fallback({ turn }: { turn: Turn }) {
  const l = OPEN_LINK[turn];
  const extra = turn === 't1' ? '\n1. Pince de maintien 25 mm v3\n2. Clip de plan de travail v1\n3. Guide de câble modulaire v2'
    : turn === 't2' ? `\nDimensions : ${pince.dims.x} × ${pince.dims.y} × ${pince.dims.z} mm` : '';
  return (
    <div className="cp-fallback">
      <Tag tone="warn">Fonction UI indisponible</Tag>
      <p>Cet hôte n'affiche pas l'interface intégrée. Repli en texte :</p>
      <pre className="mono">{ASSIST_MSG[turn]}{extra}</pre>
      <Link className="cp-open" to={l.to}>{l.label}<ArrowUpRight size={13} aria-hidden /></Link>
    </div>
  );
}

const TITLES: Record<Turn, string> = { t1: 'Résultats', t2: 'Fiche modèle', t3: 'Comparaison', t4: "Confirmation d'envoi" };

export function CompactPage() {
  const [turn, setTurn] = useState<Turn>('t1');
  const [sel, setSel] = useState<string | null>(null);
  const [part, setPart] = useState<string | null>(null);
  const [fallback, setFallback] = useState(false);

  return (
    <div className="cp">
      <div className="page-head">
        <span className="eyebrow">Maquette · MCP App</span>
        <h1>Vue compacte dans une conversation</h1>
        <p>Le widget tel qu'il serait affiché dans la conversation d'un assistant compatible MCP Apps. Seul le cadre du widget est maquetté ; panneau latéral et plein écran ne sont pas supposés disponibles.</p>
      </div>
      <div className="cp-bar">
        <Tabs<Turn> label="Tours de la conversation" value={turn} onChange={setTurn} tabs={[{ value: 't1', label: '1 · Recherche' }, { value: 't2', label: '2 · Fiche' }, { value: 't3', label: '3 · Comparaison' }, { value: 't4', label: '4 · Envoi' }]} />
        <label className="cp-toggle"><input type="checkbox" checked={fallback} onChange={(e) => setFallback(e.target.checked)} /> Simuler « fonction UI indisponible »</label>
      </div>

      <section className="cp-chat" aria-label="Conversation simulée">
        <div className="cp-chat-label mono">Conversation illustrative simulée — pas une capture, aucun assistant réel</div>
        <div className="cp-msg cp-user">{USER_MSG[turn]}</div>
        <div className="cp-msg cp-asst">{ASSIST_MSG[turn]}</div>

        <div className="cp-frame">
          <div className="cp-frame-label mono">cadre de l'hôte — simulation</div>
          <div className="cp-widget">
            {fallback ? <Fallback turn={turn} /> : (
              <>
                <header className="cp-head"><h3>{TITLES[turn]}</h3><OpenLink turn={turn} /></header>
                <div className="cp-body">
                  {turn === 't1' && <Results sel={sel} onSel={setSel} />}
                  {turn === 't2' && <Sheet part={part} onPart={setPart} />}
                  {turn === 't3' && <Compare />}
                  {turn === 't4' && <Send />}
                </div>
              </>
            )}
          </div>
        </div>
        <p className="cp-foot">Données de démonstration. Aucune imprimante, aucun fichier, aucun appel réel.</p>
      </section>
    </div>
  );
}
