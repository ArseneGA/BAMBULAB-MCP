import { describe, expect, it, vi } from 'vitest';
import {
  TRAVEL, decodeSlice, defaultHidden, featureStyle, fmtDuration, fmtFilament, isGcodeIndex, isGcodeSlice, layerThickness, loadToolpath, rampColor, rangeStats, speedRange,
} from './gcodeData';
import type { GcodeSlice, GcodeSource } from './gcodeData';
import { FEATURES, fakeSource, layerPaths, mkIndex } from './gcodeTestkit';
import { buildPositions, fillIndex, idxPerSeg, paintColors, segmentColor, vertsPerSeg } from './gcodeScene';

describe('formes et styles', () => {
  it('reconnaît le résumé et les tranches du serveur', () => {
    expect(isGcodeIndex(mkIndex())).toBe(true);
    expect(isGcodeIndex({ layers_total: 3 })).toBe(false);
    expect(isGcodeSlice({ layers: [], next: 0, unit_mm: 0.05 })).toBe(true);
    expect(isGcodeSlice({ layers: [] })).toBe(false);
  });
  it('styles par type (couleurs de Bambu Studio), inconnu = repli, déplacements masqués par défaut', () => {
    expect(featureStyle('Outer wall').color).toBe('#ff7d38');
    expect(featureStyle('outer wall').label).toBe('Paroi extérieure');
    expect(featureStyle('Prime tower').label).toBe("Tour d'essuyage");
    expect(featureStyle('Support interface').label).toBe('Interface de support');
    expect(featureStyle('Bridge').label).toBe('Pont');
    expect(featureStyle('Overhang wall').label).toBe('Paroi en surplomb');
    expect(featureStyle('Brim').label).toBe('Bordure (brim)');
    expect(featureStyle('Skirt').label).toBe('Jupe');
    expect(featureStyle('Truc inconnu', 2)).toMatchObject({ label: 'Truc inconnu' });
    expect([...defaultHidden(FEATURES)]).toEqual([1]);
    expect(TRAVEL).toBe('Travel');
  });
  it('échelles de couleur, durées, filament, épaisseur de couche', () => {
    expect(rampColor(0)).toEqual([43, 72, 190]); expect(rampColor(1)).toEqual([230, 80, 50]); expect(rampColor(NaN)).toEqual(rampColor(0)); expect(rampColor(9)).toEqual(rampColor(1));
    expect(fmtDuration(42)).toBe('42 s'); expect(fmtDuration(125)).toBe('2 min 05 s'); expect(fmtDuration(3723)).toBe('1 h 02 min');
    expect(fmtFilament(850)).toBe('850 mm'); expect(fmtFilament(12340)).toBe('12.34 m');
    expect(layerThickness([0.3, 0.5, 0.7], 1)).toBeCloseTo(0.3); expect(layerThickness([0.3, 0.5, 0.7], 3)).toBeCloseTo(0.2); expect(layerThickness([0, 0], 1)).toBe(0.2);
  });
  it('durée et filament d\'une plage de couches', () => {
    const ix = mkIndex(5);
    expect(rangeStats(ix, 2, 4)).toEqual({ layers: 3, timeS: 11 + 12 + 13, extrudedMM: 300 });
    expect(rangeStats(ix, 1, 99).timeS).toBe(10 + 11 + 12 + 13 + 14);
  });
});

describe('decodeSlice', () => {
  const sl: GcodeSlice = { artifact_sha256: 'x', plate: 1, layers_total: 2, from: 1, next: 0, unit_mm: 0.05, points: 9, done: true, layers: [{ l: 1, paths: layerPaths(true) }, { l: 2, paths: [] }] };
  it('décode les différences en segments absolus (mm), vitesse et type', () => {
    const [a, b] = decodeSlice(sl, mkIndex(2));
    expect(a.n).toBe(4 + 1 + 1); // carré (4) + diagonale (1) + déplacement (1)
    expect([a.x0[0], a.y0[0], a.x1[0], a.y1[0]]).toEqual([20, 20, 40, 20]); // (400,400) puis +(400,0), en unités de 0,05 mm
    expect(Array.from(a.f)).toEqual([0, 0, 0, 0, 3, 1]);
    expect(b.n).toBe(0);
  });
  it('segments exacts : (20,20) -> (20,40) -> (40,40)', () => {
    const g = decodeSlice({ ...sl, layers: [{ l: 1, paths: [{ f: 0, s: 50, p: [400, 400, 0, 400, 400, 0] }] }] }, mkIndex(2))[0];
    expect(g.n).toBe(2);
    expect([g.x0[0], g.y0[0], g.x1[0], g.y1[0]]).toEqual([20, 20, 20, 40]);
    expect([g.x0[1], g.y0[1], g.x1[1], g.y1[1]]).toEqual([20, 40, 40, 40]);
    expect(g.s[0]).toBe(10); // 50 mm/s / 5
    expect(g.z).toBeCloseTo(0.2); expect(g.h).toBeCloseTo(0.2);
  });
  it('filtre par type (second passage des déplacements)', () => {
    const [only] = decodeSlice(sl, mkIndex(2), (f) => f === 1);
    expect(only.n).toBe(1); expect(only.f[0]).toBe(1);
    const [none] = decodeSlice(sl, mkIndex(2), (f) => f === 1 && false);
    expect(none.n).toBe(0);
  });
  it('plage de vitesses des segments chargés', () => {
    const gs = decodeSlice(sl, mkIndex(2));
    expect(speedRange(gs)).toEqual([50, 150]);
    expect(speedRange([])).toEqual([0, 0]);
  });
});

