import { useCallback, useEffect, useRef, useState } from 'react';
import { AlertTriangle, Pause, Play, SkipBack, SkipForward } from 'lucide-react';

/**
 * Visionneuse de trajectoires (ui://bambu/layers.html, liée à show_layers) : curseur de couche, tracé 2D des filets
 * extrudés par type (FEATURE). Les segments viennent de l'outil réservé à la vue get_layer_paths (entiers de 0,05 mm,
 * taille bornée côté serveur) ; la vue ne lit aucun fichier et n'ouvre aucune connexion. Lecture seule.
 */

export interface AppLike {
  callServerTool(p: { name: string; arguments: Record<string, unknown> }): Promise<unknown>;
}
interface ToolResultLike { structuredContent?: unknown; content?: Array<{ type: string; text?: string }>; isError?: boolean }

export interface LayersInfo { artifact_sha256: string; plate: number; layers_total: number; sheet_layers?: number[] }
export interface LayerPaths {
  layer: number; layers_total: number; z_mm?: number; unit_mm: number; features: string[];
  paths: Array<{ f: number; p: number[] }>; bbox_mm: [number, number, number, number]; segments: number; points: number; tolerance_mm?: number;
}
export interface Box { minX: number; minY: number; maxX: number; maxY: number }

/** Mêmes couleurs que les aperçus PNG du serveur (palette des types de trajectoires). */
export const FEATURE_COLORS = ['#2a4d8f', '#c2572d', '#3b8a5a', '#8a3b7a', '#b0921f', '#1f8a92', '#6b6b6b'];
export const featureColor = (i: number) => FEATURE_COLORS[((i % FEATURE_COLORS.length) + FEATURE_COLORS.length) % FEATURE_COLORS.length];

export function isLayersInfo(s: unknown): s is LayersInfo {
  const o = s as Partial<LayersInfo> | undefined;
  return !!o && typeof o.artifact_sha256 === 'string' && typeof o.layers_total === 'number' && typeof o.plate === 'number';
}

export function isLayerPaths(s: unknown): s is LayerPaths {
  const o = s as Partial<LayerPaths> | undefined;
  return !!o && typeof o.layer === 'number' && typeof o.unit_mm === 'number' && Array.isArray(o.paths) && Array.isArray(o.features) && Array.isArray(o.bbox_mm);
}

/** Cadre commun (union, avec marge) : les couches restent comparables quand on déplace le curseur. */
export function unionBox(boxes: Array<[number, number, number, number]>): Box | undefined {
  const real = boxes.filter((b) => b.every(Number.isFinite) && (b[2] > b[0] || b[3] > b[1]));
  if (real.length === 0) return undefined;
  const b = { minX: Math.min(...real.map((x) => x[0])), minY: Math.min(...real.map((x) => x[1])), maxX: Math.max(...real.map((x) => x[2])), maxY: Math.max(...real.map((x) => x[3])) };
  const w = Math.max(b.maxX - b.minX, 20), h = Math.max(b.maxY - b.minY, 20);
  const cx = (b.minX + b.maxX) / 2, cy = (b.minY + b.maxY) / 2;
  return { minX: cx - w * 0.54, maxX: cx + w * 0.54, minY: cy - h * 0.54, maxY: cy + h * 0.54 };
}

/** Dessin d'une couche sur un contexte 2D (séparé du composant pour être testé). Retourne le nombre de polylignes tracées. */
export function drawLayer(ctx: CanvasRenderingContext2D, W: number, H: number, data: LayerPaths | undefined, box: Box | undefined, dark = false): number {
  ctx.fillStyle = dark ? '#161513' : '#f3f1ed';
  ctx.fillRect(0, 0, W, H);
  if (!box) return 0;
  const sc = Math.min(W / (box.maxX - box.minX), H / (box.maxY - box.minY));
  const px = (x: number) => W / 2 + (x - (box.minX + box.maxX) / 2) * sc;
  const py = (y: number) => H / 2 - (y - (box.minY + box.maxY) / 2) * sc;
  if (sc >= 2) {
    ctx.strokeStyle = dark ? '#2a2824' : '#dddad3'; ctx.lineWidth = 1; ctx.beginPath();
    for (let g = Math.ceil(box.minX / 10) * 10; g <= box.maxX; g += 10) { ctx.moveTo(px(g), py(box.minY)); ctx.lineTo(px(g), py(box.maxY)); }
    for (let g = Math.ceil(box.minY / 10) * 10; g <= box.maxY; g += 10) { ctx.moveTo(px(box.minX), py(g)); ctx.lineTo(px(box.maxX), py(g)); }
    ctx.stroke();
  }
  if (!data) return 0;
  ctx.lineCap = 'round'; ctx.lineJoin = 'round';
  ctx.lineWidth = Math.max(1, 0.45 * sc);
  let drawn = 0;
  for (const path of data.paths) {
    if (path.p.length < 4) continue;
    ctx.strokeStyle = featureColor(path.f);
    ctx.beginPath();
    ctx.moveTo(px(path.p[0] * data.unit_mm), py(path.p[1] * data.unit_mm));
    for (let i = 2; i + 1 < path.p.length; i += 2) ctx.lineTo(px(path.p[i] * data.unit_mm), py(path.p[i + 1] * data.unit_mm));
    ctx.stroke();
    drawn++;
  }
  return drawn;
}

const sc = <T,>(r: unknown): T | undefined => ((r as ToolResultLike | undefined)?.structuredContent as T | undefined);
const CACHE_MAX = 24;

