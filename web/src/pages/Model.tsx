import { useEffect, useMemo, useRef, useState } from 'react';
import { Link, useNavigate, useParams } from 'react-router-dom';
import { Box, Cpu, FileWarning, FolderInput, GitCompare, Pencil, ShieldAlert, SlidersHorizontal, Tags, Trash2, Undo2 } from 'lucide-react';
import { Button, EmptyState, Field, Input, List, Panel, StatusDot, Tag, useToast } from '../components';
import { api, ApiError, describeError } from '../api/client';
import { useAsync, useConnection } from '../api/hooks';
import type { ModelDetails, Operation, PreviewRef } from '../api/types';
import { GlbView } from '../viewer/LazyGlbView';
import type { LoadedModel } from '../viewer/glbEngine';
import { Async, Dialog, Notice, fmtBytes, fmtDate, fmtDims, fmtNum, shortSha } from './common';
import { OperationFlow } from './OperationFlow';
import './model.css';

export function ModelPage() {
  const { id = '' } = useParams();
  return <ModelView key={id} id={id} />;
}

type Edit = null | 'rename' | 'move' | 'tags' | 'license';

function ModelView({ id }: { id: string }) {
  const st = useAsync((s) => api.model(id, s), [id]);
  const d = st.data;
  const reloadRef = useRef(st.reload); reloadRef.current = st.reload;

  // Aperçu ou analyse en cours : relecture régulière jusqu'à l'état final.
  const waiting = !!d && (d.preview.state === 'pending' || d.analysis.state === 'pending' || (d.preview.state === 'none' && d.model.deleted_at === undefined && d.analysis.state === 'none'));
  useEffect(() => {
    if (!waiting) return;
    const t = window.setInterval(() => reloadRef.current(), 3000);
    return () => window.clearInterval(t);
  }, [waiting]);

  if (st.error instanceof ApiError && st.error.status === 404) return <ModelNotFound />;
  return (
    <Async state={st} label="Chargement de la fiche">
      {(m) => <ModelBody m={m} id={id} reload={st.reload} />}
    </Async>
  );
}

