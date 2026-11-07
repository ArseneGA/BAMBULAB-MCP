import { useEffect, useMemo, useState } from 'react';
import { Link, useSearchParams } from 'react-router-dom';
import { FolderInput, Tags, Trash2, Upload } from 'lucide-react';
import { Button, EmptyState, Panel, SearchInput, Segmented, Select, StatusDot, Tag, Tree } from '../components';
import type { TreeNode } from '../components';
import { api } from '../api/client';
import { useAsync, useConnection } from '../api/hooks';
import type { CollectionInfo, ModelSummary, Operation } from '../api/types';
import { Async, Dialog, FormatBadge, fmtBytes, fmtDims } from './common';
import { ExportSelected } from './ModelTools';
import { ImportDialog } from './ImportDialog';
import { UploadLauncher } from './UploadLauncher';
import { OperationFlow } from './OperationFlow';
import { Field, Input } from '../components';
import './library.css';

type Avail = 'all' | 'linux' | 'windows_only';
type Mode = 'list' | 'grid';
const PAGE = 24;
const FORMATS = ['stl', 'obj', '3mf', 'step'];

function Sync({ m }: { m: ModelSummary }) {
  return m.availability === 'windows_only'
    ? <StatusDot status="unavailable" label="Windows uniquement" />
    : <StatusDot status="online" label="Copié sur Linux" />;
}
const Formats = ({ m }: { m: ModelSummary }) => <>{m.formats.map((f) => <Tag key={f}>{f.toUpperCase()}</Tag>)}</>;

function buildTree(cols: CollectionInfo[], total: number): TreeNode[] {
  const byParent = new Map<string, CollectionInfo[]>();
  for (const c of cols) { const k = c.parent_id ?? ''; byParent.set(k, [...(byParent.get(k) ?? []), c]); }
  const node = (c: CollectionInfo): TreeNode => ({
    id: c.id,
    label: <><span>{c.name}</span><span className="lib-count">{c.total_models}</span></>,
    children: (byParent.get(c.id) ?? []).sort((a, b) => a.name.localeCompare(b.name, 'fr')).map(node),
  });
  return [{
    id: 'all',
    label: <><span>Toutes</span><span className="lib-count">{total}</span></>,
    children: (byParent.get('') ?? []).sort((a, b) => a.name.localeCompare(b.name, 'fr')).map(node),
  }];
}

