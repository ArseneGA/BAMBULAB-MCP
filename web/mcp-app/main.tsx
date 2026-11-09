import { StrictMode, useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { createRoot } from 'react-dom/client';
import { App } from '@modelcontextprotocol/ext-apps';
import { AlertTriangle, ArrowLeft } from 'lucide-react';
import '../src/design/tokens.css';
import '../src/design/base.css';
import '../src/viewer/viewer.css';
import './mcp-app.css';
import { GlbView } from '../src/viewer/GlbView';
import type { LoadedModel } from '../src/viewer/glbEngine';
import type { ModelDetails, SearchResult } from '../src/api/types';
import type { ConfirmationView } from '../src/api/printTypes';
import { LaunchConfirm, StopConfirm } from './launch';
import { ProgressCard, normalizeProgress, type Prog } from './progress';
import { LayerViewer, isLayersInfo, type LayersInfo } from './layers';
import { LiveCard, normalizeLive, type Live } from './live';

/**
 * Vue MCP App de l'atelier (dossier §14.3). Servie sous ui://bambu/search.html et ui://bambu/model.html.
 * Aucune I/O réseau : tout passe par app.callServerTool (l'iframe ne peut pas joindre localhost de façon fiable).
 *  - résultat de search_models      -> liste de modèles ;
 *  - résultat de get_print_status / wait_for_print_change -> carte de suivi en direct (live.tsx) ;
 *  - résultat de request_print_confirmation / cancel_print -> confirmation de lancement / d'arrêt par code de lancement (launch.tsx) ;
 *  - résultat de get_model_details  -> fiche compacte + viewer 3D (maillage chargé par tranches via get_mesh_chunk) ;
 *  - sélection d'une sous-partie    -> app.updateModelContext (le modèle de langage la voit au prochain tour).
 */

type View = { kind: 'live'; data: Live } | { kind: 'progress'; data: Prog } | { kind: 'layers'; data: LayersInfo } | { kind: 'confirm'; data: ConfirmationView } | { kind: 'stop'; stopUrl: string; question?: string } | { kind: 'search'; data: SearchResult } | { kind: 'model'; data: ModelDetails; from?: SearchResult } | { kind: 'text'; text: string } | { kind: 'error'; text: string };

interface ToolResultLike { structuredContent?: unknown; content?: Array<{ type: string; text?: string }>; isError?: boolean }

const textOf = (r: ToolResultLike) => (r.content ?? []).filter((c) => c.type === 'text').map((c) => c.text ?? '').join('\n').trim();

function classify(r: ToolResultLike): View {
  if (r.isError) return { kind: 'error', text: textOf(r) || "L'outil a renvoyé une erreur." };
  const s = r.structuredContent as Record<string, unknown> | undefined;
  if (isLayersInfo(s) && Array.isArray((s as { sheet_layers?: unknown }).sheet_layers)) return { kind: 'layers', data: s };
  const live = normalizeLive(s); // suivi d'impression (get_print_status, wait_for_print_change) : avant la progression d'un travail
  if (live) return { kind: 'live', data: live };
  const prog = normalizeProgress(s);
  if (prog && !(s && (typeof s.confirmation_id === 'string' || Array.isArray(s.models)))) return { kind: 'progress', data: prog };
  if (s && typeof s.confirmation_id === 'string' && typeof s.binding === 'object') return { kind: 'confirm', data: s as unknown as ConfirmationView };
  if (s && s.outcome === 'stop_requested' && typeof s.stop_url === 'string') return { kind: 'stop', stopUrl: s.stop_url, question: typeof s.question === 'string' ? s.question : undefined };
  if (s && Array.isArray(s.models) && typeof s.total === 'number') return { kind: 'search', data: s as unknown as SearchResult };
  if (s && typeof s.model === 'object' && Array.isArray(s.files)) return { kind: 'model', data: s as unknown as ModelDetails };
  return { kind: 'text', text: textOf(r) || 'Aucun contenu à afficher.' };
}

const b64ToBytes = (b64: string) => { const bin = atob(b64); const u = new Uint8Array(bin.length); for (let i = 0; i < bin.length; i++) u[i] = bin.charCodeAt(i); return u; };
const fmtDims = (d?: { x: number; y: number; z: number }) => (d ? `${+d.x.toFixed(1)} × ${+d.y.toFixed(1)} × ${+d.z.toFixed(1)} mm` : 'dimensions inconnues');

interface Chunk { chunk_count: number; size_bytes: number; index: number; data: string }

/** Charge le GLB d'aperçu par tranches (≤ 100 000 caractères base64), 4 requêtes en parallèle. */
async function fetchGlb(app: App, modelId: string, signal: AbortSignal, progress: (r: number) => void): Promise<ArrayBuffer> {
  const get = async (index: number): Promise<Chunk> => {
    if (signal.aborted) throw new DOMException('abort', 'AbortError');
    const res = await app.callServerTool({ name: 'get_mesh_chunk', arguments: { model_id: modelId, index } }) as ToolResultLike;
    if (res.isError) throw new Error(textOf(res) || 'Tranche refusée');
    const c = res.structuredContent as Chunk | undefined;
    if (!c || typeof c.data !== 'string') throw new Error('Réponse de get_mesh_chunk invalide');
    return c;
  };
  const first = await get(0);
  const parts: Uint8Array[] = new Array(first.chunk_count);
  parts[0] = b64ToBytes(first.data);
  let next = 1, done = 1;
  progress(done / first.chunk_count);
  await Promise.all(Array.from({ length: Math.min(4, first.chunk_count - 1) }, async () => {
    while (next < first.chunk_count) {
      const i = next++;
      parts[i] = b64ToBytes((await get(i)).data);
      progress(++done / first.chunk_count);
    }
  }));
  const out = new Uint8Array(first.size_bytes);
  let off = 0;
  for (const p of parts) { out.set(p, off); off += p.length; }
  if (off !== first.size_bytes) throw new Error(`Aperçu incomplet (${off}/${first.size_bytes} octets)`);
  return out.buffer;
}

function Root() {
  const [app, setApp] = useState<App | null>(null);
  const [view, setView] = useState<View | null>(null);
  const [status, setStatus] = useState<'connecting' | 'ready' | 'standalone'>('connecting');

  useEffect(() => {
    const a = new App({ name: 'Atelier Bambu Lab', version: '0.3.0' });
    const theme = (t?: string) => { if (t === 'light' || t === 'dark') document.documentElement.dataset.theme = t; };
    a.ontoolresult = (r) => setView(classify(r as ToolResultLike));
    a.onhostcontextchanged = (ctx) => theme(ctx.theme);
    let alive = true;
    // connect() DOIT être appelé après la pose des gestionnaires, sinon la vue reste invisible.
    a.connect().then(() => { if (!alive) return; theme(a.getHostContext()?.theme); setApp(a); setStatus('ready'); }, () => { if (alive) setStatus('standalone'); });
    const t = window.setTimeout(() => { if (alive) setStatus((s) => (s === 'connecting' ? 'standalone' : s)); }, 4000);
    return () => { alive = false; window.clearTimeout(t); };
  }, []);

  if (status === 'standalone') return <Box><p className="msg">Cette vue s'affiche dans un hôte MCP Apps (Claude Desktop, par exemple). Hors d'un hôte, aucune donnée n'est disponible.</p></Box>;
  if (!view) return <Box><p className="msg" role="status"><span className="spinner" aria-hidden /> {status === 'connecting' ? 'Connexion à la conversation…' : 'En attente du résultat…'}</p></Box>;
  if (!app) return null;
  if (view.kind === 'error') return <Box><p className="msg msg-err" role="alert"><AlertTriangle size={14} aria-hidden /> {view.text}</p></Box>;
  if (view.kind === 'text') return <Box><pre className="msg-pre">{view.text}</pre></Box>;
  if (view.kind === 'live') return <Box><LiveCard app={app} first={view.data} /></Box>;
  if (view.kind === 'progress') return <Box><ProgressCard app={app} first={view.data} /></Box>;
  if (view.kind === 'layers') return <Box><LayerViewer app={app} info={view.data} /></Box>;
  if (view.kind === 'confirm') return <Box><LaunchConfirm app={app} first={view.data} /></Box>;
  if (view.kind === 'stop') return <Box><StopConfirm app={app} stopUrl={view.stopUrl} question={view.question} /></Box>;
  if (view.kind === 'search') return <Box><Search app={app} data={view.data} onOpen={(d) => setView({ kind: 'model', data: d, from: view.data })} /></Box>;
  return <Box><Model app={app} data={view.data} onBack={view.from ? () => setView({ kind: 'search', data: view.from! }) : undefined} /></Box>;
}

const Box = ({ children }: { children: React.ReactNode }) => <main className="mcp">{children}</main>;

function Search({ app, data, onOpen }: { app: App; data: SearchResult; onOpen: (d: ModelDetails) => void }) {
  const [busy, setBusy] = useState<string | null>(null);
  const [err, setErr] = useState<string>();
  const open = async (id: string) => {
    setBusy(id); setErr(undefined);
    try {
      const r = await app.callServerTool({ name: 'get_model_details', arguments: { model_id: id } }) as ToolResultLike;
      const v = classify(r);
      if (v.kind === 'model') onOpen(v.data); else setErr(v.kind === 'error' || v.kind === 'text' ? v.text : 'Réponse inattendue');
    } catch (e) { setErr((e as Error).message); } finally { setBusy(null); }
  };
  if (data.models.length === 0) return <p className="msg">Aucun modèle ne correspond à la recherche.</p>;
  return (
    <>
      <header className="head"><h1>Résultats</h1><span className="mono dim">{data.models.length} sur {data.total}{data.has_more ? ' · d\'autres résultats existent' : ''}</span></header>
      <ul className="results" aria-label="Résultats de recherche">
        {data.models.map((m) => (
          <li key={m.id}>
            <button type="button" className="result" disabled={busy !== null} aria-busy={busy === m.id} onClick={() => open(m.id)}>
              <span className="r-name">{m.name}</span>
              <span className="r-meta mono">{m.formats.map((f) => f.toUpperCase()).join('/')} · {fmtDims(m.dimensions)}{m.versions > 1 ? ` · ${m.versions} versions` : ''}</span>
              <span className="r-tags">{m.availability === 'windows_only' ? <span className="tag tag-warn">Windows seul</span> : <span className="tag">{m.collection_path ?? 'sans collection'}</span>}{m.preview.state !== 'ready' && <span className="tag">aperçu : {m.preview.state}</span>}</span>
            </button>
          </li>
        ))}
      </ul>
      {err && <p className="msg msg-err" role="alert"><AlertTriangle size={14} aria-hidden /> {err}</p>}
      <p className="foot">Choisir un modèle ouvre sa fiche avec le viewer 3D.</p>
    </>
  );
}

function Model({ app, data, onBack }: { app: App; data: ModelDetails; onBack?: () => void }) {
  const [part, setPart] = useState<string | null>(null);
  const [loaded, setLoaded] = useState<LoadedModel | null>(null);
  const m = data.model;
  const cur = data.files.find((f) => f.id === data.current_file_id) ?? data.files[data.files.length - 1];
  const ver = data.versions[data.versions.length - 1];
  const dims = data.analysis.size_mm ?? m.dimensions;
  const label = `${m.name}${ver ? ` ${ver.label}` : ''}`;
  const ctxLine = useRef('');

  /** Sélection -> contexte du modèle de langage (pas de réponse immédiate). */
  const publish = useCallback((partLabel: string | null, size?: string) => {
    const text = partLabel
      ? `Dans l'interface, l'utilisateur a sélectionné la sous-partie « ${partLabel} »${size ? ` (${size})` : ''} du modèle « ${m.name} » (model_id=${m.id}${ver ? `, version ${ver.label}` : ''}${cur ? `, fichier ${cur.name}` : ''}).`
      : `Dans l'interface, l'utilisateur consulte le modèle « ${m.name} » (model_id=${m.id}${ver ? `, version ${ver.label}` : ''}${cur ? `, fichier ${cur.name}` : ''}) sans sous-partie sélectionnée.`;
    if (text === ctxLine.current) return;
    ctxLine.current = text;
    app.updateModelContext({ content: [{ type: 'text', text }] }).catch(() => { /* hôte sans updateModelContext : sans effet */ });
  }, [app, m.id, m.name, ver, cur]);

  useEffect(() => { publish(null); }, [publish]);

  const loader = useMemo(() => (signal: AbortSignal, progress: (r: number) => void) => fetchGlb(app, m.id, signal, progress), [app, m.id]);
  const onPart = (id: string | null) => {
    setPart(id);
    const p = loaded?.parts.find((x) => x.id === id);
    publish(p ? p.label : null, p ? fmtDims(p.size) : undefined);
  };
  const ready = data.preview.state === 'ready';
  const parts = loaded?.parts ?? [];

  return (
    <>
      <header className="head">
        {onBack && <button type="button" className="back" onClick={onBack} aria-label="Retour aux résultats"><ArrowLeft size={14} aria-hidden /> Résultats</button>}
        <h1>{label}</h1>
      </header>
      <div className="sheet">
        <div className="viewer">
          {ready
            ? <GlbView compact caption={false} sourceKey={data.preview.sha256 ?? m.id} loader={loader} selectedPartId={part} onSelectPart={onPart} onLoaded={setLoaded} ariaLabel={`Aperçu 3D de ${m.name}, ${fmtDims(dims)}`} />
            : <div className="msg viewer-msg" role="status">{({ pending: "Aperçu en cours de calcul : redemandez la fiche dans quelques secondes.", backend_missing: 'Backend CAO absent : pas d\'aperçu pour ce fichier STEP.', windows_only: 'Fichier disponible sur Windows seulement : pas encore d\'aperçu.', error: "L'aperçu a échoué.", none: "Pas d'aperçu pour ce fichier." } as Record<string, string>)[data.preview.state] ?? 'Aperçu indisponible.'} {data.preview.note}</div>}
        </div>
        <div className="side">
          <dl className="dims mono">
            {(['x', 'y', 'z'] as const).map((a) => <div key={a}><dt>{a.toUpperCase()}</dt><dd>{dims ? `${+dims[a].toFixed(1)} mm` : '—'}</dd></div>)}
          </dl>
          <div className="sub-label">Sous-parties</div>
          {parts.length === 0 ? <p className="dim small">{ready ? 'Lecture du maillage…' : '—'}</p> : (
            <ul className="parts">
              {parts.map((p) => <li key={p.id}><button type="button" aria-pressed={part === p.id} onClick={() => onPart(part === p.id ? null : p.id)}>{p.label}</button></li>)}
            </ul>
          )}
          <p className="small dim">{data.files.length} fichier(s) · {data.versions.length} version(s){m.license ? ` · ${m.license}` : ' · licence inconnue'}{data.analysis.unit_assumed ? ' · unité supposée' : ''}</p>
        </div>
      </div>
      <div className="context" role="status"><span className="dim">Sélection transmise au contexte :</span> <strong>{part ? `${label} · ${parts.find((p) => p.id === part)?.label ?? part}` : label}</strong></div>
      {data.analysis.warnings && data.analysis.warnings.length > 0 && <p className="small dim">{data.analysis.warnings[0]}</p>}
    </>
  );
}

createRoot(document.getElementById('root')!).render(<StrictMode><Root /></StrictMode>);
