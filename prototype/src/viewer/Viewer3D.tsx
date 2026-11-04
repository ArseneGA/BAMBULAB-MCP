import { useEffect, useMemo, useRef, useState } from 'react';
import { Canvas, useThree } from '@react-three/fiber';
import { Html, OrbitControls } from '@react-three/drei';
import { BufferGeometry, Color, EdgesGeometry, Float32BufferAttribute } from 'three';
import type { OrbitControls as OrbitControlsImpl } from 'three-stdlib';
import { Grid3x3, Maximize } from 'lucide-react';
import type { ReactNode } from 'react';
import type { DemoModel } from '../data/demo';
import { buildParts } from './geometry';
import './viewer.css';

export const PLATE_MM = 256;
export type ViewName = 'iso' | 'face' | 'dessus' | 'cote' | 'plateau';

export interface CameraState { position: [number, number, number]; target: [number, number, number] }

export interface Viewer3DProps {
  geometry: DemoModel['geometry'];
  dims: DemoModel['dims'];
  /** 0 = dernière version ; plus grand = révision plus ancienne (rendu simulé : moins de chanfreins/perçages). */
  revision?: number;
  selectedPartId?: string | null;
  onSelectPart?: (id: string | null) => void;
  compact?: boolean;
  /** Bandeau d'avertissement dans le viewer (ex. aperçu issu du cache). */
  notice?: ReactNode;
  className?: string;
  /** Caméra pilotée de l'extérieur (position + cible) : permet de synchroniser plusieurs viewers. */
  camera?: CameraState | null;
  /** Appelé à chaque mouvement de caméra (souris, vues standards). */
  onCameraChange?: (c: CameraState) => void;
}

const near = (a: readonly number[], b: readonly number[]) => a.every((v, i) => Math.abs(v - b[i]) < 1e-3);

/** Couleurs lues dans les jetons CSS, relues à chaque changement de thème (data-theme sur <html>). */
function useThemeColors() {
  const read = () => {
    const cs = getComputedStyle(document.documentElement);
    const g = (n: string) => cs.getPropertyValue(n).trim() || '#888';
    return { accent: g('--accent'), body: g('--text-3'), edge: g('--line-strong'), grid: g('--line'), gridStrong: g('--line-strong'), text: g('--text-3'), dark: document.documentElement.dataset.theme === 'dark' };
  };
  const [c, setC] = useState(read);
  useEffect(() => {
    const mo = new MutationObserver(() => setC(read()));
    mo.observe(document.documentElement, { attributes: true, attributeFilter: ['data-theme'] });
    return () => mo.disconnect();
  }, []);
  return c;
}

function PlateGrid({ color, strong }: { color: string; strong: string }) {
  const { fine, major, border } = useMemo(() => {
    const h = PLATE_MM / 2; const f: number[] = []; const m: number[] = [];
    for (let v = -h; v <= h + 0.1; v += 10) { const t = Math.abs(v) % 50 < 0.01 ? m : f; t.push(-h, v, 0, h, v, 0, v, -h, 0, v, h, 0); }
    const mk = (a: number[]) => { const g = new BufferGeometry(); g.setAttribute('position', new Float32BufferAttribute(a, 3)); return g; };
    return { fine: mk(f), major: mk(m), border: mk([-h, -h, 0, h, -h, 0, h, -h, 0, h, h, 0, h, h, 0, -h, h, 0, -h, h, 0, -h, -h, 0]) };
  }, []);
  return (
    <group>
      <lineSegments geometry={fine}><lineBasicMaterial color={color} transparent opacity={0.55} /></lineSegments>
      <lineSegments geometry={major}><lineBasicMaterial color={strong} transparent opacity={0.6} /></lineSegments>
      <lineSegments geometry={border}><lineBasicMaterial color={strong} /></lineSegments>
    </group>
  );
}

function Axes({ at, len, color }: { at: [number, number, number]; len: number; color: string }) {
  const geo = useMemo(() => { const g = new BufferGeometry(); g.setAttribute('position', new Float32BufferAttribute([0, 0, 0, len, 0, 0, 0, 0, 0, 0, len, 0, 0, 0, 0, 0, 0, len], 3)); return g; }, [len]);
  const label = (p: [number, number, number], t: string) => <Html position={p} center zIndexRange={[5, 0]} style={{ pointerEvents: 'none' }}><span className="v3-axis-label" style={{ color }}>{t}</span></Html>;
  return (
    <group position={at}>
      <lineSegments geometry={geo}><lineBasicMaterial color={color} /></lineSegments>
      {label([len * 1.25, 0, 0], 'X')}{label([0, len * 1.25, 0], 'Y')}{label([0, 0, len * 1.25], 'Z')}
    </group>
  );
}

