import { useMemo, useState } from 'react';
import { FolderOpen } from 'lucide-react';
import type { ModelSummary } from '../../src/api/types';
import { useToolCall, type CardProps } from './call';
import { fmtDims, isRec, str, type Rec } from './format';
import { LiteBadge, LiteBox, LiteButton, LiteCard, LiteState } from './lite';

/** Carte Bibliothèque : search_models (résultats) et list_collection (sous-collections + modèles). Clic sur une pièce : get_model_details. */
export interface LibraryData {
  source: 'search' | 'collection';
  title: string;
  models: ModelSummary[];
  total: number;
  hasMore: boolean;
  subcollections: Array<{ id: string; name: string; path: string; total_models?: number }>;
  collectionPath?: string;
}

const isModels = (v: unknown): v is ModelSummary[] => Array.isArray(v) && v.every((m) => isRec(m) && typeof m.id === 'string' && typeof m.name === 'string');

export function normalizeLibrary(s: unknown): LibraryData | undefined {
  if (!isRec(s)) return undefined;
  if (isModels(s.models) && typeof s.total === 'number') {
    return { source: 'search', title: 'Bibliothèque', models: s.models, total: s.total, hasMore: s.has_more === true, subcollections: [] };
  }
  if (isRec(s.collection) && isRec(s.models) && isModels(s.models.models)) {
    const c = s.collection as Rec;
    const subs = Array.isArray(s.subcollections) ? (s.subcollections as Rec[]).filter((x) => typeof x.id === 'string').map((x) => ({
      id: String(x.id), name: str(x.name) ?? String(x.id), path: str(x.path) ?? String(x.id), total_models: typeof x.total_models === 'number' ? x.total_models : typeof x.total_model === 'number' ? x.total_model : undefined,
    })) : [];
    const path = str(c.path) ?? str(c.name);
    return { source: 'collection', title: path ? `Collection ${path}` : 'Collection', models: s.models.models, total: typeof s.models.total === 'number' ? s.models.total : s.models.models.length, hasMore: s.models.has_more === true, subcollections: subs, collectionPath: path };
  }
  return undefined;
}

const PREVIEW_NOTE: Record<string, string> = { pending: 'aperçu en cours', backend_missing: 'aperçu : CAO absente', windows_only: 'Windows seul', error: 'aperçu en échec' };

export function LibraryCard({ app, data, onOpen }: CardProps<LibraryData>) {
  const { busy, error, run } = useToolCall(app, onOpen);
  const [format, setFormat] = useState<string | null>(null);
  const formats = useMemo(() => [...new Set(data.models.flatMap((m) => (m.formats ?? []).map((f) => f.toLowerCase())))].sort(), [data.models]);
  const shown = useMemo(() => (format ? data.models.filter((m) => (m.formats ?? []).some((f) => f.toLowerCase() === format)) : data.models), [data.models, format]);
  const sub = `${data.models.length} sur ${data.total}${data.hasMore ? ' · d\'autres résultats existent' : ''}`;

  return (
    <LiteCard title={data.title} sub={data.models.length ? sub : undefined} label="Bibliothèque">
      {data.subcollections.length > 0 && (
        <div className="lc-chips" aria-label="Sous-collections">
          {data.subcollections.map((c) => (
            <LiteButton key={c.id} busy={busy === `c:${c.id}`} disabled={busy !== null} onClick={() => run(`c:${c.id}`, 'list_collection', { collection: c.path })} title={`Ouvrir ${c.path}`}>
              <FolderOpen size={14} aria-hidden /> {c.name}{c.total_models !== undefined ? ` · ${c.total_models}` : ''}
            </LiteButton>
          ))}
        </div>
      )}
      {formats.length > 1 && (
        <div className="lc-chips" role="group" aria-label="Filtrer par format">
          <LiteButton pressed={format === null} onClick={() => setFormat(null)}>Tous</LiteButton>
          {formats.map((f) => <LiteButton key={f} pressed={format === f} onClick={() => setFormat(format === f ? null : f)}>{f.toUpperCase()}</LiteButton>)}
        </div>
      )}
      {data.models.length === 0 ? <LiteState kind="empty">Aucun modèle ne correspond.</LiteState>
        : shown.length === 0 ? <LiteState kind="empty">Aucun modèle au format {format?.toUpperCase()} dans cette page.</LiteState>
        : (
          <ul className="lc-grid" aria-label="Modèles">
            {shown.map((m) => (
              <li key={m.id}>
                <button type="button" className="lc-tile" disabled={busy !== null} aria-busy={busy === m.id} onClick={() => run(m.id, 'get_model_details', { model_id: m.id })} aria-label={`Ouvrir la fiche de ${m.name}`}>
                  <span className="lc-tile-art"><LiteBox dims={m.dimensions} seed={m.name} size={64} /></span>
                  <span className="lc-row-name">{m.name}</span>
                  <span className="lc-row-meta">{fmtDims(m.dimensions) ?? 'dimensions inconnues'}</span>
                  <span className="lc-tags">
                    {(m.formats ?? []).map((f) => <LiteBadge key={f} tone="accent">{f.toUpperCase()}</LiteBadge>)}
                    {m.availability === 'windows_only' && <LiteBadge tone="warn">Windows seul</LiteBadge>}
                    {m.preview && PREVIEW_NOTE[m.preview.state] && m.availability !== 'windows_only' && <LiteBadge>{PREVIEW_NOTE[m.preview.state]}</LiteBadge>}
                    {(m.tags ?? []).slice(0, 3).map((t) => <LiteBadge key={t}>{t}</LiteBadge>)}
                  </span>
                </button>
              </li>
            ))}
          </ul>
        )}
      {error && <LiteState kind="error">{error}</LiteState>}
      {data.models.length > 0 && <p className="lc-note">Choisir une pièce ouvre sa fiche avec le viewer 3D. Silhouettes aux proportions réelles, pas un rendu.</p>}
    </LiteCard>
  );
}
