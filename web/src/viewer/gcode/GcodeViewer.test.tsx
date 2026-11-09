// @vitest-environment jsdom
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { GcodeViewer } from './GcodeViewer';
import type { GcodeSource } from './gcodeData';
import { fakeFactory, fakeSource, mkIndex } from './gcodeTestkit';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let host: HTMLElement; let root: Root;
beforeEach(() => { host = document.createElement('div'); document.body.appendChild(host); root = createRoot(host); });
afterEach(() => { act(() => root.unmount()); host.remove(); vi.useRealTimers(); });

const settle = async (n = 8) => { for (let i = 0; i < n; i++) await act(async () => { await Promise.resolve(); }); };
const render = async (el: JSX.Element) => { await act(async () => { root.render(el); }); await settle(); };
const btn = (name: string) => Array.from(host.querySelectorAll('button')).find((b) => (b.getAttribute('aria-label') ?? b.textContent ?? '').trim().startsWith(name)) as HTMLButtonElement;
const slider = (label: string) => host.querySelector(`input[aria-label="${label}"]`) as HTMLInputElement;
const setSlider = async (label: string, v: number) => {
  const el = slider(label);
  await act(async () => { Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(el, String(v)); el.dispatchEvent(new Event('input', { bubbles: true })); });
};
const click = async (b: HTMLElement) => { await act(async () => { b.click(); }); await settle(3); };

/** Réponses retenues : chaque release() laisse passer UNE tranche. */
function gated() {
  const waiting: Array<() => void> = [];
  return { gate: () => new Promise<void>((res) => waiting.push(res)), release: async () => { await act(async () => { waiting.shift()?.(); await Promise.resolve(); }); await settle(4); }, pending: () => waiting.length };
}