function PartMesh({ geometry, selected, c, onPick }: { geometry: BufferGeometry; selected: boolean; c: ReturnType<typeof useThemeColors>; onPick: () => void }) {
  const edges = useMemo(() => new EdgesGeometry(geometry, 28), [geometry]);
  const tint = useMemo(() => '#' + new Color(c.body).lerp(new Color(c.accent), 0.4).getHexString(), [c.body, c.accent]);
  const selEdge = useMemo(() => '#' + new Color(c.accent).lerp(new Color(c.dark ? '#ffffff' : '#000000'), c.dark ? 0.6 : 0.55).getHexString(), [c.accent, c.dark]);
  useEffect(() => () => edges.dispose(), [edges]);
  return (
    <mesh geometry={geometry} onClick={(e) => { e.stopPropagation(); onPick(); }} onPointerOver={(e) => { e.stopPropagation(); document.body.style.cursor = 'pointer'; }} onPointerOut={() => { document.body.style.cursor = ''; }}>
      <meshStandardMaterial color={selected ? tint : c.body} emissive={selected ? c.accent : '#000000'} emissiveIntensity={selected ? 0.1 : 0} roughness={0.72} metalness={0.05} side={2} polygonOffset polygonOffsetFactor={1} polygonOffsetUnits={1} />
      <lineSegments geometry={edges}><lineBasicMaterial color={selected ? selEdge : c.edge} /></lineSegments>
    </mesh>
  );
}

