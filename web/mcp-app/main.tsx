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
import type { ModelDetails } from '../src/api/types';
import type { ConfirmationView } from '../src/api/printTypes';
import { LaunchConfirm, StopConfirm } from './launch';
import { ProgressCard, normalizeProgress, type Prog } from './progress';
import { LayerViewer, isLayersInfo, type LayersInfo } from './layers';
import { LiveCard, normalizeLive, type Live } from './live';
import { GcodePreviewCard, isGcodePreview, type GcodePreviewInfo } from './gcode';
import { FileDropCard, isFileDrop, type DropInfo } from './drop';
import { CardView, classifyCard, type CardData } from './cards';
import { StrengthCard, extractStrength, type StrengthView } from './strength';

/**
 * Vue MCP App de l'atelier (dossier §14.3). Servie sous ui://bambu/search.html et ui://bambu/model.html.
 * Aucune I/O réseau : tout passe par app.callServerTool (l'iframe ne peut pas joindre localhost de façon fiable).
 *  - résultats de search_models, list_collection, compare_variants, estimate_cost, get_project_costs, get_usage_dashboard, list_spools,
 *    get_ams_state, list_print_history, list_print_queue, get_print_log, list_printers -> cartes (cards/, docs/research/mcp-cards.md) ;
 *  - résultat de get_print_status / wait_for_print_change -> carte de suivi en direct (live.tsx) ;
 *  - résultat de request_print_confirmation / cancel_print -> confirmation de lancement / d'arrêt par code de lancement (launch.tsx) ;
 *  - résultat de get_model_details  -> fiche compacte + viewer 3D (maillage chargé par tranches via get_mesh_chunk) ;
 *  - sélection d'une sous-partie    -> app.updateModelContext (le modèle de langage la voit au prochain tour).
 */

type View = { kind: 'gcode'; data: GcodePreviewInfo } | { kind: 'drop'; data: DropInfo } | { kind: 'imported'; modelId: string } | { kind: 'strength'; data: StrengthView } | { kind: 'live'; data: Live } | { kind: 'progress'; data: Prog } | { kind: 'layers'; data: LayersInfo } | { kind: 'confirm'; data: ConfirmationView } | { kind: 'stop'; stopUrl: string; question?: string } | { kind: 'card'; card: CardData } | { kind: 'model'; data: ModelDetails } | { kind: 'text'; text: string } | { kind: 'error'; text: string };

interface ToolResultLike { structuredContent?: unknown; content?: Array<{ type: string; text?: string; data?: string; mimeType?: string }>; isError?: boolean }

const textOf = (r: ToolResultLike) => (r.content ?? []).filter((c) => c.type === 'text').map((c) => c.text ?? '').join('\n').trim();

