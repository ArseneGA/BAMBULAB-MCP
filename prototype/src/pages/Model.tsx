import { useEffect, useMemo, useState } from 'react';
import { Link, useNavigate, useParams } from 'react-router-dom';
import { AlertTriangle, FileWarning, GitCompare, SlidersHorizontal } from 'lucide-react';
import { Button, EmptyState, List, Panel, StatusDot, Tag, useToast } from '../components';
import { demoModels } from '../data/demo';
import { Viewer3D } from '../viewer/LazyViewer3D';
import { partList } from '../viewer/geometry';
import { dimsLabel, fmtSize } from './modelBits';
import './model.css';

/** Simulation : un seul modèle de démonstration porte un conflit de version (deux empreintes pour v3). */
const CONFLICTS: Record<string, { version: string; linuxSha: string; windowsSha: string }> = {
  m05: { version: 'v3', linuxSha: 'a6e931', windowsSha: '1f0c9b' },
};
/** Provenance et licence fictives, propres à la démo. null = non renseigné. */
const PROVENANCE: Record<string, { origin: string; licence: string | null }> = {
  m01: { origin: 'Conception personnelle, exportée depuis la CAO', licence: 'Usage personnel (démo)' },
  m02: { origin: 'Importé depuis un dépôt public (démo)', licence: 'CC BY-NC 4.0 (démo)' },
  m05: { origin: 'Conception personnelle, exportée depuis la CAO', licence: 'Usage personnel (démo)' },
};


export function ModelPage() {
  const { id } = useParams();
  const model = demoModels.find((m) => m.id === id);
  if (!model) {
    return (
      <>
        <div className="page-head"><span className="eyebrow">Espace</span><h1>Modèle introuvable</h1></div>
        <EmptyState title="Ce modèle n'existe pas dans le catalogue de démonstration" action={<Link to="/bibliotheque"><Button size="sm">Retour à la bibliothèque</Button></Link>} />
      </>
    );
  }
  return <ModelView key={model.id} id={model.id} />;
}

