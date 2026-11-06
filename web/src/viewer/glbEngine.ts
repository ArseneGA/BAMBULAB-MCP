import {
  AmbientLight, BufferGeometry, Color, DirectionalLight, EdgesGeometry, Float32BufferAttribute, Group, LineBasicMaterial, LineSegments,
  Mesh, MeshStandardMaterial, PerspectiveCamera, Raycaster, Scene, Vector2, Vector3, WebGLRenderer, Box3,
} from 'three';
import { GLTFLoader } from 'three/addons/loaders/GLTFLoader.js';
import { OrbitControls } from 'three/addons/controls/OrbitControls.js';

/**
 * Moteur de viewer GLB (Three.js nu, sans React) partagé par la web app et la vue MCP App.
 *
 * Conventions : le GLB d'aperçu de bambud est en mètres, Y vers le haut (« Z-up to Y-up (x,z,-y) »).
 * Le moteur le remet en millimètres, Z vers le haut, l'objet posé sur le plan z = 0 comme sur un plateau Bambu.
 * Rendu à la demande (pas de boucle d'animation) : une image n'est dessinée que lorsque la scène change.
 */

export type ViewName = 'iso' | 'face' | 'dessus' | 'cote' | 'plateau';
export const PLATE_MM = 256;

export interface ViewerColors { accent: string; body: string; edge: string; grid: string; gridStrong: string; text: string; bg: string; dark: boolean }
export interface ViewerPart { id: string; label: string; triangles: number; size: { x: number; y: number; z: number } }
export interface LoadedModel { parts: ViewerPart[]; size: { x: number; y: number; z: number }; triangles: number }

export const readCssColors = (el: Element = document.documentElement): ViewerColors => {
  const cs = getComputedStyle(el);
  const g = (n: string, d: string) => cs.getPropertyValue(n).trim() || d;
  return {
    accent: g('--accent', '#b83c08'), body: g('--text-3', '#6a655b'), edge: g('--line-strong', '#8f8a7d'), grid: g('--line', '#c4c0b5'),
    gridStrong: g('--line-strong', '#8f8a7d'), text: g('--text-3', '#6a655b'), bg: g('--viewer-bg', '#dcdad2'),
    dark: document.documentElement.dataset.theme === 'dark',
  };
};

const EDGE_TRIANGLE_LIMIT = 250_000;

interface PartEntry { part: ViewerPart; mesh: Mesh; edges?: LineSegments }

export class GlbViewer {
  readonly renderer: WebGLRenderer;
  private scene = new Scene();
  private camera = new PerspectiveCamera(32, 1, 0.5, 6000);
  private controls: OrbitControls;
  private root = new Group();
  private plate = new Group();
  private entries: PartEntry[] = [];
  private colors: ViewerColors;
  private selectedId: string | null = null;
  private raf = 0;
  private ro: ResizeObserver;
  private size = { x: 0, y: 0, z: 0 };
  private radius = 20;
  private disposed = false;
  private down: { x: number; y: number } | null = null;
  private ray = new Raycaster();
  private ambient = new AmbientLight(0xffffff, 0.85);
  private key = new DirectionalLight(0xffffff, 1.6);
  private fill = new DirectionalLight(0xffffff, 0.5);

  onPick?: (id: string | null) => void;
  onError?: (e: unknown) => void;