export function LayerViewer({ app, info, debounceMs = 120, playMs = 160 }: { app: AppLike; info: LayersInfo; debounceMs?: number; playMs?: number }) {
  const total = Math.max(1, info.layers_total);
  const [layer, setLayer] = useState(1);
  const [data, setData] = useState<LayerPaths>();
  const [box, setBox] = useState<Box>();
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string>();
  const [playing, setPlaying] = useState(false);
  const cache = useRef(new Map<number, LayerPaths>());
  const boxes = useRef<Array<[number, number, number, number]>>([]);
  const canvas = useRef<HTMLCanvasElement>(null);

  const fetchLayer = useCallback(async (l: number): Promise<LayerPaths> => {
    const hit = cache.current.get(l);
    if (hit) return hit;
    const r = await app.callServerTool({ name: 'get_layer_paths', arguments: { artifact_id: info.artifact_sha256, plate: info.plate, layer: l, layers_total: total } });
    if ((r as ToolResultLike | undefined)?.isError) throw new Error(((r as ToolResultLike).content ?? []).map((c) => c.text ?? '').join(' ').trim() || 'Couche refusée');
    const d = sc<unknown>(r);
    if (!isLayerPaths(d)) throw new Error('Réponse de get_layer_paths invalide');
    if (cache.current.size >= CACHE_MAX) cache.current.delete(cache.current.keys().next().value as number);
    cache.current.set(l, d);
    return d;
  }, [app, info.artifact_sha256, info.plate, total]);

  const grow = useCallback((d: LayerPaths) => {
    boxes.current.push(d.bbox_mm);
    setBox(unionBox(boxes.current));
  }, []);

  // cadre : première, médiane et dernière couche (le cadre ne saute plus ensuite)
  useEffect(() => {
    let alive = true;
    (async () => {
      for (const l of Array.from(new Set([1, Math.ceil(total / 2), total]))) {
        try { const d = await fetchLayer(l); if (alive) grow(d); } catch { /* le cadre se complétera avec les couches vues */ }
      }
    })();
    return () => { alive = false; };
  }, [fetchLayer, grow, total]);

  // couche courante (anti-rebond du curseur)
  useEffect(() => {
    let alive = true;
    setBusy(true);
    const t = window.setTimeout(async () => {
      try {
        const d = await fetchLayer(layer);
        if (!alive) return;
        setData(d); setErr(undefined); grow(d);
      } catch (e) { if (alive) setErr((e as Error).message); }
      finally { if (alive) setBusy(false); }
    }, debounceMs);
    return () => { alive = false; window.clearTimeout(t); };
  }, [layer, fetchLayer, grow, debounceMs]);

  useEffect(() => {
    if (!playing) return;
    const id = window.setInterval(() => setLayer((l) => { if (l >= total) { setPlaying(false); return l; } return l + 1; }), playMs);
    return () => window.clearInterval(id);
  }, [playing, total, playMs]);

  useEffect(() => {
    const c = canvas.current; const ctx = c?.getContext('2d');
    if (!c || !ctx) return;
    drawLayer(ctx, c.width, c.height, data, box, document.documentElement.dataset.theme === 'dark');
  }, [data, box]);

  const used = new Set((data?.paths ?? []).map((p) => p.f));
  return (
    <section className="layers" aria-label="Trajectoires par couche">
      <header className="head"><h1>Trajectoires par couche</h1><span className="mono dim">plaque {info.plate} · {total} couches</span></header>
      <canvas ref={canvas} className="layers-canvas" width={640} height={440} role="img"
        aria-label={`Couche ${layer} sur ${total}${data?.z_mm ? `, Z = ${data.z_mm.toFixed(2)} mm` : ''}`} />
      <div className="layers-ctl">
        <button type="button" className="icon-btn" onClick={() => setLayer((l) => Math.max(1, l - 1))} disabled={layer <= 1} aria-label="Couche précédente"><SkipBack size={14} aria-hidden /></button>
        <button type="button" className="icon-btn" onClick={() => { if (layer >= total) setLayer(1); setPlaying((p) => !p); }} aria-label={playing ? 'Pause' : 'Lecture'}>{playing ? <Pause size={14} aria-hidden /> : <Play size={14} aria-hidden />}</button>
        <button type="button" className="icon-btn" onClick={() => setLayer((l) => Math.min(total, l + 1))} disabled={layer >= total} aria-label="Couche suivante"><SkipForward size={14} aria-hidden /></button>
        <input type="range" min={1} max={total} step={1} value={layer} onChange={(e) => { setPlaying(false); setLayer(Number(e.target.value)); }} aria-label="Couche" className="layers-slider" />
        <span className="mono layers-num" role="status" aria-busy={busy}>{layer} / {total}{data?.z_mm ? ` · Z ${data.z_mm.toFixed(2)} mm` : ''}</span>
      </div>
      {data && data.features.length > 0 && (
        <ul className="legend" aria-label="Types de trajectoires">
          {data.features.map((f, i) => used.has(i) && <li key={f}><span className="swatch" style={{ background: featureColor(i) }} aria-hidden /> {f}</li>)}
        </ul>
      )}
      {data && data.tolerance_mm ? <p className="small dim">Couche très dense : tracé simplifié (tolérance {data.tolerance_mm} mm).</p> : null}
      {err && <p className="msg msg-err" role="alert"><AlertTriangle size={14} aria-hidden /> {err}</p>}
      <p className="foot">Filets extrudés seulement (déplacements à vide et rétractions non tracés). Grille 10 mm.</p>
    </section>
  );
}
