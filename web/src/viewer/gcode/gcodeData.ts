/**
 * Données de la visionneuse couche par couche : formes des réponses du serveur (outils réservés get_gcode_layers /
 * get_gcode_slice, routes /api/gcode/*), styles par type de trajectoire (couleurs inspirées de Bambu Studio),
 * décodage des tranches en segments et chargement progressif. Aucun accès réseau ici : la source est injectée.
 *
 * BUDGETS (voir docs/research/mcp-apps.md § 12) : ≤ 2 000 000 points pour toute la plaque, ≤ 10 000 points d'extrusion
 * par couche, tranches de ≤ 12 000 points (≈ 100 000 caractères JSON) ; en mode « tubes » ≤ TUBE_SEGMENT_LIMIT segments,
 * au-delà des traits (lignes).
 */

export interface GcodeIndex {
  artifact_sha256: string; plate: number; layers_total: number; unit_mm: number;
  features: string[]; z: number[]; time_s: number[]; extruded_mm: number[]; points: number[];
  total_time_s: number; time_source: 'slicer' | 'estimation' | string; filament_mm: number;
  bbox_mm: [number, number, number, number]; total_points: number; max_total_points: number; layer_budget: number;
  simplified: boolean; tolerance_mm?: number; slice_points: number;
}

export interface GcodePath { f: number; s?: number; p: number[] }
export interface GcodeSlice {
  artifact_sha256: string; plate: number; layers_total: number; from: number; next: number; unit_mm: number;
  layers: Array<{ l: number; paths: GcodePath[] }>; points: number; done: boolean;
}

/** Où prendre les données : outils MCP (vue) ou routes HTTP (interface web). */
export interface GcodeSource {
  index(signal?: AbortSignal): Promise<GcodeIndex>;
  slice(fromLayer: number, withTravel: boolean, signal?: AbortSignal): Promise<GcodeSlice>;
}

export function isGcodeIndex(s: unknown): s is GcodeIndex {
  const o = s as Partial<GcodeIndex> | undefined;
  return !!o && typeof o.layers_total === 'number' && Array.isArray(o.z) && Array.isArray(o.features) && Array.isArray(o.time_s) && typeof o.unit_mm === 'number';
}
export function isGcodeSlice(s: unknown): s is GcodeSlice {
  const o = s as Partial<GcodeSlice> | undefined;
  return !!o && Array.isArray(o.layers) && typeof o.next === 'number' && typeof o.unit_mm === 'number';
}

// ---------------------------------------------------------------- types de trajectoires

export const TRAVEL = 'Travel';
export interface FeatureStyle { label: string; color: string; defaultHidden?: boolean }

/** Couleurs proches de celles de Bambu Studio (aperçu « Type de ligne »). Clé = nom FEATURE en minuscules. */
const STYLES: Record<string, FeatureStyle> = {
  'outer wall': { label: 'Paroi extérieure', color: '#ff7d38' },
  'inner wall': { label: 'Paroi intérieure', color: '#ffe14d' },
  'overhang wall': { label: 'Paroi en surplomb', color: '#1f1fff' },
  'sparse infill': { label: 'Remplissage clairsemé', color: '#b03029' },
  'internal solid infill': { label: 'Remplissage plein', color: '#9654cc' },
  'top surface': { label: 'Surface du dessus', color: '#f04040' },
  'bottom surface': { label: 'Surface du dessous', color: '#4d80ba' },
  bridge: { label: 'Pont', color: '#4d80ba' },
  'internal bridge': { label: 'Pont interne', color: '#6ea2d9' },
  'gap infill': { label: 'Remplissage des interstices', color: '#ffffff' },
  support: { label: 'Support', color: '#00cc66' },
  'support interface': { label: 'Interface de support', color: '#008040' },
  'support transition': { label: 'Transition de support', color: '#5fd99a' },
  skirt: { label: 'Jupe', color: '#00875e' },
  brim: { label: 'Bordure (brim)', color: '#00a876' },
  'prime tower': { label: "Tour d'essuyage", color: '#b3e3ab' },
  ironing: { label: 'Lissage', color: '#f4b6c5' },
  custom: { label: 'Personnalisé', color: '#5ed1fa' },
  travel: { label: 'Déplacements à vide', color: '#8aa0a8', defaultHidden: true },
};
const FALLBACK = ['#c97bd1', '#7bd1c1', '#d1c97b', '#7b9bd1', '#d17b9b', '#9bd17b'];

