import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { AlertTriangle, Pause, Play, SkipBack } from 'lucide-react';
import {
  defaultHidden, featureStyle, fmtDuration, fmtFilament, loadToolpath, rampColor, rangeStats, rgbCss, TRAVEL,
} from './gcodeData';
import type { ColorMode, GcodeIndex, GcodeSource, LayerGeom, LoadProgress } from './gcodeData';
import type { GcodeRenderer, GcodeView, RendererFactory } from './gcodeRenderer';
import './gcode.css';

/**
 * Visionneuse couche par couche « comme Bambu Studio » : trajectoires 3D réelles du G-code découpé, curseur double de
 * couches (min / max), curseur fin dans la couche, lecture, vues iso / dessus / face, légende cliquable par type de
 * trajectoire, couleurs par type, vitesse ou hauteur, infos par couche (Z, durée estimée, filament extrudé).
 *
 * Composant partagé entre la vue MCP App (web/mcp-app/gcode.tsx : données par outils réservés) et l'interface web
 * (fiche d'impression : données par /api/gcode/*). Les données viennent d'une GcodeSource ; la scène WebGL est créée par
 * une fabrique injectable (three.js n'est chargé qu'à la création, et les tests fournissent un faux rendu).
 */

export interface GcodeViewerProps {
  source: GcodeSource;
  /** Fabrique de la scène (défaut : three.js, chargé à la demande). */
  createRenderer?: RendererFactory;
  /** Résumé déjà connu (évite un appel). */
  initialIndex?: GcodeIndex;
  compact?: boolean;
  title?: string;
  /** Période de la lecture (ms). */
  playMs?: number;
}

const webglAvailable = (): boolean => {
  try { const c = document.createElement('canvas'); return !!(c.getContext('webgl2') || c.getContext('webgl')); } catch { return false; }
};

/** Fabrique par défaut : charge la scène three.js seulement quand la visionneuse s'affiche. */
export const defaultRendererFactory: RendererFactory = async (host, o) => {
  if (!webglAvailable()) throw new Error("WebGL n'est pas disponible dans ce navigateur : utilisez la planche-contact des couches.");
  const m = await import('./gcodeScene');
  return m.createGcodeRenderer(host, o);
};

const VIEWS: Array<{ v: GcodeView; label: string }> = [{ v: 'iso', label: 'Iso' }, { v: 'dessus', label: 'Dessus' }, { v: 'face', label: 'Face' }];
const MODES: Array<{ v: ColorMode; label: string }> = [{ v: 'type', label: 'Type' }, { v: 'speed', label: 'Vitesse' }, { v: 'height', label: 'Hauteur' }];

