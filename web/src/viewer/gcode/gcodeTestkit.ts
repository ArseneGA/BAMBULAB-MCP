import type { GcodeIndex, GcodeSource } from './gcodeData';
import type { GcodeRenderer, GcodeRendererOptions, RendererFactory } from './gcodeRenderer';

// Outils de test partagés (données simulées, faux rendu) : aucun import de three.js.

export const FEATURES = ['Outer wall', 'Travel', 'Inner wall', 'Sparse infill'];

export const mkIndex = (layers = 5, over: Partial<GcodeIndex> = {}): GcodeIndex => ({
  artifact_sha256: 'a'.repeat(64), plate: 1, layers_total: layers, unit_mm: 0.05, features: FEATURES,
  z: Array.from({ length: layers }, (_, i) => +((i + 1) * 0.2).toFixed(2)), time_s: Array.from({ length: layers }, (_, i) => 10 + i), extruded_mm: Array.from({ length: layers }, () => 100),
  points: Array.from({ length: layers }, () => 6), total_time_s: 3723, time_source: 'slicer', filament_mm: 500, bbox_mm: [10, 10, 50, 50],
  total_points: layers * 6, max_total_points: 2_000_000, layer_budget: 10_000, simplified: false, slice_points: 12_000, ...over,
});

/** Une couche : un carré de paroi extérieure (4 segments), une diagonale de remplissage et, si travel, un déplacement. */
export const layerPaths = (travel: boolean) => [
  { f: 0, s: 50, p: [400, 400, 400, 0, 0, 400, -400, 0, 0, -400] },
  { f: 3, s: 100, p: [400, 400, 200, 200] },
  ...(travel ? [{ f: 1, s: 150, p: [0, 0, 100, 100] }] : []),
];

/** Source simulée : tranches de `per` couches, avec journal des appels. `gate` retient les réponses (chargement progressif). */
export function fakeSource(layers = 5, per = 2, index = mkIndex(layers), gate?: () => Promise<void>): GcodeSource & { calls: Array<{ from: number; travel: boolean }> } {
  const calls: Array<{ from: number; travel: boolean }> = [];
  return {
    calls,
    index: async () => index,
    slice: async (from, travel) => {
      calls.push({ from, travel });
      if (gate) await gate();
      const ls = Array.from({ length: Math.min(per, layers - from + 1) }, (_, i) => ({ l: from + i, paths: layerPaths(travel) }));
      const last = from + ls.length - 1;
      return { artifact_sha256: index.artifact_sha256, plate: 1, layers_total: layers, from, next: last >= layers ? 0 : last + 1, unit_mm: 0.05, layers: ls, points: ls.length * 8, done: last >= layers };
    },
  };
}

/** Faux rendu : journal des appels de la visionneuse. */
export interface FakeRenderer extends GcodeRenderer { calls: string[]; added: number[]; opts: GcodeRendererOptions; disposed: boolean }
export function fakeRenderer(opts: GcodeRendererOptions): FakeRenderer {
  const r: FakeRenderer = {
    mode: 'tubes', calls: [], added: [], opts, disposed: false,
    addLayers(l) { r.added.push(...l.map((g) => g.layer + (g.travel ? 1000 : 0))); r.calls.push(`add:${l.length}`); },
    setRange(a, b, p) { r.calls.push(`range:${a}-${b}@${Math.round(p * 100)}`); },
    setHidden(h) { r.calls.push(`hidden:${[...h].sort().join(',')}`); },
    setColorMode(m) { r.calls.push(`color:${m}`); },
    setView(v) { r.calls.push(`view:${v}`); },
    dispose() { r.disposed = true; r.calls.push('dispose'); },
  };
  return r;
}
export function fakeFactory() {
  const made: FakeRenderer[] = [];
  const factory: RendererFactory = (_host, o) => { const r = fakeRenderer(o); made.push(r); return r; };
  return { factory, made, get last() { return made[made.length - 1]; } };
}
