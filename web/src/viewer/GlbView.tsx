import { useEffect, useRef, useState } from 'react';
import type { ReactNode } from 'react';
import { AlertTriangle, Grid3x3, Maximize } from 'lucide-react';
import { GlbViewer, readCssColors } from './glbEngine';
import type { LoadedModel, ViewName } from './glbEngine';
import './viewer.css';

export interface GlbViewProps {
  /** Identité du contenu : un changement recharge le modèle. */
  sourceKey: string;
  /** Fournit les octets du GLB (HTTP signé pour la web app, outil get_mesh_chunk pour la MCP App). */
  loader: (signal: AbortSignal, progress: (ratio: number) => void) => Promise<ArrayBuffer>;
  onLoaded?: (m: LoadedModel) => void;
  selectedPartId?: string | null;
  onSelectPart?: (id: string | null) => void;
  compact?: boolean;
  notice?: ReactNode;
  className?: string;
  ariaLabel?: string;
  /** Légende en bas à gauche ; false pour la masquer. */
  caption?: string | false;
}

const VIEWS: Array<{ v: ViewName; label: string; short: string; title: string }> = [
  { v: 'iso', label: 'Iso', short: 'Iso', title: 'Vue isométrique (3/4)' },
  { v: 'face', label: 'Face', short: 'F', title: 'Vue de face' },
  { v: 'dessus', label: 'Dessus', short: 'D', title: 'Vue de dessus' },
  { v: 'cote', label: 'Côté', short: 'C', title: 'Vue de côté' },
];

type Phase = { kind: 'loading'; ratio: number } | { kind: 'ready' } | { kind: 'error'; message: string };

function webglAvailable(): boolean {
  try {
    const c = document.createElement('canvas');
    return !!(c.getContext('webgl2') || c.getContext('webgl'));
  } catch { return false; }
}

export function GlbView({ sourceKey, loader, onLoaded, selectedPartId, onSelectPart, compact, notice, className, ariaLabel, caption }: GlbViewProps) {
  const host = useRef<HTMLDivElement>(null);
  const viewer = useRef<GlbViewer | null>(null);
  const [phase, setPhase] = useState<Phase>({ kind: 'loading', ratio: 0 });
  const [view, setView] = useState<ViewName>('iso');
  const [pickedLabel, setPickedLabel] = useState<string | null>(null);
  const cb = useRef({ onLoaded, onSelectPart, loader }); cb.current = { onLoaded, onSelectPart, loader };

  useEffect(() => {
    const el = host.current; if (!el) return;
    if (!webglAvailable()) { setPhase({ kind: 'error', message: "WebGL n'est pas disponible dans ce navigateur : l'aperçu 3D ne peut pas s'afficher." }); return; }
    let v: GlbViewer;
    try { v = new GlbViewer(el); } catch (e) { setPhase({ kind: 'error', message: `Impossible d'initialiser le viewer 3D (${(e as Error).message}).` }); return; }
    viewer.current = v;
    v.onPick = (id) => { setPickedLabel(id ? v.parts.find((p) => p.id === id)?.label ?? null : null); cb.current.onSelectPart?.(id); };
    const mo = new MutationObserver(() => v.setColors(readCssColors()));
    mo.observe(document.documentElement, { attributes: true, attributeFilter: ['data-theme', 'style'] });
    return () => { mo.disconnect(); v.dispose(); viewer.current = null; };
  }, []);

  useEffect(() => {
    const v = viewer.current; if (!v) return;
    const ac = new AbortController();
    setPhase({ kind: 'loading', ratio: 0 }); setPickedLabel(null); setView('iso');
    cb.current.loader(ac.signal, (ratio) => setPhase({ kind: 'loading', ratio }))
      .then((buf) => v.load(buf))
      .then((m) => { if (ac.signal.aborted) return; setPhase({ kind: 'ready' }); cb.current.onLoaded?.(m); })
      .catch((e: unknown) => { if (ac.signal.aborted || (e as Error).name === 'AbortError') return; setPhase({ kind: 'error', message: (e as Error).message || 'Aperçu illisible' }); });
    return () => ac.abort();
  }, [sourceKey]);

  useEffect(() => {
    if (selectedPartId === undefined) return;
    viewer.current?.select(selectedPartId);
    setPickedLabel(selectedPartId ? viewer.current?.parts.find((p) => p.id === selectedPartId)?.label ?? null : null);
  }, [selectedPartId, phase.kind]);

  const setV = (x: ViewName) => { setView(x); viewer.current?.setView(x); };
  const ready = phase.kind === 'ready';
  return (
    <div className={['v3', compact && 'v3-compact', className].filter(Boolean).join(' ')}>
      <div ref={host} className="v3-host" aria-label={ariaLabel ?? 'Aperçu 3D'} />
      {ready && (
        <>
          <div className="v3-bar">
            <div className="v3-seg" role="group" aria-label="Vue">
              {VIEWS.map((x) => (
                <button key={x.v} type="button" className="v3-seg-btn" aria-pressed={view === x.v} title={x.title} aria-label={x.title} onClick={() => setV(x.v)}>{compact ? x.short : x.label}</button>
              ))}
            </div>
            <div className="v3-acts">
              {!compact && <button type="button" className="v3-act" aria-pressed={view === 'plateau'} title="Cadrer le plateau 256 × 256 mm" onClick={() => setV('plateau')}><Grid3x3 size={14} />Plateau</button>}
              <button type="button" className="v3-act" title="Recadrer sur l'objet" aria-label="Recadrer sur l'objet" onClick={() => setV(view === 'plateau' ? 'iso' : view)}><Maximize size={14} />{!compact && 'Recadrer'}</button>
            </div>
          </div>
          {caption !== false && <div className="v3-caption mono" aria-hidden>{caption ?? 'Plateau 256 × 256 mm · grille 10 mm'}</div>}
          {pickedLabel && <div className="v3-sel"><span className="sr-only">Sélection : </span>{pickedLabel}</div>}
          {notice && <div className="v3-notice">{notice}</div>}
        </>
      )}
      {phase.kind === 'loading' && (
        <div className="v3-overlay" role="status" aria-busy="true">
          <span className="spinner" aria-hidden />
          <span>Chargement de l'aperçu{phase.ratio > 0 ? ` · ${Math.round(phase.ratio * 100)} %` : ''}</span>
        </div>
      )}
      {phase.kind === 'error' && (
        <div className="v3-overlay v3-overlay-err" role="alert"><AlertTriangle size={16} aria-hidden /><span>{phase.message}</span></div>
      )}
    </div>
  );
}