export function GcodeViewer({ source, createRenderer = defaultRendererFactory, initialIndex, compact, title = 'Aperçu 3D couche par couche', playMs = 90 }: GcodeViewerProps) {
  const host = useRef<HTMLDivElement>(null);
  const rend = useRef<GcodeRenderer | null>(null);
  const [index, setIndex] = useState<GcodeIndex | undefined>(initialIndex);
  const [err, setErr] = useState<string>();
  const [rendErr, setRendErr] = useState<string>();
  const [ready, setReady] = useState(false);
  const [prog, setProg] = useState<LoadProgress>({ layers: 0, total: initialIndex?.layers_total ?? 0, points: 0 });
  const [loading, setLoading] = useState(true);
  const [min, setMin] = useState(1);
  const [max, setMax] = useState(initialIndex?.layers_total ?? 1);
  const [partial, setPartial] = useState(1);
  const [hidden, setHidden] = useState<Set<number>>(() => (initialIndex ? defaultHidden(initialIndex.features) : new Set()));
  const [mode, setMode] = useState<ColorMode>('type');
  const [view, setView] = useState<GcodeView>('iso');
  const [playing, setPlaying] = useState(false);
  const [travelBusy, setTravelBusy] = useState(false);
  const travelState = useRef<'none' | 'loading' | 'done'>('none');
  const travelAc = useRef<AbortController | null>(null);
  const [attempt, setAttempt] = useState(0);

  // 1. résumé par couche
  useEffect(() => {
    if (initialIndex) return;
    const ac = new AbortController();
    setErr(undefined);
    source.index(ac.signal).then((i) => {
      if (ac.signal.aborted) return;
      setIndex(i); setMax(i.layers_total); setHidden(defaultHidden(i.features)); setProg({ layers: 0, total: i.layers_total, points: 0 });
    }, (e: unknown) => { if (!ac.signal.aborted && (e as Error).name !== 'AbortError') setErr((e as Error).message || 'Résumé des couches indisponible'); });
    return () => ac.abort();
  }, [source, initialIndex, attempt]);

  const travelIdx = useMemo(() => index?.features.indexOf(TRAVEL) ?? -1, [index]);

  // 2. scène + chargement progressif des tranches (extrusions seulement : les déplacements à vide viennent à la demande)
  useEffect(() => {
    const el = host.current;
    if (!index || !el) return;
    const ac = new AbortController();
    let r: GcodeRenderer | null = null;
    let alive = true;
    setReady(false); setLoading(true); setRendErr(undefined); setProg({ layers: 0, total: index.layers_total, points: 0 }); travelState.current = 'none';
    (async () => {
      try {
        r = await createRenderer(el, {
          featureColors: index.features.map((f, i) => featureStyle(f, i).color),
          bboxMM: index.bbox_mm, heightMM: index.z[index.z.length - 1] ?? 0, expectedPoints: index.total_points,
        });
      } catch (e) { if (alive) { setRendErr((e as Error).message || 'Scène 3D indisponible'); setLoading(false); } return; }
      if (!alive) { r.dispose(); return; }
      rend.current = r; setReady(true);
      try {
        await loadToolpath(source, index, { signal: ac.signal, onLayers: (layers, p) => { r?.addLayers(layers); if (alive) setProg(p); } });
        if (alive) setLoading(false);
      } catch (e) { if (alive && (e as Error).name !== 'AbortError') { setErr((e as Error).message || 'Chargement interrompu'); setLoading(false); } }
    })();
    return () => { alive = false; ac.abort(); travelAc.current?.abort(); setReady(false); rend.current = null; r?.dispose(); };
  }, [index, source, createRenderer, attempt]);

  // 3. état de la scène
  useEffect(() => { if (ready) rend.current?.setRange(min, max, partial); }, [ready, min, max, partial]);
  useEffect(() => { if (ready) rend.current?.setHidden(hidden); }, [ready, hidden]);
  useEffect(() => { if (ready) rend.current?.setColorMode(mode); }, [ready, mode]);
  useEffect(() => { if (ready) rend.current?.setView(view); }, [ready, view]);

  // 4. déplacements à vide : second passage la première fois qu'on les affiche
  useEffect(() => {
    if (!ready || !index || travelIdx < 0 || hidden.has(travelIdx) || travelState.current !== 'none') return;
    travelState.current = 'loading'; setTravelBusy(true);
    const ac = new AbortController();
    travelAc.current = ac; // interrompu par le nettoyage de la scène (effet 2), pas par un simple changement de légende
    loadToolpath(source, index, {
      withTravel: true, signal: ac.signal, filter: (f) => f === travelIdx,
      onLayers: (layers) => rend.current?.addLayers(layers.map((g): LayerGeom => ({ ...g, travel: true }))),
    }).then(() => { travelState.current = 'done'; setTravelBusy(false); },
      (e: unknown) => { travelState.current = 'none'; setTravelBusy(false); if ((e as Error).name !== 'AbortError') setErr((e as Error).message); });
  }, [ready, index, travelIdx, hidden, source]);

  // 5. lecture : avance dans la couche puis de couche en couche (≈ 25 s de bout en bout)
  const total = index?.layers_total ?? 1;
  const cur = useRef({ max, partial });
  cur.current = { max, partial };
  useEffect(() => {
    if (!playing) return;
    const step = Math.max(1, Math.round(total / 280));
    const id = window.setInterval(() => {
      const { max: m, partial: p } = cur.current;
      if (step === 1 && p < 1) { setPartial(Math.min(1, p + 0.34)); return; }
      if (m >= total) { setPlaying(false); return; }
      setMax(Math.min(total, m + step));
      setPartial(step === 1 ? 0 : 1);
    }, playMs);
    return () => window.clearInterval(id);
  }, [playing, total, playMs]);

  const toggle = useCallback((i: number) => setHidden((h) => { const n = new Set(h); if (n.has(i)) n.delete(i); else n.add(i); return n; }), []);
  const reload = () => { setErr(undefined); setAttempt((a) => a + 1); };

  const stats = useMemo(() => (index ? rangeStats(index, min, max) : undefined), [index, min, max]);
  const z = index?.z[max - 1];
  const layerTime = index?.time_s[max - 1], layerFil = index?.extruded_mm[max - 1];
  const featureCounts = index?.features ?? [];

  return (
    <section className={['gv', compact && 'gv-compact'].filter(Boolean).join(' ')} aria-label={title}>
      <header className="gv-head">
        <h2>{title}</h2>
        {index && <span className="gv-sub">plaque {index.plate} · {index.layers_total} couches · {fmtDuration(index.total_time_s)}{index.time_source === 'slicer' ? ' (slicer)' : ' (estimation)'} · {fmtFilament(index.filament_mm)} de filament</span>}
      </header>

      <div className="gv-stage">
        <div ref={host} className="gv-canvas" data-testid="gv-canvas" />
        {(loading || travelBusy) && !rendErr && index && (
          <div className="gv-loading" role="status" aria-live="polite">
            <progress max={Math.max(1, prog.total)} value={prog.layers} aria-label="Chargement des trajectoires" />
            <span>{travelBusy ? 'Chargement des déplacements à vide…' : `Chargement des trajectoires : ${prog.layers} / ${prog.total} couches`}</span>
          </div>
        )}
        {!index && !err && <div className="gv-loading" role="status"><span className="spinner" aria-hidden /> <span>Lecture du G-code…</span></div>}
        {rendErr && <div className="gv-overlay-err" role="alert"><AlertTriangle size={14} aria-hidden /> {rendErr}</div>}
        <div className="gv-bar">
          <div className="gv-seg" role="group" aria-label="Vue">
            {VIEWS.map((x) => <button key={x.v} type="button" aria-pressed={view === x.v} onClick={() => setView(x.v)}>{x.label}</button>)}
          </div>
          <div className="gv-seg" role="group" aria-label="Couleur">
            {MODES.map((x) => <button key={x.v} type="button" aria-pressed={mode === x.v} onClick={() => setMode(x.v)}>{x.label}</button>)}
          </div>
        </div>
      </div>

      {err && <p className="gv-msg" role="alert"><AlertTriangle size={14} aria-hidden /> {err} <button type="button" className="gv-link" onClick={reload}>Réessayer</button></p>}

      {index && (
        <>
          <div className="gv-ctl">
            <button type="button" className="gv-ibtn" aria-label="Revenir à la première couche" onClick={() => { setPlaying(false); setMin(1); setMax(1); setPartial(1); }}><SkipBack size={14} aria-hidden /></button>
            <button type="button" className="gv-ibtn" aria-label={playing ? 'Pause' : 'Lecture'} onClick={() => { if (!playing && max >= total) { setMax(min); setPartial(0); } setPlaying((p) => !p); }}>{playing ? <Pause size={14} aria-hidden /> : <Play size={14} aria-hidden />}</button>
            <div className="gv-sliders">
              <label><span>Couche max</span>
                <input type="range" min={1} max={total} step={1} value={max} aria-label="Couche maximale"
                  onChange={(e) => { setPlaying(false); const v = Number(e.target.value); setMax(Math.max(v, min)); setPartial(1); }} /></label>
              <label><span>Couche min</span>
                <input type="range" min={1} max={total} step={1} value={min} aria-label="Couche minimale"
                  onChange={(e) => { const v = Number(e.target.value); setMin(Math.min(v, max)); }} /></label>
              <label><span>Dans la couche</span>
                <input type="range" min={0} max={100} step={1} value={Math.round(partial * 100)} aria-label="Position dans la couche"
                  onChange={(e) => { setPlaying(false); setPartial(Number(e.target.value) / 100); }} /></label>
            </div>
          </div>

          <dl className="gv-info" aria-label="Infos de la couche">
            <div><dt>Couche</dt><dd>{min === max ? `${max}` : `${min} – ${max}`} / {total}</dd></div>
            <div><dt>Hauteur Z</dt><dd>{z !== undefined ? `${z.toFixed(2)} mm` : '—'}</dd></div>
            <div><dt>Durée (couche {max})</dt><dd>{layerTime !== undefined ? fmtDuration(layerTime) : '—'}</dd></div>
            <div><dt>Filament (couche {max})</dt><dd>{layerFil !== undefined ? fmtFilament(layerFil) : '—'}</dd></div>
            {stats && stats.layers > 1 && <div><dt>Sélection</dt><dd>{stats.layers} couches · {fmtDuration(stats.timeS)} · {fmtFilament(stats.extrudedMM)}</dd></div>}
          </dl>

          {mode === 'type' ? (
            <ul className="gv-legend" aria-label="Types de trajectoires (cliquer pour masquer ou afficher)">
              {featureCounts.map((f, i) => {
                const st = featureStyle(f, i), on = !hidden.has(i);
                return (
                  <li key={f}>
                    <button type="button" aria-pressed={on} onClick={() => toggle(i)} title={on ? `Masquer : ${st.label}` : `Afficher : ${st.label}`}>
                      <span className="gv-sw" style={{ background: st.color }} aria-hidden />{st.label}
                    </button>
                  </li>
                );
              })}
            </ul>
          ) : (
            <div className="gv-scale" aria-label={mode === 'speed' ? 'Échelle de vitesse' : 'Échelle de hauteur'}>
              <span>{mode === 'speed' ? 'lent' : '0 mm'}</span>
              <span className="gv-grad" style={{ background: `linear-gradient(90deg, ${[0, 0.25, 0.5, 0.75, 1].map((t) => rgbCss(rampColor(t))).join(', ')})` }} aria-hidden />
              <span>{mode === 'speed' ? 'rapide' : `${(index.z[index.z.length - 1] ?? 0).toFixed(1)} mm`}</span>
            </div>
          )}
          <p className="gv-foot">
            {index.time_source === 'slicer' ? 'Durées réparties par couche d\'après la durée annoncée par le slicer.' : 'Durées estimées d\'après les vitesses programmées (sans accélérations).'}{' '}
            Mode {rend.current?.mode === 'lines' ? 'traits (modèle très dense)' : 'tubes'}
            {index.simplified ? ` · tracé simplifié (tolérance ${index.tolerance_mm ?? '?'} mm, ${index.layer_budget} points d'extrusion au plus par couche, ${index.max_total_points.toLocaleString('fr-FR')} au total)` : ''}.
          </p>
        </>
      )}
    </section>
  );
}