export function featureStyle(name: string, i = 0): FeatureStyle {
  return STYLES[name.trim().toLowerCase()] ?? { label: name, color: FALLBACK[i % FALLBACK.length] };
}

/** Indices des types masqués au départ (déplacements à vide). */
export const defaultHidden = (features: string[]): Set<number> =>
  new Set(features.flatMap((f, i) => (featureStyle(f, i).defaultHidden ? [i] : [])));

// ---------------------------------------------------------------- couleurs de repli (vitesse, hauteur)

export type ColorMode = 'type' | 'speed' | 'height';

const hex = (c: string): [number, number, number] => [parseInt(c.slice(1, 3), 16), parseInt(c.slice(3, 5), 16), parseInt(c.slice(5, 7), 16)];
export const hexToRgb = hex;

const RAMP: Array<[number, number, number]> = [[43, 72, 190], [38, 160, 200], [60, 190, 110], [240, 210, 60], [230, 80, 50]];
/** t dans [0,1] : bleu (lent / bas) → vert → jaune → rouge (rapide / haut). */
export function rampColor(t: number): [number, number, number] {
  const x = Math.min(1, Math.max(0, Number.isFinite(t) ? t : 0)) * (RAMP.length - 1);
  const i = Math.min(RAMP.length - 2, Math.floor(x)), f = x - i;
  return [0, 1, 2].map((k) => Math.round(RAMP[i][k] + (RAMP[i + 1][k] - RAMP[i][k]) * f)) as [number, number, number];
}
export const rgbCss = (c: [number, number, number]) => `rgb(${c[0]}, ${c[1]}, ${c[2]})`;

// ---------------------------------------------------------------- décodage

/** Une couche décodée en segments (x0,y0)->(x1,y1) à la hauteur z (mm), épaisseur h (mm). */
export interface LayerGeom {
  layer: number; z: number; h: number; n: number;
  /** vrai pour les déplacements à vide, chargés dans un second passage (même numéro de couche que les extrusions) */
  travel?: boolean;
  x0: Float32Array; y0: Float32Array; x1: Float32Array; y1: Float32Array;
  f: Uint8Array; /** type (index dans features) */
  s: Uint8Array; /** vitesse / 5 mm/s */
}

/** Épaisseur de la couche : écart avec la précédente (la 1re : sa hauteur Z). */
export const layerThickness = (z: number[], layer: number): number => {
  const h = layer <= 1 ? z[0] : z[layer - 1] - z[layer - 2];
  return h > 0.01 && h < 2 ? h : 0.2;
};

/**
 * Décode une tranche. `only` : ne garder que ces types (ex. les déplacements à vide lors d'un second passage) ;
 * `skip` : écarter ces types. Les points suivants le premier sont des différences en unités de unit_mm.
 */