describe('chargement progressif', () => {
  it('enchaîne les tranches par « next » et rend la main à chaque tranche', async () => {
    const src = fakeSource(5, 2);
    const got: number[][] = [];
    const prog: number[] = [];
    await loadToolpath(src, mkIndex(5), { onLayers: (ls, p) => { got.push(ls.map((l) => l.layer)); prog.push(p.layers); } });
    expect(got).toEqual([[1, 2], [3, 4], [5]]);
    expect(prog).toEqual([2, 4, 5]);
    expect(src.calls).toEqual([{ from: 1, travel: false }, { from: 3, travel: false }, { from: 5, travel: false }]);
  });
  it('with_travel et filtre : second passage des déplacements seulement', async () => {
    const src = fakeSource(3, 3);
    const seen: number[] = [];
    await loadToolpath(src, mkIndex(3), { withTravel: true, filter: (f) => f === 1, onLayers: (ls) => ls.forEach((l) => seen.push(l.n)) });
    expect(src.calls[0].travel).toBe(true);
    expect(seen).toEqual([1, 1, 1]);
  });
  it('interrompu par le signal, et jamais de boucle sur une réponse incohérente', async () => {
    const ac = new AbortController();
    const src = fakeSource(6, 1);
    const spy = vi.fn();
    await expect(loadToolpath(src, mkIndex(6), { signal: ac.signal, onLayers: (_l, p) => { spy(p.layers); if (p.layers === 2) ac.abort(); } })).rejects.toThrow();
    expect(spy).toHaveBeenCalledTimes(2);
    const stuck: GcodeSource = { index: async () => mkIndex(3), slice: async (from) => ({ artifact_sha256: 'x', plate: 1, layers_total: 3, from, next: from, unit_mm: 0.05, layers: [], points: 0, done: false }) };
    const n = vi.fn();
    await loadToolpath(stuck, mkIndex(3), { onLayers: n });
    expect(n).toHaveBeenCalledTimes(1);
  });
});

describe('tampons de la scène (sans WebGL)', () => {
  const g = decodeSlice({ artifact_sha256: 'x', plate: 1, layers_total: 1, from: 1, next: 0, unit_mm: 0.05, points: 4, done: true, layers: [{ l: 1, paths: layerPaths(false) }] }, mkIndex(1))[0];
  it('tubes : 12 sommets par segment, largeur 0,45 mm, dessus à Z et pied à Z - h', () => {
    const pos = buildPositions(g, 'tubes');
    expect(vertsPerSeg('tubes')).toBe(12); expect(pos.length).toBe(g.n * 12 * 3);
    // 2e segment : (40,20) -> (40,40), dirigé selon +Y : normale en X, demi-largeur 0,225
    const o = 1 * 12 * 3;
    expect(pos[o]).toBeCloseTo(40 - 0.225); expect(pos[o + 1]).toBeCloseTo(20); expect(pos[o + 2]).toBeCloseTo(g.z);
    expect(pos[o + 4 * 3 + 2]).toBeCloseTo(g.z - g.h);
  });
  it('traits : 2 sommets par segment à mi-hauteur de couche', () => {
    const pos = buildPositions(g, 'lines');
    expect(pos.length).toBe(g.n * 2 * 3); expect(pos[2]).toBeCloseTo(g.z - g.h / 2);
  });
  it('index : masquer un type retire ses segments, l\'ordre d\'impression est conservé', () => {
    const idx = new Uint32Array(g.n * idxPerSeg('lines'));
    expect(fillIndex(g, 'lines', new Set(), idx)).toBe(g.n);
    const withoutInfill = fillIndex(g, 'lines', new Set([3]), idx);
    expect(withoutInfill).toBe(g.n - 1);
    expect(Array.from(idx.slice(0, 4))).toEqual([0, 1, 2, 3]);
    const t = new Uint32Array(g.n * 18);
    expect(fillIndex(g, 'tubes', new Set(), t)).toBe(g.n);
    expect(Array.from(t.slice(0, 6))).toEqual([0, 1, 2, 0, 2, 3]);
    expect(fillIndex(g, 'tubes', new Set([0, 3]), t)).toBe(0);
  });
  it('couleurs : par type (Bambu), par vitesse, par hauteur ; flancs assombris', () => {
    const ctx = { featureRgb: [[255, 125, 56], [138, 160, 168], [255, 225, 77], [176, 48, 41]] as Array<[number, number, number]>, heightMM: 1, speedMin: 50, speedMax: 100 };
    expect(segmentColor(g, 0, 'type', ctx)).toEqual([255, 125, 56]);
    const last = g.n - 1; // diagonale de remplissage à 100 mm/s
    expect(segmentColor(g, last, 'speed', ctx)).toEqual(rampColor(1));
    expect(segmentColor(g, 0, 'speed', ctx)).toEqual(rampColor(0));
    expect(segmentColor(g, 0, 'height', ctx)).toEqual(rampColor(g.z / 1));
    const col = new Uint8Array(g.n * 12 * 3);
    paintColors(g, col, 'tubes', 'type', ctx);
    expect([col[0], col[1], col[2]]).toEqual([255, 125, 56]); // dessus : pleine couleur
    expect(col[4 * 3]).toBeLessThan(col[0]); expect(col[8 * 3]).toBeLessThan(col[4 * 3]); // flanc gauche puis droit : plus sombres
  });
});
