import {
  BufferAttribute, BufferGeometry, DoubleSide, Float32BufferAttribute, Group, LineBasicMaterial, LineSegments, Mesh, MeshBasicMaterial,
  PerspectiveCamera, Scene, WebGLRenderer,
} from 'three';
import { OrbitControls } from 'three/addons/controls/OrbitControls.js';
import { TUBE_SEGMENT_LIMIT, hexToRgb, rampColor, speedRange } from './gcodeData';
import type { ColorMode, LayerGeom } from './gcodeData';
import type { GcodeRenderer, GcodeRendererOptions, GcodeView } from './gcodeRenderer';

/**
 * Scène three.js de la visionneuse couche par couche (Three.js nu, rendu à la demande). Une géométrie par couche :
 *  - « tubes » (≤ TUBE_SEGMENT_LIMIT segments) : chaque segment est un prisme ouvert (dessus + deux flancs, 12 sommets,
 *    flancs assombris) de largeur 0,45 mm et de la hauteur de la couche ;
 *  - « traits » au-delà (2 sommets par segment) pour garder la mémoire GPU raisonnable.
 * La visibilité d'une couche est un simple object.visible ; la couche du haut est tronquée par drawRange (curseur fin) ;
 * masquer un type reconstruit le tampon d'indices de chaque couche (l'ordre d'impression est conservé).
 */

export type Mode = 'tubes' | 'lines';
export const LINE_WIDTH_MM = 0.45;
const SHADE = [1, 0.78, 0.6]; // dessus, flanc gauche, flanc droit

export const vertsPerSeg = (m: Mode) => (m === 'tubes' ? 12 : 2);
export const idxPerSeg = (m: Mode) => (m === 'tubes' ? 18 : 2);

/** Positions des sommets d'une couche (fonction pure, testée sans WebGL). */
export function buildPositions(g: LayerGeom, mode: Mode, width = LINE_WIDTH_MM): Float32Array {
  const vp = vertsPerSeg(mode), pos = new Float32Array(g.n * vp * 3);
  const zt = g.z, zb = g.z - g.h, hw = width / 2;
  for (let i = 0; i < g.n; i++) {
    const x0 = g.x0[i], y0 = g.y0[i], x1 = g.x1[i], y1 = g.y1[i], o = i * vp * 3;
    if (mode === 'lines') {
      const z = g.z - g.h / 2;
      pos.set([x0, y0, z, x1, y1, z], o);
      continue;
    }
    const dx = x1 - x0, dy = y1 - y0, len = Math.hypot(dx, dy) || 1;
    const nx = (-dy / len) * hw, ny = (dx / len) * hw;
    // dessus, flanc +n, flanc -n
    pos.set([
      x0 + nx, y0 + ny, zt, x0 - nx, y0 - ny, zt, x1 - nx, y1 - ny, zt, x1 + nx, y1 + ny, zt,
      x0 + nx, y0 + ny, zb, x0 + nx, y0 + ny, zt, x1 + nx, y1 + ny, zt, x1 + nx, y1 + ny, zb,
      x0 - nx, y0 - ny, zb, x0 - nx, y0 - ny, zt, x1 - nx, y1 - ny, zt, x1 - nx, y1 - ny, zb,
    ], o);
  }
  return pos;
}

export interface ColorCtx { featureRgb: Array<[number, number, number]>; heightMM: number; speedMin: number; speedMax: number }

/** Couleur (0..255) d'un segment selon le mode. */
export function segmentColor(g: LayerGeom, i: number, mode: ColorMode, c: ColorCtx): [number, number, number] {
  if (mode === 'speed') {
    const v = g.s[i] * 5, span = c.speedMax - c.speedMin;
    return rampColor(span > 0 ? (v - c.speedMin) / span : 0.5);
  }
  if (mode === 'height') return rampColor(c.heightMM > 0 ? g.z / c.heightMM : 0);
  return c.featureRgb[g.f[i]] ?? [200, 200, 200];
}

/** Remplit les couleurs des sommets d'une couche (Uint8, 3 par sommet). */
export function paintColors(g: LayerGeom, col: Uint8Array, geom: Mode, mode: ColorMode, c: ColorCtx): void {
  const vp = vertsPerSeg(geom);
  for (let i = 0; i < g.n; i++) {
    const [r, gg, b] = segmentColor(g, i, mode, c);
    for (let v = 0; v < vp; v++) {
      const k = geom === 'tubes' ? SHADE[Math.floor(v / 4)] : 1;
      const o = (i * vp + v) * 3;
      col[o] = r * k; col[o + 1] = gg * k; col[o + 2] = b * k;
    }
  }
}