describe('GcodeViewer', () => {
  it('légende cliquable : un bouton par type, les déplacements à vide masqués par défaut, masquer / afficher un type', async () => {
    const f = fakeFactory(); const src = fakeSource(5, 5);
    await render(<GcodeViewer source={src} createRenderer={f.factory} />);
    const legend = host.querySelector('ul[aria-label^="Types de trajectoires"]')!;
    const names = Array.from(legend.querySelectorAll('button')).map((b) => b.textContent);
    expect(names).toEqual(['Paroi extérieure', 'Déplacements à vide', 'Paroi intérieure', 'Remplissage clairsemé']);
    const pressed = Array.from(legend.querySelectorAll('button')).map((b) => b.getAttribute('aria-pressed'));
    expect(pressed).toEqual(['true', 'false', 'true', 'true']);
    expect(f.last.calls).toContain('hidden:1');

    await click(btn('Paroi extérieure'));
    expect(f.last.calls[f.last.calls.length - 1]).toBe('hidden:0,1');
    expect(btn('Paroi extérieure').getAttribute('aria-pressed')).toBe('false');
    await click(btn('Paroi extérieure'));
    expect(f.last.calls[f.last.calls.length - 1]).toBe('hidden:1');
    expect(btn('Paroi extérieure').getAttribute('aria-pressed')).toBe('true');
  });

  it('curseur double de couches (min ≤ max) et curseur fin dans la couche', async () => {
    const f = fakeFactory();
    await render(<GcodeViewer source={fakeSource(5, 5)} createRenderer={f.factory} />);
    expect(f.last.calls).toContain('range:1-5@100');
    await setSlider('Couche maximale', 3);
    expect(f.last.calls[f.last.calls.length - 1]).toBe('range:1-3@100');
    await setSlider('Couche minimale', 2);
    expect(f.last.calls[f.last.calls.length - 1]).toBe('range:2-3@100');
    await setSlider('Couche minimale', 5); // ne dépasse jamais la couche max
    expect(f.last.calls[f.last.calls.length - 1]).toBe('range:3-3@100');
    await setSlider('Couche maximale', 1); // ne passe jamais sous la couche min
    expect(f.last.calls[f.last.calls.length - 1]).toBe('range:3-3@100');
    await setSlider('Position dans la couche', 40);
    expect(f.last.calls[f.last.calls.length - 1]).toBe('range:3-3@40');
    await setSlider('Couche maximale', 4); // changer de couche remet la couche complète
    expect(f.last.calls[f.last.calls.length - 1]).toBe('range:3-4@100');
  });

  it('infos : Z, durée et filament de la couche, durée de la plage, totaux du slicer', async () => {
    await render(<GcodeViewer source={fakeSource(5, 5)} createRenderer={fakeFactory().factory} />);
    expect(host.querySelector('.gv-sub')!.textContent).toContain('1 h 02 min (slicer)');
    expect(host.querySelector('.gv-sub')!.textContent).toContain('500 mm de filament');
    const info = () => host.querySelector('dl[aria-label="Infos de la couche"]')!.textContent!;
    expect(info()).toContain('1.00 mm'); // couche 5
    expect(info()).toContain('14 s'); // 10 + 4
    await setSlider('Couche maximale', 2);
    expect(info()).toContain('0.40 mm'); expect(info()).toContain('11 s');
    await setSlider('Couche minimale', 1);
    expect(info()).toContain('2 couches'); expect(info()).toContain('21 s'); // 10 + 11
  });

  it('chargement progressif : les couches arrivent tranche par tranche, avec la progression', async () => {
    const f = fakeFactory(); const g = gated();
    await render(<GcodeViewer source={fakeSource(5, 2, mkIndex(5), g.gate)} createRenderer={f.factory} />);
    const status = () => host.querySelector('.gv-loading')?.textContent ?? '';
    expect(status()).toContain('0 / 5 couches');
    expect(f.last.added).toEqual([]);
    await g.release();
    expect(f.last.added).toEqual([1, 2]); expect(status()).toContain('2 / 5 couches');
    await g.release();
    expect(f.last.added).toEqual([1, 2, 3, 4]); expect(status()).toContain('4 / 5 couches');
    // la scène est déjà utilisable pendant le chargement
    await setSlider('Couche maximale', 2);
    expect(f.last.calls[f.last.calls.length - 1]).toBe('range:1-2@100');
    await g.release();
    expect(f.last.added).toEqual([1, 2, 3, 4, 5]);
    expect(host.querySelector('.gv-loading')).toBeNull();
  });

  it('déplacements à vide : chargés seulement quand on les affiche (second passage with_travel)', async () => {
    const f = fakeFactory(); const src = fakeSource(4, 2);
    await render(<GcodeViewer source={src} createRenderer={f.factory} />);
    expect(src.calls.every((c) => !c.travel)).toBe(true);
    const n = src.calls.length;
    await click(btn('Déplacements à vide'));
    await settle(10);
    expect(src.calls.slice(n).every((c) => c.travel)).toBe(true);
    expect(src.calls.length).toBeGreaterThan(n);
    expect(f.last.added.filter((l) => l > 1000)).toEqual([1001, 1002, 1003, 1004]); // une géométrie de déplacements par couche
    expect(f.last.calls).toContain('hidden:'); // plus aucun type masqué
    // masquer puis réafficher ne recharge rien
    const m = src.calls.length;
    await click(btn('Déplacements à vide')); await click(btn('Déplacements à vide'));
    expect(src.calls.length).toBe(m);
  });

  it('couleurs : type / vitesse / hauteur remplacent la légende par une échelle ; vues iso / dessus / face', async () => {
    const f = fakeFactory();
    await render(<GcodeViewer source={fakeSource(3, 3)} createRenderer={f.factory} />);
    expect(btn('Type').getAttribute('aria-pressed')).toBe('true');
    await click(btn('Vitesse'));
    expect(f.last.calls[f.last.calls.length - 1]).toBe('color:speed');
    expect(host.querySelector('[aria-label="Échelle de vitesse"]')).not.toBeNull();
    expect(host.querySelector('ul[aria-label^="Types"]')).toBeNull();
    await click(btn('Hauteur'));
    expect(host.querySelector('[aria-label="Échelle de hauteur"]')!.textContent).toContain('0.6 mm');
    await click(btn('Type'));
    expect(host.querySelector('ul[aria-label^="Types"]')).not.toBeNull();
    for (const [label, v] of [['Dessus', 'dessus'], ['Face', 'face'], ['Iso', 'iso']]) {
      await click(btn(label));
      expect(f.last.calls[f.last.calls.length - 1]).toBe(`view:${v}`);
    }
  });

  it('lecture : avance dans la couche puis de couche en couche et s\'arrête à la fin', async () => {
    vi.useFakeTimers();
    const f = fakeFactory();
    await render(<GcodeViewer source={fakeSource(3, 3)} createRenderer={f.factory} playMs={10} />);
    expect(btn('Lecture')).toBeDefined();
    await act(async () => { btn('Lecture').click(); });
    expect(f.last.calls[f.last.calls.length - 1]).toBe('range:1-1@0'); // repart de la couche 1 (sélection complète au départ)
    for (let i = 0; i < 3; i++) await act(async () => { await vi.advanceTimersByTimeAsync(10); }); // un pas de lecture par rendu
    expect(f.last.calls.some((c) => /^range:1-1@(34|68|100)$/.test(c))).toBe(true); // progression dans la couche
    for (let i = 0; i < 30; i++) await act(async () => { await vi.advanceTimersByTimeAsync(10); });
    expect(f.last.calls).toContain('range:1-3@100');
    expect(btn('Lecture')).toBeDefined(); expect(btn('Pause')).toBeUndefined(); // arrêtée
  });

  it('erreurs : scène indisponible (WebGL), résumé refusé avec « Réessayer », nettoyage au démontage', async () => {
    const bad = () => { throw new Error('WebGL absent'); };
    await render(<GcodeViewer source={fakeSource(3, 3)} createRenderer={bad} />);
    expect(host.querySelector('[role=alert]')!.textContent).toContain('WebGL absent');

    act(() => root.unmount()); root = createRoot(host);
    let fail = true;
    const src: GcodeSource = { index: async () => { if (fail) throw new Error('Aucun G-code'); return mkIndex(2); }, slice: fakeSource(2, 2).slice };
    const f = fakeFactory();
    await render(<GcodeViewer source={src} createRenderer={f.factory} />);
    expect(host.querySelector('[role=alert]')!.textContent).toContain('Aucun G-code');
    fail = false;
    await click(btn('Réessayer'));
    await settle(10);
    expect(host.querySelector('ul[aria-label^="Types"]')).not.toBeNull();

    act(() => root.unmount()); root = createRoot(host);
    expect(f.last.disposed).toBe(true);
  });

  it('transmet à la scène l\'emprise, la hauteur et les couleurs de Bambu Studio', async () => {
    const f = fakeFactory();
    await render(<GcodeViewer source={fakeSource(5, 5)} createRenderer={f.factory} />);
    expect(f.last.opts.bboxMM).toEqual([10, 10, 50, 50]);
    expect(f.last.opts.heightMM).toBe(1);
    expect(f.last.opts.featureColors[0]).toBe('#ff7d38');
    expect(f.last.opts.expectedPoints).toBe(30);
  });
});