function CameraRig({ view, nonce, radius, height, shared, onCameraChange }: { view: ViewName; nonce: number; radius: number; height: number; shared?: CameraState | null; onCameraChange?: (c: CameraState) => void }) {
  const { camera, invalidate } = useThree();
  const controls = useThree((st) => st.controls) as OrbitControlsImpl | null;
  const applying = useRef(false);
  const cb = useRef(onCameraChange); cb.current = onCameraChange;
  const sharedRef = useRef(shared); sharedRef.current = shared;
  const first = useRef(true);
  const apply = (p: readonly number[], t: readonly number[]) => {
    const c = controls; if (!c) return;
    applying.current = true;
    camera.position.set(p[0], p[1], p[2]); c.target.set(t[0], t[1], t[2]); c.update();
    applying.current = false; invalidate();
  };
  /* Vue standard (ou recadrage) : diffusée aux autres viewers via onCameraChange. */
  useEffect(() => {
    const c = controls; if (!c) return;
    const wasFirst = first.current; first.current = false;
    const s = sharedRef.current;
    if (wasFirst && s) { apply(s.position, s.target); return; }
    const r = view === 'plateau' ? 190 : radius; const dist = r * 3.8;
    const tz = view === 'plateau' ? 0 : height / 2;
    const dir: Record<ViewName, [number, number, number]> = { iso: [0.62, -0.78, 0.55], face: [0, -1, 0.04], dessus: [0, -0.0008, 1], cote: [1, 0, 0.04], plateau: [0.35, -0.8, 0.75] };
    const [dx, dy, dz] = dir[view]; const n = Math.hypot(dx, dy, dz);
    camera.position.set((dx / n) * dist, (dy / n) * dist, tz + (dz / n) * dist);
    c.target.set(0, 0, tz); c.update(); invalidate();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [view, nonce, radius, height, camera, invalidate, controls]);
  /* Mouvement utilisateur → remontée de la caméra. */
  useEffect(() => {
    const c = controls; if (!c) return;
    const h = () => {
      if (applying.current || !cb.current) return;
      cb.current({ position: [camera.position.x, camera.position.y, camera.position.z], target: [c.target.x, c.target.y, c.target.z] });
    };
    c.addEventListener('change', h);
    return () => c.removeEventListener('change', h);
  }, [controls, camera]);
  /* Caméra reçue de l'extérieur → appliquée seulement si elle diffère (évite les boucles). */
  useEffect(() => {
    const c = controls; if (!c || !shared) return;
    const p = [camera.position.x, camera.position.y, camera.position.z]; const t = [c.target.x, c.target.y, c.target.z];
    if (near(p, shared.position) && near(t, shared.target)) return;
    apply(shared.position, shared.target);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [shared, controls]);
  return <OrbitControls makeDefault enableDamping={false} maxPolarAngle={Math.PI} minDistance={4} maxDistance={900} />;
}

const VIEWS: Array<{ v: ViewName; label: string; short: string; title: string }> = [
  { v: 'iso', label: 'Iso', short: 'Iso', title: 'Vue isométrique (3/4)' },
  { v: 'face', label: 'Face', short: 'F', title: 'Vue de face' },
  { v: 'dessus', label: 'Dessus', short: 'D', title: 'Vue de dessus' },
  { v: 'cote', label: 'Côté', short: 'C', title: 'Vue de côté' },
];

export function Viewer3D({ geometry, dims, revision = 0, selectedPartId, onSelectPart, compact, notice, className, camera, onCameraChange }: Viewer3DProps) {
  const c = useThemeColors();
  const [inner, setInner] = useState<string | null>(null);
  const selected = selectedPartId === undefined ? inner : selectedPartId;
  const pick = (id: string | null) => { setInner(id); onSelectPart?.(id); };
  const parts = useMemo(() => buildParts(geometry, dims, revision), [geometry, dims, revision]);
  useEffect(() => () => parts.forEach((p) => p.geometry.dispose()), [parts]);
  const [view, setView] = useState<ViewName>('iso');
  const [nonce, setNonce] = useState(0);
  const radius = Math.max(8, Math.hypot(dims.x, dims.y, dims.z) / 2);
  const setV = (v: ViewName) => { setView(v); setNonce((n) => n + 1); };

  return (
    <div className={['v3', compact && 'v3-compact', className].filter(Boolean).join(' ')}>
      <Canvas frameloop="demand" dpr={[1, 1.5]} camera={{ up: [0, 0, 1], fov: 32, near: 0.5, far: 4000 }} onPointerMissed={() => pick(null)} aria-label={`Aperçu 3D, ${dims.x} par ${dims.y} par ${dims.z} millimètres`}>
        <ambientLight intensity={c.dark ? 0.7 : 0.85} />
        <directionalLight position={[120, -200, 260]} intensity={c.dark ? 1.5 : 1.6} />
        <directionalLight position={[-160, 120, 80]} intensity={0.5} />
        <PlateGrid color={c.grid} strong={c.gridStrong} />
        <Axes at={[-dims.x / 2 - radius * 0.25, -dims.y / 2 - radius * 0.25, 0]} len={Math.max(6, radius * 0.35)} color={c.text} />
        {parts.map((p) => <PartMesh key={p.id} geometry={p.geometry} selected={selected === p.id} c={c} onPick={() => pick(p.id)} />)}
        <CameraRig view={view} nonce={nonce} radius={radius} height={dims.z} shared={camera} onCameraChange={onCameraChange} />
      </Canvas>
      <div className="v3-bar">
        <div className="v3-seg" role="group" aria-label="Vue">
          {VIEWS.map((x) => (
            <button key={x.v} type="button" className="v3-seg-btn" aria-pressed={view === x.v} title={x.title} aria-label={x.title} onClick={() => setV(x.v)}>{compact ? x.short : x.label}</button>
          ))}
        </div>
        <div className="v3-acts">
          {!compact && <button type="button" className="v3-act" aria-pressed={view === 'plateau'} title="Cadrer le plateau 256 × 256 mm" onClick={() => setV('plateau')}><Grid3x3 size={14} />Plateau</button>}
          <button type="button" className="v3-act" title="Recadrer sur l'objet" aria-label="Recadrer sur l'objet" onClick={() => setV(view)}><Maximize size={14} />{!compact && 'Recadrer'}</button>
        </div>
      </div>
      <div className="v3-caption mono" aria-hidden>Plateau P1S 256 × 256 mm · grille 10 mm</div>
      {selected && <div className="v3-sel"><span className="sr-only">Sélection : </span>{parts.find((p) => p.id === selected)?.label}</div>}
      {notice && <div className="v3-notice">{notice}</div>}
    </div>
  );
}
