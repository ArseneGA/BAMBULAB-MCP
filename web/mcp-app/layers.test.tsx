// @vitest-environment jsdom
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { FEATURE_COLORS, LayerViewer, drawLayer, featureColor, isLayerPaths, isLayersInfo, unionBox, type AppLike, type LayerPaths, type LayersInfo } from './layers';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const INFO: LayersInfo = { artifact_sha256: 'a'.repeat(64), plate: 1, layers_total: 12, sheet_layers: [1, 6, 12] };
const paths = (layer: number): LayerPaths => ({
  layer, layers_total: 12, z_mm: layer * 0.2, unit_mm: 0.05, features: ['Outer wall', 'Sparse infill'], segments: 4, points: 5,
  paths: [{ f: 0, p: [400, 400, 400 + layer * 100, 400, 400 + layer * 100, 400 + layer * 100] }, { f: 1, p: [500, 500, 600, 600] }],
  bbox_mm: [20, 20, 20 + layer * 5, 20 + layer * 5],
});

function fakeCtx() {
  const calls: string[] = [];
  const ctx = new Proxy({} as Record<string, unknown>, {
    get: (t, k: string) => (k in t ? t[k] : (..._a: unknown[]) => { calls.push(k); }),
    set: (t, k: string, v) => { t[k] = v; if (k === 'strokeStyle') calls.push(`color:${String(v)}`); return true; },
  });
  return { ctx: ctx as unknown as CanvasRenderingContext2D, calls };
}

let host: HTMLElement; let root: Root; let getContext: ReturnType<typeof vi.fn>; let last: ReturnType<typeof fakeCtx>;
beforeEach(() => {
  vi.useFakeTimers();
  host = document.createElement('div'); document.body.appendChild(host); root = createRoot(host);
  last = fakeCtx();
  getContext = vi.fn(() => last.ctx);
  HTMLCanvasElement.prototype.getContext = getContext as unknown as typeof HTMLCanvasElement.prototype.getContext;
});
afterEach(() => { act(() => root.unmount()); host.remove(); vi.useRealTimers(); });

const mkApp = () => {
  const calls: Array<{ name: string; arguments: Record<string, unknown> }> = [];
  const app = {
    callServerTool: vi.fn(async (p: { name: string; arguments: Record<string, unknown> }) => {
      calls.push(p);
      const l = Number(p.arguments.layer);
      if (l === 99) return { isError: true, content: [{ type: 'text', text: 'couche hors plage' }] };
      return { structuredContent: paths(l) };
    }),
  };
  return { app: app as unknown as AppLike & typeof app, calls };
};
const render = async (el: JSX.Element) => { await act(async () => { root.render(el); }); };
const tick = async (ms: number) => { await act(async () => { await vi.advanceTimersByTimeAsync(ms); }); };

describe('formes et cadrage', () => {
  it('reconnaît le résultat de show_layers et celui de get_layer_paths', () => {
    expect(isLayersInfo(INFO)).toBe(true);
    expect(isLayersInfo({ layers_total: 3 })).toBe(false);
    expect(isLayerPaths(paths(2))).toBe(true);
    expect(isLayerPaths({ layer: 1 })).toBe(false);
  });
  it('cadre commun : union avec marge, minimum 20 mm, ignore les couches vides', () => {
    const b = unionBox([[0, 0, 0, 0], [10, 10, 50, 30], [40, 5, 60, 25]])!;
    expect(b.minX).toBeLessThan(10); expect(b.maxX).toBeGreaterThan(60); expect(b.maxY - b.minY).toBeGreaterThanOrEqual(20);
    expect(unionBox([[0, 0, 0, 0]])).toBeUndefined();
    const tiny = unionBox([[10, 10, 11, 11]])!;
    expect(tiny.maxX - tiny.minX).toBeGreaterThanOrEqual(20);
  });
  it('couleurs des types : cycle de la palette serveur', () => {
    expect(featureColor(0)).toBe(FEATURE_COLORS[0]); expect(featureColor(FEATURE_COLORS.length)).toBe(FEATURE_COLORS[0]); expect(featureColor(-1)).toBe(FEATURE_COLORS[FEATURE_COLORS.length - 1]);
  });
});

