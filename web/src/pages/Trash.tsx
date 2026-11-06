import { useState } from 'react';
import { Link } from 'react-router-dom';
import { Undo2 } from 'lucide-react';
import { Button, EmptyState, Tabs, Tag, useToast } from '../components';
import { api, describeError } from '../api/client';
import { useAsync } from '../api/hooks';
import { Async, FormatBadge, fmtDate } from './common';
import './library.css';

type Tab = 'trash' | 'dups';

/** Corbeille (restauration) et doublons exacts (lecture seule : rien n'est jamais supprimé automatiquement). */
export function TrashPage() {
  const [tab, setTab] = useState<Tab>('trash');
  return (
    <>
      <div className="page-head">
        <span className="eyebrow">Espace</span><h1>Corbeille et doublons</h1>
        <p>Les suppressions vont en corbeille et restent récupérables. Les doublons exacts (même empreinte sha256) sont signalés mais jamais supprimés automatiquement.</p>
      </div>
      <Tabs<Tab> label="Sections" value={tab} onChange={setTab} tabs={[{ value: 'trash', label: 'Corbeille' }, { value: 'dups', label: 'Doublons exacts' }]} />
      <div style={{ marginTop: 'var(--sp-4)' }}>{tab === 'trash' ? <TrashList /> : <Duplicates />}</div>
    </>
  );
}

function TrashList() {
  const toast = useToast();
  const st = useAsync((s) => api.trash(200, 0, s), []);
  const [sel, setSel] = useState<string[]>([]);
  const [busy, setBusy] = useState(false);
  const restore = async (ids: string[]) => {
    setBusy(true);
    try {
      const r = await api.restore(ids);
      toast('ok', `${r.restored.length} modèle(s) restauré(s).`);
      setSel([]); st.reload();
    } catch (e) { toast('err', describeError(e)); } finally { setBusy(false); }
  };
  return (
    <Async state={st} label="Chargement de la corbeille" empty={(d) => d.models.length === 0
      ? <EmptyState title="La corbeille est vide" text="Les modèles que vous mettez à la corbeille depuis la bibliothèque apparaissent ici, et peuvent être restaurés." action={<Link to="/bibliotheque"><Button size="sm">Retour à la bibliothèque</Button></Link>} /> : null}>
      {(d) => (
        <>
          <div className="lib-meta" style={{ marginBottom: 'var(--sp-3)' }}>
            <span><span className="mono">{d.total}</span> modèle{d.total > 1 ? 's' : ''} en corbeille</span>
            <Button size="sm" variant="primary" icon={<Undo2 size={14} />} disabled={sel.length === 0 || busy} onClick={() => restore(sel)}>Restaurer la sélection ({sel.length})</Button>
          </div>
          <ul className="rows" aria-label="Modèles en corbeille">
            {d.models.map((m) => (
              <li key={m.id} className="lib-row">
                <input type="checkbox" className="row-check" aria-label={`Sélectionner ${m.name}`} checked={sel.includes(m.id)} onChange={() => setSel((l) => (l.includes(m.id) ? l.filter((x) => x !== m.id) : [...l, m.id]))} />
                <div className="row-link" style={{ gridTemplateColumns: '40px minmax(0, 2fr) minmax(0, 1fr) auto auto' }}>
                  <FormatBadge formats={m.formats} />
                  <Link to={`/modele/${m.id}`} style={{ minWidth: 0, color: 'inherit', textDecoration: 'none' }}><span className="nm" style={{ display: 'block' }}>{m.name}</span><span className="sub" style={{ display: 'block' }}>{m.tags.join(' · ') || 'sans étiquette'}</span></Link>
                  <span className="sub">{m.collection_path ?? '—'}</span>
                  <span className="sub mono">{fmtDate(m.updated_at)}</span>
                  <Button size="sm" icon={<Undo2 size={14} />} disabled={busy} onClick={() => restore([m.id])}>Restaurer</Button>
                </div>
              </li>
            ))}
          </ul>
        </>
      )}
    </Async>
  );
}

function Duplicates() {
  const st = useAsync((s) => api.duplicates(50, s), []);
  return (
    <Async state={st} label="Recherche des doublons" empty={(d) => d.total_groups === 0
      ? <EmptyState title="Aucun doublon exact" text="Aucun contenu n'est présent deux fois au catalogue. Le stockage par empreinte ne garde de toute façon qu'un seul exemplaire des octets." /> : null}>
      {(d) => (
        <ul className="rows" aria-label="Groupes de doublons">
          {d.groups.map((g) => (
            <li key={g.sha256} style={{ padding: 'var(--sp-3)', display: 'grid', gap: 'var(--sp-2)' }}>
              <div style={{ display: 'flex', gap: 'var(--sp-2)', alignItems: 'center', flexWrap: 'wrap' }}><Tag tone="warn">{g.copies} exemplaires</Tag><span className="mono sub">sha256 {g.sha256.slice(0, 12)}…</span></div>
              {g.files.map((f) => (
                <div key={f.file_id} style={{ display: 'flex', gap: 'var(--sp-3)', fontSize: 'var(--fs-sm)' }}>
                  <Link to={`/modele/${f.model_id}`}>{f.model_name}</Link><span className="mono sub">{f.name}</span><span className="sub">{f.collection_path}</span>{f.trashed && <Tag>corbeille</Tag>}
                </div>
              ))}
            </li>
          ))}
        </ul>
      )}
    </Async>
  );
}