export function Library() {
  const [sp, setSp] = useSearchParams();
  const q = sp.get('q') ?? '';
  const coll = sp.get('c') ?? 'all';
  const fmt = sp.get('f') ?? 'all';
  const avail = (sp.get('a') ?? 'all') as Avail;
  const sort = (sp.get('s') ?? 'name') as 'name' | 'created' | 'updated';
  const tags = sp.getAll('t');
  const page = Math.max(0, Number(sp.get('p') ?? 0) || 0);
  const [mode, setMode] = useState<Mode>('list');
  const conn = useConnection();

  const set = (patch: Record<string, string | string[] | null>) => {
    const n = new URLSearchParams(sp);
    for (const [k, v] of Object.entries(patch)) {
      n.delete(k);
      if (Array.isArray(v)) v.forEach((x) => n.append(k, x));
      else if (v !== null && v !== '' && v !== 'all') n.set(k, v);
    }
    if (!('p' in patch)) n.delete('p');
    setSp(n, { replace: true });
  };

  // Saisie de recherche locale, répercutée dans l'URL après une courte pause.
  const [qLocal, setQLocal] = useState(q);
  useEffect(() => { setQLocal(q); }, [q]);
  useEffect(() => { const t = window.setTimeout(() => { if (qLocal !== q) set({ q: qLocal }); }, 250); return () => window.clearTimeout(t); }, [qLocal]); // eslint-disable-line react-hooks/exhaustive-deps

  const [refresh, setRefresh] = useState(0);
  const cols = useAsync((s) => api.collections(s), [refresh]);
  const res = useAsync((s) => api.models({ q, tag: tags, collection: coll === 'all' ? undefined : coll, format: fmt === 'all' ? undefined : fmt, availability: avail === 'all' ? undefined : avail, sort, desc: sort !== 'name', limit: PAGE, offset: page * PAGE }, s), [q, coll, fmt, avail, sort, tags.join('|'), page, refresh]);

  const total = res.data?.total ?? 0;
  const dirty = q !== '' || fmt !== 'all' || coll !== 'all' || tags.length > 0 || avail !== 'all';
  const reset = () => setSp(new URLSearchParams(), { replace: true });
  const toggleTag = (t: string) => set({ t: tags.includes(t) ? tags.filter((x) => x !== t) : [...tags, t] });
  const pageTags = useMemo(() => Array.from(new Set([...(res.data?.models ?? []).flatMap((m) => m.tags), ...tags])).sort((a, b) => a.localeCompare(b, 'fr')), [res.data, tags]);

  /* sélection et opérations groupées */
  const [selected, setSelected] = useState<string[]>([]);
  useEffect(() => { setSelected([]); }, [q, coll, fmt, avail, page, tags.join('|')]);
  const [importOpen, setImportOpen] = useState(false);
  const [ops, setOps] = useState<Operation[] | null>(null);
  const [opTitle, setOpTitle] = useState('');
  const [ask, setAsk] = useState<'tag' | 'move' | null>(null);
  const [askVal, setAskVal] = useState('');
  const toggleSel = (id: string) => setSelected((l) => (l.includes(id) ? l.filter((x) => x !== id) : [...l, id]));
  const startOps = (title: string, o: Operation[]) => { setOpTitle(title); setOps(o); };
  const treeData = cols.data ? buildTree(cols.data.collections, cols.data.collections.filter((c) => !c.parent_id).reduce((n, c) => n + c.total_models, 0)) : [];

  const body = (d: NonNullable<typeof res.data>) => d.models.length === 0 ? (
    total === 0 && !dirty ? (
      <EmptyState title="Le catalogue est vide" text="Importez un dossier de modèles (STL, OBJ, 3MF, STEP) : les originaux ne sont jamais déplacés ni modifiés." action={<Button variant="primary" icon={<Upload size={15} />} onClick={() => setImportOpen(true)}>Importer des modèles</Button>} />
    ) : (
      <EmptyState title="Aucun modèle ne correspond" text={q ? `Rien pour « ${q} » avec les filtres actuels. Vérifiez l'orthographe (la recherche ignore la casse mais pas les accents), ou retirez une étiquette, un format ou une restriction.` : 'Aucun modèle avec ces filtres.'} action={<Button size="sm" onClick={reset}>Réinitialiser les filtres</Button>} />
    )
  ) : mode === 'list' ? (
    <ul className="rows" aria-label="Modèles">
      {d.models.map((m) => (
        <li key={m.id} className="lib-row">
          <input type="checkbox" className="row-check" aria-label={`Sélectionner ${m.name}`} checked={selected.includes(m.id)} onChange={() => toggleSel(m.id)} />
          <Link to={`/modele/${m.id}`} className={`row-link${m.availability === 'windows_only' ? ' row-win' : ''}`}>
            <FormatBadge formats={m.formats} />
            <span style={{ minWidth: 0 }}><span className="nm" style={{ display: 'block' }}>{m.name}</span><span className="sub" style={{ display: 'block' }}>{m.tags.join(' · ') || 'sans étiquette'}</span></span>
            <span className="col-coll sub">{m.collection_path ?? '—'}</span>
            <span className="col-fmt fm"><Formats m={m} />{m.versions > 1 && <Tag tone="accent">{m.versions} versions</Tag>}</span>
            <span className="col-dim dm">{m.dimensions ? fmtDims(m.dimensions) : 'analyse en cours'}</span>
            <span className="col-sync"><Sync m={m} /></span>
          </Link>
        </li>
      ))}
    </ul>
  ) : (
    <ul className="tiles" aria-label="Modèles">
      {d.models.map((m) => (
        <li key={m.id} style={{ background: 'var(--surface)' }}>
          <Link to={`/modele/${m.id}`} className={`tile${m.availability === 'windows_only' ? ' row-win' : ''}`}>
            <FormatBadge formats={m.formats} size={84} />
            <span className="nm">{m.name}</span>
            <span className="sub">{m.collection_path ?? '—'} · <span className="mono">{m.dimensions ? fmtDims(m.dimensions) : 'analyse en cours'}</span></span>
            <span style={{ display: 'flex', gap: 4, flexWrap: 'wrap', alignItems: 'center' }}><Formats m={m} /><span className="sub mono">{fmtBytes(m.size_bytes)}</span></span>
            <Sync m={m} />
          </Link>
        </li>
      ))}
    </ul>
  );

  return (
    <>
      <div className="page-head lib-head">
        <div><span className="eyebrow">Espace</span><h1>Bibliothèque</h1><p>Recherche, collections et fichiers du catalogue de bambud. Les originaux importés ne sont jamais modifiés.</p></div>
        <div className="lib-head-acts">
          <UploadLauncher onUploaded={() => setRefresh((n) => n + 1)} />
          <Link to="/corbeille"><Button size="sm" icon={<Trash2 size={14} />}>Corbeille</Button></Link>
          <Button variant="primary" icon={<Upload size={15} />} onClick={() => setImportOpen(true)}>Importer</Button>
        </div>
      </div>
      <div className="lib">
        <aside className="lib-side">
          <Panel title="Collections" flush>
            {cols.error && !cols.data ? <p className="hint" style={{ padding: 'var(--sp-3)', margin: 0 }}>Collections indisponibles.</p>
              : cols.data ? <Tree label="Collections" nodes={treeData} selectedId={coll} onSelect={(id) => set({ c: id })} /> : <p className="hint" style={{ padding: 'var(--sp-3)', margin: 0 }}>Chargement…</p>}
          </Panel>
        </aside>
        <div className="lib-main">
          <div className="lib-bar">
            <SearchInput value={qLocal} onChange={setQLocal} placeholder="Rechercher un modèle, une étiquette, un fichier" />
            <Select aria-label="Format" value={fmt} onChange={(e) => set({ f: e.target.value })} options={[{ value: 'all', label: 'Tous formats' }, ...FORMATS.map((f) => ({ value: f, label: f.toUpperCase() }))]} />
            <Select aria-label="Tri" value={sort} onChange={(e) => set({ s: e.target.value })} options={[{ value: 'name', label: 'Nom' }, { value: 'updated', label: 'Modifiés récemment' }, { value: 'created', label: 'Importés récemment' }]} />
            <Segmented<Avail> label="Disponibilité" value={avail} onChange={(v) => set({ a: v })} options={[{ value: 'all', label: 'Tous' }, { value: 'linux', label: 'Sur Linux' }, { value: 'windows_only', label: 'Windows seul' }]} />
            <Segmented<Mode> label="Affichage" value={mode} onChange={setMode} options={[{ value: 'list', label: 'Liste' }, { value: 'grid', label: 'Grille' }]} />
          </div>
          {pageTags.length > 0 && (
            <div className="lib-tags" role="group" aria-label="Étiquettes">
              <span className="eyebrow" style={{ marginRight: 4 }}>Étiquettes</span>
              {pageTags.map((t) => <button key={t} type="button" className={`tag${tags.includes(t) ? ' on' : ''}`} aria-pressed={tags.includes(t)} onClick={() => toggleTag(t)}>{t}</button>)}
            </div>
          )}
          <div className="lib-meta" aria-live="polite">
            <span>{res.data ? <><span className="mono">{total === 0 ? 0 : page * PAGE + 1}–{Math.min(total, page * PAGE + res.data.models.length)}</span> sur <span className="mono">{total}</span> modèle{total > 1 ? 's' : ''}{res.loading && ' · actualisation…'}</> : res.error ? '' : 'Chargement…'}</span>
            {dirty && <Button size="sm" variant="ghost" onClick={reset}>Réinitialiser les filtres</Button>}
          </div>

          {selected.length > 0 && (
            <div className="lib-bulk" role="region" aria-label="Actions sur la sélection">
              <strong>{selected.length} sélectionné{selected.length > 1 ? 's' : ''}</strong>
              <Button size="sm" icon={<Tags size={14} />} onClick={() => { setAsk('tag'); setAskVal(''); }}>Ajouter une étiquette…</Button>
              <Button size="sm" icon={<FolderInput size={14} />} onClick={() => { setAsk('move'); setAskVal(''); }}>Déplacer…</Button>
              <ExportSelected ids={selected} />
              <Button size="sm" variant="danger" icon={<Trash2 size={14} />} onClick={() => startOps(`Mettre ${selected.length} modèle(s) à la corbeille`, [{ op: 'delete', model_ids: selected }])}>Corbeille…</Button>
              <Button size="sm" variant="ghost" onClick={() => setSelected([])}>Tout désélectionner</Button>
            </div>
          )}

          <Async state={res} label="Chargement du catalogue">{body}</Async>

          {res.data && total > PAGE && (
            <nav className="lib-pager" aria-label="Pagination">
              <Button size="sm" disabled={page === 0} onClick={() => set({ p: String(page - 1) })}>Précédent</Button>
              <span className="mono">page {page + 1} / {Math.ceil(total / PAGE)}</span>
              <Button size="sm" disabled={!res.data.has_more} onClick={() => set({ p: String(page + 1) })}>Suivant</Button>
            </nav>
          )}
          {conn.server === 'online' && res.error && res.data && <p className="hint">La dernière actualisation a échoué : les données affichées sont celles de la requête précédente.</p>}
        </div>
      </div>

      <ImportDialog open={importOpen} onClose={() => setImportOpen(false)} onImported={() => setRefresh((n) => n + 1)} />
      <OperationFlow title={opTitle} operations={ops} onClose={() => setOps(null)} onApplied={() => { setSelected([]); setRefresh((n) => n + 1); }} />
      <Dialog open={ask !== null} onClose={() => setAsk(null)} title={ask === 'tag' ? 'Ajouter une étiquette' : 'Déplacer vers une collection'}
        footer={<><Button onClick={() => setAsk(null)}>Annuler</Button><Button variant="primary" disabled={!askVal.trim()} onClick={() => {
          const v = askVal.trim(); const ids = selected; const kind = ask; setAsk(null);
          startOps(kind === 'tag' ? `Ajouter l'étiquette « ${v} »` : `Déplacer vers « ${v} »`, [kind === 'tag' ? { op: 'add_tags', model_ids: ids, tags: [v] } : { op: 'move', model_ids: ids, collection: v }]);
        }}>Prévisualiser</Button></>}>
        <Field label={ask === 'tag' ? 'Étiquette' : 'Collection (identifiant, nom ou chemin « A/B » ; créée si elle n\'existe pas)'}><Input value={askVal} onChange={(e) => setAskVal(e.target.value)} autoFocus onKeyDown={(e) => { if (e.key === 'Enter' && askVal.trim()) (e.currentTarget.closest('dialog')?.querySelector('.btn-primary') as HTMLButtonElement | null)?.click(); }} /></Field>
        <p className="hint" style={{ margin: 0 }}>{selected.length} modèle(s) concerné(s). Un aperçu des changements s'affiche avant toute modification.</p>
      </Dialog>
    </>
  );
}