describe('drawLayer', () => {
  it('trace une polyligne par trajet avec la couleur de son type', () => {
    const { ctx, calls } = fakeCtx();
    const n = drawLayer(ctx, 640, 440, paths(3), unionBox([[20, 20, 35, 35]]), false);
    expect(n).toBe(2);
    expect(calls.filter((c) => c === 'stroke').length).toBeGreaterThanOrEqual(3); // grille + 2 trajets
    expect(calls).toContain(`color:${FEATURE_COLORS[0]}`); expect(calls).toContain(`color:${FEATURE_COLORS[1]}`);
  });
  it('sans données ni cadre : fond seulement', () => {
    const { ctx, calls } = fakeCtx();
    expect(drawLayer(ctx, 100, 100, undefined, undefined)).toBe(0);
    expect(calls).toEqual(['fillRect']);
  });
});

describe('LayerViewer', () => {
  it('charge la couche 1 et le cadre (1, médiane, dernière) puis dessine', async () => {
    const { app, calls } = mkApp();
    await render(<LayerViewer app={app} info={INFO} debounceMs={50} />);
    await tick(200);
    const asked = calls.map((c) => c.arguments.layer);
    expect(new Set(asked)).toEqual(new Set([1, 6, 12]));
    expect(calls[0].name).toBe('get_layer_paths');
    expect(calls[0].arguments).toMatchObject({ artifact_id: INFO.artifact_sha256, plate: 1, layers_total: 12 });
    expect(getContext).toHaveBeenCalled();
    expect(last.calls).toContain(`color:${FEATURE_COLORS[0]}`);
    expect(host.textContent).toContain('1 / 12');
    expect(host.textContent).toContain('Outer wall');
  });

  it('le curseur change de couche (anti-rebond) et réutilise le cache', async () => {
    const { app, calls } = mkApp();
    await render(<LayerViewer app={app} info={INFO} debounceMs={50} />);
    await tick(200);
    const slider = host.querySelector('input[type=range]') as HTMLInputElement;
    await act(async () => { Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(slider, '4'); slider.dispatchEvent(new Event('input', { bubbles: true })); });
    await tick(200);
    expect(calls.filter((c) => c.arguments.layer === 4).length).toBe(1);
    expect(host.textContent).toContain('4 / 12'); expect(host.textContent).toContain('Z 0.80 mm');
    // retour sur la couche 1 : déjà en cache, pas de nouvel appel
    const before = calls.length;
    await act(async () => { Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(slider, '1'); slider.dispatchEvent(new Event('input', { bubbles: true })); });
    await tick(200);
    expect(calls.length).toBe(before);
  });

  it('boutons suivante / précédente et lecture jusqu\'à la fin', async () => {
    const { app } = mkApp();
    await render(<LayerViewer app={app} info={{ ...INFO, layers_total: 3 }} debounceMs={10} playMs={50} />);
    await tick(100);
    const btn = (label: string) => host.querySelector(`button[aria-label="${label}"]`) as HTMLButtonElement;
    expect(btn('Couche précédente').disabled).toBe(true);
    await act(async () => { btn('Couche suivante').click(); });
    await tick(60);
    expect(host.textContent).toContain('2 / 3');
    await act(async () => { btn('Lecture').click(); });
    await tick(400);
    expect(host.textContent).toContain('3 / 3');
    expect(btn('Couche suivante').disabled).toBe(true);
    expect(host.querySelector('button[aria-label="Lecture"]')).not.toBeNull(); // la lecture s'est arrêtée
  });

  it('affiche l\'erreur de l\'outil sans planter', async () => {
    const { app } = mkApp();
    await render(<LayerViewer app={app} info={{ ...INFO, layers_total: 99 }} debounceMs={10} />);
    await tick(100);
    const slider = host.querySelector('input[type=range]') as HTMLInputElement;
    await act(async () => { Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(slider, '99'); slider.dispatchEvent(new Event('input', { bubbles: true })); });
    await tick(100);
    expect(host.querySelector('[role=alert]')?.textContent).toContain('couche hors plage');
  });
});