function ModelView({ id }: { id: string }) {
  const m = demoModels.find((x) => x.id === id)!;
  const nav = useNavigate();
  const toast = useToast();
  const [vIdx, setVIdx] = useState(0);
  const [part, setPart] = useState<string | null>(null);
  const parts = useMemo(() => partList(m.geometry, m.dims), [m]);
  useEffect(() => { setVIdx(0); setPart(null); }, [m.id]);
  const ver = m.versions[vIdx];
  const conflict = CONFLICTS[m.id];
  const winOnly = m.sync === 'windows-only';
  const prov = PROVENANCE[m.id];
  const simulate = (t: string) => toast('info', `${t} — simulation, aucune action réelle.`);

  return (
    <>
      <div className="mdl-head">
        <div>
          <nav className="mdl-crumb" aria-label="Fil d'Ariane"><Link to="/bibliotheque">Bibliothèque</Link><span aria-hidden>/</span><span>{m.collection}</span></nav>
          <h1>{m.name}</h1>
          <div style={{ display: 'flex', gap: 4, flexWrap: 'wrap', marginTop: 8 }}>{m.tags.map((t) => <Tag key={t}>{t}</Tag>)}</div>
        </div>
        <div className="mdl-actions">
          <Button icon={<GitCompare size={15} />} onClick={() => nav(`/comparaison?model=${m.id}`)}>Comparer</Button>
          {winOnly
            ? <span title="Fichier à copier sur Linux avant préparation"><Button variant="primary" icon={<SlidersHorizontal size={15} />} disabled>Préparer</Button></span>
            : <Button variant="primary" icon={<SlidersHorizontal size={15} />} onClick={() => nav(`/preparation?model=${m.id}`)}>Préparer</Button>}
        </div>
      </div>

      <div className="mdl">
        <div className="mdl-stage">
          {conflict && (
            <div className="notice notice-err" role="alert">
              <AlertTriangle size={16} aria-hidden />
              <div>
                <h3>Conflit de version : {conflict.version} existe en deux exemplaires</h3>
                <p>Le serveur Linux garde l'empreinte <span className="mono">{conflict.linuxSha}</span>, l'agent Windows en signale une autre, <span className="mono">{conflict.windowsSha}</span>. Aucune des deux n'est écartée tant que vous n'avez pas choisi.</p>
                <div className="acts">
                  <Button size="sm" onClick={() => simulate('Version Linux conservée')}>Garder l'exemplaire Linux</Button>
                  <Button size="sm" onClick={() => simulate('Version Windows conservée')}>Garder l'exemplaire Windows</Button>
                  <Button size="sm" variant="ghost" onClick={() => nav(`/comparaison?model=${m.id}`)}>Comparer les deux</Button>
                </div>
              </div>
            </div>
          )}
          {winOnly && (
            <div className="notice notice-warn" role="status">
              <FileWarning size={16} aria-hidden />
              <div>
                <h3>Fichier non copié sur le serveur Linux</h3>
                <p>Ce modèle n'est connu que par l'index de l'agent Windows (actuellement hors ligne). L'aperçu ci-dessous vient du cache d'indexation ; la préparation reste bloquée jusqu'à la copie.</p>
                <div className="acts"><Button size="sm" onClick={() => simulate('Copie demandée à l\'agent Windows')}>Demander la copie</Button></div>
              </div>
            </div>
          )}
          <div className="mdl-view">
            <Viewer3D geometry={m.geometry} dims={m.dims} revision={vIdx} selectedPartId={part} onSelectPart={setPart} notice={winOnly ? 'Aperçu issu du cache — fichier source non copié sur Linux.' : undefined} />
          </div>
          <p className="hint">Géométrie générée localement à partir du type de forme de la démo, pas du fichier réel. Version affichée : <span className="mono">{ver.label}</span>{vIdx > 0 && ' (rendu simplifié : moins de chanfreins et de perçages)'}.</p>
        </div>

        <div className="mdl-side">
          <Panel title="Dimensions">
            <div className="dims" aria-label={dimsLabel(m.dims)}>
              {(['x', 'y', 'z'] as const).map((a) => <div key={a}><span className="ax">{a.toUpperCase()}</span><span className="v">{m.dims[a]}<span style={{ fontSize: 'var(--fs-xs)', color: 'var(--text-3)' }}> mm</span></span></div>)}
            </div>
            <div className="hint">Boîte englobante. Plateau P1S : 256 × 256 mm.</div>
          </Panel>

          <Panel title="Sous-parties">
            <div className="partlist" role="group" aria-label="Sous-parties du modèle">
              {parts.map((p) => <button key={p.id} type="button" aria-pressed={part === p.id} onClick={() => setPart(part === p.id ? null : p.id)}>{p.label}</button>)}
            </div>
            <div className="hint">Cliquez dans le viewer ou ici pour surligner.</div>
          </Panel>

          <Panel title="Fichiers associés">
            {m.files.map((f) => (
              <div key={f.name} className={`frow${f.sync === 'windows-only' ? ' frow-miss' : ''}`}>
                <div className="fn"><span>{f.name}</span><Tag>{f.format}</Tag></div>
                <div className="fs"><span className="mono">{fmtSize(f.sizeKB)}</span>{f.sync === 'windows-only' ? <StatusDot status="unavailable" label="Windows uniquement, non copié" /> : <StatusDot status="online" label="Copié sur Linux" />}</div>
              </div>
            ))}
          </Panel>

          <Panel title="Versions" flush>
            <List label="Versions" items={m.versions} getKey={(v) => v.id} selectedKey={ver.id} onSelect={(v) => setVIdx(m.versions.indexOf(v))}
              render={(v) => (
                <span className="ver" style={{ display: 'flex', width: '100%' }}>
                  <span className="t"><strong>{v.label}</strong>{m.versions[0] === v && <Tag tone="accent">Dernière</Tag>}{conflict?.version === v.id && <Tag tone="err">Conflit</Tag>}<span className="d">{v.date}</span></span>
                  <span className="n">{v.note}</span>
                  <span className="h">sha256 {v.sha256Short}…</span>
                </span>
              )} />
          </Panel>

          <Panel title="Provenance et licence">
            <dl className="dl">
              <dt>Origine</dt><dd>{prov?.origin ?? (winOnly ? 'Indexé depuis l\'agent Windows' : 'Importé sur le serveur Linux')}</dd>
              <dt>Licence</dt><dd>{prov?.licence ?? <Tag tone="warn">Non renseignée</Tag>}</dd>
              <dt>Collection</dt><dd>{m.collection}</dd>
              <dt>Empreinte</dt><dd className="mono">{ver.sha256Short}…</dd>
            </dl>
          </Panel>
        </div>
      </div>
    </>
  );
}
