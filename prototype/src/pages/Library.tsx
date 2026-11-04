import { useMemo, useState } from 'react';
import { Link } from 'react-router-dom';
import { Button, EmptyState, Panel, SearchInput, Segmented, Select, StatusDot, Tag, Tree } from '../components';
import type { TreeNode } from '../components';
import { allTags, collections, demoModels } from '../data/demo';
import type { DemoModel, FileFormat } from '../data/demo';
import { Thumb, dimsLabel, norm } from './modelBits';
import './library.css';

type Avail = 'all' | 'linux' | 'windows';
type Mode = 'list' | 'grid';
const formats: FileFormat[] = ['STEP', 'STL', '3MF'];

function Sync({ m }: { m: DemoModel }) {
  return m.sync === 'windows-only'
    ? <StatusDot status="unavailable" label="Windows uniquement" />
    : <StatusDot status="online" label="Copié sur Linux" />;
}
const Formats = ({ m }: { m: DemoModel }) => <>{Array.from(new Set(m.files.map((f) => f.format))).map((f) => <Tag key={f}>{f}</Tag>)}</>;

export function Library() {
  const [q, setQ] = useState('');
  const [fmt, setFmt] = useState<'all' | FileFormat>('all');
  const [coll, setColl] = useState('all');
  const [tags, setTags] = useState<string[]>([]);
  const [avail, setAvail] = useState<Avail>('all');
  const [mode, setMode] = useState<Mode>('list');

  const results = useMemo(() => {
    const terms = norm(q).split(/\s+/).filter(Boolean);
    return demoModels.filter((m) => {
      if (coll !== 'all' && m.collection !== coll) return false;
      if (fmt !== 'all' && !m.files.some((f) => f.format === fmt)) return false;
      if (avail === 'linux' && m.sync !== 'copied-linux') return false;
      if (avail === 'windows' && m.sync !== 'windows-only') return false;
      if (tags.length && !tags.every((t) => m.tags.includes(t))) return false;
      const hay = norm([m.name, m.collection, ...m.tags, ...m.files.map((f) => f.name)].join(' '));
      return terms.every((t) => hay.includes(t));
    });
  }, [q, fmt, coll, tags, avail]);

  const tree: TreeNode[] = [{
    id: 'all',
    label: <><span>Toutes</span><span className="lib-count">{demoModels.length}</span></>,
    children: collections.map((c) => ({ id: c, label: <><span>{c}</span><span className="lib-count">{demoModels.filter((m) => m.collection === c).length}</span></> })),
  }];
  const dirty = q !== '' || fmt !== 'all' || coll !== 'all' || tags.length > 0 || avail !== 'all';
  const reset = () => { setQ(''); setFmt('all'); setColl('all'); setTags([]); setAvail('all'); };
  const toggleTag = (t: string) => setTags((l) => (l.includes(t) ? l.filter((x) => x !== t) : [...l, t]));

  return (
    <>
      <div className="page-head"><span className="eyebrow">Espace</span><h1>Bibliothèque</h1><p>Recherche, collections et fichiers du catalogue. Données de démonstration.</p></div>
      <div className="lib">
        <aside className="lib-side"><Panel title="Collections" flush><Tree label="Collections" nodes={tree} selectedId={coll} onSelect={setColl} /></Panel></aside>
        <div className="lib-main">
          <div className="lib-bar">
            <SearchInput value={q} onChange={setQ} placeholder="Rechercher un modèle ou tag" />
            <Select aria-label="Format" value={fmt} onChange={(e) => setFmt(e.target.value as 'all' | FileFormat)} options={[{ value: 'all', label: 'Tous formats' }, ...formats.map((f) => ({ value: f, label: f }))]} />
            <Segmented<Avail> label="Disponibilité" value={avail} onChange={setAvail} options={[{ value: 'all', label: 'Tous' }, { value: 'linux', label: 'Sur Linux' }, { value: 'windows', label: 'Windows seul' }]} />
            <Segmented<Mode> label="Affichage" value={mode} onChange={setMode} options={[{ value: 'list', label: 'Liste' }, { value: 'grid', label: 'Grille' }]} />
          </div>
          <div className="lib-tags" role="group" aria-label="Tags">
            <span className="eyebrow" style={{ marginRight: 4 }}>Tags</span>
            {allTags.map((t) => <button key={t} type="button" className={`tag${tags.includes(t) ? ' on' : ''}`} aria-pressed={tags.includes(t)} onClick={() => toggleTag(t)}>{t}</button>)}
          </div>
          <div className="lib-meta" aria-live="polite">
            <span><span className="mono">{results.length}</span> / <span className="mono">{demoModels.length}</span> modèles{coll !== 'all' && <> dans « {coll} »</>}</span>
            {dirty && <Button size="sm" variant="ghost" onClick={reset}>Réinitialiser les filtres</Button>}
          </div>

          {results.length === 0 ? (
            <EmptyState title="Aucun modèle ne correspond" text={q ? `Rien pour « ${q} » avec les filtres actuels. Vérifiez l'orthographe, ou retirez un tag, un format ou la restriction de disponibilité.` : 'Aucun modèle avec ces filtres.'} action={<Button size="sm" onClick={reset}>Réinitialiser les filtres</Button>} />
          ) : mode === 'list' ? (
            <ul className="rows" aria-label="Modèles">
              {results.map((m) => (
                <li key={m.id}>
                  <Link to={`/modele/${m.id}`} className={`row-link${m.sync === 'windows-only' ? ' row-win' : ''}`}>
                    <Thumb geometry={m.geometry} />
                    <span style={{ minWidth: 0 }}><span className="nm" style={{ display: 'block' }}>{m.name}</span><span className="sub" style={{ display: 'block' }}>{m.tags.join(' · ')}</span></span>
                    <span className="col-coll sub">{m.collection}</span>
                    <span className="col-fmt fm"><Formats m={m} /></span>
                    <span className="col-dim dm">{dimsLabel(m.dims)}</span>
                    <span className="col-sync"><Sync m={m} /></span>
                  </Link>
                </li>
              ))}
            </ul>
          ) : (
            <ul className="tiles" aria-label="Modèles">
              {results.map((m) => (
                <li key={m.id} style={{ background: 'var(--surface)' }}>
                  <Link to={`/modele/${m.id}`} className="tile">
                    <Thumb geometry={m.geometry} size={84} />
                    <span className="nm">{m.name}</span>
                    <span className="sub">{m.collection} · <span className="mono">{dimsLabel(m.dims)}</span></span>
                    <span style={{ display: 'flex', gap: 4, flexWrap: 'wrap', alignItems: 'center' }}><Formats m={m} /></span>
                    <Sync m={m} />
                  </Link>
                </li>
              ))}
            </ul>
          )}
        </div>
      </div>
    </>
  );
}