function ModelBody({ m, id, reload }: { m: ModelDetails; id: string; reload: () => void }) {
  const nav = useNavigate();
  const toast = useToast();
  const conn = useConnection();
  const [edit, setEdit] = useState<Edit>(null);
  const [val, setVal] = useState('');
  const [ops, setOps] = useState<Operation[] | null>(null);
  const [opTitle, setOpTitle] = useState('');
  const start = (title: string, o: Operation[]) => { setOpTitle(title); setOps(o); };
  const [vIdx, setVIdx] = useState(0);
  const [part, setPart] = useState<string | null>(null);
  const [loaded, setLoaded] = useState<LoadedModel | null>(null);
  const versions = useMemo(() => [...m.versions].reverse(), [m.versions]); // plus récente d'abord
  const ver = versions[vIdx];
  const trashed = !!m.model.deleted_at;
  const winOnly = m.files.find((f) => f.id === m.current_file_id)?.availability === 'windows_only' || m.preview.state === 'windows_only';
  const cur = m.files.find((f) => f.id === m.current_file_id) ?? m.files[m.files.length - 1];
  const dims = m.analysis.size_mm ?? m.model.dimensions;
  const a = m.analysis;

  const loader = useMemo(() => async (signal: AbortSignal) => {
    const url = m.preview.url;
    if (!url) throw new Error("Pas d'URL d'aperçu");
    try { return await api.glb(url, signal); } catch (e) {
      if (e instanceof ApiError && e.code === 'expired') { // URL signée expirée : on redemande la fiche puis on réessaie une fois
        const fresh = await api.model(id, signal);
        if (fresh.preview.url) return api.glb(fresh.preview.url, signal);
      }
      throw e;
    }
  }, [m.preview.url]); // eslint-disable-line react-hooks/exhaustive-deps

  const restore = async () => {
    try { await api.restore([id]); toast('ok', 'Modèle restauré.'); reload(); } catch (e) { toast('err', describeError(e)); }
  };

  const submitEdit = () => {
    const v = val.trim(); if (!v) return;
    const e = edit; setEdit(null);
    if (e === 'rename') start(`Renommer en « ${v} »`, [{ op: 'rename', model_ids: [id], new_name: v }]);
    if (e === 'move') start(`Déplacer vers « ${v} »`, [{ op: 'move', model_ids: [id], collection: v }]);
    if (e === 'tags') {
      const list = v.split(',').map((t) => t.trim()).filter(Boolean);
      const remove = list.filter((t) => t.startsWith('-')).map((t) => t.slice(1).trim()).filter(Boolean);
      const add = list.filter((t) => !t.startsWith('-'));
      const o: Operation[] = [];
      if (add.length) o.push({ op: 'add_tags', model_ids: [id], tags: add });
      if (remove.length) o.push({ op: 'remove_tags', model_ids: [id], tags: remove });
      start('Modifier les étiquettes', o);
    }
    if (e === 'license') start(`Licence : ${v}`, [{ op: 'set_license', model_ids: [id], license: v }]);
  };

  const previewBlock = (p: PreviewRef) => {
    switch (p.state) {
      case 'ready': return (
        <GlbView sourceKey={p.sha256 ?? m.model.id} loader={loader} selectedPartId={part} onSelectPart={setPart} onLoaded={setLoaded}
          ariaLabel={`Aperçu 3D de ${m.model.name}${dims ? `, ${fmtDims(dims)}` : ''}`}
          notice={winOnly ? 'Fichier source non copié sur Linux.' : undefined} />
      );
      case 'pending': return <PreviewPlaceholder icon={<span className="spinner" aria-hidden />} title="Aperçu en cours de calcul" text="Le service prépare le GLB d'aperçu. Cette page se met à jour toute seule." />;
      case 'backend_missing': return <PreviewPlaceholder icon={<Cpu size={18} aria-hidden />} title="Backend CAO absent" text={`Un fichier STEP nécessite le worker géométrie (uv + CadQuery) pour être converti. ${p.note ?? ''} L'original est conservé intact.`} tone="warn" />;
      case 'windows_only': return <PreviewPlaceholder icon={<FileWarning size={18} aria-hidden />} title="Fichier disponible sur Windows seulement" text="Importez-le avec copie pour obtenir un aperçu : il n'est pas encore sur le serveur Linux." tone="warn" />;
      case 'error': return <PreviewPlaceholder icon={<ShieldAlert size={18} aria-hidden />} title="L'aperçu a échoué" text={p.note ?? "Le calcul de l'aperçu a produit une erreur."} tone="err" />;
      default: return <PreviewPlaceholder icon={<Box size={18} aria-hidden />} title="Pas encore d'aperçu" text={p.note ?? "L'aperçu n'a pas été demandé pour ce fichier."} />;
    }
  };

  const parts = loaded?.parts ?? [];
  return (
    <>
      <div className="mdl-head">
        <div>
          <nav className="mdl-crumb" aria-label="Fil d'Ariane"><Link to="/bibliotheque">Bibliothèque</Link><span aria-hidden>/</span><span>{m.collection_path ?? 'Sans collection'}</span></nav>
          <h1>{m.model.name}</h1>
          <div style={{ display: 'flex', gap: 4, flexWrap: 'wrap', marginTop: 8 }}>
            {(m.model.tags ?? []).length === 0 ? <span className="hint" style={{ margin: 0 }}>Aucune étiquette</span> : (m.model.tags ?? []).map((t) => <Tag key={t}>{t}</Tag>)}
          </div>
        </div>
        <div className="mdl-actions">
          <Button icon={<GitCompare size={15} />} onClick={() => nav('/comparaison')} title="Ouvre la dernière expérience de comparaison">Comparer</Button>
          <Button variant="primary" icon={<SlidersHorizontal size={15} />} disabled={winOnly || trashed} onClick={() => nav(`/preparation?model=${id}`)} title={winOnly ? 'Fichier à copier sur Linux avant préparation' : 'Objectif de finition et variantes'}>Préparer</Button>
        </div>
      </div>

      <div className="mdl">
        <div className="mdl-stage">
          {trashed && (
            <Notice tone="warn" icon={<Trash2 size={16} aria-hidden />} title="Ce modèle est dans la corbeille" actions={<Button size="sm" icon={<Undo2 size={14} />} onClick={restore}>Restaurer</Button>}>
              Il n'apparaît plus dans la bibliothèque. Rien n'a été supprimé définitivement : les fichiers et l'historique sont conservés.
            </Notice>
          )}
          {winOnly && !trashed && (
            <Notice tone="warn" icon={<FileWarning size={16} aria-hidden />} title="Fichier non copié sur le serveur Linux">
              Ce modèle est connu par son index seulement. Il ne fait pas partie des capacités hors ligne tant qu'il n'est pas copié {conn.windows === 'online' ? "(agent Windows en ligne)." : "(agent Windows hors ligne)."}
            </Notice>
          )}
          <div className="mdl-view">{previewBlock(m.preview)}</div>
          <p className="hint">Aperçu de visualisation uniquement (GLB dérivé{m.analysis.triangles ? `, ${fmtNum(m.analysis.triangles, 0)} triangles analysés` : ''}) : l'original reste la référence de fabrication. {a.not_interpreted && a.not_interpreted.length > 0 && <>Non lu par l'analyse : {a.not_interpreted.join(', ')}.</>}</p>
        </div>

        <div className="mdl-side">
          <Panel title="Dimensions">
            {dims ? (
              <>
                <div className="dims" aria-label={fmtDims(dims)}>
                  {(['x', 'y', 'z'] as const).map((ax) => <div key={ax}><span className="ax">{ax.toUpperCase()}</span><span className="v">{fmtNum(dims[ax])}<span style={{ fontSize: 'var(--fs-xs)', color: 'var(--text-3)' }}> mm</span></span></div>)}
                </div>
                <div className="hint">Boîte englobante calculée. {a.unit_assumed ? <Tag tone="warn">unité supposée</Tag> : null}</div>
              </>
            ) : <p className="hint" style={{ margin: 0 }}>{a.state === 'pending' ? 'Analyse en cours…' : 'Dimensions inconnues : analyse indisponible.'}</p>}
          </Panel>

          <Panel title="Analyse géométrique">
            <AnalysisBlock a={a} cad={m.cad} />
          </Panel>

          <Panel title="Sous-parties">
            {parts.length === 0 ? <p className="hint" style={{ margin: 0 }}>{m.preview.state === 'ready' ? 'Lecture du maillage…' : "Disponibles quand l'aperçu est prêt."}</p> : (
              <>
                <div className="partlist" role="group" aria-label="Sous-parties du modèle">
                  {parts.map((p) => <button key={p.id} type="button" aria-pressed={part === p.id} title={`${fmtDims(p.size)} · ${fmtNum(p.triangles, 0)} triangles`} onClick={() => setPart(part === p.id ? null : p.id)}>{p.label}</button>)}
                </div>
                <div className="hint">{parts.length === 1 ? "Ce fichier ne contient qu'un objet." : 'Cliquez dans le viewer ou ici pour surligner.'}</div>
              </>
            )}
          </Panel>

          <Panel title="Fichiers associés">
            {m.files.map((f) => (
              <div key={f.id} className={`frow${f.availability === 'windows_only' ? ' frow-miss' : ''}`}>
                <div className="fn"><span>{f.name}</span><Tag>{f.format.toUpperCase()}</Tag></div>
                <div className="fs">
                  <span className="mono">{fmtBytes(f.size_bytes)}</span>
                  {f.availability === 'windows_only' ? <StatusDot status="unavailable" label="Windows uniquement, non copié" /> : <StatusDot status="online" label="Copié sur Linux" />}
                  <span className="mono" title={f.sha256}>sha256 {shortSha(f.sha256)}…</span>
                  {f.unit && <span className="mono">unité {f.unit}</span>}
                </div>
                {f.rel_path && f.rel_path !== f.name && <div className="fs"><span className="mono">{f.rel_path}</span></div>}
              </div>
            ))}
            {m.same_content_models && m.same_content_models.length > 0 && (
              <p className="hint">Même contenu que : {m.same_content_models.map((o) => <Link key={o} to={`/modele/${o}`} className="mono">{o.slice(0, 12)}…</Link>)}</p>
            )}
          </Panel>

          <Panel title="Versions (immuables)" flush>
            <List label="Versions" items={versions} getKey={(v) => v.id} selectedKey={ver?.id} onSelect={(v) => setVIdx(versions.indexOf(v))}
              render={(v) => (
                <span className="ver" style={{ display: 'flex', width: '100%' }}>
                  <span className="t"><strong>{v.label}</strong>{versions[0] === v && <Tag tone="accent">Dernière</Tag>}<span className="d">{fmtDate(v.created_at)}</span></span>
                  <span className="n">{v.note || 'Sans note'}</span>
                  <span className="h">{v.source_file_ids.length} fichier(s){v.parent_id ? ' · issue de la version précédente' : ''}</span>
                </span>
              )} />
          </Panel>
          {m.versions.length > 1 && <p className="hint" style={{ margin: 0 }}>L'aperçu et l'analyse portent sur la version la plus récente ; les anciennes versions restent consultables dans l'historique.</p>}

          <Panel title="Provenance et licence">
            <dl className="dl">
              <dt>Origine</dt><dd>{m.model.provenance?.source || (m.model.provenance?.origin_path ? 'Import' : <Tag tone="warn">Non renseignée</Tag>)}</dd>
              {m.model.provenance?.author && <><dt>Auteur</dt><dd>{m.model.provenance.author}</dd></>}
              {m.model.provenance?.url && <><dt>Source</dt><dd className="mono">{m.model.provenance.url}</dd></>}
              {m.model.provenance?.origin_path && <><dt>Chemin d'origine</dt><dd className="mono">{m.model.provenance.origin_path}</dd></>}
              <dt>Licence</dt><dd>{m.model.license || <Tag tone="warn">Non renseignée</Tag>}</dd>
              <dt>Collection</dt><dd>{m.collection_path ?? 'Aucune'}</dd>
              <dt>Importé</dt><dd className="mono">{fmtDate(m.model.provenance?.imported_at ?? m.model.created_at)}</dd>
              {cur && <><dt>Empreinte</dt><dd className="mono" title={cur.sha256}>{shortSha(cur.sha256)}…</dd></>}
            </dl>
            {m.model.description && <p style={{ margin: 'var(--sp-2) 0 0', color: 'var(--text-2)' }}>{m.model.description}</p>}
          </Panel>

          <Panel title="Modifier">
            <div className="ops-grid">
              <Button size="sm" icon={<Pencil size={14} />} disabled={trashed} onClick={() => { setEdit('rename'); setVal(m.model.name); }}>Renommer…</Button>
              <Button size="sm" icon={<FolderInput size={14} />} disabled={trashed} onClick={() => { setEdit('move'); setVal(m.collection_path ?? ''); }}>Déplacer…</Button>
              <Button size="sm" icon={<Tags size={14} />} disabled={trashed} onClick={() => { setEdit('tags'); setVal(''); }}>Étiquettes…</Button>
              <Button size="sm" disabled={trashed} onClick={() => { setEdit('license'); setVal(m.model.license ?? ''); }}>Licence…</Button>
              <Button size="sm" variant="danger" icon={<Trash2 size={14} />} disabled={trashed} onClick={() => start('Mettre à la corbeille', [{ op: 'delete', model_ids: [id] }])}>Corbeille…</Button>
            </div>
            <div className="hint">Chaque modification montre d'abord ce qui changera, puis demande confirmation. Les originaux ne sont jamais touchés.</div>
          </Panel>

          <Panel title="Journal" flush>
            {m.audit.length === 0 ? <p className="hint" style={{ margin: 'var(--sp-3)' }}>Aucune entrée.</p> : (
              <ul className="audit">
                {m.audit.slice(0, 8).map((l, i) => <li key={i}><span className="mono">{fmtDate(l.at)}</span><span>{l.operation}</span><span className="sub">{l.actor}</span></li>)}
              </ul>
            )}
          </Panel>
        </div>
      </div>

      <Dialog open={edit !== null} onClose={() => setEdit(null)} title={{ rename: 'Renommer le modèle', move: 'Déplacer vers une collection', tags: 'Modifier les étiquettes', license: 'Définir la licence', null: '' }[String(edit) as 'rename']}
        footer={<><Button onClick={() => setEdit(null)}>Annuler</Button><Button variant="primary" disabled={!val.trim()} onClick={submitEdit}>Prévisualiser</Button></>}>
        <Field label={{ rename: 'Nouveau nom', move: "Collection (identifiant, nom ou chemin « A/B » ; créée si elle n'existe pas)", tags: 'Étiquettes à ajouter, séparées par des virgules ; préfixer par « - » pour retirer', license: 'Licence (SPDX ou texte libre)', null: '' }[String(edit) as 'rename']}>
          <Input value={val} onChange={(e) => setVal(e.target.value)} autoFocus onKeyDown={(e) => { if (e.key === 'Enter') submitEdit(); }} />
        </Field>
        <p className="hint" style={{ margin: 0 }}>Un aperçu des changements s'affiche avant toute modification.</p>
      </Dialog>
      <OperationFlow title={opTitle} operations={ops} onClose={() => setOps(null)} onApplied={() => reload()} />
    </>
  );
}
function PreviewPlaceholder({ icon, title, text, tone }: { icon: React.ReactNode; title: string; text: string; tone?: 'warn' | 'err' }) {
  return (
    <div className="v3 v3-placeholder">
      <div className={`state${tone === 'err' ? ' state-err' : ''}`} role={tone === 'err' ? 'alert' : 'status'} style={{ maxWidth: 420, background: 'var(--surface)' }}>
        {icon}<h3>{title}</h3><p>{text}</p>
      </div>
    </div>
  );
}

function AnalysisBlock({ a, cad }: { a: ModelDetails['analysis']; cad?: ModelDetails['cad'] }) {
  if (a.state === 'pending') return <p className="hint" style={{ margin: 0 }} role="status"><span className="spinner" aria-hidden /> Analyse en cours…</p>;
  if (a.state === 'backend_missing') return <Notice title="Analyse indisponible">{a.note ?? cad?.note ?? 'Backend CAO absent.'}</Notice>;
  if (a.state === 'error') return <Notice tone="err" title="L'analyse a échoué">{a.note ?? 'Maillage illisible ou vide.'}</Notice>;
  if (a.state === 'none') return <p className="hint" style={{ margin: 0 }}>Pas encore analysé. {a.note}</p>;
  return (
    <>
      <dl className="dl">
        <dt>Triangles</dt><dd className="mono">{a.triangles !== undefined ? fmtNum(a.triangles, 0) : 'inconnu'}</dd>
        <dt>Objets</dt><dd className="mono">{a.objects ?? 'inconnu'}</dd>
        <dt>Volume</dt><dd className="mono">{a.volume_mm3 !== undefined ? `${fmtNum(a.volume_mm3 / 1000)} cm³` : 'inconnu'}</dd>
        <dt>Surface</dt><dd className="mono">{a.area_mm2 !== undefined ? `${fmtNum(a.area_mm2 / 100)} cm²` : 'inconnue'}</dd>
        <dt>Étanche</dt><dd>{a.watertight ? <Tag tone="ok">oui</Tag> : <Tag tone="warn">non ou inconnu</Tag>}</dd>
        <dt>Arêtes ouvertes</dt><dd className="mono">{a.open_edges ?? 0}</dd>
        <dt>Arêtes non manifold</dt><dd className="mono">{a.non_manifold_edges ?? 0}</dd>
        <dt>Composantes</dt><dd className="mono">{a.components ?? 'inconnu'}</dd>
        {a.unit && <><dt>Unité</dt><dd className="mono">{a.unit === 'unknown' ? 'inconnue' : a.unit}</dd></>}
      </dl>
      {a.unit_assumed && <p className="hint">{a.unit_note || 'Unité supposée en millimètres : confirmez-la pour des dimensions fiables.'}</p>}
      {a.warnings && a.warnings.length > 0 && <ul className="hint" style={{ paddingLeft: 'var(--sp-4)', margin: 'var(--sp-2) 0 0' }}>{a.warnings.map((w) => <li key={w}>{w}</li>)}</ul>}
      {a.level && <div className="hint">Niveau de confiance : <span className="mono">{a.level}</span> (mesures géométriques, aucune certification mécanique).</div>}
    </>
  );
}

export function ModelNotFound() {
  return (
    <>
      <div className="page-head"><span className="eyebrow">Espace</span><h1>Modèle introuvable</h1></div>
      <EmptyState title="Ce modèle n'existe pas dans le catalogue" action={<Link to="/bibliotheque"><Button size="sm">Retour à la bibliothèque</Button></Link>} />
    </>
  );
}