  constructor(private host: HTMLElement, opts: { colors?: ViewerColors; showPlate?: boolean } = {}) {
    this.colors = opts.colors ?? readCssColors();
    this.renderer = new WebGLRenderer({ antialias: true, alpha: true, powerPreference: 'low-power' });
    this.renderer.setPixelRatio(Math.min(window.devicePixelRatio || 1, 1.5));
    this.renderer.domElement.style.display = 'block';
    this.renderer.domElement.style.touchAction = 'none';
    this.renderer.domElement.setAttribute('role', 'img');
    host.appendChild(this.renderer.domElement);

    this.camera.up.set(0, 0, 1);
    this.scene.add(this.ambient, this.key, this.fill, this.plate, this.root);
    this.key.position.set(120, -200, 260);
    this.fill.position.set(-160, 120, 80);
    // GLB (m, Y haut) -> mm, Z haut.
    this.root.scale.setScalar(1000);
    this.root.rotation.x = Math.PI / 2;
    if (opts.showPlate !== false) this.buildPlate();

    this.controls = new OrbitControls(this.camera, this.renderer.domElement);
    this.controls.enableDamping = false;
    this.controls.minDistance = 4; this.controls.maxDistance = 1500;
    this.controls.addEventListener('change', this.requestRender);

    const el = this.renderer.domElement;
    el.addEventListener('pointerdown', this.onDown);
    el.addEventListener('pointerup', this.onUp);
    this.ro = new ResizeObserver(() => this.resize());
    this.ro.observe(host);
    this.applyColors();
    this.resize();
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

  private buildPlate() {
    const h = PLATE_MM / 2, fine: number[] = [], major: number[] = [];
    for (let v = -h; v <= h + 0.1; v += 10) {
      const t = Math.abs(v) % 50 < 0.01 ? major : fine;
      t.push(-h, v, 0, h, v, 0, v, -h, 0, v, h, 0);
    }
    const mk = (a: number[]) => { const g = new BufferGeometry(); g.setAttribute('position', new Float32BufferAttribute(a, 3)); return g; };
    const border = mk([-h, -h, 0, h, -h, 0, h, -h, 0, h, h, 0, h, h, 0, -h, h, 0, -h, h, 0, -h, -h, 0]);
    const mat = (o = 1) => new LineBasicMaterial({ transparent: true, opacity: o });
    this.plate.add(new LineSegments(mk(fine), mat(0.55)), new LineSegments(mk(major), mat(0.6)), new LineSegments(border, mat()));
    this.plate.position.z = -0.05; // sous l'objet : pas de scintillement
  }

  setColors(c: ViewerColors) { this.colors = c; this.applyColors(); }

  private applyColors() {
    const c = this.colors;
    this.ambient.intensity = c.dark ? 0.7 : 0.85;
    this.key.intensity = c.dark ? 1.5 : 1.6;
    const [fine, major, border] = this.plate.children as LineSegments[];
    if (fine) { (fine.material as LineBasicMaterial).color.set(c.grid); (major.material as LineBasicMaterial).color.set(c.gridStrong); (border.material as LineBasicMaterial).color.set(c.gridStrong); }
    for (const e of this.entries) this.paint(e);
    this.requestRender();
  }

  private paint(e: PartEntry) {
    const c = this.colors, sel = e.part.id === this.selectedId;
    const m = e.mesh.material as MeshStandardMaterial;
    const tint = new Color(c.body).lerp(new Color(c.accent), 0.4);
    m.color.copy(sel ? tint : new Color(c.body));
    m.emissive.set(sel ? c.accent : '#000000'); m.emissiveIntensity = sel ? 0.1 : 0;
    if (e.edges) (e.edges.material as LineBasicMaterial).color.set(sel ? new Color(c.accent).lerp(new Color(c.dark ? '#ffffff' : '#000000'), c.dark ? 0.6 : 0.55) : new Color(c.edge));
  }

  /** Charge un GLB déjà en mémoire. Remplace le modèle courant. */
  load(buf: ArrayBuffer): Promise<LoadedModel> {
    this.clearModel();
    return new Promise((resolve, reject) => {
      new GLTFLoader().parse(buf, '', (gltf) => {
        if (this.disposed) return;
        let n = 0, total = 0;
        gltf.scene.traverse((o) => {
          const mesh = o as Mesh;
          if (!mesh.isMesh) return;
          const geo = mesh.geometry as BufferGeometry;
          const tris = (geo.index ? geo.index.count : geo.getAttribute('position').count) / 3;
          total += tris;
          const mat = new MeshStandardMaterial({ roughness: 0.72, metalness: 0.05, side: 2, polygonOffset: true, polygonOffsetFactor: 1, polygonOffsetUnits: 1 });
          mesh.material = mat;
          const id = `p${n}`;
          const label = (mesh.name || mesh.parent?.name || '').trim() || `Objet ${n + 1}`;
          this.entries.push({ part: { id, label, triangles: tris, size: { x: 0, y: 0, z: 0 } }, mesh });
          mesh.userData.partId = id;
          n++;
        });
        if (!this.entries.length) { reject(new Error("Le fichier d'aperçu ne contient aucun maillage")); return; }
        this.root.add(gltf.scene);
        this.root.updateMatrixWorld(true);
        const box = new Box3().setFromObject(this.root);
        const sz = box.getSize(new Vector3());
        // Pose l'objet sur le plateau et le centre en XY.
        gltf.scene.position.set(0, 0, 0);
        const c = box.getCenter(new Vector3());
        this.root.position.set(-c.x, -c.y, -box.min.z);
        this.root.updateMatrixWorld(true);
        for (const e of this.entries) {
          const b = new Box3().setFromObject(e.mesh), s = b.getSize(new Vector3());
          e.part.size = { x: s.x, y: s.y, z: s.z };
          if (total <= EDGE_TRIANGLE_LIMIT) {
            e.edges = new LineSegments(new EdgesGeometry(e.mesh.geometry as BufferGeometry, 28), new LineBasicMaterial());
            e.mesh.add(e.edges);
          }
          this.paint(e);
        }
        this.size = { x: sz.x, y: sz.y, z: sz.z };
        this.radius = Math.max(8, Math.hypot(sz.x, sz.y, sz.z) / 2);
        this.setView('iso');
        resolve({ parts: this.entries.map((e) => e.part), size: this.size, triangles: total });
      }, (err) => { this.onError?.(err); reject(err instanceof Error ? err : new Error('GLB illisible')); });
    });
  }

  private clearModel() {
    for (const e of this.entries) {
      e.edges?.geometry.dispose(); (e.edges?.material as LineBasicMaterial | undefined)?.dispose();
      e.mesh.geometry.dispose(); (e.mesh.material as MeshStandardMaterial).dispose();
    }
    this.entries = []; this.selectedId = null;
    this.root.clear(); this.root.position.set(0, 0, 0);
    this.requestRender();
  }

  setView(view: ViewName) {
    const r = view === 'plateau' ? 190 : this.radius, dist = r * 3.8;
    const tz = view === 'plateau' ? 0 : this.size.z / 2;
    const dir: Record<ViewName, [number, number, number]> = { iso: [0.62, -0.78, 0.55], face: [0, -1, 0.04], dessus: [0, -0.0008, 1], cote: [1, 0, 0.04], plateau: [0.35, -0.8, 0.75] };
    const [dx, dy, dz] = dir[view], n = Math.hypot(dx, dy, dz);
    this.camera.position.set((dx / n) * dist, (dy / n) * dist, tz + (dz / n) * dist);
    this.controls.target.set(0, 0, tz);
    this.controls.update();
    this.requestRender();
  }

  select(id: string | null) {
    this.selectedId = id;
    for (const e of this.entries) this.paint(e);
    this.requestRender();
  }

  get parts(): ViewerPart[] { return this.entries.map((e) => e.part); }

  private onDown = (ev: PointerEvent) => { this.down = { x: ev.clientX, y: ev.clientY }; };
  private onUp = (ev: PointerEvent) => {
    const d = this.down; this.down = null;
    if (!d || Math.hypot(ev.clientX - d.x, ev.clientY - d.y) > 4) return; // un glissement est une rotation, pas un clic
    const r = this.renderer.domElement.getBoundingClientRect();
    this.ray.setFromCamera(new Vector2(((ev.clientX - r.left) / r.width) * 2 - 1, -((ev.clientY - r.top) / r.height) * 2 + 1), this.camera);
    const hit = this.ray.intersectObjects(this.entries.map((e) => e.mesh), false)[0];
    const id = (hit?.object.userData.partId as string | undefined) ?? null;
    this.select(id);
    this.onPick?.(id);
  };

  dispose() {
    this.disposed = true;
    cancelAnimationFrame(this.raf);
    this.ro.disconnect();
    this.controls.dispose();
    this.clearModel();
    this.plate.traverse((o) => { const l = o as LineSegments; if (l.isLineSegments) { l.geometry.dispose(); (l.material as LineBasicMaterial).dispose(); } });
    this.renderer.dispose();
    this.renderer.domElement.remove();
  }
}