/** Tampon d'indices des segments visibles, dans l'ordre d'impression. Rend le nombre de segments visibles. */
export function fillIndex(g: LayerGeom, mode: Mode, hidden: ReadonlySet<number>, idx: Uint32Array): number {
  let n = 0;
  for (let i = 0; i < g.n; i++) {
    if (hidden.has(g.f[i])) continue;
    const b = i * vertsPerSeg(mode);
    if (mode === 'lines') { idx[n * 2] = b; idx[n * 2 + 1] = b + 1; } else {
      const o = n * 18;
      idx.set([b, b + 1, b + 2, b, b + 2, b + 3, b + 4, b + 5, b + 6, b + 4, b + 6, b + 7, b + 8, b + 9, b + 10, b + 8, b + 10, b + 11], o);
    }
    n++;
  }
  return n;
}

/** Clé d'une géométrie : extrusions et déplacements à vide d'une même couche sont deux géométries distinctes. */
const layerKey = (g: LayerGeom) => g.layer + (g.travel ? 1_000_000 : 0);

interface LayerObj { g: LayerGeom; obj: Mesh | LineSegments; col: BufferAttribute; idx: BufferAttribute; vis: number }

const DIRS: Record<GcodeView, [number, number, number]> = { iso: [0.62, -0.78, 0.55], dessus: [0, -0.0008, 1], face: [0, -1, 0.04] };

export class GcodeScene implements GcodeRenderer {
  readonly mode: Mode;
  private renderer: WebGLRenderer;
  private scene = new Scene();
  private camera = new PerspectiveCamera(32, 1, 0.5, 6000);
  private controls: OrbitControls;
  private root = new Group();
  private grid?: LineSegments;
  private layers = new Map<number, LayerObj>();
  private tubeMat = new MeshBasicMaterial({ vertexColors: true, side: DoubleSide });
  private lineMat = new LineBasicMaterial({ vertexColors: true });
  private hidden: ReadonlySet<number> = new Set();
  private colorMode: ColorMode = 'type';
  private ctx: ColorCtx;
  private range = { min: 1, max: Infinity, partial: 1 };
  private raf = 0;
  private ro: ResizeObserver;
  private disposed = false;
  private radius = 40;

  constructor(private host: HTMLElement, private o: GcodeRendererOptions) {
    this.mode = o.expectedPoints <= TUBE_SEGMENT_LIMIT ? 'tubes' : 'lines';
    this.ctx = { featureRgb: o.featureColors.map(hexToRgb), heightMM: o.heightMM, speedMin: 0, speedMax: 0 };
    this.renderer = new WebGLRenderer({ antialias: true, alpha: true, powerPreference: 'low-power' });
    this.renderer.setPixelRatio(Math.min(window.devicePixelRatio || 1, 1.5));
    const el = this.renderer.domElement;
    el.style.display = 'block'; el.style.touchAction = 'none'; el.setAttribute('role', 'img');
    host.appendChild(el);
    this.camera.up.set(0, 0, 1);
    this.scene.add(this.root);
    const [x0, y0, x1, y1] = o.bboxMM;
    this.root.position.set(-(x0 + x1) / 2, -(y0 + y1) / 2, 0); // modèle centré sur le plateau
    const w = Math.max(10, x1 - x0), d = Math.max(10, y1 - y0);
    this.radius = Math.max(15, Math.hypot(w, d, o.heightMM) / 2);
    this.buildGrid(Math.ceil((Math.max(w, d) + 20) / 10) * 10);
    this.controls = new OrbitControls(this.camera, el);
    this.controls.enableDamping = false;
    this.controls.minDistance = 8; this.controls.maxDistance = 3000;
    this.controls.addEventListener('change', this.requestRender);
    this.ro = new ResizeObserver(() => this.resize());
    this.ro.observe(host);
    this.resize();
    this.setView('iso');
  }

  private css(n: string, d: string) { return getComputedStyle(this.host).getPropertyValue(n).trim() || d; }

  private buildGrid(size: number) {
    const h = size / 2, a: number[] = [];
    for (let v = -h; v <= h + 0.01; v += 10) a.push(-h, v, 0, h, v, 0, v, -h, 0, v, h, 0);
    const g = new BufferGeometry(); g.setAttribute('position', new Float32BufferAttribute(a, 3));
    this.grid = new LineSegments(g, new LineBasicMaterial({ color: this.css('--line-strong', '#8f8a7d'), transparent: true, opacity: 0.45 }));
    this.grid.position.z = -0.02;
    this.scene.add(this.grid);
  }