export function decodeSlice(sl: GcodeSlice, index: Pick<GcodeIndex, 'z'>, filter: (f: number) => boolean = () => true): LayerGeom[] {
  const u = sl.unit_mm;
  return sl.layers.map((ly) => {
    let n = 0;
    for (const p of ly.paths) if (filter(p.f)) n += Math.max(0, p.p.length / 2 - 1);
    const g: LayerGeom = {
      layer: ly.l, z: index.z[ly.l - 1] ?? 0, h: layerThickness(index.z, ly.l), n,
      x0: new Float32Array(n), y0: new Float32Array(n), x1: new Float32Array(n), y1: new Float32Array(n), f: new Uint8Array(n), s: new Uint8Array(n),
    };
    let k = 0;
    for (const p of ly.paths) {
      if (!filter(p.f) || p.p.length < 4) continue;
      let x = p.p[0], y = p.p[1];
      const spd = Math.min(255, Math.round((p.s ?? 0) / 5));
      for (let i = 2; i + 1 < p.p.length; i += 2) {
        const nx = x + p.p[i], ny = y + p.p[i + 1];
        g.x0[k] = x * u; g.y0[k] = y * u; g.x1[k] = nx * u; g.y1[k] = ny * u; g.f[k] = p.f; g.s[k] = spd;
        x = nx; y = ny; k++;
      }
    }
    return g;
  });
}

export interface LoadProgress { layers: number; total: number; points: number }

/**
 * Charge toutes les tranches l'une après l'autre (chaque réponse donne la couche suivante) et appelle onLayers à
 * chaque tranche : l'affichage est progressif. `filter` choisit les types décodés ; withTravel demande aussi les
 * déplacements à vide au serveur.
 */
export async function loadToolpath(
  source: GcodeSource, index: GcodeIndex,
  o: { withTravel?: boolean; filter?: (f: number) => boolean; signal?: AbortSignal; onLayers: (layers: LayerGeom[], p: LoadProgress) => void },
): Promise<void> {
  let from = 1, done = 0, points = 0;
  for (let guard = 0; from > 0 && guard < 20_000; guard++) {
    if (o.signal?.aborted) throw new DOMException('abort', 'AbortError');
    const sl = await source.slice(from, !!o.withTravel, o.signal);
    if (o.signal?.aborted) throw new DOMException('abort', 'AbortError');
    const layers = decodeSlice(sl, index, o.filter);
    done += sl.layers.length; points += sl.points;
    o.onLayers(layers, { layers: done, total: index.layers_total, points });
    if (sl.done || sl.next <= from) break; // jamais de boucle infinie sur une réponse incohérente
    from = sl.next;
  }
}

// ---------------------------------------------------------------- libellés

export const fmtDuration = (s: number): string => {
  const t = Math.max(0, Math.round(s));
  if (t < 60) return `${t} s`;
  if (t < 3600) return `${Math.floor(t / 60)} min ${String(t % 60).padStart(2, '0')} s`;
  return `${Math.floor(t / 3600)} h ${String(Math.floor((t % 3600) / 60)).padStart(2, '0')} min`;
};
export const fmtFilament = (mm: number): string => (mm >= 1000 ? `${(mm / 1000).toFixed(2)} m` : `${Math.round(mm)} mm`);

export interface RangeStats { layers: number; timeS: number; extrudedMM: number }
/** Durée et filament cumulés des couches min..max (1 à N, incluses). */
export function rangeStats(index: Pick<GcodeIndex, 'time_s' | 'extruded_mm'>, min: number, max: number): RangeStats {
  let timeS = 0, extrudedMM = 0;
  for (let l = Math.max(1, min); l <= max && l <= index.time_s.length; l++) { timeS += index.time_s[l - 1] ?? 0; extrudedMM += index.extruded_mm[l - 1] ?? 0; }
  return { layers: Math.max(0, max - min + 1), timeS, extrudedMM };
}

/** Plage de vitesses (mm/s) des segments chargés, pour l'échelle du mode « vitesse ». */
export function speedRange(layers: Iterable<LayerGeom>): [number, number] {
  let lo = Infinity, hi = -Infinity;
  for (const g of layers) for (let i = 0; i < g.n; i++) { const v = g.s[i]; if (v > 0) { lo = Math.min(lo, v); hi = Math.max(hi, v); } }
  return Number.isFinite(lo) ? [lo * 5, hi * 5] : [0, 0];
}

/** Au-delà de ce nombre de segments le rendu passe des tubes aux traits (mémoire GPU). */
export const TUBE_SEGMENT_LIMIT = 250_000;