function classify(r: ToolResultLike): View {
  if (r.isError) return { kind: 'error', text: textOf(r) || "L'outil a renvoyé une erreur." };
  const s = r.structuredContent as Record<string, unknown> | undefined;
  if (isGcodePreview(s)) return { kind: 'gcode', data: s }; // show_gcode_preview : visionneuse 3D couche par couche
  if (isFileDrop(s)) return { kind: 'drop', data: s }; // show_file_drop : carte de dépôt
  // envoi / import terminé (upload_file, get_upload_status d'un lien, import_model) : la carte de la pièce, pas un rendu fabriqué
  const imported = (s?.file as { model_id?: unknown } | undefined)?.model_id;
  if (typeof imported === 'string' && imported && (s?.status === 'complete' || s?.done === true)) return { kind: 'imported', modelId: imported };
  const strength = extractStrength(s); // test de résistance (assess_strength, get_strength_report) : l'image PNG est dans le contenu du résultat
  if (strength) {
    const detail = (s as { detail?: { test_id?: string }; test_id?: string } | undefined);
    return { kind: 'strength', data: { result: strength, testId: detail?.detail?.test_id ?? detail?.test_id, images: (r.content ?? []).filter((c) => c.type === 'image' && typeof c.data === 'string').map((c) => ({ data: c.data as string, mime: c.mimeType ?? 'image/png' })) } };
  }
  const card = classifyCard(s); // cartes bibliothèque, comparaison, coûts, bobines, impressions, imprimantes (formes disjointes des autres vues)
  if (card) return { kind: 'card', card };
  if (isLayersInfo(s) && Array.isArray((s as { sheet_layers?: unknown }).sheet_layers)) return { kind: 'layers', data: s };
  const live = normalizeLive(s); // suivi d'impression (get_print_status, wait_for_print_change) : avant la progression d'un travail
  if (live) return { kind: 'live', data: live };
  const prog = normalizeProgress(s);
  if (prog && !(s && (typeof s.confirmation_id === 'string' || Array.isArray(s.models)))) return { kind: 'progress', data: prog };
  if (s && typeof s.confirmation_id === 'string' && typeof s.binding === 'object') return { kind: 'confirm', data: s as unknown as ConfirmationView };
  if (s && s.outcome === 'stop_requested' && typeof s.stop_url === 'string') return { kind: 'stop', stopUrl: s.stop_url, question: typeof s.question === 'string' ? s.question : undefined };
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
  const [trail, setTrail] = useState<View[]>([]); // pile de retour : une carte qui ouvre un autre résultat (fiche, suivi, état) y empile la vue courante
  const [status, setStatus] = useState<'connecting' | 'ready' | 'standalone'>('connecting');

  useEffect(() => {
    const a = new App({ name: 'Atelier Bambu Lab', version: '0.3.0' });
    const theme = (t?: string) => { if (t === 'light' || t === 'dark') document.documentElement.dataset.theme = t; };
    a.ontoolresult = (r) => { setTrail([]); setView(classify(r as ToolResultLike)); };
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
  const navigate = (r: unknown) => { setTrail((t) => [...t, view]); setView(classify(r as ToolResultLike)); };
  const back = trail.length > 0 ? () => { setView(trail[trail.length - 1]); setTrail(trail.slice(0, -1)); } : undefined;
  const wrapBox = (node: React.ReactNode) => <Box card={view.kind === 'card'}>{back && <button type="button" className="back" onClick={back} aria-label="Retour"><ArrowLeft size={14} aria-hidden /> Retour</button>}{node}</Box>;
  if (view.kind === 'error') return wrapBox(<p className="msg msg-err" role="alert"><AlertTriangle size={14} aria-hidden /> {view.text}</p>);
  if (view.kind === 'text') return wrapBox(<pre className="msg-pre">{view.text}</pre>);
  if (view.kind === 'strength') return wrapBox(<StrengthCard view={view.data} />);
  if (view.kind === 'gcode') return wrapBox(<GcodePreviewCard app={app} info={view.data} />);
  if (view.kind === 'drop') return wrapBox(<FileDropCard app={app} info={view.data} renderPreview={(id) => <ModelById app={app} id={id} />} />);
  if (view.kind === 'imported') return wrapBox(<ModelById app={app} id={view.modelId} />);
  if (view.kind === 'live') return wrapBox(<LiveCard app={app} first={view.data} />);
  if (view.kind === 'progress') return wrapBox(<ProgressCard app={app} first={view.data} />);
  if (view.kind === 'layers') return wrapBox(<LayerViewer app={app} info={view.data} />);
  if (view.kind === 'confirm') return wrapBox(<LaunchConfirm app={app} first={view.data} />);
  if (view.kind === 'stop') return wrapBox(<StopConfirm app={app} stopUrl={view.stopUrl} question={view.question} />);
  if (view.kind === 'card') return wrapBox(<CardView app={app} card={view.card} onOpen={navigate} />);
  return wrapBox(<Model app={app} data={view.data} />);
}

const Box = ({ children, card }: { children: React.ReactNode; card?: boolean }) => <main className={card ? 'mcp mcp-card' : 'mcp'}>{children}</main>;

/** Carte de la pièce après un import ou un envoi : fiche + viewer 3D existants ; l'aperçu en cours de calcul est réinterrogé. */
function ModelById({ app, id, retryMs = 3000, maxTries = 12 }: { app: App; id: string; retryMs?: number; maxTries?: number }) {
  const [data, setData] = useState<ModelDetails>();
  const [err, setErr] = useState<string>();
  useEffect(() => {
    let alive = true, timer = 0, tries = 0;
    const load = async () => {
      try {
        const r = await app.callServerTool({ name: 'get_model_details', arguments: { model_id: id } }) as ToolResultLike;
        const v = classify(r);
        if (!alive) return;
        if (v.kind !== 'model') { setErr(v.kind === 'error' || v.kind === 'text' ? v.text : 'Réponse inattendue'); return; }
        setData(v.data);
        if (v.data.preview.state === 'pending' && ++tries < maxTries) timer = window.setTimeout(load, retryMs);
      } catch (e) { if (alive) setErr((e as Error).message); }
    };
    void load();
    return () => { alive = false; window.clearTimeout(timer); };
  }, [app, id, retryMs, maxTries]);
  if (err) return <p className="msg msg-err" role="alert"><AlertTriangle size={14} aria-hidden /> {err}</p>;
  if (!data) return <p className="msg" role="status"><span className="spinner" aria-hidden /> Ouverture de la fiche de la pièce…</p>;
  return <Model app={app} data={data} />;
}

function Model({ app, data }: { app: App; data: ModelDetails }) {
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