  private requestRender = () => {
    if (this.raf || this.disposed) return;
    this.raf = requestAnimationFrame(() => { this.raf = 0; if (!this.disposed) this.renderer.render(this.scene, this.camera); });
  };

  private resize() {
    const w = Math.max(1, this.host.clientWidth), h = Math.max(1, this.host.clientHeight);
    this.renderer.setSize(w, h, false);
    this.renderer.domElement.style.width = '100%'; this.renderer.domElement.style.height = '100%';
    this.camera.aspect = w / h; this.camera.updateProjectionMatrix();
    this.requestRender();
  }

  addLayers(gs: LayerGeom[]): void {
    for (const g of gs) {
      const key = layerKey(g);
      if (this.layers.has(key)) this.removeLayer(key);
      const pos = buildPositions(g, this.mode);
      const colArr = new Uint8Array(pos.length);
      paintColors(g, colArr, this.mode, this.colorMode, this.ctx);
      const geo = new BufferGeometry();
      geo.setAttribute('position', new BufferAttribute(pos, 3));
      const col = new BufferAttribute(colArr, 3, true);
      geo.setAttribute('color', col);
      const idx = new BufferAttribute(new Uint32Array(g.n * idxPerSeg(this.mode)), 1);
      geo.setIndex(idx);
      const obj = this.mode === 'tubes' ? new Mesh(geo, this.tubeMat) : new LineSegments(geo, this.lineMat);
      obj.frustumCulled = false;
      const lo: LayerObj = { g, obj, col, idx, vis: 0 };
      this.layers.set(key, lo);
      this.root.add(obj);
      this.refill(lo);
    }
    this.applyRange();
  }

  private removeLayer(l: number) {
    const lo = this.layers.get(l); if (!lo) return;
    this.root.remove(lo.obj); lo.obj.geometry.dispose(); this.layers.delete(l);
  }

  private refill(lo: LayerObj) {
    lo.vis = fillIndex(lo.g, this.mode, this.hidden, lo.idx.array as Uint32Array);
    lo.idx.needsUpdate = true;
  }

  private applyRange() {
    const { min, max, partial } = this.range;
    for (const lo of this.layers.values()) {
      const l = lo.g.layer;
      lo.obj.visible = l >= min && l <= max;
      if (!lo.obj.visible) continue;
      const count = l === max ? Math.floor(lo.vis * Math.min(1, Math.max(0, partial))) : lo.vis;
      lo.obj.geometry.setDrawRange(0, count * idxPerSeg(this.mode));
    }
    this.requestRender();
  }

  setRange(min: number, max: number, partial: number): void { this.range = { min, max, partial }; this.applyRange(); }

  setHidden(hidden: ReadonlySet<number>): void {
    this.hidden = new Set(hidden);
    for (const lo of this.layers.values()) this.refill(lo);
    this.applyRange();
  }

  setColorMode(mode: ColorMode): void {
    this.colorMode = mode;
    if (mode === 'speed') {
      const [lo, hi] = speedRange(Array.from(this.layers.values(), (l) => l.g));
      this.ctx = { ...this.ctx, speedMin: lo, speedMax: hi };
    }
    for (const lo of this.layers.values()) { paintColors(lo.g, lo.col.array as Uint8Array, this.mode, mode, this.ctx); lo.col.needsUpdate = true; }
    this.requestRender();
  }

  setView(view: GcodeView): void {
    const dist = this.radius * 3.4, tz = this.o.heightMM / 2;
    const [dx, dy, dz] = DIRS[view], n = Math.hypot(dx, dy, dz);
    this.camera.position.set((dx / n) * dist, (dy / n) * dist, tz + (dz / n) * dist);
    this.controls.target.set(0, 0, tz);
    this.controls.update();
    this.requestRender();
  }

  dispose(): void {
    this.disposed = true;
    cancelAnimationFrame(this.raf);
    this.ro.disconnect();
    this.controls.dispose();
    for (const l of Array.from(this.layers.keys())) this.removeLayer(l);
    this.tubeMat.dispose(); this.lineMat.dispose();
    if (this.grid) { this.grid.geometry.dispose(); (this.grid.material as LineBasicMaterial).dispose(); }
    this.renderer.dispose();
    this.renderer.domElement.remove();
  }
}

export const createGcodeRenderer = (host: HTMLElement, o: GcodeRendererOptions): GcodeRenderer => new GcodeScene(host, o);
